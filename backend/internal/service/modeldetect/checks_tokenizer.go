package modeldetect

import (
	"context"
	"fmt"
	"strings"
)

// tokenizerProbeTexts 分词器指纹的几段文本：中英混排、纯英文、纯中文、代码，不同分词器在这几类上差得最开。
var tokenizerProbeTexts = []string{
	"The quick brown fox 跳过了懒狗。 Σ∫∂ 🍣🦊 naïve façade — def f(x): return x**2  # 注释",
	"Internationalization and localization are means of adapting computer software to different languages.",
	"中华人民共和国成立于一九四九年十月一日，首都位于北京。",
	"for (let i = 0; i < arr.length; i++) { console.log(arr[i] ?? 'null'); }",
}

// claude5TokenizerDeltas Claude 5 代（Fable 5 / 5.1、Opus 5 / 5.5、Sonnet 5）上，后三段文本的输入量减去
// 第一段的差。2026-11-02 在已知真渠道上实测五个型号输入分别为 60/28/33/43 与 62/30/35/45（5.1 / 5.5
// 模板多 2），差值完全一致；Opus 4.7 是 −32/−26/−17，Sonnet 4.6 是 −34/−23/−20。
//
// 只比差值：网关往请求里注入的系统提示每次一样多，差值里会消掉；固定偏移外还能对上，才是同一个分词器。
// 2026-11-03 实测一条已知真渠道注入了 508 tokens，减掉后与 60/28/33/43 一字不差；已知是假的那条是
// 49/34/39/44，差值 −15/−10/−5，换任何偏移都对不上。
var claude5TokenizerDeltas = []int{-32, -27, -17}

const (
	// tokenizerExact 差值都在这个范围内算对上（实测完全一致，留 1 的余量）。
	tokenizerExact = 1
	// tokenizerOff 差值偏出这么多算明确对不上。
	tokenizerOff = 3
	// tokenizerUnstable 差值偏出这么多不可能是分词器差异，是注入在请求之间变了，不下结论。
	tokenizerUnstable = 100
)

// tokenizerReference 型号对应的分词差值基线；只收实测过的 Claude 5 代。
func tokenizerReference(model string) ([]int, bool) {
	v, ok := parseModelVersion(model)
	if !ok || v.major != 5 {
		return nil, false
	}
	switch v.family {
	case "fable", "opus", "sonnet":
		return claude5TokenizerDeltas, true
	}
	return nil, false
}

// checkTokenizer 分词器指纹：几段固定文本的输入量差值必须与该代 Claude 一致。
//
// 网关能改写型号名、伪造签名、模仿报错，却很难让另一个模型的分词器数出一模一样的 token。
// max_tokens 取 32：实测有网关把 ≤16 的请求当探活拦下、回固定的「OK」与固定计量。
func checkTokenizer(ctx context.Context, c *Client, _ *runState) *CheckResult {
	meta, _ := checkByID("tokenizer")
	r := newResult(meta)
	ref, ok := tokenizerReference(c.Target().Model)
	if !ok {
		r.Status = StatusUnsupported
		return r.finish("没有该型号的分词基线")
	}
	totals := make([]int, len(tokenizerProbeTexts))
	refused := 0
	for i, text := range tokenizerProbeTexts {
		ex := c.Post(ctx, KindMessages, map[string]any{
			"model": c.Target().Model, "max_tokens": 32,
			"messages": []map[string]any{{"role": "user", "content": text}},
		})
		r.Exchanges = append(r.Exchanges, ex)
		r.DurationMs += ex.DurationMs
		// 拒答照样计量：分类器拦的是输出，输入已经按真分词器数过了（实测 2026-11-03 一条渠道把第一段
		// 拒答，usage 里仍是一字不差的 60）。但缓存回放的拒答计量是假的，所以有拒答时只许加分、不许定罪。
		total, _, ok := inputTotal(usageOf(ex.JSON))
		if !ex.OK() || ex.JSON == nil || str(ex.JSON["type"]) == "error" || !ok || total == 0 {
			r.Status = StatusInconclusive
			r.diagnose("分词探针拿到计量", false, fmt.Sprintf("第 %d 段：%s", i+1, describeFailure(ex)))
			return r.finish("分词探针请求失败")
		}
		if isRefusal(ex) {
			refused++
		}
		totals[i] = total
	}

	deltas := make([]int, len(ref))
	worst, off := 0, 0
	for i := range ref {
		deltas[i] = totals[i+1] - totals[0]
		dev := abs(deltas[i] - ref[i])
		worst = max(worst, dev)
		if dev >= tokenizerOff {
			off++
		}
	}
	detail := fmt.Sprintf("输入 %s，差值 %s，Claude 5 基线差值 %s", joinInts(totals), joinInts(deltas), joinInts(ref))
	if refused > 0 {
		detail += fmt.Sprintf("（%d 段被拒答，计量照常采用）", refused)
	}
	switch {
	case allEqual(totals):
		r.Status = StatusInconclusive
		r.diagnose("四段文本的计量各不相同", false, detail+"（网关回了固定计量）")
		return r.finish("四段文本计量完全相同，像网关拦截后回的固定响应")
	case worst > tokenizerUnstable:
		r.Status = StatusInconclusive
		r.diagnose("注入量在请求之间稳定", false, detail)
		return r.finish("注入量在请求之间变化，差值不可比")
	case worst <= tokenizerExact:
		r.assert("分词器与 Claude 5 一致", true, detail)
		r.AuthScore = 10
		return r.finish("分词器与 Claude 5 一致：" + joinInts(deltas))
	case off >= 2 && refused > 0:
		r.Status = StatusInconclusive
		r.diagnose("分词器与 Claude 5 一致", false, detail+"；有拒答参与，可能是缓存回放的假计量，不据此定罪")
		return r.finish("分词差值对不上，但有拒答参与，不下结论")
	case off >= 2:
		r.assert("分词器与 Claude 5 一致", false, detail)
		r.AuthCapReason = "分词器与 Claude 5 不一致：同一组文本的 token 差值对不上（后端不是该代 Claude）"
		r.addEvidence("tokenizer_mismatch", "分词器与 Claude 5 不一致", detail, ClassWrapper, 5)
		return r.finish("分词器对不上：" + joinInts(deltas))
	}
	r.Status = StatusInconclusive
	r.diagnose("分词器与 Claude 5 一致", false, detail)
	return r.finish("分词差值略有偏差，不下结论：" + joinInts(deltas))
}

func joinInts(v []int) string {
	parts := make([]string, len(v))
	for i, n := range v {
		parts[i] = fmt.Sprint(n)
	}
	return strings.Join(parts, "/")
}

func allEqual(v []int) bool {
	for _, n := range v[1:] {
		if n != v[0] {
			return false
		}
	}
	return true
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
