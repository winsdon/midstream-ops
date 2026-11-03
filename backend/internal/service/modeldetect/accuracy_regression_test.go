package modeldetect

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// 本文件把 2026-09-28 那份报告（#78，【ant】ccm 0注入 / claude-opus-5-5）与随后代码审查
// 暴露的误判固化成回归用例：判据改回去就会红。

// ---- Kiro 归属：否认句不能算据为己有 ----

// ---- 参数校验：网关规范化请求不等于后端假；max_tokens=0 是官方预热 ----

func TestParamAcceptanceDoesNotCapGenuineBackend(t *testing.T) {
	up := &fakeUpstream{behaviour: "official", strictParams: false, signatureChecked: true}
	run := runAgainst(t, up, []string{"ping", "param-strict", "thinking-sig", "sig-tamper"})

	ps := findCheck(run.Checks, "param-strict")
	if ps == nil || ps.AuthCapReason != "" {
		t.Fatalf("参数放行不应再封顶真实性：%+v", ps)
	}
	assertNoWrapperEvidence(t, "参数校验严格性", ps)
	// 假上游把 max_tokens=0 按官方的缓存预热形态返回（零输出），这一项本就符合官方行为；
	// 另外两项被放行，拿不到分。关键是放行不产生任何负分证据。
	if ps.AuthScore >= 15 {
		t.Fatalf("被放行的硬校验不应得分，实际 %d", ps.AuthScore)
	}
	if !hasEvidenceKey(findCheck(run.Checks, "sig-tamper"), "signature_verified") {
		t.Fatal("签名校验应通过")
	}
	if run.Verdict.Authenticity.Capped || run.Verdict.Authenticity.Grade == GradeFake {
		t.Fatalf("签名级证据成立的后端不应判 fake：%+v", run.Verdict.Authenticity)
	}
}

// TestLooseParamsNeverJudgeWrapper 参数校验严格性只作加分项：非法参数被放行既不产生包装证据、
// 不加包装分、不判「结果可疑」，也不会因此落到 wrapper —— 放行是网关行为，不是后端身份。
func TestLooseParamsNeverJudgeWrapper(t *testing.T) {
	up := &fakeUpstream{behaviour: "official", strictParams: false, signatureChecked: true}
	run := runAgainst(t, up, []string{"param-strict"})

	ps := findCheck(run.Checks, "param-strict")
	if ps == nil {
		t.Fatal("缺少 param-strict 结果")
	}
	if ps.Status != StatusInconclusive {
		t.Fatalf("参数被放行应记「未观察到官方参数校验」，实际 %s", ps.Status)
	}
	if ps.AuthScore >= 15 {
		t.Fatalf("被放行的硬校验不应得分，实际 %d", ps.AuthScore)
	}
	assertNoWrapperEvidence(t, "参数校验严格性", ps)
	if run.Verdict == nil {
		t.Fatal("应给出判定")
	}
	if run.Verdict.Scores[ClassWrapper] != 0 || run.Verdict.Label == LabelWrapper {
		t.Fatalf("参数放行不得判包装：%s %v", run.Verdict.Label, run.Verdict.Scores)
	}
	if run.Verdict.Authenticity.Capped {
		t.Fatalf("参数放行不得封顶真实性：%v", run.Verdict.Authenticity.CapReason)
	}
}

// TestParamStrictIsBonusOnly 四项全被放行时：本项 0 分、状态「未观察到官方参数校验」、
// 不产出任何证据 —— 没有证据就没有包装分，也就不会把真后端判成 wrapper / fake。
func TestParamStrictIsBonusOnly(t *testing.T) {
	res := checkParamStrict(context.Background(), looseParamServer(t),
		&runState{profile: ResolveProfile("claude-opus-5-5")})

	if res.AuthScore != 0 {
		t.Fatalf("非法参数全被放行不应得分，实际 %d", res.AuthScore)
	}
	if res.Status != StatusInconclusive {
		t.Fatalf("应记证据不足（本项不加分），实际 %s", res.Status)
	}
	if len(res.Evidence) != 0 {
		t.Fatalf("参数严格性不应产出包装证据：%+v", res.Evidence)
	}
	if res.AuthCapReason != "" {
		t.Fatalf("参数放行不得封顶真实性：%s", res.AuthCapReason)
	}
}

// looseParamServer 网关把所有非法请求规范化后转发：一律 200，max_tokens=0 也被换成默认值生成输出。
func looseParamServer(t *testing.T) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"id": "msg_01ABC", "type": "message", "stop_reason": "end_turn",
			"content": []any{map[string]any{"type": "text", "text": "Hi there!"}},
			"usage":   map[string]any{"input_tokens": 8, "output_tokens": 44}})
	}))
	t.Cleanup(srv.Close)
	target, _ := Target{Name: "t", BaseURL: srv.URL, APIKey: "sk-test-key-1234567890", Model: "claude-opus-5-5"}.Normalize()
	return NewClient(target)
}

