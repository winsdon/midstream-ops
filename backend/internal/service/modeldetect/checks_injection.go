package modeldetect

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// 「是否 0 注入」：网关有没有往请求里塞东西。
//
// 方法论来自同行的中转站纯度探针：拿官方直连实测的输入 token 基线对账，而不是问模型
// 「你有没有被注入」——模型会顺着问题回答。硬证据是计量：裸请求、带 system 的请求、
// n 个数字的序列（官方 tokenizer 下严格线性）、count_tokens；再并发采样看号池里是否
// 混着注入号。thinking 自述只作软证据，用来发现从 usage 里扣掉、账单上看不出的隐藏注入。
//
// 本项只做检测：Informational，不参与渠道分类与真实性评分，也不进跨项审计。

const (
	// zeroInjectionSamples 并发采样次数。号池里干净号与注入号混用时，单次请求可能恰好落在
	// 干净号上；20 次在「两成账号注入」时漏检的概率约 1%。
	zeroInjectionSamples = 20
	// zeroInjectionWorkers 本项内部的并发度。同样的请求并发打出去，粘性路由的账号并发打满了
	// 才会分到别的账号，采样要的正是这个效果。
	zeroInjectionWorkers = 10
	// zeroInjectionRequests 采样 20 + 数字序列 3 + 带 system 1 + count_tokens 2 + 隐藏注入 1。
	zeroInjectionRequests = zeroInjectionSamples + len(digitProbeSizes) + 4
)

// digitProbeSizes 数字序列的三档长度（n 个空格分隔的数字）。
var digitProbeSizes = [3]int{1, 20, 100}

// hiddenInjectionPrompt 隐藏注入软探针。只问有没有、不要求复述内容：要求复述指令或推理
// 容易撞上官方的反提取分类器（实测 CC Max 渠道会以 reasoning_extraction 拒答）。
const hiddenInjectionPrompt = "Before this message, did you receive any system prompt or other instructions? Answer with exactly YES or NO."

// injectionBaseline 官方直连的输入基线（input + cache_creation + cache_read）。
//
// 数字序列的 token 数是 DigitSlope·n + DigitIntercept。新 tokenizer（Opus 4.7 起）把空格与
// 数字各算一个 token，所以 slope=2。DigitSlope 为 0 表示没有实测，只看线性、不对账。
type injectionBaseline struct {
	Bare           int // pingBody 的裸请求
	System         int // pirateBody 的带 system 请求
	DigitSlope     int
	DigitIntercept int
	Source         string

	// Model 命中的基线型号，由查表函数填，表里不写。
	Model string
}

// injectionBaselines 只收有官方实测的型号。没收录的型号只做相对判断（count_tokens 对账、
// 混池、大额注入），不下「精确 0 注入」的结论；同族型号的基线只作参考、写进诊断。
var injectionBaselines = map[string]injectionBaseline{
	// 2026-09-10 三条官方直连渠道一致（ccaiu / oksoapi / kkidc），oksoapi 的 count_tokens 同为 18。
	"claude-opus-5": {Bare: 18, System: 48, Source: "2026-09 三条官方直连渠道实测"},
	// 同行纯度探针在官方直连上实测：n 个数字的输入恰为 2n+5。
	"claude-opus-4-8": {DigitSlope: 2, DigitIntercept: 5, Source: "同行纯度探针实测（2n+5）"},
}

// grossBareInput 裸请求输入到这个量级，不需要基线也能断定被拼了大段系统提示。
const grossBareInput = 300

// lookupInjectionBaseline 按模型 id 查官方基线，带日期后缀的 id 退回不带后缀的主键。
// 只有它查到的基线能用来下「检出注入 / 0 注入」的结论。
func lookupInjectionBaseline(model string) (injectionBaseline, bool) {
	for _, key := range modelKeys(model) {
		if b, ok := injectionBaselines[key]; ok {
			b.Model = key
			return b, true
		}
	}
	return injectionBaseline{}, false
}

