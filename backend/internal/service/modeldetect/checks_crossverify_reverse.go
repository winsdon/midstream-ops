package modeldetect

import (
	"context"
	"fmt"
)

// 反向交叉验证的参数。
const (
	// reverseMinThinking 目标签出的思考至少这么多 token 才拿去验证，太少的话输入差被固定开销淹没。
	reverseMinThinking = 200
	// reverseMaxShare 输入差超过目标自报思考的这么多倍，说明签名里装的不是这次的思考（贴来的旧签名）。
	// 实测真渠道 1.0 左右（xigua 1302 / 1300、766 / 768）。
	reverseMaxShare = 1.5
	// reverseIssueAttempts 让目标签出足够长思考的尝试次数。
	reverseIssueAttempts = 2
)

// crossVerifyReference 本轮可用的真实基准账号（参照目标），型号跟随被测目标。
// 基准账号就是被测目标本身时不做反向验证：自己验证自己没有意义。
func crossVerifyReference(c *Client, st *runState) (Target, bool) {
	if st == nil || st.baseline == nil || st.baseline.Reference == nil {
		return Target{}, false
	}
	ref := *st.baseline.Reference
	t := c.Target()
	if ref.BaseURL == t.BaseURL && ref.APIKey == t.APIKey {
		return Target{}, false
	}
	ref.Model = t.Model
	return ref, true
}

// reverseCrossVerify 反向：让目标签一份思考，交给真实基准账号续写。
//
// 实测 2026-11-03 xigua：它的中转只放行自签签名，内置样本到它那里被剥掉（输入差 2），正向验不出；
// 它签的签名交给 kaola，带 / 去思考的输入差 1302，与它自报的思考 1300 分毫不差 —— 真 Anthropic 认它。
// 要求输入差落在自报思考的 0.5–1.5 倍：太少是没送达，太多说明签名里装的是别处的思考（贴签名）。
func reverseCrossVerify(ctx context.Context, c *Client, ref Target, r *CheckResult) crossOutcome {
	question, content, thinking, ok := issueThinking(ctx, c, r)
	if !ok {
		r.diagnose("反向：目标签出足够长的思考", false, fmt.Sprintf("思考不足 %d tokens 或没有签名，无法做反向验证", reverseMinThinking))
		return crossInconclusive
	}
	var textOnly []map[string]any
	for _, b := range content {
		if str(b["type"]) == "text" {
			textOnly = append(textOnly, b)
		}
	}
	detail := fmt.Sprintf("反向：目标签发的思考 %d tokens，交给真实基准账号 %s 验证", thinking, ref.Name)
	got := continueWith(ctx, NewClient(ref), question, content, textOnly, r, true)
	if rejectedBySignature(got.with) {
		return rejectSignature(r, got.with, "反向：目标签发的 thinking 签名被真实基准账号以签名无效拒绝", detail)
	}
	if !got.ok() {
		r.diagnose("反向：基准账号续写成功", false, describeFailure(firstFailed(got.with, got.without)))
		return crossInconclusive
	}
	gap := got.gap()
	lo, hi := crossVerifyThinkingShare*float64(thinking), reverseMaxShare*float64(thinking)
	r.diagnose("反向：目标的 thinking 在基准账号上进了上下文", float64(gap) >= lo, got.describe(thinking))
	switch {
	case float64(gap) > hi:
		r.diagnose("反向：签名里的思考与目标自报一致", false, fmt.Sprintf("输入差 %d 远超自报思考 %d：签名装的是别处的思考", gap, thinking))
		return crossInconclusive
	case float64(gap) >= lo:
		r.assert("目标签发的签名通过真实基准账号校验", true, detail)
		return crossPassed
	}
	return crossInconclusive
}

// issueThinking 让目标做一道必须思考的题，返回提问、带签名的 content 与思考 token 数。
// 题里的数字每次随机，免得撞上中转对相同请求的响应缓存。
func issueThinking(ctx context.Context, c *Client, r *CheckResult) (string, []map[string]any, int, bool) {
	model := c.Target().Model
	profile := ResolveProfile(model)
	if !profile.SupportsThinking {
		return "", nil, 0, false
	}
	for attempt := 0; attempt < reverseIssueAttempts; attempt++ {
		question := reverseQuestion()
		body := map[string]any{
			"model": model, "max_tokens": 16000, "thinking": ThinkingParamFor(profile),
			"messages": []map[string]any{{"role": "user", "content": question}},
		}
		if effort := EffortFor(model, "high"); effort != "" {
			body["output_config"] = map[string]any{"effort": effort}
		}
		ex := c.Post(ctx, KindMessages, body)
		r.Exchanges = append(r.Exchanges, ex)
		r.DurationMs += ex.DurationMs
		if !ex.OK() {
			continue
		}
		thinking, _ := thinkingTokens(usageOf(ex.JSON))
		var content []map[string]any
		signed := false
		for _, raw := range contentBlocks(ex.JSON) {
			blk := mapOf(raw)
			switch str(blk["type"]) {
			case "thinking":
				signed = signed || str(blk["signature"]) != ""
				content = append(content, blk)
			case "text":
				content = append(content, blk)
			}
		}
		if signed && thinking >= reverseMinThinking {
			return question, content, thinking, true
		}
	}
	return "", nil, 0, false
}

// reverseQuestion 一道需要认真推演的同余题，参数随机。
func reverseQuestion() string {
	n := nonce("Q")
	a, b, cc := int(n[len(n)-1])%6+1, int(n[len(n)-2])%10+1, int(n[len(n)-3])%12+1
	return fmt.Sprintf("一个正整数被 7 除余 %d、被 11 除余 %d、被 13 除余 %d，同时它的各位数字之和是奇数。"+
		"求满足条件的最小正整数，并逐一验证。先想清楚再回答，最后一行只写 ANSWER: 数字。（%s）", a, b, cc, n)
}