// assertNoWrapperEvidence 该检测项不得给出任何包装证据（含带权重的其他类别证据）。
func assertNoWrapperEvidence(t *testing.T, name string, res *CheckResult) {
	t.Helper()
	for _, ev := range res.Evidence {
		if ev.Class == ClassWrapper || ev.Weight > 0 {
			t.Fatalf("%s 出现包装证据：%+v", name, ev)
		}
	}
}

// paramServer 对 max_tokens=0 按 zeroReply 回应，其余非法请求一律 400。
func paramServer(t *testing.T, zeroReply map[string]any) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		if mt, ok := num(body["max_tokens"]); ok && mt == 0 {
			writeJSON(w, 200, zeroReply)
			return
		}
		writeJSON(w, 400, map[string]any{"type": "error", "error": map[string]any{
			"type": "invalid_request_error", "message": `"thinking.type.enabled" is not supported for this model.`}})
	}))
	t.Cleanup(srv.Close)
	target, _ := Target{Name: "t", BaseURL: srv.URL, APIKey: "sk-test-key-1234567890", Model: "claude-opus-5-5"}.Normalize()
	return NewClient(target)
}

func TestParamStrictMaxTokensZeroIsPrewarm(t *testing.T) {
	st := &runState{profile: ResolveProfile("claude-opus-5-5")}
	// 官方：空 content、stop_reason=max_tokens、零输出。
	prewarm := map[string]any{"id": "msg_01ABC", "type": "message", "content": []any{}, "stop_reason": "max_tokens",
		"usage": map[string]any{"input_tokens": 8, "output_tokens": 0}}
	res := checkParamStrict(context.Background(), paramServer(t, prewarm), st)
	if res.Status != StatusPassed || res.AuthScore != 15 || len(res.Evidence) > 0 {
		t.Fatalf("官方的缓存预热应算符合官方行为，实际 %s score=%d %+v", res.Status, res.AuthScore, res.Evidence)
	}
	// 网关把 max_tokens=0 换成了默认值：生成了输出。少得 5 分，但不产生包装证据。
	rewritten := map[string]any{"id": "msg_01ABC", "type": "message", "stop_reason": "end_turn",
		"content": []any{map[string]any{"type": "text", "text": "Hi there!"}},
		"usage":   map[string]any{"input_tokens": 8, "output_tokens": 44}}
	res = checkParamStrict(context.Background(), paramServer(t, rewritten), st)
	if res.Status != StatusInconclusive || res.AuthScore != 10 {
		t.Fatalf("max_tokens=0 却生成了输出应判未观察到官方参数校验，实际 %s score=%d", res.Status, res.AuthScore)
	}
	if len(res.Evidence) > 0 {
		t.Fatalf("参数被放行不记证据：%+v", res.Evidence)
	}
}

// ---- 签名完整性：回放、剥离、伪造各归各的 ----

func tamperExchange(input int, text string, sigs ...string) *Exchange {
	content := []any{}
	for _, s := range sigs {
		content = append(content, map[string]any{"type": "thinking", "thinking": "", "signature": s})
	}
	content = append(content, map[string]any{"type": "text", "text": text})
	return &Exchange{Kind: KindMessages, Status: 200, JSON: map[string]any{
		"usage": map[string]any{"input_tokens": float64(input), "output_tokens": float64(12)}, "content": content,
	}}
}

// tamperState 取签名那次：输入 70、输出 1098，其中思考 484（#78 的真实数值）。
func tamperState(model string, split bool) *runState {
	return &runState{
		profile:         ResolveProfile(model),
		thinkingContent: []any{map[string]any{"type": "thinking", "thinking": "…", "signature": "SIG_ORIGINAL"}},
		thinkingIndex:   0,
		thinkingUsage:   thinkingUsage{input: 70, output: 1098, thoughts: 484, split: split},
	}
}