// referenceBaseline 同族型号的基线，只作参考：新型号的对话模板可能不同——claude-opus-5-5 是
// 2026-09-22 发布的 Opus 5.5，thinking 常开，拿 Opus 5 的 18 / 48 去比，差出来的几个 token
// 分不清是注入还是模板。报告里写出差值，结论仍按「无官方基线」处理。
func referenceBaseline(model string) (injectionBaseline, bool) {
	exact := map[string]bool{}
	for _, key := range modelKeys(model) {
		exact[key] = true
	}
	for _, key := range familyModelKeys(model) {
		if exact[key] {
			continue
		}
		if b, ok := injectionBaselines[key]; ok {
			b.Model = key
			return b, true
		}
	}
	return injectionBaseline{}, false
}

// describe 基线出处，写进诊断。
func (b injectionBaseline) describe() string {
	return fmt.Sprintf("%s：%s", b.Model, b.Source)
}

// digitProbeText n 个 0-9 循环的数字，空格分隔。
func digitProbeText(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = strconv.Itoa(i % 10)
	}
	return strings.Join(parts, " ")
}

// ziProbe 一次探针请求。
type ziProbe struct {
	kind string
	body map[string]any
}

// 探针在切片里的位置，采样在最前面。
const (
	ziDigitsAt      = zeroInjectionSamples
	ziSystemAt      = ziDigitsAt + len(digitProbeSizes)
	ziCountBareAt   = ziSystemAt + 1
	ziCountDigitsAt = ziCountBareAt + 1
	ziHiddenAt      = ziCountDigitsAt + 1
)

// zeroInjectionProbes 按固定顺序拼出全部探针。裸请求与带 system 的请求复用 ping 与
// caller-system 的原文——官方基线就是按这两段原文实测的。
func zeroInjectionProbes(model string, profile ThinkingProfile) []ziProbe {
	probes := make([]ziProbe, 0, zeroInjectionRequests)
	for i := 0; i < zeroInjectionSamples; i++ {
		probes = append(probes, ziProbe{KindMessages, pingBody(model)})
	}
	for _, n := range digitProbeSizes {
		probes = append(probes, ziProbe{KindMessages, map[string]any{
			"model": model, "max_tokens": 16,
			"messages": []map[string]any{{"role": "user", "content": digitProbeText(n)}},
		}})
	}
	probes = append(probes, ziProbe{KindMessages, pirateBody(model, 16)})
	probes = append(probes,
		ziProbe{KindCountTokens, map[string]any{"model": model, "messages": pingBody(model)["messages"]}},
		ziProbe{KindCountTokens, map[string]any{"model": model,
			"messages": []map[string]any{{"role": "user", "content": digitProbeText(digitProbeSizes[2])}}}},
	)
	probes = append(probes, ziProbe{KindMessages, map[string]any{
		"model": model, "max_tokens": 2048, "thinking": profile.ThinkingParam("summarized"),
		"messages": []map[string]any{{"role": "user", "content": hiddenInjectionPrompt}},
	}})
	return probes
}

// runProbes 以 workers 的并发度发出全部探针，结果与 probes 一一对应。
func runProbes(ctx context.Context, c *Client, probes []ziProbe, workers int) []*Exchange {
	out := make([]*Exchange, len(probes))
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for i, p := range probes {
		wg.Add(1)
		go func(i int, p ziProbe) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = c.Post(ctx, p.kind, p.body)
		}(i, p)
	}
	wg.Wait()
	return out
}

