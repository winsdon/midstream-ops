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

func TestEmbeddedFableSignatureSample(t *testing.T) {
	s, ok := lookupSignatureSample("claude-fable-5-20260101")
	if !ok || s.ThinkingTokens < 200 || s.Question == "" {
		t.Fatalf("应能按日期后缀退回主键找到 Fable 5 样本，实际 %v %+v", ok, s)
	}
	var sig string
	for _, b := range s.Content {
		if str(b["type"]) == "thinking" {
			sig = str(b["signature"])
		}
	}
	if sh := ParseSignatureShape(sig); sh.Model != "claude-fable-5" {
		t.Fatalf("样本签名应由 claude-fable-5 签发，实际 %+v", sh)
	}
	if _, ok := lookupSignatureSample("claude-opus-4-7"); ok {
		t.Fatal("没有样本的型号不应命中")
	}
}

// crossVerifyServer 按「带不带 thinking 块」分别回应：reject=true 时带 thinking 的请求报签名无效。
func crossVerifyServer(t *testing.T, reject bool, thinkingInput int) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		msgs := sliceOf(body["messages"])
		withThinking := strings.Contains(mustJSON(msgs[1]), `"thinking"`)
		ask := str(mapOf(msgs[2])["content"])
		w.Header().Set("Content-Type", "application/json")
		if withThinking && reject {
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]any{"type": "error", "error": map[string]any{
				"type": "invalid_request_error", "message": "messages.1.content.0: Invalid `signature` in `thinking` block"}})
			return
		}
		in := 700
		if withThinking {
			in += thinkingInput
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"type": "message", "stop_reason": "end_turn",
			"content": []any{map[string]any{"type": "text", "text": strings.TrimSuffix(strings.TrimPrefix(ask, "只回复 "), "。")}},
			"usage":   map[string]any{"input_tokens": in, "output_tokens": 8}})
	}))
	t.Cleanup(srv.Close)
	return NewClient(Target{BaseURL: srv.URL, APIKey: "sk-test-key-value", Model: "claude-fable-5", Timeout: 5 * time.Second})
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// TestCrossVerifyRejectsForgedBackend 实测 claude神：真签名以 Invalid signature 被拒。
func TestCrossVerifyRejectsForgedBackend(t *testing.T) {
	r := checkSignatureCrossVerify(context.Background(), crossVerifyServer(t, true, 0), &runState{})
	if r.AuthCapReason == "" || !hasEvidenceKey(r, "sig_foreign_rejected") {
		t.Fatalf("拒绝真签名应封顶，实际 %s %q", r.Status, r.Summary)
	}
}

// TestCrossVerifyAcceptsRealBackend 实测 yimo / pet：带思考比去思考多出约一份思考。
func TestCrossVerifyAcceptsRealBackend(t *testing.T) {
	r := checkSignatureCrossVerify(context.Background(), crossVerifyServer(t, false, 780), &runState{})
	if r.AuthScore != 40 || r.AuthCapReason != "" {
		t.Fatalf("真签名通过校验应 +40，实际 %d %q", r.AuthScore, r.Summary)
	}
}

// TestCrossVerifyStrippedThinkingIsInconclusive 网关剥掉 thinking 再转发：没被拒，也证明不了什么。
func TestCrossVerifyStrippedThinkingIsInconclusive(t *testing.T) {
	r := checkSignatureCrossVerify(context.Background(), crossVerifyServer(t, false, 0), &runState{})
	if r.Status != StatusInconclusive || r.AuthScore != 0 || r.AuthCapReason != "" {
		t.Fatalf("thinking 没进上下文应记证据不足，实际 %s %d", r.Status, r.AuthScore)
	}
}

