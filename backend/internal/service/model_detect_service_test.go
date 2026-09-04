package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// 手填目标不依赖线上库，可以在没有 PG 的机器上完整跑通检测链路。
func newDetectService() *ModelDetectService {
	return NewModelDetectService(nil, nil, nil, nil)
}

func TestModelDetectStartRejectsBadInput(t *testing.T) {
	svc := newDetectService()
	cases := []struct {
		name string
		req  DetectRunRequest
		want string
	}{
		{"空目标", DetectRunRequest{}, "至少选择一个"},
		{"缺 Base URL", DetectRunRequest{Targets: []DetectTargetInput{{APIKey: "sk-xxxxxxxx"}}}, "Base URL"},
		{"缺 Key", DetectRunRequest{Targets: []DetectTargetInput{{BaseURL: "https://api.example.com"}}}, "API Key"},
		{"地址无协议", DetectRunRequest{Targets: []DetectTargetInput{
			{BaseURL: "api.example.com", APIKey: "sk-xxxxxxxx"}}}, "http"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Start(context.Background(), tc.req)
			if err == nil {
				t.Fatal("期望报错，实际通过")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("错误信息应包含 %q，实际 %q", tc.want, err.Error())
			}
		})
	}
}

func TestModelDetectStartRejectsTooManyTargets(t *testing.T) {
	svc := newDetectService()
	req := DetectRunRequest{}
	for i := 0; i <= maxDetectTargets; i++ {
		req.Targets = append(req.Targets, DetectTargetInput{
			BaseURL: "https://api.example.com", APIKey: "sk-xxxxxxxxxx"})
	}
	if _, err := svc.Start(context.Background(), req); err == nil {
		t.Fatalf("超过 %d 个目标应被拒绝", maxDetectTargets)
	}
}

func TestModelDetectAccountTargetRequiresPG(t *testing.T) {
	svc := newDetectService()
	id := int64(7)
	_, err := svc.Start(context.Background(), DetectRunRequest{
		Targets: []DetectTargetInput{{AccountID: &id}},
	})
	if err == nil || !strings.Contains(err.Error(), "线上数据库") {
		t.Fatalf("PG 不可用时选账号应返回专门的错误，实际 %v", err)
	}
}

// 端到端：手填目标 → 作业推进 → 落到 completed，并且快照里没有密钥。
func TestModelDetectJobLifecycle(t *testing.T) {
	const key = "sk-lifecycle-secret-key-value"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/models") {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "msg_01TESTTESTTESTTESTTEST", "type": "message", "role": "assistant",
			"model": "claude-opus-5", "stop_reason": "end_turn",
			"usage":   map[string]any{"input_tokens": 12, "output_tokens": 1},
			"content": []any{map[string]any{"type": "text", "text": "PONG " + r.Header.Get("x-api-key")}},
		})
	}))
	defer srv.Close()

	svc := newDetectService()
	jobID, err := svc.Start(context.Background(), DetectRunRequest{
		Targets: []DetectTargetInput{{Name: "手填目标", BaseURL: srv.URL, APIKey: key}},
		Model:   "claude-opus-5",
		Checks:  []string{"models", "ping"},
	})
	if err != nil {
		t.Fatalf("发起检测失败: %v", err)
	}

	deadline := time.Now().Add(20 * time.Second)
	var job *DetectJob
	for time.Now().Before(deadline) {
		snap, ok := svc.Get(jobID)
		if !ok {
			t.Fatal("作业快照丢失")
		}
		if snap.Status != DetectJobRunning {
			job = snap
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if job == nil {
		t.Fatal("作业未在超时前完成")
	}
	if job.Status != DetectJobCompleted {
		t.Fatalf("期望状态 %s，实际 %s", DetectJobCompleted, job.Status)
	}
	if len(job.Targets) != 1 || job.Targets[0].Verdict == nil {
		t.Fatal("目标结果或判定缺失")
	}
	if job.Done != job.Total {
		t.Fatalf("完成数 %d 与总数 %d 不符", job.Done, job.Total)
	}

	blob, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("序列化作业失败: %v", err)
	}
	if strings.Contains(string(blob), key) {
		t.Fatal("作业快照中出现 API Key 明文")
	}
}

func TestModelDetectCancelUnknownJob(t *testing.T) {
	if newDetectService().Cancel("no-such-job") {
		t.Fatal("取消不存在的作业应返回 false")
	}
}

func TestClampDetectConcurrency(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{0, 3},
		{-2, 1},
		{1, 1},
		{4, 4},
		{10, 10},
		{11, 10},
		{99, 10},
	}
	for _, tc := range cases {
		if got := clampDetectConcurrency(tc.in); got != tc.want {
			t.Fatalf("clampDetectConcurrency(%d) = %d，期望 %d", tc.in, got, tc.want)
		}
	}
}

