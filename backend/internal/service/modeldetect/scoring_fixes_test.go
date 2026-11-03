package modeldetect

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// cannedExchange 一次 200 响应：固定正文与输入计量。
func cannedExchange(text string, input int) *Exchange {
	body := map[string]any{"type": "message", "stop_reason": "end_turn",
		"content": []any{map[string]any{"type": "text", "text": text}},
		"usage":   map[string]any{"input_tokens": float64(input), "output_tokens": float64(1)}}
	return &Exchange{Status: 200, Kind: KindMessages, JSON: body}
}

// TestCannedShortRepliesDetectsGatewayIntercept 实测 #107：1/20/100 个数字和人设题全回「OK」、input 恒为 33。
func TestCannedShortRepliesDetectsGatewayIntercept(t *testing.T) {
	exs := []*Exchange{cannedExchange("OK", 33), cannedExchange("OK", 33), cannedExchange("OK", 33), cannedExchange("OK", 33)}
	note, ok := cannedShortReplies(exs)
	if !ok || !strings.Contains(note, "OK") || !strings.Contains(note, "33") {
		t.Fatalf("应识别为网关固定回复，实际 ok=%v note=%q", ok, note)
	}
}

func TestCannedShortRepliesIgnoresRealReplies(t *testing.T) {
	cases := map[string][]*Exchange{
		"计量随长度增长": {cannedExchange("OK", 8), cannedExchange("OK", 46), cannedExchange("OK", 206), cannedExchange("OK", 40)},
		"正文不同":    {cannedExchange("0", 33), cannedExchange("OK", 33)},
		"有请求失败":   {cannedExchange("OK", 33), {Status: 502}},
		"只有一条":    {cannedExchange("OK", 33)},
	}
	for name, exs := range cases {
		if _, ok := cannedShortReplies(exs); ok {
			t.Fatalf("%s：不应判为固定回复", name)
		}
	}
}

// TestCannedRepliesVoidPersonaSignal 固定回复时人设题作废，不能报「调用方 system 被覆盖」。
func TestCannedRepliesVoidPersonaSignal(t *testing.T) {
	exs := make([]*Exchange, zeroInjectionRequests)
	for i := range exs {
		exs[i] = cannedExchange("PONG", 18)
	}
	for i := ziDigitsAt; i <= ziSystemAt; i++ {
		exs[i] = cannedExchange("OK", 33)
	}
	o := observeZeroInjection(exs)
	if o.canned == "" || o.systemOK || o.digitsOK != [3]bool{} {
		t.Fatalf("固定回复应作废数字序列与人设题，实际 %+v", o)
	}
	r := &CheckResult{}
	judgeZeroInjection(r, o, injectionBaseline{})
	for _, a := range r.Assertions {
		if strings.Contains(a.Label, "遵循调用方 system") {
			t.Fatalf("固定回复下不应再判人设：%+v", a)
		}
	}
}

func TestSignatureReplayDeductsInRun(t *testing.T) {
	up := &fakeUpstream{behaviour: "official", strictParams: false, signature: "SIGVALID_replayed_same_signature"}
	run := runAgainst(t, up, []string{"thinking-sig", "param-strict"})
	res := auditOf(t, run)
	if !hasEvidenceKey(res, "sig_replayed") || res.AuthScore != -sigReplayPenalty {
		t.Fatalf("同一签名出现在多个请求里应扣分，实际 score=%d evidence=%+v", res.AuthScore, res.Evidence)
	}
	if run.Verdict.Authenticity.Capped {
		t.Fatal("回放扣分不应封顶成伪造")
	}
}

func TestAuthenticityScoreNeverNegative(t *testing.T) {
	v := Classify([]*CheckResult{{ID: AuditCheckID, Status: StatusFailed, AuthScore: -sigReplayPenalty,
		Exchanges: []*Exchange{{Status: 200}}}})
	if v.Authenticity.Score != 0 {
		t.Fatalf("真实性评分不应为负，实际 %d", v.Authenticity.Score)
	}
}

// TestBaselineQualityPassesNormalReply 回归：质量判定曾在正文填入前计算，永远判失败。
func TestBaselineQualityPassesNormalReply(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type": "message", "model": "claude-fable-5", "stop_reason": "end_turn",
			"content": []any{map[string]any{"type": "text", "text": "因为皮亚诺公理……"}},
			"usage": map[string]any{"input_tokens": 20, "output_tokens": 1500,
				"output_tokens_details": map[string]any{"thinking_tokens": 70}},
		})
	}))
	defer srv.Close()
	st := &runState{baseline: &BaselineStats{Model: "claude-fable-5", QualityOK: true, OutputTokens: 1504, ThinkingTokens: 69}}
	c := NewClient(Target{BaseURL: srv.URL, APIKey: "sk-test-key-value", Model: "claude-fable-5", Timeout: 5 * time.Second})
	r := checkBaselineQuality(context.Background(), c, st)
	if r.AuthScore != 30 {
		t.Fatalf("统计落在基准范围内且正常结束应 +30，实际 %d（%s）", r.AuthScore, r.Summary)
	}
}

// TestHelloEntropyRefusalsAreInconclusive 实测 2026-11-03：kaola / pet 的 Fable 5 把 Hello 一律按 cyber 拒答，
// 拒答没有正文，曾被算成「0 种不同回复」判失败。
func TestHelloEntropyRefusalsAreInconclusive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type": "message", "model": "claude-fable-5", "stop_reason": "refusal", "content": []any{},
			"stop_details": map[string]any{"type": "refusal", "category": "cyber"},
			"usage":        map[string]any{"input_tokens": 2, "output_tokens": 0},
		})
	}))
	defer srv.Close()
	c := NewClient(Target{BaseURL: srv.URL, APIKey: "sk-test-key-value", Model: "claude-fable-5", Timeout: 5 * time.Second})
	r := checkHelloEntropy(context.Background(), c, &runState{})
	if r.Status != StatusInconclusive || len(r.Evidence) != 0 {
		t.Fatalf("拒答应记证据不足且不记模板化证据，实际 %s %+v", r.Status, r.Evidence)
	}
	if !strings.Contains(r.Summary, "拒答") {
		t.Fatalf("摘要应说明被拒答，实际 %q", r.Summary)
	}
}
