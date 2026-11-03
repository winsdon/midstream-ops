package modeldetect

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"strings"
)

// signatureSamples 已知真渠道签发的 thinking 块，按模型 id 存放（signaturesamples/<model>.json）。
//
//go:embed signaturesamples/*.json
var signatureSamples embed.FS

// signatureSample 一份真签名样本：原始提问与 Anthropic 返回的 thinking + text 块。
type signatureSample struct {
	Model          string           `json:"model"`
	CapturedAt     string           `json:"captured_at"`
	Source         string           `json:"source"`
	ThinkingTokens int              `json:"thinking_tokens"`
	Question       string           `json:"question"`
	Content        []map[string]any `json:"content"`
}

// lookupSignatureSample 按模型 id 找内置样本，带日期后缀的 id 退回不带后缀的主键。
func lookupSignatureSample(model string) (signatureSample, bool) {
	for _, key := range modelKeys(model) {
		raw, err := signatureSamples.ReadFile("signaturesamples/" + key + ".json")
		if err != nil {
			continue
		}
		var s signatureSample
		if json.Unmarshal(raw, &s) == nil && len(s.Content) > 0 {
			return s, true
		}
	}
	return signatureSample{}, false
}

// crossVerifyThinkingShare 带思考比去思考多出的输入，至少要占样本思考 token 的这么多，才算 thinking
// 真的进了上下文。实测真渠道 80%–103%（yimo 791 / pet 772 / kaola 616，样本 769）；网关剥掉 thinking
// 再重试时差值接近 0。
const crossVerifyThinkingShare = 0.5

// crossVerifyAttempts thinking 没进上下文时最多试几轮。
const crossVerifyAttempts = 2

// crossVerifyMinGap 带 / 去思考的输入差至少这么多才算 thinking 送达（剥离时实测差 0–2）。
const crossVerifyMinGap = 50

// 交叉验证的结论。
type crossOutcome int

const (
	crossInconclusive crossOutcome = iota // 没送达、请求失败或没有可用材料
	crossPassed                           // 签名送达后端并通过校验
	crossRejected                         // 以签名无效被拒
)

// checkSignatureCrossVerify 官方签名交叉验证，分正反两向。
//
// 「签名完整性」（sig-tamper）只能证明后端会校验它自己签发的签名：伪造后端用自己的密钥签名、
// 模仿 Anthropic 的报错文本，同样能做到原样可续写、篡改即拒。2026-11-03 实测一条已知是假的
// claude-fable-5 渠道就这样拿满了签名完整性。真 Anthropic 跨账号、跨渠道互认签名，验证是对称的。
//
//   - 正向：把内置的已知真签名交给目标续写。假后端没有 Anthropic 的密钥，会以 Invalid signature 拒绝。
//   - 反向：让目标签一份思考，交给已选的真实基准账号续写。目标的签名若不是 Anthropic 签的，会被基准拒绝。
//
// 正向先跑；正向没下结论（典型是中转只放行自签签名、别人的 thinking 一律剥掉：实测 xigua）才跑反向。
// 任一方向通过 +40，任一方向被以签名无效拒绝即封顶。Bedrock / Vertex 与第一方的签名互不通用，
// 拒绝只对第一方渠道成立，报错像 Bedrock 时只记证据不足。
func checkSignatureCrossVerify(ctx context.Context, c *Client, st *runState) *CheckResult {
	meta, _ := checkByID("sig-crossverify")
	r := newResult(meta)
	ref, hasRef := crossVerifyReference(c, st)

	sample, hasSample := lookupSignatureSample(c.Target().Model)
	if hasSample {
		switch forwardCrossVerify(ctx, c, sample, r) {
		case crossPassed:
			r.AuthScore = 40
			return r.finish("真签名通过后端校验（后端持有 Anthropic 密钥）")
		case crossRejected:
			return r.finish("真签名被拒：后端不是 Anthropic")
		}
	}
	if hasRef {
		switch reverseCrossVerify(ctx, c, ref, r) {
		case crossPassed:
			r.AuthScore = 40
			r.Status = ""
			return r.finish("目标签发的签名通过真实基准账号的校验（后端持有 Anthropic 密钥）")
		case crossRejected:
			r.Status = ""
			return r.finish("目标签发的签名被真实基准账号拒绝：后端不是 Anthropic")
		}
	}

	switch {
	case !hasSample && !hasRef:
		r.Status = StatusUnsupported
		return r.finish("没有该型号的内置真签名样本，也没有选真实基准账号做反向验证")
	case !hasRef:
		r.diagnose("反向交叉验证", false, "未选真实基准账号（或基准就是本目标），无法把目标的签名交给真渠道验证")
	}
	r.Status = StatusInconclusive
	return r.finish("签名没有被拒，但 thinking 没送达后端（中转可能剥掉了别人的签名），无法判定")
}