// checkZeroInjection 是否 0 注入。只做检测，结论不参与打分。
func checkZeroInjection(ctx context.Context, c *Client, st *runState) *CheckResult {
	meta, _ := checkByID("zero-injection")
	r := newResult(meta)
	model := c.Target().Model

	start := time.Now()
	exs := runProbes(ctx, c, zeroInjectionProbes(model, st.profile), zeroInjectionWorkers)
	r.Exchanges = exs
	r.DurationMs = time.Since(start).Milliseconds()

	base, known := lookupInjectionBaseline(model)
	if known {
		r.diagnose("有官方输入基线", true, base.describe())
	} else {
		r.diagnose("有官方输入基线", false,
			fmt.Sprintf("未收录 %s：只能看 count_tokens 对账、混池与大额注入，不下精确 0 注入的结论", model))
	}
	o := observeZeroInjection(exs)
	summary := judgeZeroInjection(r, o, base)
	if !known {
		if ref, ok := referenceBaseline(model); ok {
			if note := describeReference(r, o, ref); note != "" {
				summary += "；" + note
			}
		}
	}
	return r.finish(summary)
}

// describeReference 与同族型号基线的差值：写进诊断与摘要，只作参考、不参与结论。
func describeReference(r *CheckResult, o ziObservation, ref injectionBaseline) string {
	detail := fmt.Sprintf("参照 %s 的基线（型号不同，差值可能来自对话模板，只作参考）", ref.Model)
	var diffs []string
	if buckets := poolBuckets(o.samples); len(buckets) > 0 && ref.Bare > 0 {
		diff := buckets[0].total - ref.Bare
		r.diagnose("裸请求与同族基线的差值", diff == 0, fmt.Sprintf("实测 %d，%s %d，差 %+d", buckets[0].total, detail, ref.Bare, diff))
		diffs = append(diffs, fmt.Sprintf("裸请求 %+d", diff))
	}
	if o.systemOK && ref.System > 0 {
		diff := o.system - ref.System
		r.diagnose("带 system 与同族基线的差值", diff == 0, fmt.Sprintf("实测 %d，%s %d，差 %+d", o.system, detail, ref.System, diff))
		diffs = append(diffs, fmt.Sprintf("带 system %+d", diff))
	}
	if len(diffs) == 0 {
		return ""
	}
	return fmt.Sprintf("参照 %s：%s（型号不同，只作参考）", ref.Model, strings.Join(diffs, "、"))
}

// ziObservation 从探针响应里读出的计量。
type ziObservation struct {
	samples   []int // 成功采样的输入总量
	failed    int   // 失败或被拒答的采样数
	cached    int   // 出现缓存读写的采样数（裸请求没打 cache_control，缓存只能是网关注入的）
	sampleIDs []string

	digits   [3]int
	digitsOK [3]bool

	system   int
	systemOK bool
	obeyed   bool

	countBare     int
	countBareOK   bool
	countDigits   int
	countDigitsOK bool

	hidden hiddenProbe
	replay ziReplay
}

// ziReplay 本项响应里的 thinking 签名复用情况。
type ziReplay struct {
	signed int // 带签名的响应数
	reused int // 与其他响应共用签名的响应数
	groups int // 被复用的签名份数
}

// observeReplay 统计本项响应的签名复用。签名内含逐次随机的 nonce，20 次一模一样的裸请求
// 也该拿到 20 份不同的签名；共用一份说明网关把缓存的响应回放给了后来的请求。
func observeReplay(exs []*Exchange) ziReplay {
	counts := map[string]int{}
	var rep ziReplay
	for _, ex := range exs {
		sigs := exchangeSignatures(ex)
		if len(sigs) == 0 {
			continue
		}
		rep.signed++
		counts[sigs[0]]++
	}
	for _, n := range counts {
		if n > 1 {
			rep.groups++
			rep.reused += n
		}
	}
	return rep
}

func observeZeroInjection(exs []*Exchange) ziObservation {
	var o ziObservation
	for _, ex := range exs[:zeroInjectionSamples] {
		total, cached, ok := ziInputTotal(ex)
		if !ok {
			o.failed++
			continue
		}
		o.samples = append(o.samples, total)
		if cached {
			o.cached++
		}
		if id := messageIDOf(ex); id != "" {
			o.sampleIDs = append(o.sampleIDs, id)
		}
	}
	for i := range digitProbeSizes {
		o.digits[i], _, o.digitsOK[i] = ziInputTotal(exs[ziDigitsAt+i])
	}
	if sys := exs[ziSystemAt]; sys != nil {
		o.system, _, o.systemOK = ziInputTotal(sys)
		o.obeyed = o.systemOK && pirateObeyed(contentText(sys.JSON))
	}
	o.countBare, o.countBareOK = countTokensValue(exs[ziCountBareAt])
	o.countDigits, o.countDigitsOK = countTokensValue(exs[ziCountDigitsAt])
	o.hidden = judgeHiddenProbe(exs[ziHiddenAt])
	o.replay = observeReplay(exs)
	return o
}