func TestJudgeTamperAccepted(t *testing.T) {
	cases := []struct {
		name     string
		st       *runState
		pos, neg *Exchange
		status   string
		evidence string
		capped   bool
	}{
		{"回复的是正样本的校验串：回放", tamperState("claude-opus-5", true),
			tamperExchange(1191, "CONT_A"), tamperExchange(1191, "CONT_A"), StatusInconclusive, "sig_replayed", false},
		{"两个校验串都没有：无从判断", tamperState("claude-opus-5", true),
			tamperExchange(1191, "CONT_A"), tamperExchange(1191, "ok"), StatusInconclusive, "", false},
		{"新回复带着正样本的签名：伪造", tamperState("claude-opus-5", true),
			tamperExchange(1191, "CONT_A", "SIG1"), tamperExchange(1191, "CONT_B", "SIG1"), StatusSuspicious, "sig_unbound", true},
		{"新回复带着回传的原签名：伪造", tamperState("claude-opus-5", true),
			tamperExchange(1191, "CONT_A"), tamperExchange(2, "CONT_B", "SIG_ORIGINAL"), StatusSuspicious, "sig_unbound", true},
		{"续写输入不大于首轮：usage 不可能", tamperState("claude-opus-5", true),
			tamperExchange(2, "CONT_A"), tamperExchange(2, "CONT_B"), StatusInconclusive, "usage_implausible", false},
		{"负样本输入骤降：剥离后重发", tamperState("claude-opus-5", true),
			tamperExchange(1191, "CONT_A"), tamperExchange(707, "CONT_B"), StatusInconclusive, "thinking_stripped", false},
		{"负样本输入反而多很多", tamperState("claude-opus-5", true),
			tamperExchange(843, "CONT_A"), tamperExchange(1409, "CONT_B"), StatusInconclusive, "", false},
		{"两次都没带 thinking：网关一律剥离", tamperState("claude-opus-5", true),
			tamperExchange(707, "CONT_A"), tamperExchange(709, "CONT_B"), StatusInconclusive, "thinking_stripped", false},
		{"thinking 进了上下文且新生成：后端放行", tamperState("claude-opus-5", true),
			tamperExchange(1191, "CONT_A", "SIG1"), tamperExchange(1193, "CONT_B", "SIG2"), StatusSuspicious, "signature_not_verified", true},
		{"未拆分 thinking：输入盖得住首轮全部输出才算进了上下文", tamperState("claude-opus-5", false),
			tamperExchange(1191, "CONT_A"), tamperExchange(1191, "CONT_B"), StatusSuspicious, "signature_not_verified", true},
		{"未拆分 thinking 且输入不够：无从确认", tamperState("claude-opus-5", false),
			tamperExchange(707, "CONT_A"), tamperExchange(707, "CONT_B"), StatusInconclusive, "thinking_stripped", false},
		{"不保留历史 thinking 的型号：一律不封顶", tamperState("claude-sonnet-4-5-20250929", true),
			tamperExchange(1191, "CONT_A"), tamperExchange(1191, "CONT_B"), StatusInconclusive, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := judgeTamperAccepted(&CheckResult{}, tc.st, tc.pos, tc.neg, "CONT_A", "CONT_B")
			if r.Status != tc.status {
				t.Fatalf("状态应为 %s，实际 %s：%s", tc.status, r.Status, r.Summary)
			}
			if tc.evidence != "" && !hasEvidenceKey(r, tc.evidence) {
				t.Fatalf("应给出 %s，实际 %+v", tc.evidence, r.Evidence)
			}
			if tc.evidence == "" && len(r.Evidence) > 0 {
				t.Fatalf("不应有证据，实际 %+v", r.Evidence)
			}
			if (r.AuthCapReason != "") != tc.capped {
				t.Fatalf("封顶应为 %v，实际 %q", tc.capped, r.AuthCapReason)
			}
		})
	}
}

// TestSigTamperStrippingGatewayIsNotFake 网关剥掉校验不过的 thinking 块再重发：后端从没见过篡改的签名。
func TestSigTamperStrippingGatewayIsNotFake(t *testing.T) {
	up := &fakeUpstream{behaviour: "official", strictParams: true, signatureChecked: true, stripInvalidThinking: true}
	run := runAgainst(t, up, []string{"ping", "thinking-sig", "sig-tamper"})
	res := findCheck(run.Checks, "sig-tamper")
	if res.Status != StatusInconclusive || !hasEvidenceKey(res, "thinking_stripped") {
		t.Fatalf("剥离 thinking 的网关应判无法核对，实际 %s：%s %+v", res.Status, res.Summary, res.Evidence)
	}
	if run.Verdict.Authenticity.Capped {
		t.Fatalf("剥离 thinking 不应封顶真实性：%v", run.Verdict.Authenticity.CapReason)
	}
}

// TestSigTamperAlwaysStrippingGatewayIsNotFake 网关一律剥掉历史 thinking：正负样本输入一样，
// 但回传的 thinking 根本没进上下文，不能据「两次一致」判后端不校验。
func TestSigTamperAlwaysStrippingGatewayIsNotFake(t *testing.T) {
	up := &fakeUpstream{behaviour: "official", strictParams: true, signatureChecked: true, stripAllThinking: true}
	run := runAgainst(t, up, []string{"ping", "thinking-sig", "sig-tamper"})
	res := findCheck(run.Checks, "sig-tamper")
	if res.Status != StatusInconclusive || res.AuthCapReason != "" {
		t.Fatalf("一律剥离的网关应判无法核对且不封顶，实际 %s：%s（%s）", res.Status, res.Summary, res.AuthCapReason)
	}
}