// forwardCrossVerify 正向：内置真签名交给目标续写。
func forwardCrossVerify(ctx context.Context, c *Client, sample signatureSample, r *CheckResult) crossOutcome {
	var withText []map[string]any
	for _, b := range sample.Content {
		if str(b["type"]) == "text" {
			withText = append(withText, b)
		}
	}
	detail := fmt.Sprintf("正向，样本：%s，%s 采自%s，思考 %d tokens", sample.Model, sample.CapturedAt, sample.Source, sample.ThinkingTokens)
	got := continueWith(ctx, c, sample.Question, sample.Content, withText, r, false)
	if rejectedBySignature(got.with) {
		return rejectSignature(r, got.with, "正向：已知真渠道签发的 thinking 签名被目标以签名无效拒绝", detail)
	}
	if !got.ok() {
		r.diagnose("正向交叉验证请求成功", false, describeFailure(firstFailed(got.with, got.without)))
		return crossInconclusive
	}
	reached := float64(got.gap()) >= crossVerifyThinkingShare*float64(sample.ThinkingTokens)
	r.diagnose("正向：真签名被接受并照做", got.followed(), fmt.Sprintf("回复 %s｜%s", clip(contentText(got.with.JSON), 60), detail))
	r.diagnose("正向：真签名的 thinking 进了上下文", reached, got.describe(sample.ThinkingTokens))
	if got.followed() && reached {
		r.assert("真签名通过后端校验", true, detail)
		return crossPassed
	}
	return crossInconclusive
}

// continuation 一对「带思考 / 去思考」续写请求。
type continuation struct {
	with, without *Exchange
	nonce         string
}

func (c continuation) ok() bool { return c.with.OK() && c.without.OK() }

func (c continuation) followed() bool { return strings.Contains(contentText(c.with.JSON), c.nonce) }

func (c continuation) gap() int {
	inWith, _, _ := inputTotal(usageOf(c.with.JSON))
	inWithout, _, _ := inputTotal(usageOf(c.without.JSON))
	return inWith - inWithout
}

func (c continuation) describe(thinking int) string {
	inWith, _, _ := inputTotal(usageOf(c.with.JSON))
	inWithout, _, _ := inputTotal(usageOf(c.without.JSON))
	return fmt.Sprintf("带思考输入 %d，去思考 %d，差 %d（思考 %d）", inWith, inWithout, inWith-inWithout, thinking)
}

// continueWith 在 c 上续写：提问 → assistant（带 / 去思考）→ 要求回复校验串。
//
// 号池里有的账号会把历史 thinking 剥掉再转发（实测 2026-11-03：同一渠道一轮输入差 2、下一轮 770），
// 没送达就再试一轮，免得一次撞上剥离的账号就丢掉结论。
// reference 为 true 表示 c 是真实基准账号，这些请求要从跨请求审计里排除。
func continueWith(ctx context.Context, c *Client, question string, with, without []map[string]any, r *CheckResult, reference bool) continuation {
	var got continuation
	for attempt := 0; attempt < crossVerifyAttempts; attempt++ {
		got.with, got.nonce = continuePost(ctx, c, question, with)
		got.without, _ = continuePost(ctx, c, question, without)
		got.with.Reference, got.without.Reference = reference, reference
		r.Exchanges = append(r.Exchanges, got.with, got.without)
		r.DurationMs += got.with.DurationMs + got.without.DurationMs
		if rejectedBySignature(got.with) || !got.ok() || got.gap() >= crossVerifyMinGap {
			break
		}
	}
	return got
}

// continuePost 续写一轮。
func continuePost(ctx context.Context, c *Client, question string, assistant []map[string]any) (*Exchange, string) {
	n := nonce("XVERIFY")
	ex := c.Post(ctx, KindMessages, map[string]any{
		"model": c.Target().Model, "max_tokens": 200,
		"thinking": map[string]any{"type": "adaptive"},
		"messages": []map[string]any{
			{"role": "user", "content": question},
			{"role": "assistant", "content": assistant},
			{"role": "user", "content": "只回复 " + n + "。"},
		},
	})
	return ex, n
}

// rejectedBySignature 以签名无效被拒（4xx + 签名报错）。
func rejectedBySignature(ex *Exchange) bool {
	return ex.Status >= 400 && ex.Status < 500 && signatureRejectRe.MatchString(ex.ErrorText())
}

// rejectSignature 记一次「签名被以无效拒绝」：第一方形态的报错封顶，Bedrock 形态只记证据不足。
func rejectSignature(r *CheckResult, ex *Exchange, capReason, detail string) crossOutcome {
	if bedrockErrRe.MatchString(ex.ErrorText()) {
		r.diagnose("签名被接受", false, "Bedrock 形态的报错：第一方签名本就不通用，不据此判伪｜"+clip(ex.ErrorText(), 160))
		return crossInconclusive
	}
	r.assert("签名被接受", false, fmt.Sprintf("HTTP %d：%s｜%s", ex.Status, clip(ex.ErrorText(), 160), detail))
	r.AuthCapReason = capReason + "：后端没有 Anthropic 的签名密钥"
	r.addEvidence("sig_foreign_rejected", "签名与 Anthropic 不互认（签名体系是伪造的）", detail, ClassWrapper, 5)
	return crossRejected
}

// firstFailed 返回第一个失败的请求。
func firstFailed(exs ...*Exchange) *Exchange {
	for _, ex := range exs {
		if !ex.OK() {
			return ex
		}
	}
	return exs[0]
}
