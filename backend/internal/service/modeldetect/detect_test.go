package modeldetect

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeUpstream 模拟一条上游渠道。behaviour 决定它像哪种后端。
type fakeUpstream struct {
	mu        sync.Mutex
	behaviour string // official | bedrock | wrapper
	// signatureChecked 为 false 时，篡改的签名也会被接受（伪装特征）。
	signatureChecked bool
	// strictParams 为 false 时，非法参数一律放行。
	strictParams bool
	// injectCache 为 true 时，裸请求也返回大段 cache_creation（号池注入特征）。
	injectCache     bool
	cacheWrite      int
	cacheReadOffset int
	callCount       int
}

func newFakeServer(t *testing.T, up *fakeUpstream) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up.mu.Lock()
		up.callCount++
		up.mu.Unlock()
		if r.URL.Path == "/v1/models" {
			writeJSON(w, 200, map[string]any{"data": []any{map[string]any{"id": "claude-opus-5"}}})
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)

		if r.URL.Path == "/v1/messages/count_tokens" {
			writeJSON(w, 200, map[string]any{"input_tokens": 12 + len(string(raw))/40})
			return
		}
		if up.behaviour == "bedrock" {
			w.Header().Set("x-amzn-requestid", "11111111-2222-3333-4444-555555555555")
		} else if up.behaviour == "official" {
			w.Header().Set("anthropic-ratelimit-requests-remaining", "999")
		}

		if err := up.validate(body); err != nil {
			writeJSON(w, 400, map[string]any{
				"type": "error", "error": map[string]any{"type": "invalid_request_error", "message": err.Error()}})
			return
		}
		up.reply(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// validate 复刻官方网关的参数校验；strictParams=false 的渠道全部放行。
func (up *fakeUpstream) validate(body map[string]any) error {
	if !up.strictParams {
		return nil
	}
	if mt, ok := num(body["max_tokens"]); ok && mt < 1 {
		return errText("max_tokens: Input should be greater than or equal to 1")
	}
	if th := mapOf(body["thinking"]); th != nil {
		if b, ok := num(th["budget_tokens"]); ok && b < 1024 {
			return errText("thinking.budget_tokens: Input should be greater than or equal to 1024")
		}
		if _, hasTemp := body["temperature"]; hasTemp {
			return errText("temperature may only be set to 1 when thinking is enabled")
		}
	}
	known := map[string]bool{"model": true, "max_tokens": true, "messages": true, "system": true,
		"thinking": true, "tools": true, "tool_choice": true, "stream": true, "temperature": true,
		"output_config": true}
	for k := range body {
		if !known[k] {
			return errText("probe_unknown_field: Extra inputs are not permitted")
		}
	}
	// 篡改过的签名必须被拒
	if !up.signatureChecked {
		return nil
	}
	for _, m := range sliceOf(body["messages"]) {
		msg := mapOf(m)
		for _, raw := range sliceOf(msg["content"]) {
			blk := mapOf(raw)
			if str(blk["type"]) == "thinking" && !strings.HasPrefix(str(blk["signature"]), "SIGVALID") {
				return errText("Invalid signature on thinking block")
			}
		}
	}
	return nil
}

func (up *fakeUpstream) reply(w http.ResponseWriter, body map[string]any) {
	id := "msg_01ABCDEFGHIJKLMNOPQRSTUV"
	toolID := "toolu_01ABCDEFGHIJKLMNOP"
	switch up.behaviour {
	case "bedrock":
		id = "msg_bdrk_01ABCDEFGHIJKLMNOPQR"
		toolID = "tooluse_ABCDEFGHIJKLMNOPQR"
	case "wrapper":
		id = "msg_" + "6f1c2b7e-1111-4b0e-9e2a-abcdef123456"
		toolID = "call_abc123"
	}

	usage := map[string]any{"input_tokens": 13, "output_tokens": 5}
	if up.injectCache {
		up.mu.Lock()
		usage["input_tokens"] = 14
		if up.cacheWrite == 0 {
			up.cacheWrite = 2400
			usage["cache_creation_input_tokens"] = 2400
		} else {
			usage["cache_read_input_tokens"] = 2400 + up.cacheReadOffset
		}
		up.mu.Unlock()
	}

	// 工具调用
	if tools := sliceOf(body["tools"]); len(tools) > 0 {
		tool := mapOf(tools[0])
		name := str(tool["name"])
		input := map[string]any{}
		text := userText(body)
		switch name {
		case "calculate_sum":
			a, b := twoNumbers(text)
			input = map[string]any{"a": a, "b": b}
		case "lookup_order":
			input = map[string]any{"order_id": firstToken(text, "ORDER_")}
		}
		writeJSON(w, 200, map[string]any{
			"id": id, "type": "message", "role": "assistant", "model": str(body["model"]),
			"stop_reason": "tool_use", "usage": usage,
			"content": []any{map[string]any{"type": "tool_use", "id": toolID, "name": name, "input": input}},
		})
		return
	}

	// thinking
	if mapOf(body["thinking"]) != nil && !hasThinkingBlock(body) {
		writeJSON(w, 200, map[string]any{
			"id": id, "type": "message", "role": "assistant", "model": str(body["model"]),
			"stop_reason": "end_turn", "usage": usage,
			"content": []any{
				map[string]any{"type": "thinking", "thinking": "让我算一下……", "signature": "SIGVALID_" + strings.Repeat("x", 40)},
				map[string]any{"type": "text", "text": "答案是 262。"},
			},
		})
		return
	}

	limit := 4096
	if v, ok := num(body["max_tokens"]); ok {
		limit = int(v)
	}
	text := replyFor(userText(body))
	out := len(strings.Fields(text))
	stop := "end_turn"
	if out > limit {
		if up.behaviour == "wrapper" {
			// 包装渠道无视 max_tokens
			out = 800
		} else {
			text = strings.Join(strings.Fields(text)[:limit], " ")
			out = limit
			stop = "max_tokens"
		}
	}
	usage["output_tokens"] = out
	writeJSON(w, 200, map[string]any{
		"id": id, "type": "message", "role": "assistant", "model": str(body["model"]),
		"stop_reason": stop, "usage": usage,
		"content": []any{map[string]any{"type": "text", "text": text}},
	})
}

// replyFor 按提问内容给出足以让断言成立的回复。
func replyFor(prompt string) string {
	switch {
	case strings.Contains(prompt, "PONG"):
		return "PONG"
	case strings.Contains(prompt, "TOKEN"):
		return strings.TrimSpace(strings.Repeat("TOKEN ", 200))
	case strings.Contains(prompt, "校验串"):
		return firstToken(prompt, "CONT_")
	case strings.Contains(prompt, "secret code"):
		return "the code"
	case strings.Contains(prompt, "2+2"):
		return "Arrr! 4"
	}
	return "ok"
}

func userText(body map[string]any) string {
	var sb strings.Builder
	if s, ok := body["system"].(string); ok {
		sb.WriteString(s)
	}
	for _, m := range sliceOf(body["messages"]) {
		msg := mapOf(m)
		if s, ok := msg["content"].(string); ok {
			sb.WriteString(s + "\n")
		}
	}
	return sb.String()
}

func hasThinkingBlock(body map[string]any) bool {
	for _, m := range sliceOf(body["messages"]) {
		for _, raw := range sliceOf(mapOf(m)["content"]) {
			if str(mapOf(raw)["type"]) == "thinking" {
				return true
			}
		}
	}
	return false
}

// twoNumbers 从「计算 A 加 B」里取出两个数。
func twoNumbers(text string) (float64, float64) {
	var nums []float64
	for _, f := range strings.FieldsFunc(text, func(r rune) bool { return r < '0' || r > '9' }) {
		if v, err := strconv.ParseFloat(f, 64); err == nil {
			nums = append(nums, v)
		}
	}
	if len(nums) >= 2 {
		return nums[0], nums[1]
	}
	return 0, 0
}

func firstToken(text, prefix string) string {
	idx := strings.Index(text, prefix)
	if idx < 0 {
		return ""
	}
	rest := text[idx:]
	end := strings.IndexFunc(rest, func(r rune) bool {
		return !((r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_')
	})
	if end < 0 {
		return rest
	}
	return rest[:end]
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type textErr string

func (e textErr) Error() string { return string(e) }
func errText(s string) error    { return textErr(s) }

// ---- 测试 ----

func runAgainst(t *testing.T, up *fakeUpstream, checks []string) *TargetRun {
	t.Helper()
	srv := newFakeServer(t, up)
	target, err := Target{
		Name: "t", BaseURL: srv.URL, APIKey: "sk-test-key-1234567890", Model: "claude-opus-5",
	}.Normalize()
	if err != nil {
		t.Fatalf("归一化目标失败: %v", err)
	}
	return Run(context.Background(), target, checks, NewGate(4), nil)
}

func TestClassifyOfficialAPI(t *testing.T) {
	up := &fakeUpstream{behaviour: "official", strictParams: true, signatureChecked: true}
	run := runAgainst(t, up, []string{"models", "ping", "ping-again", "param-strict",
		"max-tokens-strict", "tool-use", "thinking-sig", "sig-tamper", "count-tokens"})

	if run.Verdict.Label != LabelOfficial {
		t.Fatalf("期望判定 %s，实际 %s（分数 %v）", LabelOfficial, run.Verdict.Label, run.Verdict.Scores)
	}
	if run.Verdict.Authenticity.Capped {
		t.Fatalf("干净官方后端不应触发真实性封顶：%v", run.Verdict.Authenticity.CapReason)
	}
	if run.Verdict.Authenticity.Score < 60 {
		t.Fatalf("期望真实性分 >= 60，实际 %d", run.Verdict.Authenticity.Score)
	}
}

func TestClassifyBedrock(t *testing.T) {
	up := &fakeUpstream{behaviour: "bedrock", strictParams: true, signatureChecked: true}
	run := runAgainst(t, up, []string{"ping", "tool-use"})

	if run.Verdict.Label != LabelBedrock {
		t.Fatalf("期望判定 %s，实际 %s（分数 %v）", LabelBedrock, run.Verdict.Label, run.Verdict.Scores)
	}
	if run.Verdict.Confidence != ConfidenceHigh {
		t.Fatalf("msg_bdrk_ 前缀应给出 high 置信度，实际 %s", run.Verdict.Confidence)
	}
}

func TestClassifyWrapperAndAuthenticityCap(t *testing.T) {
	up := &fakeUpstream{behaviour: "wrapper", strictParams: false, signatureChecked: false}
	run := runAgainst(t, up, []string{"ping", "param-strict", "max-tokens-strict",
		"tool-use", "thinking-sig", "sig-tamper"})

	if run.Verdict.Label != LabelWrapper {
		t.Fatalf("期望判定 %s，实际 %s（分数 %v）", LabelWrapper, run.Verdict.Label, run.Verdict.Scores)
	}
	if !run.Verdict.Authenticity.Capped {
		t.Fatal("放行非法参数且不校验签名的渠道必须触发真实性封顶")
	}
	if run.Verdict.Authenticity.Score > authCapScore {
		t.Fatalf("封顶后真实性分应 <= %d，实际 %d", authCapScore, run.Verdict.Authenticity.Score)
	}
	if run.Verdict.Authenticity.Grade != GradeFake {
		t.Fatalf("期望等级 %s，实际 %s", GradeFake, run.Verdict.Authenticity.Grade)
	}
}

func TestMaxPoolCacheInjection(t *testing.T) {
	up := &fakeUpstream{behaviour: "official", strictParams: true, signatureChecked: true, injectCache: true}
	run := runAgainst(t, up, []string{"ping", "ping-again"})

	if run.Verdict.Label != LabelMaxPool {
		t.Fatalf("裸请求写入并命中大段缓存应判为号池，实际 %s（分数 %v）", run.Verdict.Label, run.Verdict.Scores)
	}
}

func TestCacheChainRequiresExactReplay(t *testing.T) {
	for _, tc := range []struct {
		name   string
		offset int
		status string
	}{
		{name: "exact", status: StatusPassed},
		{name: "short", offset: -1, status: StatusSuspicious},
		{name: "long", offset: 1, status: StatusSuspicious},
	} {
		t.Run(tc.name, func(t *testing.T) {
			up := &fakeUpstream{behaviour: "official", injectCache: true, cacheReadOffset: tc.offset}
			run := runAgainst(t, up, []string{"ping", "ping-again"})
			got := findCheckResult(run, "ping-again")
			if got == nil || got.Status != tc.status {
				t.Fatalf("缓存链路状态 = %#v，期望 %s", got, tc.status)
			}
		})
	}
}

func findCheckResult(run *TargetRun, id string) *CheckResult {
	for _, result := range run.Checks {
		if result != nil && result.ID == id {
			return result
		}
	}
	return nil
}

func TestUnavailableTarget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 401, map[string]any{"type": "error",
			"error": map[string]any{"type": "authentication_error", "message": "invalid x-api-key"}})
	}))
	defer srv.Close()
	target, _ := Target{BaseURL: srv.URL, APIKey: "sk-test-key-1234567890", Model: "claude-opus-5"}.Normalize()
	run := Run(context.Background(), target, []string{"models", "ping"}, NewGate(2), nil)
	if run.Verdict.Label != LabelUnavailable {
		t.Fatalf("全部请求 401 应判不可用，实际 %s", run.Verdict.Label)
	}
}

