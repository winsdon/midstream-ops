package modeldetect

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// pelicanPrompt 鹈鹕测试（Simon Willison 的 pelican-on-a-bicycle）原题。
const pelicanPrompt = "Generate an SVG of a pelican riding a bicycle and animate this"

// candyPrompt 糖果题。原题的表格在转述时被压成了一行，这里按行排成 Markdown 表格；
// 末尾的格式行只规定作答格式、不含任何解题提示，自动判分只认它。
const candyPrompt = "在一个黑色的袋子里放有三种口味的糖果，每种糖果有两种不同的形状（圆形和五角星形，不同的形状靠手感可以分辨）。" +
	"现已知不同口味的糖和不同形状的数量统计如下表。参赛者需要在活动前决定摸出的糖果数目，那么，" +
	"最少取出多少个糖果才能保证手中同时拥有不同形状的苹果味和桃子味的糖？" +
	"（同时手中有圆形苹果味匹配五角星桃子味糖果，或者有圆形桃子味匹配五角星苹果味糖果都满足要求）\n\n" +
	"| | 苹果味 | 桃子味 | 西瓜味 |\n" +
	"|---|---|---|---|\n" +
	"| 圆形 | 7 | 9 | 8 |\n" +
	"| 五角星形 | 7 | 6 | 4 |\n\n" +
	"禁止联网,禁止写代码算出答案.禁止使用外部工具\n\n" +
	"请在回答最后单独一行按「最终答案：数字」的格式给出答案。"

// candyAnswer 糖果题标准答案。关键在题干「不同的形状靠手感可以分辨」：口味摸不出、形状摸得出，
// 摸的人能自己决定圆的、五角星各摸几颗。摸 12 颗五角星——不是苹果的五角星最多 6+4=10 颗、
// 不是桃子的最多 7+4=11 颗，所以苹果、桃子各至少 1 颗；再摸 9 颗圆的——圆形西瓜只有 8 颗，
// 至少 1 颗是苹果或桃子。圆的那颗不论是哪种都能和五角星配成一对，共 21 颗，且没有更少的分法。
// 把形状当成摸不出来、只按总数算会得到 29，正是这道题要筛掉的漏读题干的答案。
const candyAnswer = 21

// candyNote 勾选面板上的题目说明，标准答案取自常量，免得两处写得不一致。
var candyNote = fmt.Sprintf("最坏情况推理：自动核对最终答案（标准答案 %d）", candyAnswer)

const (
	pelicanMaxTokens = 32000
	candyMaxTokens   = 16000
	// iqStreamBudget 智商题整段读取的总时限。两题都带 thinking、输出又长，
	// 默认「3×超时+10s」的上限在慢渠道上会在写完前截断；需要提前结束可以在页面上取消。
	iqStreamBudget = 15 * time.Minute
	// maxOutputTextRunes 回复全文的保存上限。糖果题的推理通常几千字，留足余量。
	maxOutputTextRunes = 20000
	// maxDocumentBytes 作品的保存上限。作品不能截断（半截 SVG 渲染不出来），超限只记诊断不存。
	maxDocumentBytes = 256 << 10
)

// askIQ 发一道智商题：流式 + thinking（与质量基准同一套 thinking 口径），把回复还原出来。
//
// 走流式是因为非流式要等整段生成完才回响应头，默认 90s 的首字节超时会被打穿。
// HTTP 层面失败时记「证据不足」并返回 false。流在 200 之后才断（读超时、流内 overloaded、
// 网关没收尾）同样是临时故障：把原因记进 NetworkError，让它和 429 / 5xx 一样可以一键重试，
// 已经收到的内容照常返回，留给调用方保存以便查看。
func askIQ(ctx context.Context, c *Client, st *runState, r *CheckResult, prompt string, maxTokens int) (streamedReply, bool) {
	ex := c.PostStreamWithin(ctx, KindMessages, map[string]any{
		"model": c.Target().Model, "max_tokens": maxTokens, "stream": true,
		"thinking": qualityThinkingParam(st.profile),
		"messages": []map[string]any{{"role": "user", "content": prompt}},
	}, iqStreamBudget)
	r.Exchanges = []*Exchange{ex}
	r.DurationMs = ex.DurationMs
	if !ex.OK() {
		r.Status = StatusInconclusive
		r.diagnose("请求成功", false, describeFailure(ex))
		return streamedReply{}, false
	}

	reply := assembleStream(ex.Events)
	if len(ex.Events) == 0 && ex.JSON != nil {
		reply = replyFromJSON(ex.JSON)
	}
	// 原始 SSE 在解析前已脱敏，但 key 若被切在两个 delta 里，拼接后才完整出现，这里再擦一遍
	reply.Text = c.redact(reply.Text)

	stop := reply.StopReason
	if stop == "" {
		stop = "-"
	}
	r.diagnose("响应统计", true, fmt.Sprintf("TTFT=%s，总耗时=%dms，输出=%d tokens，thinking=%d 字符，stop_reason=%s",
		fmtMsPtr(ex.TTFTMs), ex.DurationMs, reply.OutputTokens, reply.ThinkingChars, stop))
	if !reply.Complete {
		reason := ex.NetworkError
		if reason == "" {
			reason = reply.StreamError
		}
		if reason == "" {
			reason = "没有收到 message_stop，响应中途断开"
		}
		r.diagnose("响应完整", false, reason)
		if ex.NetworkError == "" {
			ex.NetworkError = "流式响应不完整：" + reason
		}
	}
	return reply, true
}