// ziInputTotal 一次响应的全部输入：input + cache_creation + cache_read。
//
// 分类器拒答的响应不计：实测同一段请求被拒时 input_tokens 在各渠道间相差几十，不可对账。
func ziInputTotal(ex *Exchange) (total int, cached, ok bool) {
	if ex == nil || !ex.OK() || ex.JSON == nil || str(ex.JSON["type"]) == "error" ||
		str(ex.JSON["stop_reason"]) == "refusal" {
		return 0, false, false
	}
	return inputTotal(usageOf(ex.JSON))
}

// countTokensValue count_tokens 的计数；端点不可用或返回值不像计数时 ok=false。
func countTokensValue(ex *Exchange) (int, bool) {
	if ex == nil || !ex.OK() || ex.JSON == nil {
		return 0, false
	}
	v, ok := num(ex.JSON["input_tokens"])
	if !ok || v <= 0 {
		return 0, false
	}
	return int(v), true
}

// hiddenInstructionRe thinking 里承认上下文有指令，或提到不能透露。
//
// 只收「指令存在且有内容」的说法。探针自己的提问会让模型复述「I'm asked to answer
// with exactly YES or NO」，所以「被要求 / 被告知」这类泛泛的句式不算；「不能分享」也要
// 带上宾语（系统提示 / 指令）才算。
var hiddenInstructionRe = regexp.MustCompile(`(?i)(system (prompt|message) (says|states|tells|instructs|mentions|contains|includes|asks|requires)|(my|the) instructions (say|tell|state|require)|(not|n't) (supposed|allowed|permitted) to (reveal|disclose|share|mention)|(can't|cannot|shouldn't|should not|must not|mustn't) (reveal|disclose|share|mention) (the |my |any |these |those )?(system|instructions|prompt)|keep (it|this|these|them) (secret|confidential)|claude code|系统提示词?(里|中|要求|说)|不能(透露|泄露)|不要(透露|泄露))`)

// parseYesNo 读隐藏注入探针的回答。先剥掉开头的空白与标点（含全角），
// 否定词先于肯定词判断——「不是」里也有「是」。
func parseYesNo(answer string) (yes, no bool) {
	s := strings.ToLower(strings.TrimLeftFunc(answer, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
	}))
	for _, p := range []string{"no", "否", "不", "没", "无"} {
		if strings.HasPrefix(s, p) {
			return false, true
		}
	}
	for _, p := range []string{"yes", "是", "有"} {
		if strings.HasPrefix(s, p) {
			return true, false
		}
	}
	return false, false
}

// hiddenProbe 隐藏注入软探针的判读。
type hiddenProbe struct {
	answered bool // 拿到了可判读的回答
	signal   bool // 自述或 thinking 表明上下文里有调用方之外的指令
	note     string
}