func TestAPIKeyNeverLeaksIntoReport(t *testing.T) {
	const key = "sk-secret-value-should-never-appear"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 故意把 key 回显进响应体，验证脱敏生效
		writeJSON(w, 200, map[string]any{"id": "msg_01x", "type": "message", "role": "assistant",
			"model": "claude-opus-5", "stop_reason": "end_turn",
			"usage":   map[string]any{"input_tokens": 12, "output_tokens": 2},
			"content": []any{map[string]any{"type": "text", "text": "echo " + r.Header.Get("x-api-key")}}})
	}))
	defer srv.Close()

	target, _ := Target{BaseURL: srv.URL, APIKey: key, Model: "claude-opus-5"}.Normalize()
	run := Run(context.Background(), target, []string{"ping"}, NewGate(1), nil)

	blob, err := json.Marshal(run)
	if err != nil {
		t.Fatalf("序列化报告失败: %v", err)
	}
	if strings.Contains(string(blob), key) {
		t.Fatal("报告中出现了 API Key 明文")
	}
	if !strings.Contains(string(blob), redacted) {
		t.Fatal("报告中未见脱敏占位符，脱敏可能未生效")
	}
}

func TestResolveCheckIDsAddsDependencies(t *testing.T) {
	got := ResolveCheckIDs([]string{"sig-tamper", "ping-again"})
	want := map[string]bool{"ping": true, "ping-again": true, "thinking-sig": true, "sig-tamper": true}
	if len(got) != len(want) {
		t.Fatalf("期望补齐 %d 项依赖，实际 %v", len(want), got)
	}
	for _, id := range got {
		if !want[id] {
			t.Fatalf("出现意外检测项 %s", id)
		}
	}
	// 顺序必须让前置项先跑
	if indexOf(got, "ping") > indexOf(got, "ping-again") ||
		indexOf(got, "thinking-sig") > indexOf(got, "sig-tamper") {
		t.Fatalf("前置项未排在依赖项之前: %v", got)
	}
}