// tokenizerServer 按文本序号回固定的输入量。
func tokenizerServer(t *testing.T, totals []int) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		text := str(mapOf(sliceOf(body["messages"])[0])["content"])
		in := 0
		for i, probe := range tokenizerProbeTexts {
			if probe == text {
				in = totals[i]
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"type": "message", "stop_reason": "end_turn",
			"content": []any{map[string]any{"type": "text", "text": "ok"}},
			"usage":   map[string]any{"input_tokens": in, "output_tokens": 1}})
	}))
	t.Cleanup(srv.Close)
	return NewClient(Target{BaseURL: srv.URL, APIKey: "sk-test-key-value", Model: "claude-fable-5", Timeout: 5 * time.Second})
}

func TestTokenizerFingerprint(t *testing.T) {
	cases := []struct {
		name   string
		totals []int
		score  int
		capped bool
		status string
	}{
		{"真 Fable 5 裸请求", []int{60, 28, 33, 43}, 10, false, StatusPassed},
		{"真渠道注入 508 tokens（pet）", []int{568, 536, 541, 551}, 10, false, StatusPassed},
		{"已知假渠道（claude神）", []int{49, 34, 39, 44}, 0, true, StatusFailed},
		{"网关回固定计量", []int{33, 33, 33, 33}, 0, false, StatusInconclusive},
		{"注入量在请求之间变了", []int{60, 12396, 33, 43}, 0, false, StatusInconclusive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := checkTokenizer(context.Background(), tokenizerServer(t, tc.totals), &runState{})
			if r.AuthScore != tc.score || (r.AuthCapReason != "") != tc.capped || r.Status != tc.status {
				t.Fatalf("期望 %d/%v/%s，实际 %d/%q/%s（%s）", tc.score, tc.capped, tc.status, r.AuthScore, r.AuthCapReason, r.Status, r.Summary)
			}
		})
	}
}

func thinkingBody(checkID string, sigLen, thinking int) auditBody {
	b := sigLenBody(checkID, sigLen, thinking+200)
	b.body["usage"] = map[string]any{"input_tokens": float64(20), "output_tokens": float64(thinking + 200),
		"output_tokens_details": map[string]any{"thinking_tokens": float64(thinking)}}
	return b
}

// TestSignatureFloorCatchesForgedScheme 实测 claude神：思考 645 / 824 tokens，签名恒为 356 字符。
func TestSignatureFloorCatchesForgedScheme(t *testing.T) {
	r := &CheckResult{}
	auditSignatureFloor(r, []auditBody{thinkingBody("thinking-sig", 356, 645), thinkingBody("baseline-quality", 356, 824)})
	if r.AuthCapReason == "" || !hasEvidenceKey(r, "sig_too_short") {
		t.Fatalf("签名装不下思考应封顶，实际 %+v", r.Assertions)
	}
}

// TestSignatureFloorPassesRealSignatures 真渠道实测：pet 490→1836、384→1320，yimo 1348→4212，kaola 2322→6704。
func TestSignatureFloorPassesRealSignatures(t *testing.T) {
	r := &CheckResult{}
	auditSignatureFloor(r, []auditBody{
		thinkingBody("a", 1836, 490), thinkingBody("b", 1320, 384), thinkingBody("c", 4212, 1348),
		thinkingBody("d", 6704, 2322), thinkingBody("e", 724, 39),
	})
	if r.AuthCapReason != "" || hasEvidenceKey(r, "sig_too_short") {
		t.Fatalf("真签名不应判伪，实际 %+v", r.Assertions)
	}
}

// TestSignatureFloorDefersToPasting 贴签名的渠道里短签名是贴来的旧签名（实测 walk），不另封顶。
func TestSignatureFloorDefersToPasting(t *testing.T) {
	r := &CheckResult{}
	r.addEvidence("sig_pasted", "贴签名", "", ClassWrapper, 4)
	auditSignatureFloor(r, []auditBody{thinkingBody("baseline-quality", 3016, 2327)})
	if r.AuthCapReason != "" || hasEvidenceKey(r, "sig_too_short") {
		t.Fatalf("贴签名渠道不应另行封顶，实际 %q", r.AuthCapReason)
	}
}