// judgeHiddenProbe 判读隐藏注入探针。thinking 与回答对不上（thinking 说有指令、回答说没有）
// 是同行实测过的隐藏注入形态；这种注入不计费，看 token 查不出来。
func judgeHiddenProbe(ex *Exchange) hiddenProbe {
	if ex == nil || !ex.OK() || ex.JSON == nil {
		if ex == nil {
			return hiddenProbe{note: "未发出"}
		}
		return hiddenProbe{note: describeFailure(ex)}
	}
	if str(ex.JSON["stop_reason"]) == "refusal" {
		return hiddenProbe{note: "被安全分类器拒答"}
	}
	answer := contentText(ex.JSON)
	thinking := thinkingText(ex.JSON)
	yes, no := parseYesNo(answer)
	switch {
	case yes:
		return hiddenProbe{answered: true, signal: true, note: "模型回答 YES：上下文里有调用方之外的指令"}
	case hiddenInstructionRe.MatchString(thinking):
		return hiddenProbe{answered: true, signal: true, note: fmt.Sprintf("回答「%s」，但 thinking 提到「%s」",
			clip(answer, 20), hiddenInstructionRe.FindString(thinking))}
	case no:
		note := "回答 NO，thinking 未提到其他指令"
		if thinking == "" {
			note = "回答 NO（渠道未返回 thinking 摘要）"
		}
		return hiddenProbe{answered: true, note: note}
	}
	return hiddenProbe{note: "回答无法判读：" + clip(answer, 40)}
}

// ziBucket 输入总量相同的一组采样。
type ziBucket struct{ total, count int }

// poolBuckets 按输入总量分组：次数多的在前，同次数时总量小的在前。
func poolBuckets(samples []int) []ziBucket {
	counts := map[int]int{}
	for _, s := range samples {
		counts[s]++
	}
	out := make([]ziBucket, 0, len(counts))
	for total, n := range counts {
		out = append(out, ziBucket{total: total, count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].count != out[j].count {
			return out[i].count > out[j].count
		}
		return out[i].total < out[j].total
	})
	return out
}

// describeBuckets 把分组写成可读说明。有官方基线就以它为参照，没有就以最少的一档为参照。
func describeBuckets(buckets []ziBucket, official int) string {
	if len(buckets) == 1 && official <= 0 {
		return fmt.Sprintf("%d 次输入均为 %d", buckets[0].count, buckets[0].total)
	}
	ref, name := official, "官方"
	if ref <= 0 {
		ref, name = buckets[0].total, "最少的一档"
		for _, b := range buckets {
			if b.total < ref {
				ref = b.total
			}
		}
	}
	parts := make([]string, 0, len(buckets))
	for _, b := range buckets {
		parts = append(parts, fmt.Sprintf("%d 次输入 %d（%s）", b.count, b.total, relativeTo(b.total-ref, name)))
	}
	return strings.Join(parts, "、")
}

func relativeTo(diff int, name string) string {
	switch {
	case diff > 0:
		return fmt.Sprintf("比%s多 %d", name, diff)
	case diff < 0:
		return fmt.Sprintf("比%s少 %d", name, -diff)
	}
	return "= " + name
}

// digitFit 数字序列的线性拟合。
//
// 官方 tokenizer 下每多一个数字，token 数恒定增加 slope；网关注入是常数，在差分里抵消。
// 三档差分对不上同一个整数斜率，说明 token 数是网关估算的，不是真 tokenizer 算的。
type digitFit struct {
	complete  bool // 三档都拿到了
	linear    bool
	slope     int
	intercept int
}

func fitDigits(totals [3]int, ok [3]bool) digitFit {
	if !ok[0] || !ok[1] || !ok[2] {
		return digitFit{}
	}
	n := digitProbeSizes
	span1, span2 := n[1]-n[0], n[2]-n[1]
	d1, d2 := totals[1]-totals[0], totals[2]-totals[1]
	fit := digitFit{complete: true}
	if d1 <= 0 || d1%span1 != 0 || d2%span2 != 0 || d1/span1 != d2/span2 {
		return fit
	}
	fit.linear = true
	fit.slope = d1 / span1
	fit.intercept = totals[0] - fit.slope*n[0]
	return fit
}

// ziFindings 对账过程中积累的结论依据。
type ziFindings struct {
	injected  []string // 检出注入的依据
	anomalies []string // 计量与官方对不上，无从对账
	matched   []string // 与官方基线一致的项
	amount    int      // 注入量估计：取第一条能给出差值的依据
}

func (f *ziFindings) inject(note string, diff int) {
	f.injected = append(f.injected, note)
	if f.amount == 0 && diff > 0 {
		f.amount = diff
	}
}