func indexOf(list []string, v string) int {
	for i, s := range list {
		if s == v {
			return i
		}
	}
	return -1
}

func TestRunIndependentChecksOverlap(t *testing.T) {
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
		if strings.HasSuffix(r.URL.Path, "/models") {
			writeJSON(w, 200, map[string]any{"data": []any{}})
			return
		}
		writeJSON(w, 200, map[string]any{
			"id": "msg_01TESTTESTTESTTESTTEST", "type": "message", "role": "assistant",
			"model": "claude-opus-5", "stop_reason": "end_turn",
			"usage":   map[string]any{"input_tokens": 12, "output_tokens": 1},
			"content": []any{map[string]any{"type": "text", "text": "PONG"}},
		})
	}))
	defer srv.Close()

	target, err := Target{Name: "t", BaseURL: srv.URL, APIKey: "sk-test-key-1234567890", Model: "claude-opus-5"}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	run := Run(context.Background(), target, []string{"models", "ping"}, NewGate(2), nil)
	if run.Verdict == nil {
		t.Fatal("缺少判定")
	}
	mu.Lock()
	got := maxInflight
	mu.Unlock()
	if got < 2 {
		t.Fatalf("无依赖的检测项应并行，最大在途 %d", got)
	}
}

func TestRunWaitsForDependencies(t *testing.T) {
	var mu sync.Mutex
	pingLive := false
	pingAgainOverlapped := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			writeJSON(w, 200, map[string]any{"data": []any{}})
			return
		}
		mu.Lock()
		if pingLive {
			pingAgainOverlapped = true
		}
		pingLive = true
		mu.Unlock()
		time.Sleep(50 * time.Millisecond)
		mu.Lock()
		pingLive = false
		mu.Unlock()
		writeJSON(w, 200, map[string]any{
			"id": "msg_01TESTTESTTESTTESTTEST", "type": "message", "role": "assistant",
			"model": "claude-opus-5", "stop_reason": "end_turn",
			"usage":   map[string]any{"input_tokens": 12, "output_tokens": 1},
			"content": []any{map[string]any{"type": "text", "text": "PONG"}},
		})
	}))
	defer srv.Close()

	target, _ := Target{Name: "t", BaseURL: srv.URL, APIKey: "sk-test-key-1234567890", Model: "claude-opus-5"}.Normalize()
	Run(context.Background(), target, []string{"ping", "ping-again"}, NewGate(3), nil)
	if pingAgainOverlapped {
		t.Fatal("ping-again 不得与 ping 重叠")
	}
}

