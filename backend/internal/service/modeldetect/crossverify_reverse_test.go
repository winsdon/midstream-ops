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

// stripTarget 被测目标：会出思考题（自报 thinking 800 tokens、带签名），续写时把别人的 thinking 剥掉（实测 xigua）。
func stripTarget(t *testing.T) Target {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		msgs := sliceOf(body["messages"])
		w.Header().Set("Content-Type", "application/json")
		if len(msgs) == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{"type": "message", "id": "msg_01TargetIssue", "stop_reason": "end_turn",
				"content": []any{
					map[string]any{"type": "thinking", "thinking": "", "signature": strings.Repeat("S", 2500)},
					map[string]any{"type": "text", "text": "ANSWER: 7"},
				},
				"usage": map[string]any{"input_tokens": 80, "output_tokens": 900,
					"output_tokens_details": map[string]any{"thinking_tokens": 800}}})
			return
		}
		ask := str(mapOf(msgs[2])["content"])
		_ = json.NewEncoder(w).Encode(map[string]any{"type": "message", "stop_reason": "end_turn",
			"content": []any{map[string]any{"type": "text", "text": strings.TrimSuffix(strings.TrimPrefix(ask, "只回复 "), "。")}},
			"usage":   map[string]any{"input_tokens": 776, "output_tokens": 8}})
	}))
	t.Cleanup(srv.Close)
	return Target{Name: "xigua", BaseURL: srv.URL, APIKey: "sk-target-key-value", Model: "claude-fable-5", Timeout: 5 * time.Second}
}

// referenceServer 真实基准账号：mode=accept 时带思考多出 gap 个输入，mode=reject 时以签名无效拒绝。
func referenceServer(t *testing.T, mode string, gap int) Target {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		msgs := sliceOf(body["messages"])
		withThinking := strings.Contains(mustJSON(msgs[1]), `"thinking"`)
		w.Header().Set("Content-Type", "application/json")
		if withThinking && mode == "reject" {
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]any{"type": "error", "error": map[string]any{
				"type": "invalid_request_error", "message": "messages.1.content.0: Invalid `signature` in `thinking` block"}})
			return
		}
		in := 600
		if withThinking {
			in += gap
		}
		ask := str(mapOf(msgs[2])["content"])
		_ = json.NewEncoder(w).Encode(map[string]any{"type": "message", "id": "msg_01ReferenceSameId", "stop_reason": "end_turn",
			"content": []any{map[string]any{"type": "text", "text": strings.TrimSuffix(strings.TrimPrefix(ask, "只回复 "), "。")}},
			"usage":   map[string]any{"input_tokens": in, "output_tokens": 8}})
	}))
	t.Cleanup(srv.Close)
	return Target{Name: "yimo", BaseURL: srv.URL, APIKey: "sk-reference-key", Model: "claude-opus-5", Timeout: 5 * time.Second}
}

func runCrossVerify(t *testing.T, target Target, ref *Target) *CheckResult {
	t.Helper()
	st := &runState{baseline: &BaselineStats{Reference: ref}}
	return checkSignatureCrossVerify(context.Background(), NewClient(target), st)
}

// TestReverseCrossVerifyPasses 实测 xigua：正向被中转剥掉，它签的签名在真渠道上验证通过、思考量一致。
func TestReverseCrossVerifyPasses(t *testing.T) {
	ref := referenceServer(t, "accept", 802)
	r := runCrossVerify(t, stripTarget(t), &ref)
	if r.AuthScore != 40 || r.AuthCapReason != "" || r.Status != StatusPassed {
		t.Fatalf("反向通过应 +40，实际 %d %s %q", r.AuthScore, r.Status, r.Summary)
	}
	refCalls := 0
	for _, ex := range r.Exchanges {
		if ex.Reference {
			refCalls++
			if !strings.HasPrefix(ex.URL, ref.BaseURL) {
				t.Fatalf("标了基准的请求应发往基准账号：%s", ex.URL)
			}
			if body, _ := ex.RequestBody.(map[string]any); str(body["model"]) != "claude-fable-5" {
				t.Fatalf("基准账号应按被测目标的型号验证，实际 %v", body["model"])
			}
		}
	}
	if refCalls != 2 {
		t.Fatalf("应向基准账号发 2 个续写请求，实际 %d", refCalls)
	}
}

func TestReverseCrossVerifyRejected(t *testing.T) {
	ref := referenceServer(t, "reject", 0)
	r := runCrossVerify(t, stripTarget(t), &ref)
	if r.AuthCapReason == "" || !strings.Contains(r.AuthCapReason, "反向") {
		t.Fatalf("目标的签名被真实基准拒绝应封顶，实际 %q", r.AuthCapReason)
	}
}

// TestReverseCrossVerifyPastedSignature 签名里装的思考远多于目标自报：贴来的别处签名，不给分。
func TestReverseCrossVerifyPastedSignature(t *testing.T) {
	ref := referenceServer(t, "accept", 3000)
	r := runCrossVerify(t, stripTarget(t), &ref)
	if r.AuthScore != 0 || r.Status != StatusInconclusive {
		t.Fatalf("思考量对不上应记证据不足，实际 %d %s", r.AuthScore, r.Status)
	}
}

func TestCrossVerifyWithoutReferenceIsInconclusive(t *testing.T) {
	r := runCrossVerify(t, stripTarget(t), nil)
	if r.Status != StatusInconclusive || r.AuthScore != 0 {
		t.Fatalf("正向被剥、又没有基准时应记证据不足，实际 %s %d", r.Status, r.AuthScore)
	}
	for _, ex := range r.Exchanges {
		if ex.Reference {
			t.Fatal("没有基准账号时不应发出基准请求")
		}
	}
}

func TestCrossVerifySkipsSelfReference(t *testing.T) {
	target := stripTarget(t)
	self := target
	r := runCrossVerify(t, target, &self)
	for _, ex := range r.Exchanges {
		if ex.Reference {
			t.Fatal("基准就是被测目标时不应做反向验证")
		}
	}
}

// TestAuditIgnoresReferenceExchanges 基准账号的响应（这里两次同一个消息 id）不能算到被测渠道头上。
func TestAuditIgnoresReferenceExchanges(t *testing.T) {
	ref := referenceServer(t, "accept", 802)
	r := runCrossVerify(t, stripTarget(t), &ref)
	audit := AuditRun([]*CheckResult{r}, ResolveProfile("claude-fable-5"), time.Now().Add(-time.Minute))
	if audit != nil && hasEvidenceKey(audit, "msg_id_duplicate") {
		t.Fatalf("基准账号的响应不应进审计：%+v", audit.Evidence)
	}
}

// TestBaselineReferenceNeverSerialized 参照目标带着基准账号的密钥，绝不能进报告或接口响应。
func TestBaselineReferenceNeverSerialized(t *testing.T) {
	b := BaselineStats{Model: "claude-fable-5", Reference: &Target{BaseURL: "https://ref.example", APIKey: "sk-reference-secret"}}
	raw, _ := json.Marshal(b)
	if strings.Contains(string(raw), "sk-reference-secret") || strings.Contains(string(raw), "ref.example") {
		t.Fatalf("参照目标不应序列化：%s", raw)
	}
}