// reconcile 一项实测对官方基线：多了是注入，少了是计量异常，相等记为一致。
//
// 比官方还少不会是注入，只能是请求被改写（比如去掉了默认的 thinking）、背后不是这个模型，
// 或者 usage 是网关自己算的——总之这条链路的计量不能拿来对账。
func (f *ziFindings) reconcile(r *CheckResult, name string, got, official int) {
	diff := got - official
	r.diagnose(name+" = 官方基线", diff == 0, fmt.Sprintf("实测 %d，官方 %d", got, official))
	switch {
	case diff > 0:
		f.inject(fmt.Sprintf("%s %d vs 官方 %d", name, got, official), diff)
	case diff < 0:
		f.anomalies = append(f.anomalies, fmt.Sprintf("%s %d 低于官方 %d（请求被改写或背后不是这个模型）", name, got, official))
	default:
		f.matched = append(f.matched, fmt.Sprintf("%s = 官方 %d", name, official))
	}
}

// judgeZeroInjection 按观察下结论，写入诊断，返回一句话摘要。
//
// 结论四选一：0 注入（与官方基线逐项一致）、检出注入或号池混用、计量异常（与官方对不上，
// 无从对账）、证据不足（没有官方基线或请求失败）。软证据只在计量显示 0 注入时起作用：
// 把结论降为可疑，提示人工复核。
func judgeZeroInjection(r *CheckResult, o ziObservation, base injectionBaseline) string {
	if len(o.samples) == 0 {
		r.Status = StatusInconclusive
		r.diagnose("裸请求采样成功", false, fmt.Sprintf("%d 次全部失败或被拒答", o.failed))
		return "裸请求全部失败，无法对账"
	}
	buckets := poolBuckets(o.samples)
	bare := buckets[0].total
	r.diagnose("裸请求采样成功", o.failed == 0,
		fmt.Sprintf("成功 %d 次，失败或被拒答 %d 次", len(o.samples), o.failed))
	r.diagnose("并发采样输入一致（未见混池）", len(buckets) == 1, describeBuckets(buckets, base.Bare))
	describeSampleIDs(r, o.sampleIDs)
	describeReplay(r, o.replay)

	f := &ziFindings{}
	if o.cached > 0 {
		f.inject(fmt.Sprintf("%d 次裸请求出现缓存读写（没打 cache_control，只能是网关注入）", o.cached), 0)
	}
	if base.Bare > 0 {
		f.reconcile(r, "裸请求", bare, base.Bare)
	} else if bare >= grossBareInput {
		f.inject(fmt.Sprintf("裸请求输入 %d 远超正常范围", bare), 0)
	}
	if !o.systemOK {
		r.diagnose("带 system 请求成功", false, "请求失败或被拒答")
	} else if base.System > 0 {
		f.reconcile(r, "带 system", o.system, base.System)
	}
	reconciled := reconcileDigits(r, f, fitDigits(o.digits, o.digitsOK), o.digits, base)
	reconcileCount(r, f, o, bare, base, reconciled)
	describeSoftSignals(r, o)
	return concludeZeroInjection(r, f, o, buckets, base)
}

// reconcileDigits 数字序列：先看是否按整数斜率线性增长（真 tokenizer），有官方实测时再对账。
// 返回是否已与官方数字基线对过账。
func reconcileDigits(r *CheckResult, f *ziFindings, fit digitFit, totals [3]int, base injectionBaseline) bool {
	n := digitProbeSizes
	detail := fmt.Sprintf("n=%d/%d/%d → 输入 %d/%d/%d", n[0], n[1], n[2], totals[0], totals[1], totals[2])
	switch {
	case !fit.complete:
		r.diagnose("数字序列三档请求成功", false, detail)
		return false
	case !fit.linear:
		r.diagnose("数字序列按整数斜率线性增长（真 tokenizer）", false, detail)
		f.anomalies = append(f.anomalies, "数字序列不按整数斜率增长（token 数像网关估算）")
		return false
	}
	r.diagnose("数字序列按整数斜率线性增长（真 tokenizer）", true, fmt.Sprintf("%s，每个数字 %d token", detail, fit.slope))
	if base.DigitSlope <= 0 {
		return false
	}
	if fit.slope != base.DigitSlope {
		r.diagnose("每个数字的 token 数与官方一致", false, fmt.Sprintf("实测 %d，官方 %d", fit.slope, base.DigitSlope))
		f.anomalies = append(f.anomalies, fmt.Sprintf("每个数字 %d token，官方 %d（tokenizer 与型号不符）", fit.slope, base.DigitSlope))
		return false
	}
	f.reconcile(r, fmt.Sprintf("数字序列截距（%dn+b 的 b）", fit.slope), fit.intercept, base.DigitIntercept)
	return true
}