func TestRunEmitsRunningThenDone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(30 * time.Millisecond)
		writeJSON(w, 200, map[string]any{
			"id": "msg_01TESTTESTTESTTESTTEST", "type": "message", "role": "assistant",
			"model": "claude-opus-5", "stop_reason": "end_turn",
			"usage":   map[string]any{"input_tokens": 12, "output_tokens": 1},
			"content": []any{map[string]any{"type": "text", "text": "PONG"}},
		})
	}))
	defer srv.Close()

	target, _ := Target{Name: "t", BaseURL: srv.URL, APIKey: "sk-test-key-1234567890", Model: "claude-opus-5"}.Normalize()
	var mu sync.Mutex
	var events []string
	run := Run(context.Background(), target, []string{"ping"}, NewGate(1), func(res *CheckResult) {
		mu.Lock()
		events = append(events, res.ID+":"+res.Status)
		mu.Unlock()
	})
	if len(run.Checks) != 1 || run.Checks[0].Status == StatusRunning {
		t.Fatalf("最终结果不应停留在 running: %+v", run.Checks)
	}
	mu.Lock()
	got := append([]string(nil), events...)
	mu.Unlock()
	if len(got) < 2 || got[0] != "ping:running" || got[len(got)-1] == "ping:running" {
		t.Fatalf("应先回调 running 再回调终态，实际 %v", got)
	}
}