// TestGradeInsufficientWhenNothingConcluded 实测 pet：整轮大面积 502，计分项几乎都是证据不足。
func TestGradeInsufficientWhenNothingConcluded(t *testing.T) {
	ok := []*Exchange{{Status: 200}}
	v := Classify([]*CheckResult{
		{ID: "sig-crossverify", Status: StatusInconclusive, Exchanges: ok},
		{ID: "tool-use", Status: StatusInconclusive, Exchanges: ok},
		{ID: "baseline-quality", Status: StatusSuspicious, Exchanges: ok},
	})
	if v.Authenticity.Grade != GradeInsufficient {
		t.Fatalf("几乎没有结论时应判证据不足，实际 %s", v.Authenticity.Grade)
	}
	capped := Classify([]*CheckResult{{ID: "sig-crossverify", Status: StatusFailed, Exchanges: ok, AuthCapReason: "拒绝真签名"}})
	if capped.Authenticity.Grade != GradeFake {
		t.Fatalf("有封顶证据时仍应判伪，实际 %s", capped.Authenticity.Grade)
	}
}

// refusalTokenizerServer 第一段拒答（usage 照常计量），其余正常回复。
func refusalTokenizerServer(t *testing.T, totals []int) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		text := str(mapOf(sliceOf(body["messages"])[0])["content"])
		resp := map[string]any{"type": "message", "stop_reason": "end_turn",
			"content": []any{map[string]any{"type": "text", "text": "ok"}}}
		for i, probe := range tokenizerProbeTexts {
			if probe == text {
				resp["usage"] = map[string]any{"input_tokens": totals[i], "output_tokens": 1}
				if i == 0 {
					resp["stop_reason"], resp["content"] = "refusal", []any{}
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return NewClient(Target{BaseURL: srv.URL, APIKey: "sk-test-key-value", Model: "claude-fable-5", Timeout: 5 * time.Second})
}

// TestTokenizerUsesRefusalUsage 实测 xigua：第一段被按 cyber 拒答，usage 仍是一字不差的 60。
func TestTokenizerUsesRefusalUsage(t *testing.T) {
	r := checkTokenizer(context.Background(), refusalTokenizerServer(t, []int{60, 28, 33, 43}), &runState{})
	if r.AuthScore != 10 || r.Status != StatusPassed {
		t.Fatalf("拒答的计量对上也应加分，实际 %d %s %q", r.AuthScore, r.Status, r.Summary)
	}
	bad := checkTokenizer(context.Background(), refusalTokenizerServer(t, []int{31, 28, 33, 43}), &runState{})
	if bad.AuthCapReason != "" || bad.Status != StatusInconclusive {
		t.Fatalf("有拒答参与时对不上不应定罪（可能是缓存回放的假计量），实际 %q %s", bad.AuthCapReason, bad.Status)
	}
}

// TestCrossVerifyRetriesWhenThinkingStripped 号池第一轮剥掉 thinking、第二轮送达（实测 xigua：差 2 → 770）。
func TestCrossVerifyRetriesWhenThinkingStripped(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		msgs := sliceOf(body["messages"])
		calls++
		in := 776
		if strings.Contains(mustJSON(msgs[1]), `"thinking"`) && calls > 2 {
			in += 770
		}
		ask := str(mapOf(msgs[2])["content"])
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"type": "message", "stop_reason": "end_turn",
			"content": []any{map[string]any{"type": "text", "text": strings.TrimSuffix(strings.TrimPrefix(ask, "只回复 "), "。")}},
			"usage":   map[string]any{"input_tokens": in, "output_tokens": 8}})
	}))
	defer srv.Close()
	c := NewClient(Target{BaseURL: srv.URL, APIKey: "sk-test-key-value", Model: "claude-fable-5", Timeout: 5 * time.Second})
	r := checkSignatureCrossVerify(context.Background(), c, &runState{})
	if r.AuthScore != 40 || len(r.Exchanges) != 4 {
		t.Fatalf("第二轮送达应 +40 且共发 4 个请求，实际 %d / %d（%s）", r.AuthScore, len(r.Exchanges), r.Summary)
	}
}