func TestDetectAccountEmptyGroupsJSON(t *testing.T) {
	blob, err := json.Marshal(DetectAccount{AccountID: 1, Groups: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(blob), `"groups":[]`) {
		t.Fatalf("空分组应序列化为 [] 而不是 null: %s", blob)
	}
}

func TestModelDetectReportsRunningCheckStatus(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "msg_01TESTTESTTESTTESTTEST", "type": "message", "role": "assistant",
			"model": "claude-opus-5", "stop_reason": "end_turn",
			"usage":   map[string]any{"input_tokens": 1, "output_tokens": 1},
			"content": []any{map[string]any{"type": "text", "text": "ok"}},
		})
	}))
	defer srv.Close()
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})

	svc := newDetectService()
	jobID, err := svc.Start(context.Background(), DetectRunRequest{
		Targets: []DetectTargetInput{{Name: "slow", BaseURL: srv.URL, APIKey: "sk-xxxxxxxxxx"}},
		Model:   "claude-opus-5", Checks: []string{"ping"}, Concurrency: 1,
	})
	if err != nil {
		t.Fatalf("发起检测失败: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("上游请求未发出")
	}
	deadline := time.Now().Add(2 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		snap, ok := svc.Get(jobID)
		if !ok {
			t.Fatal("作业快照丢失")
		}
		for _, tgt := range snap.Targets {
			for _, c := range tgt.Checks {
				if c != nil && c.Status == "running" && c.ID == "ping" {
					found = true
				}
			}
		}
		if found {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !found {
		t.Fatal("检测项执行中应在作业快照里暴露 running 状态")
	}
	close(release)
	job := waitDetectJob(t, svc, jobID, 10*time.Second)
	if job.Status != DetectJobCompleted {
		t.Fatalf("期望 completed，实际 %s", job.Status)
	}
	if len(job.Targets) != 1 || len(job.Targets[0].Checks) != 1 || job.Targets[0].Checks[0].Status == "running" {
		t.Fatal("完成后不应仍是 running")
	}
}

func TestModelDetectConcurrencyRunsTargetsInParallel(t *testing.T) {
	var mu sync.Mutex
	inflight, maxInflight := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		inflight++
		if inflight > maxInflight {
			maxInflight = inflight
		}
		mu.Unlock()
		time.Sleep(80 * time.Millisecond)
		mu.Lock()
		inflight--
		mu.Unlock()
		w.Header().Set("content-type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/models") {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "msg_01TESTTESTTESTTESTTEST", "type": "message", "role": "assistant",
			"model": "claude-opus-5", "stop_reason": "end_turn",
			"usage":   map[string]any{"input_tokens": 1, "output_tokens": 1},
			"content": []any{map[string]any{"type": "text", "text": "ok"}},
		})
	}))
	defer srv.Close()

	svc := newDetectService()
	req := DetectRunRequest{Model: "claude-opus-5", Checks: []string{"models"}, Concurrency: 3}
	for i := 0; i < 3; i++ {
		req.Targets = append(req.Targets, DetectTargetInput{
			Name: fmt.Sprintf("t%d", i), BaseURL: srv.URL, APIKey: "sk-xxxxxxxxxx",
		})
	}
	jobID, err := svc.Start(context.Background(), req)
	if err != nil {
		t.Fatalf("发起检测失败: %v", err)
	}
	job := waitDetectJob(t, svc, jobID, 20*time.Second)
	if job.Status != DetectJobCompleted {
		t.Fatalf("期望 completed，实际 %s", job.Status)
	}
	if job.Concurrency != 3 {
		t.Fatalf("作业应记下并发度 3，实际 %d", job.Concurrency)
	}
	if len(job.Targets) != 3 {
		t.Fatalf("应有 3 个目标结果，实际 %d", len(job.Targets))
	}
	mu.Lock()
	gotMax := maxInflight
	mu.Unlock()
	if gotMax < 2 {
		t.Fatalf("concurrency=3 时应同时打多个目标，最大在途 %d", gotMax)
	}
}

func TestModelDetectCancelStopsQueuedTargets(t *testing.T) {
	started := make(chan struct{}, 1)
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-block
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "msg_01TESTTESTTESTTESTTEST", "type": "message", "role": "assistant",
			"model": "claude-opus-5", "stop_reason": "end_turn",
			"content": []any{map[string]any{"type": "text", "text": "ok"}},
		})
	}))
	defer srv.Close()
	t.Cleanup(func() {
		select {
		case <-block:
		default:
			close(block)
		}
	})

	svc := newDetectService()
	jobID, err := svc.Start(context.Background(), DetectRunRequest{
		Targets: []DetectTargetInput{
			{Name: "slow", BaseURL: srv.URL, APIKey: "sk-xxxxxxxxxx"},
		},
		Model: "claude-opus-5", Checks: []string{"ping"}, Concurrency: 1,
	})
	if err != nil {
		t.Fatalf("发起检测失败: %v", err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("上游请求未发出")
	}
	if !svc.Cancel(jobID) {
		t.Fatal("取消应返回 true")
	}
	close(block)
	job := waitDetectJob(t, svc, jobID, 10*time.Second)
	if job.Status != DetectJobCancelled && job.Status != DetectJobCompleted {
		t.Fatalf("取消后状态应为 cancelled 或 completed，实际 %s", job.Status)
	}
}

func waitDetectJob(t *testing.T, svc *ModelDetectService, jobID string, timeout time.Duration) *DetectJob {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		snap, ok := svc.Get(jobID)
		if !ok {
			t.Fatal("作业快照丢失")
		}
		if snap.Status != DetectJobRunning {
			return snap
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("作业未在超时前完成")
	return nil
}

func TestTargetFingerprintStableAndKeySensitive(t *testing.T) {
	a := TargetFingerprint("https://api.example.com", "sk-1")
	b := TargetFingerprint("https://api.example.com/", "sk-1")
	c := TargetFingerprint("https://api.example.com", "sk-2")
	if a != b {
		t.Fatal("尾斜杠不应改变指纹")
	}
	if a == c {
		t.Fatal("不同 key 必须得到不同指纹")
	}
	if len(a) != 16 || strings.Contains(a, "sk-") {
		t.Fatalf("指纹形态异常: %s", a)
	}
}
