package modeldetect

import (
	"strings"
	"testing"
)

// sigLenBody 一条带签名 thinking 块的响应：签名 sigLen 字符，输出 out 个 token。
func sigLenBody(checkID string, sigLen, out int) auditBody {
	return auditBody{checkID: checkID, body: map[string]any{
		"type": "message",
		"content": []any{
			map[string]any{"type": "thinking", "thinking": "", "signature": strings.Repeat("A", sigLen)},
			map[string]any{"type": "text", "text": "PONG"},
		},
		"usage": map[string]any{"input_tokens": float64(18), "output_tokens": float64(out)},
	}}
}

// TestSignatureLengthFlagsPastedSignatures 实测 ant / 哈基米 / walk：几个 token 的回复带着几千字符的签名。
func TestSignatureLengthFlagsPastedSignatures(t *testing.T) {
	r := &CheckResult{}
	auditSignatureLength(r, []auditBody{
		sigLenBody("max-tokens-strict", 5020, 10),
		sigLenBody("ping", 1268, 5),
		sigLenBody("thinking-sig", 1396, 1091),
	})
	if !hasEvidenceKey(r, "sig_pasted") {
		t.Fatalf("签名远超输出所能容纳的思考应判贴签名，实际 %+v", r.Assertions)
	}
	scoreSignatureReplay(r)
	if r.AuthScore != -sigReplayPenalty || r.AuthCapReason != "" {
		t.Fatalf("贴签名应扣 %d 且不封顶，实际 %d / %q", sigReplayPenalty, r.AuthScore, r.AuthCapReason)
	}
}

// TestSignatureLengthPassesRealChannels 真渠道的实测样本：v19 小思考、v16 空思考、长思考，以及单条偏长。
func TestSignatureLengthPassesRealChannels(t *testing.T) {
	r := &CheckResult{}
	auditSignatureLength(r, []auditBody{
		sigLenBody("hello-entropy", 632, 21),
		sigLenBody("ping", 356, 3),
		sigLenBody("zero-injection", 1232, 165),
		sigLenBody("thinking-sig", 6704, 3806),
		sigLenBody("trivial", 904, 17),
	})
	if hasEvidenceKey(r, "sig_pasted") || r.AuthScore != 0 {
		t.Fatalf("真渠道样本不应判贴签名，实际 %+v", r.Assertions)
	}
}

// TestSignatureLengthNeedsTwoHits 单条超限不定性。
func TestSignatureLengthNeedsTwoHits(t *testing.T) {
	r := &CheckResult{}
	auditSignatureLength(r, []auditBody{sigLenBody("ping", 3000, 5), sigLenBody("tool-use", 700, 40)})
	if hasEvidenceKey(r, "sig_pasted") {
		t.Fatal("只有一条超限不应定性")
	}
}

// TestReplayAndPastedDeductOnce 签名重复与贴签名同源，只扣一次。
func TestReplayAndPastedDeductOnce(t *testing.T) {
	r := &CheckResult{}
	r.addEvidence("sig_replayed", "回放", "", ClassWrapper, 3)
	auditSignatureLength(r, []auditBody{sigLenBody("ping", 5000, 5), sigLenBody("ping-again", 4000, 9)})
	scoreSignatureReplay(r)
	if r.AuthScore != -sigReplayPenalty {
		t.Fatalf("应只扣一次 %d，实际 %d", sigReplayPenalty, r.AuthScore)
	}
}