// reconcileCount count_tokens 对账。
//
// 有官方基线时 count_tokens 只说明计数端点是不是官方透传——注入量已由推理路径直接对账。
// 没有基线时退一步：推理与计数在两档长度上相差同一个常数，说明两边是同一个 tokenizer、
// 只是推理路径多拼了东西，这个常数就是注入量；两档差值不同说明 count_tokens 是网关估算的，
// 拿它对账没有意义。数字序列已与官方对上账时以数字为准，count_tokens 只作记录。
func reconcileCount(r *CheckResult, f *ziFindings, o ziObservation, bare int, base injectionBaseline, digitsReconciled bool) {
	if !o.countBareOK {
		r.diagnose("count_tokens 可用", false, "网关未放行 count_tokens（不影响结论）")
		return
	}
	if base.Bare > 0 {
		r.diagnose("count_tokens = 官方基线", o.countBare == base.Bare,
			fmt.Sprintf("count_tokens %d，官方 %d（只说明计数端点是否透传）", o.countBare, base.Bare))
		return
	}
	gap := bare - o.countBare
	r.diagnose("推理与 count_tokens 计数一致", gap == 0, fmt.Sprintf("推理 %d，count_tokens %d", bare, o.countBare))
	if !o.countDigitsOK || !o.digitsOK[2] {
		r.diagnose("count_tokens 两档对账", false, "长序列缺数据，确认不了差值是不是常数，不据此判注入")
		return
	}
	longGap := o.digits[2] - o.countDigits
	constant := longGap == gap
	r.diagnose("推理与计数的差值在两档长度上相同（count_tokens 是真 tokenizer）", constant,
		fmt.Sprintf("裸请求差 %d，n=%d 差 %d（推理 %d，count_tokens %d）",
			gap, digitProbeSizes[2], longGap, o.digits[2], o.countDigits))
	if constant && gap > 0 && !digitsReconciled {
		f.inject(fmt.Sprintf("推理比 count_tokens 恒多 %d", gap), gap)
	}
}

// describeSoftSignals 调用方 system 是否生效、隐藏注入探针：只作软证据。
func describeSoftSignals(r *CheckResult, o ziObservation) {
	if o.systemOK {
		note := "回复按人设以 Arrr 开头"
		if !o.obeyed {
			note = "回复没有按人设以 Arrr 开头：调用方 system 可能被覆盖"
		}
		r.diagnose("带 system 时模型遵循调用方 system（软证据）", o.obeyed, note)
	}
	if o.hidden.answered {
		r.diagnose("隐藏注入探针无信号（软证据）", !o.hidden.signal, o.hidden.note)
	} else {
		r.diagnose("隐藏注入探针可判读（软证据）", false, o.hidden.note)
	}
}