// TestSigTamperStillCapsBackendWithoutVerification 篡改块原样进了上下文、后端照单全收：仍要封顶。
func TestSigTamperStillCapsBackendWithoutVerification(t *testing.T) {
	up := &fakeUpstream{behaviour: "official", strictParams: true, signatureChecked: false}
	run := runAgainst(t, up, []string{"ping", "thinking-sig", "sig-tamper"})
	res := findCheck(run.Checks, "sig-tamper")
	if res.AuthCapReason == "" || !hasEvidenceKey(res, "signature_not_verified") {
		t.Fatalf("不校验签名的后端应封顶，实际 %s：%s %+v", res.Status, res.Summary, res.Assertions)
	}
}

// TestSigTamperUsesFreshNonce 负样本换了校验串：按内容缓存的网关没法拿正样本的回复顶替。
func TestSigTamperUsesFreshNonce(t *testing.T) {
	run := runAgainst(t, &fakeUpstream{behaviour: "official", strictParams: true, signatureChecked: true},
		[]string{"thinking-sig", "sig-tamper"})
	res := findCheck(run.Checks, "sig-tamper")
	if len(res.Exchanges) != 2 {
		t.Fatalf("应发正负两个样本，实际 %d", len(res.Exchanges))
	}
	last := func(ex *Exchange) string {
		msgs, _ := ex.RequestBody.(map[string]any)["messages"].([]map[string]any)
		return str(msgs[len(msgs)-1]["content"])
	}
	if last(res.Exchanges[0]) == last(res.Exchanges[1]) {
		t.Fatalf("正负样本的校验串应不同：%s", last(res.Exchanges[0]))
	}
}

// TestTamperSigKeepsStructure 篡改后签名的 protobuf 结构、版本号与模型名都不变，只有持密钥的一方认得出。
func TestTamperSigKeepsStructure(t *testing.T) {
	const v17Sig = "CAISuAIKpgEIERgCKkDwdNvB3RB+OxiKEVcsXwLxiSdHsXzQYtXXVyxj+5Aad9T+sxY0WZNF02RnQ0dI/5wXvLg3BCvqxHbkT3Rvx1dAMg1jbGF1ZGUtb3B1cy01OAFCCHRoaW5raW5nWiRkMWRlMTVlNy1jZWViLTRiYWYtOWI3MS1kOGE0MjNiY2IwZGVyEFmXpmOOcoaDAXn4SwAtQuSIAQGoAZil8dQGsAECEgxo1pRa0g58s4HHnw4aDG/l4Lo+E0n4vc+41iIw8aR5OZqyd3cPimrH9rqwVDmtjXLVZVOK7xmeOXb+vtHFfDWJnHoYfDDBDt5PYCJFKj/Lm/iV9VaFArzDVewwZ2Krummb6Ue5zzswWNUAd3JFATZRiDCPQ8FpmXlZA7uUk2zGzvqqljT/E8ysswbGWaAYAQ=="
	for _, sig := range []string{v17Sig, buildSig(18, "claude-opus-5", "353adbae-f8bd-47d4-8b39-a7dda23fb7ba", "", time.Now().Unix())} {
		tampered := tamperSig(sig)
		if tampered == sig {
			t.Fatal("篡改后签名应不同")
		}
		blob, err := base64.StdEncoding.DecodeString(tampered)
		if err != nil || !isProtoMessage(blob) {
			t.Fatalf("篡改后应仍是合法 base64 + protobuf：%v", err)
		}
		before, after := ParseSignatureShape(sig), ParseSignatureShape(tampered)
		if before.Version != after.Version || before.Model != after.Model {
			t.Fatalf("版本号与模型名应不变：%+v → %+v", before, after)
		}
	}
	if sig := fakeSig(1); len(tamperSig(sig)) != len(sig) || tamperSig(sig) == sig {
		t.Fatalf("非 protobuf 签名改正中间的一个字符，实际 %q", tamperSig(sig))
	}
}

// ---- 回放：签名复用 ----

func TestAuditFlagsReusedSignatures(t *testing.T) {
	// 两次带 thinking 的请求拿到同一份签名：网关把缓存的响应回放了（或贴了旧签名）。
	up := &fakeUpstream{behaviour: "official", strictParams: false, signature: "SIGVALID_replayed_same_signature"}
	run := runAgainst(t, up, []string{"thinking-sig", "param-strict"})
	res := auditOf(t, run)
	if !hasEvidenceKey(res, "sig_replayed") {
		t.Fatalf("签名复用应判回放，实际 %+v", res.Evidence)
	}
	if res.AuthCapReason != "" {
		t.Fatalf("回放说明渠道不纯，不说明后端假，不应封顶：%s", res.AuthCapReason)
	}

	clean := runAgainst(t, &fakeUpstream{behaviour: "official", strictParams: false}, []string{"thinking-sig", "param-strict"})
	if hasEvidenceKey(auditOf(t, clean), "sig_replayed") {
		t.Fatal("每次一份新签名的渠道不应判回放")
	}
}