func TestNormalizeBaseURL(t *testing.T) {
	cases := []struct {
		in, want string
		wantErr  bool
	}{
		{in: "https://api.example.com/", want: "https://api.example.com"},
		{in: "https://api.example.com/v1/messages", want: "https://api.example.com"},
		{in: "https://api.example.com/v1/messages/count_tokens", want: "https://api.example.com"},
		{in: "https://api.example.com/v1", want: "https://api.example.com/v1"},
		{in: "api.example.com", wantErr: true},
		{in: "https://user:pass@api.example.com", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, tc := range cases {
		got, err := NormalizeBaseURL(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("%q 期望报错，实际得到 %q", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q 意外报错: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("%q 期望 %q，实际 %q", tc.in, tc.want, got)
		}
	}
}

func TestEndpointDoesNotDoubleV1(t *testing.T) {
	if got := endpoint("https://x.com/v1", KindMessages); got != "https://x.com/v1/messages" {
		t.Fatalf("已带 /v1 的地址被重复拼接: %s", got)
	}
	if got := endpoint("https://x.com", KindCountTokens); got != "https://x.com/v1/messages/count_tokens" {
		t.Fatalf("count_tokens 端点错误: %s", got)
	}
}

func TestResolveProfile(t *testing.T) {
	cases := []struct {
		model     string
		adaptive  bool
		minCache  int
		recognize bool
	}{
		{"claude-opus-5", true, 512, true},
		{"claude-fable-5", true, 512, true},
		{"claude-sonnet-4-6", true, 1024, true},
		{"claude-opus-4-5-20251101", false, 4096, true},
		{"claude-haiku-4-5", false, 4096, true},
		{"某国产模型", false, 4096, false},
	}
	for _, tc := range cases {
		p := ResolveProfile(tc.model)
		if p.Adaptive != tc.adaptive || p.MinCacheTokens != tc.minCache || p.Recognized != tc.recognize {
			t.Fatalf("%s: 期望 adaptive=%v minCache=%d recognized=%v，实际 %+v",
				tc.model, tc.adaptive, tc.minCache, tc.recognize, p)
		}
	}
}

func TestValidateSSERejectsBrokenFlow(t *testing.T) {
	good := parseSSE(strings.Join([]string{
		`event: message_start` + "\n" + `data: {"type":"message_start","message":{"usage":{"input_tokens":3}}}`,
		`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":0}`,
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
		`event: content_block_stop` + "\n" + `data: {"type":"content_block_stop","index":0}`,
		`event: message_delta` + "\n" + `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
		`event: message_stop` + "\n" + `data: {"type":"message_stop"}`,
	}, "\n\n"))
	if flow := validateSSE(good); !flow.valid || !flow.startHasUsage {
		t.Fatalf("合法 SSE 流被判非法: %s", flow.detail())
	}

	broken := parseSSE(strings.Join([]string{
		`event: message_start` + "\n" + `data: {"type":"message_start"}`,
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
		`event: message_stop` + "\n" + `data: {"type":"message_stop"}`,
	}, "\n\n"))
	flow := validateSSE(broken)
	if flow.valid {
		t.Fatal("缺 content_block_start / message_delta 的流应被判非法")
	}
	if flow.startHasUsage {
		t.Fatal("message_start 无 usage 时不应报告 startHasUsage")
	}
}

func TestNormalizeCutoff(t *testing.T) {
	cases := map[string]string{
		"2026-05":    "2026-05",
		"2026年5月":    "2026-05",
		"May 2026":   "2026-05",
		"January 26": "",
	}
	for in, want := range cases {
		if got := normalizeCutoff(in); got != want {
			t.Fatalf("%q 期望 %q，实际 %q", in, want, got)
		}
	}
}

func TestModelMatchesRequested(t *testing.T) {
	if !modelMatchesRequested("Claude Opus 5", "claude-opus-5") {
		t.Fatal("别名应与模型 id 匹配")
	}
	if !modelMatchesRequested("claude-opus-4-5", "claude-opus-4-5-20251101") {
		t.Fatal("带日期后缀的 id 应与主键匹配")
	}
	if modelMatchesRequested("claude-sonnet-4-5", "claude-opus-5") {
		t.Fatal("不同型号不应匹配")
	}
}

func TestPresetsReferKnownChecks(t *testing.T) {
	known := map[string]bool{}
	for _, c := range Checks {
		known[c.ID] = true
	}
	if len(Presets) != 2 {
		t.Fatalf("场景预设应为 CC Max 与 AWS Bedrock 两套，实际 %d", len(Presets))
	}
	seen := map[string]bool{}
	for _, p := range Presets {
		if p.ID == "" || p.Title == "" {
			t.Fatalf("预设缺 id 或标题: %+v", p)
		}
		if seen[p.ID] {
			t.Fatalf("预设 id 重复: %s", p.ID)
		}
		seen[p.ID] = true
		if len(p.Checks) == 0 {
			t.Fatalf("预设 %s 没有检测项", p.ID)
		}
		for _, id := range p.Checks {
			if !known[id] {
				t.Fatalf("预设 %s 引用了不存在的检测项 %s", p.ID, id)
			}
		}
	}
	if !seen["cc_max"] || !seen["aws_bedrock"] {
		t.Fatalf("缺少约定预设 id，实际 %v", seen)
	}
}