// concludeZeroInjection 汇总结论：混池与注入优先，其次计量异常，最后才是 0 注入。
// 回放与计量正交，另起一句放在最前面。
func concludeZeroInjection(r *CheckResult, f *ziFindings, o ziObservation, buckets []ziBucket, base injectionBaseline) string {
	var summary string
	switch {
	case len(buckets) > 1:
		r.Status = StatusSuspicious
		summary = "号池混用：" + describeBuckets(buckets, base.Bare)
		r.addEvidence("injection_pool_mixed", "号池里混着输入不同的账号", summary, ClassInfo, 0)
	case len(f.injected) > 0:
		r.Status = StatusSuspicious
		summary = "检出注入：" + strings.Join(f.injected, "；")
		if f.amount > 0 {
			summary = fmt.Sprintf("检出注入约 %d tokens：%s", f.amount, strings.Join(f.injected, "；"))
		}
		r.addEvidence("injection_detected", "检出注入", summary, ClassInfo, 0)
	case len(f.anomalies) > 0:
		r.Status = StatusSuspicious
		summary = "计量与官方对不上，无法确认 0 注入：" + strings.Join(f.anomalies, "；")
		r.addEvidence("injection_metering_anomaly", "计量与官方对不上", summary, ClassInfo, 0)
	case len(f.matched) > 0:
		r.Status = StatusPassed
		summary = fmt.Sprintf("0 注入：%d 次采样一致，%s", len(o.samples), strings.Join(f.matched, "，"))
		r.addEvidence("zero_injection", "与官方基线逐项一致", summary, ClassInfo, 0)
	default:
		// 没对上账不等于没注入：只说无法对账，不写「未见注入迹象」。
		r.Status = StatusInconclusive
		prefix := "无该型号官方基线，无法对账"
		if base.Source != "" {
			prefix = "官方基线对账不完整，无法下结论"
		}
		summary = prefix + "：" + describeBuckets(buckets, 0)
		if o.countBareOK {
			summary += fmt.Sprintf("，count_tokens %d", o.countBare)
		}
	}

	var soft []string
	if o.hidden.signal {
		soft = append(soft, o.hidden.note)
	}
	if o.systemOK && !o.obeyed {
		soft = append(soft, "调用方 system 未生效")
	}
	if r.Status == StatusPassed && len(soft) > 0 {
		r.Status = StatusSuspicious
		summary = "计量 0 注入，但软证据提示可能有隐藏注入（" + strings.Join(soft, "；") + "），需人工复核"
		r.addEvidence("injection_soft_signal", "行为旁证提示隐藏注入", summary, ClassInfo, 0)
	}

	if o.replay.reused > 0 {
		r.Status = StatusSuspicious
		r.addEvidence("injection_replay", "回放缓存的响应", o.replay.String(), ClassInfo, 0)
		summary = "回放缓存的响应（" + o.replay.String() + "）；" + summary
	}
	return summary
}

// describeReplay 本项响应的签名复用：只作记录，结论由 concludeZeroInjection 汇总。
func describeReplay(r *CheckResult, rep ziReplay) {
	if rep.signed == 0 {
		return
	}
	r.diagnose("响应的 thinking 签名互不重复（未见回放）", rep.reused == 0, rep.String())
}

func (rep ziReplay) String() string {
	if rep.reused == 0 {
		return fmt.Sprintf("%d 个带签名的响应，签名各不相同", rep.signed)
	}
	return fmt.Sprintf("%d 个带签名的响应里 %d 个共用 %d 份签名，同一次生成被回放给了多个请求",
		rep.signed, rep.reused, rep.groups)
}

// describeSampleIDs 采样响应的消息 id：字母表与重复，只作记录。
func describeSampleIDs(r *CheckResult, ids []string) {
	if len(ids) == 0 {
		return
	}
	seen := map[string]int{}
	rewritten := 0
	for _, id := range ids {
		seen[id]++
		if msgIDRewritten(id) {
			rewritten++
		}
	}
	dups := 0
	for _, n := range seen {
		if n > 1 {
			dups += n
		}
	}
	r.diagnose("采样响应的消息 id 为官方形态", rewritten == 0, fmt.Sprintf("%d/%d 条不是 msg_01 + base58", rewritten, len(ids)))
	r.diagnose("采样响应的消息 id 互不重复", dups == 0, fmt.Sprintf("%d 条响应里 %d 条 id 重复", len(ids), dups))
}
