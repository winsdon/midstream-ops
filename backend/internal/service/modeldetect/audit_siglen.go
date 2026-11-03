package modeldetect

import (
	"fmt"
	"strings"
)

// 签名长度与输出用量的相容性。
//
// thinking 签名里装着加密后的思考正文，长度随思考 token 线性增长：2026-10/11 在真渠道上实测
// 约「600–850 字符的固定部分 + 每个思考 token 3–4 字符」（15 token → 648，536 → 1952，
// 2322 → 6704）。思考 token 计在 output_tokens 里，不可能超过它，所以签名长度有一条用输出量
// 就能算出的上限。阈值放得很宽（固定部分按 1100、每 token 按 5 字符），只抓量级上的矛盾。
const (
	sigLenBase     = 1100
	sigLenPerToken = 5
	// sigPastedMin 至少这么多条响应超限才定性，单条偏长（实测真渠道见过 904 字符 / 17 token）不作数。
	sigPastedMin = 2
)

// sigLenLimit 输出 out 个 token 的响应，签名总长不该超过的字符数。
func sigLenLimit(out int) int { return sigLenBase + sigLenPerToken*out }

// auditSignatureLength 签名比本次输出所能容纳的思考长得多：thinking 块不是这次生成的，是网关
// 从缓存里贴上去的。2026-11-03 实测：回复 10 个 token 却带 5020 字符的签名；同一网关上内容不同的
// PONG 拿到 1128–7252 字符、来自不同账号的签名。真渠道（claude神 51 个、yimo 14 个、pet 3 个签名）
// 一条都没超，贴签名的渠道（ant 49/51、哈基米 46/51、walk 52/56）几乎条条超。
//
// 这比「签名重复」更早暴露问题：签名池足够大时同一份签名未必在一轮里撞上两次，长度矛盾每条都看得出。
// 贴签名通常意味着后端根本没开思考（省成本），再拿旧签名装作在思考。扣分归入 scoreSignatureReplay。
func auditSignatureLength(r *CheckResult, bodies []auditBody) {
	var pasted []string
	checked := 0
	for _, b := range bodies {
		out := intOf(usageOf(b.body)["output_tokens"])
		total := 0
		for _, raw := range contentBlocks(b.body) {
			blk := mapOf(raw)
			if str(blk["type"]) == "thinking" {
				total += len(str(blk["signature"]))
			}
		}
		if total == 0 || out <= 0 {
			continue
		}
		checked++
		if total > sigLenLimit(out) {
			pasted = append(pasted, fmt.Sprintf("%s：输出 %d tokens，签名 %d 字符（上限约 %d）",
				b.checkID, out, total, sigLenLimit(out)))
		}
	}
	if checked == 0 {
		return
	}
	if len(pasted) < sigPastedMin {
		r.diagnose("签名长度与输出用量相容", true, fmt.Sprintf("%d 个带签名的响应，超限 %d 个", checked, len(pasted)))
		return
	}
	shown := pasted
	if len(shown) > 5 {
		shown = shown[:5]
	}
	d := fmt.Sprintf("%d/%d 个响应的签名比输出所能容纳的思考长得多：%s", len(pasted), checked, strings.Join(shown, "；"))
	r.assert("签名长度与输出用量相容", false, d)
	r.addEvidence("sig_pasted", "thinking 签名是贴上去的（不是本次生成，疑似关闭思考）", d, ClassWrapper, 4)
}

// 签名长度下限：签名装着加密后的思考，思考越多签名越长。实测真签名每个思考 token 约 2.1–3.8 字符
// （pet 490 → 1836、yimo 1348 → 4212、kaola 2322 → 6704）。下限放到「600 + 1.5 × 思考 token」，
// 只在思考 ≥ sigFloorMinThinking 时判，免得固定部分的波动左右结论。
const (
	sigFloorBase        = 600
	sigFloorPerThinking = 1.5
	sigFloorMinThinking = 200
)

// auditSignatureFloor 报了大量思考、签名却短得装不下：签名里根本没有思考，是伪造后端自己的签名体系。
// 2026-11-03 实测已知是假的 claude-fable-5 渠道：思考 645 / 824 tokens，签名恒为 356 字符（v16），
// 每 token 不到 0.6 字符；它还以 Invalid signature 拒掉了真 Anthropic 签名。
// 网关只能贴、只能剥，造不出更短的真签名，所以这条直接封顶；渠道已在贴签名时除外（见下）。
func auditSignatureFloor(r *CheckResult, bodies []auditBody) {
	var short []string
	checked := 0
	for _, b := range bodies {
		think, _ := thinkingTokens(usageOf(b.body))
		if think < sigFloorMinThinking {
			continue
		}
		total := 0
		for _, raw := range contentBlocks(b.body) {
			blk := mapOf(raw)
			if str(blk["type"]) == "thinking" {
				total += len(str(blk["signature"]))
			}
		}
		if total == 0 {
			continue
		}
		checked++
		floor := sigFloorBase + int(sigFloorPerThinking*float64(think))
		if total < floor {
			short = append(short, fmt.Sprintf("%s：思考 %d tokens，签名只有 %d 字符（至少应有 %d）", b.checkID, think, total, floor))
		}
	}
	if checked == 0 {
		return
	}
	if len(short) == 0 {
		r.diagnose("签名长度随思考增长", true, fmt.Sprintf("%d 个思考 ≥ %d tokens 的响应均满足", checked, sigFloorMinThinking))
		return
	}
	d := strings.Join(short, "；")
	// 已经在贴签名 / 回放的渠道，短签名多半是贴上来的旧签名（实测 walk：思考 2327、签名 3016），
	// 与伪造签名体系是两回事；那边已经扣过分，这里只记诊断。
	if hasEvidence([]*CheckResult{r}, "sig_pasted", "sig_replayed") {
		r.diagnose("签名长度随思考增长", false, d+"（渠道在贴签名，按同源证据处理，不另封顶）")
		return
	}
	r.assert("签名长度随思考增长", false, d)
	r.addEvidence("sig_too_short", "签名装不下思考（签名体系是伪造的）", d, ClassWrapper, 5)
	if r.AuthCapReason == "" {
		r.AuthCapReason = "thinking 签名不随思考增长，里面没有装思考：签名是伪造后端自己签的"
	}
}