func TestExchangeSignaturesFromStream(t *testing.T) {
	raw := "event: content_block_start\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"CAQSabc"}}` + "\n\n" +
		"event: content_block_start\n" +
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"thinking","thinking":"","signature":"SIGX"}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"signature_delta","signature":"SIGX"}}` + "\n\n"
	got := exchangeSignatures(&Exchange{Status: 200, Raw: raw})
	if len(got) != 2 || got[0] != "CAQSabc" || got[1] != "SIGX" {
		t.Fatalf("应从 signature_delta 取签名，start 与 delta 都带时不拼接，实际 %v", got)
	}
	if exchangeSignatures(&Exchange{Status: 400, Raw: raw}) != nil {
		t.Fatal("失败的请求不取签名")
	}
	dup := &Exchange{Status: 200, JSON: map[string]any{"content": []any{
		map[string]any{"type": "thinking", "signature": "S1"}, map[string]any{"type": "thinking", "signature": "S1"}}}}
	if got := exchangeSignatures(dup); len(got) != 1 {
		t.Fatalf("同一响应里的重复签名只算一次，实际 %v", got)
	}
}

// ---- 0 注入：官方基线才下结论，同族基线只作参考；回放 ----

func TestInjectionBaselineLookup(t *testing.T) {
	cases := []struct {
		model     string
		exact     string
		reference string
	}{
		{"claude-opus-5", "claude-opus-5", ""},
		{"claude-opus-5-20261001", "claude-opus-5", ""},
		{"claude-opus-5-5", "", "claude-opus-5"},
		{"claude-opus-4-8-thinking", "", "claude-opus-4-8"},
		{"claude-sonnet-5", "", ""},
	}
	for _, tc := range cases {
		b, ok := lookupInjectionBaseline(tc.model)
		if (tc.exact != "") != ok || b.Model != tc.exact {
			t.Fatalf("%s：官方基线应为 %q，实际 %q（ok=%v）", tc.model, tc.exact, b.Model, ok)
		}
		ref, ok := referenceBaseline(tc.model)
		if (tc.reference != "") != ok || ref.Model != tc.reference {
			t.Fatalf("%s：参考基线应为 %q，实际 %q（ok=%v）", tc.model, tc.reference, ref.Model, ok)
		}
	}
	if _, ok := LookupCutoff("claude-opus-5-5"); ok {
		t.Fatal("截止日期不按同族回退")
	}
}

// TestZeroInjectionReferenceBaselineOnlyInforms #78 的形态：Opus 5.5 是另一个型号，每个请求比 Opus 5
// 的基线多 2 个 token、count_tokens 又不可用，分不清是注入还是模板——写出差值，不下结论。
func TestZeroInjectionReferenceBaselineOnlyInforms(t *testing.T) {
	res := runZeroInjection(t, &ziUpstream{inject: 2, noCount: true}, "claude-opus-5-5")
	if res.Status != StatusInconclusive || hasEvidenceKey(res, "injection_detected") {
		t.Fatalf("同族基线只作参考，不应判检出注入，实际 %s：%s", res.Status, res.Summary)
	}
	if !strings.Contains(res.Summary, "参照 claude-opus-5：裸请求 +2、带 system +2") {
		t.Fatalf("摘要应写出与同族基线的差值，实际 %s", res.Summary)
	}
	if strings.Contains(res.Summary, "未见注入迹象") {
		t.Fatalf("对不上账不能写成未见注入，实际 %s", res.Summary)
	}
}

func TestZeroInjectionDetectsReplayedResponses(t *testing.T) {
	res := runZeroInjection(t, &ziUpstream{bareSignature: "SIGREPLAYED_same"}, "claude-opus-5")
	if res.Status != StatusSuspicious || !hasEvidenceKey(res, "injection_replay") ||
		!strings.HasPrefix(res.Summary, "回放缓存的响应") {
		t.Fatalf("20 次裸请求共用一份签名应判回放，实际 %s：%s", res.Status, res.Summary)
	}
	if !strings.Contains(res.Summary, "0 注入") {
		t.Fatalf("回放之外的计量结论应保留，实际 %s", res.Summary)
	}
}

// ---- sys-dump：注入量用同一张基线表 ----

func TestDescribeBareInjectionUsesBaseline(t *testing.T) {
	r := &CheckResult{}
	describeBareInjection(r, "claude-opus-5", map[string]any{"input_tokens": float64(20)})
	if !r.Assertions[0].OK || !strings.Contains(r.Assertions[0].Detail, "claude-opus-5 官方基线 18，差 +2") {
		t.Fatalf("应按 claude-opus-5 官方基线 18 报差 +2，实际 %+v", r.Assertions)
	}
	r = &CheckResult{}
	describeBareInjection(r, "claude-opus-5-5", map[string]any{"input_tokens": float64(20)})
	if !r.Assertions[0].OK || !strings.Contains(r.Assertions[0].Detail, "型号不同，只作参考") {
		t.Fatalf("别的型号只能参照同族基线，实际 %+v", r.Assertions)
	}
	r = &CheckResult{}
	describeBareInjection(r, "claude-opus-5", map[string]any{"input_tokens": float64(18), "cache_creation_input_tokens": float64(2400)})
	if r.Assertions[0].OK {
		t.Fatalf("缓存里拼进大段系统提示应判大额注入，实际 %+v", r.Assertions)
	}
	r = &CheckResult{}
	describeBareInjection(r, "claude-sonnet-5", map[string]any{"input_tokens": float64(20)})
	if !r.Assertions[0].OK || !strings.Contains(r.Assertions[0].Detail, "没有官方基线") {
		t.Fatalf("无基线时只看是否远超正常范围，实际 %+v", r.Assertions)
	}
}

// ---- 型号比对：自报按前缀宽松比，回显按版本号精确比 ----

// TestModelMatchesPlatformIDs 平台形态的请求 id 要能对上自报的主型号；自报只说到大版本时不算不一致。
func TestModelMatchesPlatformIDs(t *testing.T) {
	if !modelMatchesRequested("claude-opus-5", "anthropic.claude-opus-5-v1:0") {
		t.Fatal("Bedrock 形态的 id 应对上自报的 claude-opus-5")
	}
	if !modelMatchesRequested("Claude Opus 5.5", "anthropic.claude-opus-5-5") {
		t.Fatal("Bedrock 形态的 id 应对上自报的 Claude Opus 5.5")
	}
	if modelMatchesRequested("claude-sonnet-5", "anthropic.claude-opus-5-v1:0") {
		t.Fatal("族不同不应算一致")
	}
	if !modelMatchesRequested("Claude Opus 5", "claude-opus-5-5") {
		t.Fatal("自报只说到大版本不应算不一致")
	}
}

// TestSameModelRequiresMinorVersion 回显是 API 原样返回的：日期后缀、官方别名到快照、平台形态不算不一致，
// 小版本不同（claude-opus-5 顶替 claude-opus-5-5）要算。
func TestSameModelRequiresMinorVersion(t *testing.T) {
	cases := []struct {
		got, want string
		same      bool
	}{
		{"claude-opus-5-5", "claude-opus-5-5", true},
		{"claude-opus-4-5-20251101", "claude-opus-4-5", true},
		{"claude-sonnet-4-20250514", "claude-sonnet-4", true},
		{"claude-opus-4-20250514", "claude-opus-4-0", true},
		{"claude-3-7-sonnet-20250219", "claude-3-7-sonnet-latest", true},
		{"claude-3-5-haiku-20241022", "claude-3-5-haiku-latest", true},
		{"global.anthropic.claude-opus-5", "claude-opus-5", true},
		{"anthropic.claude-opus-5-5-v1:0", "claude-opus-5-5", true},
		{"claude-opus-5", "claude-opus-5-1m", true}, // 1m 是上下文后缀，不是 5.1
		{"claude-opus-4-5-20251101", "claude-opus-4-5-1m", true},
		{"claude-opus-5", "claude-opus-5-128000", true}, // 长数字不是小版本
		{"claude-sonnet-4-20250514", "claude-sonnet-4-2025-05-14", true},
		{"claude-opus-4-1-20250805", "claude-opus-41", true}, // 渠道把 4.1 写成 41
		{"claude-mythos-preview@20260101", "claude-mythos-preview", true},
		{"claude-opus-5", "claude-opus-5-5", false},
		{"claude-opus-5-5", "claude-opus-5", false},
		{"claude-opus-4", "claude-opus-4-8", false},
		{"claude-sonnet-5", "claude-opus-5", false},
		{"claude-opus", "claude-opus-5-5", false}, // 读不出版本号的回显不能靠「是请求的前缀」过关
		{"claude", "claude-opus-5-5", false},
	}
	for _, tc := range cases {
		if got := sameModel(tc.got, tc.want); got != tc.same {
			t.Errorf("回显 %s、请求 %s：期望一致=%v，实际 %v", tc.got, tc.want, tc.same, got)
		}
	}

	r := &CheckResult{}
	analyzeModelEcho(r, "claude-opus-5", "claude-opus-5-5")
	if !hasEvidenceKey(r, "model_echo_mismatch") {
		t.Fatalf("claude-opus-5 顶替 claude-opus-5-5 应记回显不一致，实际 %+v", r.Evidence)
	}
	r = &CheckResult{}
	analyzeModelEcho(r, "global.anthropic.claude-opus-5-v1:0", "claude-opus-5-5")
	if !hasEvidenceKey(r, "model_echo_mismatch") || !hasEvidenceKey(r, "model_echo_bedrock") {
		t.Fatalf("映射到 Bedrock 形态的低版本也要记回显不一致，实际 %+v", r.Evidence)
	}
	r = &CheckResult{}
	analyzeModelEcho(r, "global.anthropic.claude-opus-5", "claude-opus-5")
	if hasEvidenceKey(r, "model_echo_mismatch") || !hasEvidenceKey(r, "model_echo_bedrock") {
		t.Fatalf("同型号的 Bedrock 形态回显只记平台证据，不算型号不一致，实际 %+v", r.Evidence)
	}
}

// ---- 型号档案：默认是否思考、历史 thinking 是否留在上下文 ----

func TestProfileThinkingBehaviour(t *testing.T) {
	cases := []struct {
		model                                   string
		adaptive, defaultThink, keeps, supports bool
		minCache                                int
	}{
		{"claude-opus-5-5", true, true, true, true, 512},
		{"claude-fable-5-1", true, true, true, true, 512},
		{"claude-opus-4-8", true, false, true, true, 1024},
		{"claude-opus-4-6", true, false, true, true, 4096},
		{"claude-sonnet-4-6", true, false, true, true, 1024},
		{"claude-sonnet-5", true, true, true, true, 1024},
		{"claude-opus-4-5-20251101", false, false, true, true, 4096},
		{"claude-sonnet-4-5-20250929", false, false, false, true, 1024},
		{"claude-haiku-4-5-20251001", false, false, false, true, 4096},
		{"claude-opus-4-1-20250805", false, false, false, true, 1024},
		{"claude-sonnet-4-20250514", false, false, false, true, 1024}, // 日期不是小版本号
		{"claude-3-7-sonnet-20250219", false, false, false, true, 1024},
		{"claude-3-5-haiku-20241022", false, false, false, false, 2048}, // thinking 会 400
	}
	for _, tc := range cases {
		p := ResolveProfile(tc.model)
		if !p.Recognized || p.Adaptive != tc.adaptive || p.DefaultThinking != tc.defaultThink ||
			p.KeepsThinking != tc.keeps || p.SupportsThinking != tc.supports || p.MinCacheTokens != tc.minCache {
			t.Fatalf("%s：期望 adaptive=%v default=%v keeps=%v supports=%v minCache=%d，实际 %+v",
				tc.model, tc.adaptive, tc.defaultThink, tc.keeps, tc.supports, tc.minCache, p)
		}
	}
}

// ---- slope：默认思考不是强制注入 ----

// ---- caller-system：拒答不是没送达，但也不能替没送达打掩护 ----

// ---- tool-use：不支持强制工具调用是型号行为 ----

func TestToolUseFallsBackWhenForcedToolChoiceUnsupported(t *testing.T) {
	var forced, auto int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		if str(mapOf(body["tool_choice"])["type"]) == "tool" {
			forced++
			writeJSON(w, 400, map[string]any{"type": "error", "error": map[string]any{"type": "invalid_request_error",
				"message": `tool_choice: type "tool" and "any" are not supported for this model.`}})
			return
		}
		auto++
		tool := mapOf(sliceOf(body["tools"])[0])
		text := userText(body)
		input := map[string]any{"order_id": firstToken(text, "ORDER_")}
		if str(tool["name"]) == "calculate_sum" {
			a, b := twoNumbers(text)
			input = map[string]any{"a": a, "b": b}
		}
		writeJSON(w, 200, map[string]any{"id": "msg_01ABC", "type": "message", "role": "assistant", "stop_reason": "tool_use",
			"usage":   map[string]any{"input_tokens": 500, "output_tokens": 40},
			"content": []any{map[string]any{"type": "tool_use", "id": "toolu_01ABC", "name": str(tool["name"]), "input": input}}})
	}))
	t.Cleanup(srv.Close)
	target, _ := Target{Name: "t", BaseURL: srv.URL, APIKey: "sk-test-key-1234567890", Model: "claude-opus-5-5"}.Normalize()
	res := checkToolUse(context.Background(), NewClient(target), &runState{})
	if res.Status != StatusPassed || res.AuthScore != 10 || forced != 2 || auto != 2 {
		t.Fatalf("强制调用被拒后应改用 auto 并照常判定，实际 %s score=%d forced=%d auto=%d：%s",
			res.Status, res.AuthScore, forced, auto, res.Summary)
	}
	// 中转把字段路径打成 *** 时也要认得出。
	masked := strings.ToLower(`{"error":{"message":"***: type \"tool\" and \"any\" are not supported for this model."}}`)
	if !forcedToolChoiceRe.MatchString(masked) {
		t.Fatal("字段路径被打码的报错也应识别")
	}
}

// ---- 审计：只在要了摘要的请求上判「从未出现思考正文」 ----

func TestAuditThinkingCoherenceNeedsSummaryRequest(t *testing.T) {
	emptySigned := map[string]any{"content": []any{map[string]any{"type": "thinking", "thinking": "", "signature": "S"}}}
	summarized := map[string]any{"thinking": map[string]any{"type": "adaptive", "display": "summarized"}}

	r := &CheckResult{}
	auditThinkingCoherence(r, []auditBody{{checkID: "ping", body: emptySigned, request: map[string]any{}}})
	if r.AuthCapReason != "" {
		t.Fatalf("默认 display=omitted 的空 thinking 不应定罪（#60）：%s", r.AuthCapReason)
	}
	r = &CheckResult{}
	auditThinkingCoherence(r, []auditBody{{checkID: "thinking-sig", body: emptySigned, request: summarized}})
	if r.AuthCapReason != "" {
		t.Fatal("只有一个样本不足以定罪")
	}
	r = &CheckResult{}
	auditThinkingCoherence(r, []auditBody{
		{checkID: "thinking-sig", body: emptySigned, request: summarized},
		{checkID: "sig-tamper", body: emptySigned, request: summarized},
	})
	if r.AuthCapReason == "" {
		t.Fatal("要了摘要却多次全程无正文应定罪")
	}
}

// ---- 签名来源：读不出来时要说出来 ----

// headerOnlySig 2026-09-28 起出现的签名头：只有版本号与块类型，没有型号、账号与签发时间。
func headerOnlySig(version int) string {
	var inner []byte
	inner = protoVarint(inner, 1, uint64(version))
	inner = protoVarint(inner, 3, 2)
	inner = protoBytes(inner, 8, []byte("thinking"))
	var out []byte
	out = protoVarint(out, 1, 4)
	out = protoBytes(out, 2, protoBytes(nil, 1, inner))
	return base64.StdEncoding.EncodeToString(out)
}

func TestAuditReportsUnreadableProvenance(t *testing.T) {
	run := runAgainst(t, &fakeUpstream{behaviour: "official", signature: headerOnlySig(17)}, []string{"ping", "thinking-sig"})
	res := auditOf(t, run)
	found := false
	for _, a := range res.Assertions {
		if a.Label == "签名来源字段可读" {
			found = !a.OK && a.Diagnostic && strings.Contains(a.Detail, "v17×1")
		}
	}
	if !found {
		t.Fatalf("签名头读不出来源字段时应留一条诊断，实际 %+v", res.Assertions)
	}
}

func TestSigFieldsReadForV18(t *testing.T) {
	issued := time.Now().Unix()
	got := ParseSignatureShape(buildSig(18, "claude-opus-5", "353adbae-f8bd-47d4-8b39-a7dda23fb7ba", "", issued))
	if got.AccountRef != "353adbae-f8bd-47d4-8b39-a7dda23fb7ba" || got.IssuedAt.Unix() != issued {
		t.Fatalf("v18 的账号与签发时间与 v17 同义，应读出，实际 %+v", got)
	}
}

// ---- 取消与缺项：结果标记为已取消，由服务层决定不落库 ----

func TestCancelledRunIsMarked(t *testing.T) {
	srv := newFakeServer(t, &fakeUpstream{behaviour: "official"})
	target, _ := Target{Name: "t", BaseURL: srv.URL, APIKey: "sk-test-key-1234567890", Model: "claude-opus-5"}.Normalize()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if run := Run(ctx, target, []string{"ping", "tool-use"}, NewGate(1), nil); !run.Cancelled() {
		t.Fatalf("取消的一轮应标为已取消，实际 error=%q", run.Error)
	}
	done := Run(context.Background(), target, []string{"ping"}, NewGate(1), nil)
	if done.Cancelled() {
		t.Fatal("跑完的一轮不应标为已取消")
	}
}

func TestMissingChecksAndComplete(t *testing.T) {
	run := &TargetRun{Checks: []*CheckResult{{ID: "ping", Status: StatusPassed}, {ID: "tool-use", Status: StatusRunning}}}
	selected := []string{"ping", "zero-injection", "tool-use"}
	if got := MissingChecks(run, selected); strings.Join(got, ",") != "zero-injection,tool-use" {
		t.Fatalf("缺的应是 zero-injection 与还在跑的 tool-use，实际 %v", got)
	}
	if Complete(run, selected) {
		t.Fatal("计分项 tool-use 没结果时不算完整")
	}
	run.Checks[1].Status = StatusPassed
	if !Complete(run, selected) {
		t.Fatal("只缺只检测不计分的项时仍算完整")
	}
}