// checkPelican 鹈鹕测试。
//
// 画得好不好只能人看：本项只核对「交出了完整、带动画的 SVG」，作品原样保存，
// 由前端放进沙箱 iframe 渲染、多目标并排对照。
func checkPelican(ctx context.Context, c *Client, st *runState) *CheckResult {
	meta, _ := checkByID("iq-pelican")
	r := newResult(meta)
	prompt := pelicanPrompt
	if st != nil && strings.TrimSpace(st.pelicanPrompt) != "" {
		prompt = st.pelicanPrompt
	}
	r.Prompt = prompt
	reply, ok := askIQ(ctx, c, st, r, prompt, pelicanMaxTokens)
	if !ok {
		return r.finish("请求失败，没拿到作品")
	}

	doc, kind := extractDocument(reply.Text)
	r.Output = artworkOutput(r, reply.Text, doc, kind)
	if !reply.Complete {
		r.Status = StatusInconclusive
		return r.finish("响应中途断开，作答不完整")
	}
	truncated := reply.StopReason == "max_tokens"
	if doc == "" {
		if truncated {
			r.Status = StatusInconclusive
			r.diagnose("输出未被 max_tokens 截断", false, fmt.Sprintf("max_tokens=%d 内没写完作品", pelicanMaxTokens))
			return r.finish("输出被 max_tokens 截断，没拿到完整 SVG")
		}
		r.Status = StatusSuspicious
		r.assert("交出完整的 SVG 作品", false, "回复里没有闭合的 <svg>…</svg> 或 <html>…</html>")
		return r.finish("没有交出完整的 SVG")
	}

	label := fmt.Sprintf("%s %.1f KB", strings.ToUpper(kind), float64(len(doc))/1024)
	r.assert("交出完整的 SVG 作品", true, label)
	kinds := animationKinds(doc)
	animation := strings.Join(kinds, " / ")
	if animation == "" {
		animation = "未发现 SMIL / CSS / JS 动画"
	}
	r.assert("作品包含动画", len(kinds) > 0, animation)
	if len(kinds) == 0 {
		if truncated {
			// 可能是先交了静态版、动画版还没写完就被截断
			r.Status = StatusInconclusive
			r.diagnose("输出未被 max_tokens 截断", false, "动画版可能还没写完")
			return r.finish(label + "，被 max_tokens 截断前没写出动画")
		}
		r.Status = StatusSuspicious
		return r.finish(label + "，没有动画")
	}
	return r.finish(fmt.Sprintf("%s · %s 动画 · 画面请人工评判", label, animation))
}

// artworkOutput 作品原样保存；没抽到作品时保留回复原文，方便看模型到底交了什么。
// 作品超过上限不存：半截 SVG 渲染不出来，截断没有意义。
func artworkOutput(r *CheckResult, text, doc, kind string) *ModelOutput {
	if doc == "" {
		return &ModelOutput{Text: truncateRunes(text, maxOutputTextRunes)}
	}
	if len(doc) > maxDocumentBytes {
		r.diagnose("作品可保存预览", false,
			fmt.Sprintf("%.1f KB 超过 %d KB 上限，未保存", float64(len(doc))/1024, maxDocumentBytes>>10))
		return nil
	}
	return &ModelOutput{Document: doc, DocumentKind: kind}
}

// checkCandy 糖果题：最坏情况推理，自动核对「最终答案」行。
func checkCandy(ctx context.Context, c *Client, st *runState) *CheckResult {
	meta, _ := checkByID("iq-candy")
	r := newResult(meta)
	reply, ok := askIQ(ctx, c, st, r, candyPrompt, candyMaxTokens)
	if !ok {
		return r.finish("请求失败，没拿到回答")
	}

	text := truncateRunes(reply.Text, maxOutputTextRunes)
	expected := strconv.Itoa(candyAnswer)
	if !reply.Complete {
		r.Output = &ModelOutput{Text: text, Expected: expected}
		r.Status = StatusInconclusive
		return r.finish("响应中途断开，作答不完整")
	}

	got, found := extractFinalAnswer(reply.Text)
	if reply.StopReason == "max_tokens" {
		// 被截断的回答没有真正的结尾，推理里的中间数不能当结论：只认恰好落在最后一行的格式行
		got, found = answerOnLastLine(reply.Text)
		if !found {
			r.Output = &ModelOutput{Text: text, Expected: expected}
			r.Status = StatusInconclusive
			r.diagnose("输出未被 max_tokens 截断", false, fmt.Sprintf("max_tokens=%d 内没写完", candyMaxTokens))
			return r.finish("输出被 max_tokens 截断，没给出答案")
		}
	}
	if !found {
		r.Output = &ModelOutput{Text: text, Expected: expected}
		r.Status = StatusSuspicious
		r.assert("按要求给出最终答案", false, "回复里找不到「最终答案：数字」")
		return r.finish("没有按要求给出最终答案")
	}

	answer := strconv.Itoa(got)
	r.Output = &ModelOutput{Text: text, Answer: answer, Expected: expected}
	correct := got == candyAnswer
	r.assert(fmt.Sprintf("答案正确（标准答案 %d）", candyAnswer), correct, "解析到 "+answer)
	if !correct {
		r.Status = StatusSuspicious
		return r.finish(fmt.Sprintf("答 %d，标准答案 %d", got, candyAnswer))
	}
	return r.finish("答对 " + answer)
}
