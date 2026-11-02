package modeldetect

import (
	"strings"
	"testing"
	"time"
)

// realFableV19Sig 2026-11-02 从已知真渠道的 claude-fable-5 拿到的签名（schema v19）。
const realFableV19Sig = "CAISyAMK2gIIExgCKkDF26Y89Nf+1q9WfYjMIVIiYvXlJ1UBfW8KwGC4z6Ha4cIGSOgnH2Aar+f9Nq6eXvLBNVNJs+/ljWfbRXUP8B16Mg5jbGF1ZGUtZmFibGUtNTgBQgh0aGlua2luZ1okODg1ZmJkNmItNTU4Yi00MzNiLTgzZmItNTFlZDgzYmRjMTMxchByZX5k7C3z4exM/1xNWYKFiAEBmgEQCg5jbGF1ZGUtZmFibGUtNagB0/ih1wawAQLCAToIBBAAGgwC2ooDTzcFHJoEJq0gATIEEnHPJToEODMjikIEODMjikoEODMjilAAWgTZBeFWYAJoAXAA2gFgChZwcmVmaXhfYmVmb3JlX2Jsb2NrX3YxEiArovlxlQ9oV3c5OFzSFVHYKgOripgLb+0tgX8Mpvj1oyIAMiDCwBPIapyS4i+Wbz2GF3aPi3pEyQKtzAl7+h4JivtOH0gDEgyrOOAMly6b6KGiAwsaDM3INamq2sqj4f6ZbiIwR6eNH4eEwdwAWpFQqo8NSwW62EakOqcere5QR0C2EbQo298zPREAcILnMWjZyRd+KhtomiA/7gPJ1HJSb9cCQhGUDEZoKBCa8AI+M6cYAQ=="

// sigModelBody 一条带签名 thinking 块的审计响应。
func sigModelBody(checkID, requested, sig string) auditBody {
	return auditBody{
		checkID: checkID,
		request: map[string]any{"model": requested},
		body: map[string]any{"type": "message", "content": []any{
			map[string]any{"type": "thinking", "thinking": "", "signature": sig},
			map[string]any{"type": "text", "text": "323"},
		}},
	}
}

func TestParseSignatureShapeReadsRealV19(t *testing.T) {
	got := ParseSignatureShape(realFableV19Sig)
	if got.Version != 19 || got.Model != "claude-fable-5" {
		t.Fatalf("应读出 v19 与 claude-fable-5，实际 %+v", got)
	}
	if !sigUUIDValidRe.MatchString(got.AccountRef) {
		t.Fatalf("v19 的 f11 应是合法 UUID，实际 %q", got.AccountRef)
	}
	if day := got.IssuedAt.UTC().Format(time.DateOnly); day != "2026-11-02" {
		t.Fatalf("v19 的 f21 应是采样当天，实际 %s", day)
	}
}

func TestAuditSignatureModelPassesRealFable(t *testing.T) {
	r := &CheckResult{}
	auditSignatureModel(r, []auditBody{sigModelBody("thinking-sig", "claude-fable-5", realFableV19Sig)})
	if hasEvidenceKey(r, "sig_model_mismatch") || r.AuthCapReason != "" {
		t.Fatalf("真 fable-5 签名不应告警：%+v %s", r.Evidence, r.AuthCapReason)
	}
	if len(r.Assertions) != 1 || !r.Assertions[0].OK {
		t.Fatalf("应留下一条通过的诊断，实际 %+v", r.Assertions)
	}
}

// TestAuditSignatureModelFlagsSubstitution 请求 fable-5-1 / opus-5-5，签名却是 fable-5 签的：型号被顶替。
func TestAuditSignatureModelFlagsSubstitution(t *testing.T) {
	for _, requested := range []string{"claude-fable-5-1", "claude-opus-5-5", "claude-opus-5"} {
		t.Run(requested, func(t *testing.T) {
			r := &CheckResult{}
			auditSignatureModel(r, []auditBody{
				sigModelBody("ping", requested, buildSig(19, requested, "", "", 0)),
				sigModelBody("thinking-sig", requested, realFableV19Sig),
			})
			if !hasEvidenceKey(r, "sig_model_mismatch") {
				t.Fatalf("混入其他型号的签名应告警，实际 %+v", r.Evidence)
			}
			if !strings.Contains(r.AuthCapReason, "claude-fable-5") {
				t.Fatalf("应封顶真实性并写出签名型号，实际 %q", r.AuthCapReason)
			}
		})
	}
}

// TestAuditSignatureModelSkipsUnreadable 读不出公开型号 id 的签名不下结论：新格式签名头没有型号，
// 旧样本里还有内部代号，请求方用了认不出版本的自定义别名时也没法比。
func TestAuditSignatureModelSkipsUnreadable(t *testing.T) {
	cases := []auditBody{
		sigModelBody("ping", "claude-opus-5", buildSig(19, "", "", "", 0)),
		sigModelBody("ping", "claude-opus-5", buildSig(15, "pelican", "", "", 0)),
		sigModelBody("ping", "my-custom-alias", realFableV19Sig),
		sigModelBody("ping", "claude-opus-5", "claude#vertex-opaque"),
	}
	r := &CheckResult{}
	auditSignatureModel(r, cases)
	if len(r.Assertions) != 0 || len(r.Evidence) != 0 || r.AuthCapReason != "" {
		t.Fatalf("读不出型号时应静默跳过，实际 %+v %+v %q", r.Assertions, r.Evidence, r.AuthCapReason)
	}
}

// TestAuditSignatureModelSameVersionAliases 日期后缀、1M 上下文后缀都算同一型号。
func TestAuditSignatureModelSameVersionAliases(t *testing.T) {
	r := &CheckResult{}
	auditSignatureModel(r, []auditBody{
		sigModelBody("ping", "claude-fable-5-20260101", realFableV19Sig),
		sigModelBody("ping", "claude-fable-5-1m", realFableV19Sig),
	})
	if hasEvidenceKey(r, "sig_model_mismatch") {
		t.Fatalf("同型号的别名不应告警：%+v", r.Evidence)
	}
}

// TestRunCapsAuthenticityOnSignatureModelMismatch 整轮跑下来，签名型号不符要把真实性封顶。
func TestRunCapsAuthenticityOnSignatureModelMismatch(t *testing.T) {
	sonnet := buildSig(18, "claude-sonnet-4-6", "353adbae-f8bd-47d4-8b39-a7dda23fb7ba", "", time.Now().Unix())
	up := &fakeUpstream{behaviour: "official", strictParams: true, signatureChecked: true, signature: sonnet}
	run := runAgainst(t, up, []string{"ping", "thinking-sig"})
	if !hasEvidenceKey(auditOf(t, run), "sig_model_mismatch") {
		t.Fatal("请求 opus-5、签名 sonnet-4-6 应告警")
	}
	if !run.Verdict.Authenticity.Capped {
		t.Fatalf("型号被顶替应封顶真实性，实际 %+v", run.Verdict.Authenticity)
	}
}
