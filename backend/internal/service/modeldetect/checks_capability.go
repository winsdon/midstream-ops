package modeldetect

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"strconv"
	"strings"
)

// solidPNG 生成一张纯色 PNG 的 base64。纯文本套壳渠道在这一步会直接失败。
func solidPNG(c color.RGBA, size int) string {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

// imageColors 使用无歧义的标准色，避免模型在边界色上「答对也像答错」。
var imageColors = []struct {
	Name string
	RGBA color.RGBA
}{
	{"RED", color.RGBA{230, 35, 45, 255}},
	{"GREEN", color.RGBA{20, 170, 80, 255}},
	{"BLUE", color.RGBA{35, 95, 225, 255}},
	{"YELLOW", color.RGBA{255, 255, 0, 255}},
}

// checkImage 随机纯色识别。
func checkImage(ctx context.Context, c *Client, _ *runState) *CheckResult {
	meta, _ := checkByID("image")
	r := newResult(meta)
	pick := imageColors[rand.Intn(len(imageColors))]
	data := solidPNG(pick.RGBA, 64)
	if data == "" {
		r.Status = StatusInconclusive
		return r.finish("本地生成测试图片失败")
	}

	ex := c.Post(ctx, KindMessages, map[string]any{
		"model": c.Target().Model, "max_tokens": 64,
		"messages": []map[string]any{{"role": "user", "content": []map[string]any{
			{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": data}},
			{"type": "text", "text": "图片是纯色方块。只输出它的英文颜色名（大写），不要解释。"},
		}}},
	})
	r.Exchanges = []*Exchange{ex}
	r.DurationMs = ex.DurationMs
	if !ex.OK() {
		r.Status = StatusUnsupported
		r.diagnose("图片请求成功", false, describeFailure(ex))
		return r.finish("目标不支持图片输入")
	}
	text := contentText(ex.JSON)
	got := strings.ToUpper(keepLetters(text))
	ok := got == pick.Name
	r.assert("随机颜色精确识别", ok, fmt.Sprintf("期望 %s，观察到 %s", pick.Name, clip(text, 60)))
	if ok {
		r.AuthScore = 5
	} else {
		r.addEvidence("image_wrong", "图片识别错误", fmt.Sprintf("期望 %s，得到 %s", pick.Name, clip(text, 40)), ClassWrapper, 2)
	}
	return r.finish(fmt.Sprintf("期望 %s，观察到 %s", pick.Name, clip(text, 40)))
}

// keepLetters 只保留字母，抹掉标点与空白后再比对。
func keepLetters(s string) string {
	var sb strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// keepAlnum 保留字母、数字与下划线，用于比对随机标记。
func keepAlnum(s string) string {
	var sb strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// buildPDF 生成一份只含一行随机标记的最小合法 PDF。
func buildPDF(marker string) string {
	escaped := strings.NewReplacer("(", `\(`, ")", `\)`, `\`, `\\`).Replace(marker)
	stream := fmt.Sprintf("BT\n/F1 28 Tf\n72 720 Td\n(%s) Tj\nET\n", escaped)
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(stream), stream),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	var sb strings.Builder
	sb.WriteString("%PDF-1.4\n")
	offsets := make([]int, 0, len(objects))
	for i, obj := range objects {
		offsets = append(offsets, sb.Len())
		sb.WriteString(fmt.Sprintf("%d 0 obj\n%s\nendobj\n", i+1, obj))
	}
	xref := sb.Len()
	sb.WriteString(fmt.Sprintf("xref\n0 %d\n0000000000 65535 f \n", len(objects)+1))
	for _, off := range offsets {
		sb.WriteString(fmt.Sprintf("%010d 00000 n \n", off))
	}
	sb.WriteString(fmt.Sprintf("trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref))
	return base64.StdEncoding.EncodeToString([]byte(sb.String()))
}

// checkPDF 随机标记 PDF 读取。
func checkPDF(ctx context.Context, c *Client, _ *runState) *CheckResult {
	meta, _ := checkByID("pdf")
	r := newResult(meta)
	marker := nonce("PDF")
	ex := c.Post(ctx, KindMessages, map[string]any{
		"model": c.Target().Model, "max_tokens": 64,
		"messages": []map[string]any{{"role": "user", "content": []map[string]any{
			{"type": "document", "source": map[string]any{
				"type": "base64", "media_type": "application/pdf", "data": buildPDF(marker)}},
			{"type": "text", "text": "读取这份 PDF 页面中的标记。只输出标记，不要解释。"},
		}}},
	})
	r.Exchanges = []*Exchange{ex}
	r.DurationMs = ex.DurationMs
	if !ex.OK() {
		r.Status = StatusUnsupported
		r.diagnose("PDF 请求成功", false, describeFailure(ex))
		return r.finish("目标不支持 PDF 输入")
	}
	text := contentText(ex.JSON)
	ok := strings.EqualFold(keepAlnum(text), marker)
	r.assert("读出随机 PDF 标记", ok, fmt.Sprintf("期望 %s，观察到 %s", marker, clip(text, 60)))
	if ok {
		r.AuthScore = 5
	}
	return r.finish(fmt.Sprintf("期望 %s，观察到 %s", marker, clip(text, 40)))
}

// checkStrictSchema 结构化输出：随机 enum + 必填字段，校验模型是否真按 schema 约束生成。
func checkStrictSchema(ctx context.Context, c *Client, _ *runState) *CheckResult {
	meta, _ := checkByID("strict-schema")
	r := newResult(meta)
	probeID := nonce("SCHEMA")
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"probe_id":   map[string]any{"type": "string", "enum": []string{probeID}},
			"answer":     map[string]any{"type": "string"},
			"confidence": map[string]any{"type": "number", "minimum": 0, "maximum": 1},
		},
		"required":             []string{"probe_id", "answer", "confidence"},
		"additionalProperties": false,
	}
	ex := c.Post(ctx, KindMessages, map[string]any{
		"model": c.Target().Model, "max_tokens": 256,
		"output_config": map[string]any{"format": map[string]any{"type": "json_schema", "schema": schema}},
		"messages": []map[string]any{{"role": "user",
			"content": "用一句话说明天空为什么是蓝色，并给出置信度；probe_id 必须原样返回 " + probeID + "。"}},
	})
	r.Exchanges = []*Exchange{ex}
	r.DurationMs = ex.DurationMs
	if !ex.OK() {
		r.Status = StatusUnsupported
		r.diagnose("结构化输出请求成功", false, describeFailure(ex))
		return r.finish("目标不支持 output_config.json_schema")
	}

	text := strings.TrimSpace(contentText(ex.JSON))
	parsed := parseJSONObject(text)
	r.assert("返回内容是有效 JSON", parsed != nil, clip(text, 120))
	problems := validateProbeSchema(parsed, probeID)
	r.assert("满足 JSON Schema 约束", parsed != nil && len(problems) == 0, strings.Join(problems, "；"))
	stop := str(ex.JSON["stop_reason"])
	r.diagnose("正常结束（非 refusal/max_tokens）", stop == "end_turn", "stop_reason="+stop)

	if parsed != nil && len(problems) == 0 {
		r.AuthScore = 5
	}
	return r.finish(clip(text, 120))
}

// validateProbeSchema 校验本项自己那份固定 schema。
//
// 这里不引第三方 JSON Schema 库：约束只有几条且由我们自己定义，
// 手写校验反而更好读，也少一个依赖。
func validateProbeSchema(v map[string]any, probeID string) []string {
	if v == nil {
		return []string{"不是 JSON 对象"}
	}
	var problems []string
	if got := str(v["probe_id"]); got != probeID {
		problems = append(problems, fmt.Sprintf("probe_id 期望 %s，实际 %q", probeID, got))
	}
	if str(v["answer"]) == "" {
		problems = append(problems, "answer 缺失或不是字符串")
	}
	conf, ok := num(v["confidence"])
	if !ok {
		problems = append(problems, "confidence 不是数字")
	} else if conf < 0 || conf > 1 {
		problems = append(problems, "confidence 超出 [0,1]："+strconv.FormatFloat(conf, 'f', -1, 64))
	}
	allowed := map[string]bool{"probe_id": true, "answer": true, "confidence": true}
	for k := range v {
		if !allowed[k] {
			problems = append(problems, "出现未允许的字段 "+k)
		}
	}
	return problems
}

// checkPromptCache 缓存正负对照：同前缀应命中，改动首部应不命中。
//
// 前缀长度按模型的最小可缓存阈值放大 1.5 倍——短于阈值官方会静默不缓存，
// 那会把「前缀太短」误判成「缓存失效」。
func checkPromptCache(ctx context.Context, c *Client, st *runState) *CheckResult {
	meta, _ := checkByID("prompt-cache")
	r := newResult(meta)
	model := c.Target().Model
	prefixID := nonce("CACHE")

	// 一段约 12 token 的句子，按阈值 1.5 倍估算重复次数
	unit := "This is a stable prompt cache probe segment that must stay byte identical. "
	repeat := st.profile.MinCacheTokens*3/2/12 + 20
	prefix := prefixID + " " + strings.Repeat(unit, repeat)

	build := func(pfx, q string) map[string]any {
		return map[string]any{
			"model": model, "max_tokens": 32,
			"system": []map[string]any{{"type": "text", "text": pfx,
				"cache_control": map[string]any{"type": "ephemeral"}}},
			"messages": []map[string]any{{"role": "user", "content": q}},
		}
	}
	first := c.Post(ctx, KindMessages, build(prefix, "只回复 CACHE_OK。"))
	second := c.Post(ctx, KindMessages, build(prefix, "只回复 CACHE_OK。"))
	third := c.Post(ctx, KindMessages, build("CHANGED-"+prefixID+"-X. "+prefix, "只回复 CACHE_OK。"))
	r.Exchanges = []*Exchange{first, second, third}
	r.DurationMs = first.DurationMs + second.DurationMs + third.DurationMs

	if !first.OK() || !second.OK() {
		r.Status = StatusInconclusive
		r.diagnose("缓存对照请求成功", false, describeFailure(first))
		return r.finish("缓存对照请求失败")
	}
	w1 := cacheCreationTokens(usageOf(first.JSON))
	r1 := cacheReadTokens(usageOf(first.JSON))
	r2 := cacheReadTokens(usageOf(second.JSON))
	w3 := cacheCreationTokens(usageOf(third.JSON))
	r3 := cacheReadTokens(usageOf(third.JSON))

	r.assert("首次请求写入缓存", w1 > 0, fmt.Sprintf("write=%d read=%d", w1, r1))
	r.assert("相同前缀第二次命中缓存", r2 > 0, fmt.Sprintf("read=%d", r2))
	r.diagnose("变更首部后不命中旧缓存", r3 == 0 && w3 > 0, fmt.Sprintf("write=%d read=%d", w3, r3))

	if w1 > 0 && r2 > 0 {
		r.AuthScore = 5
	} else if w1 == 0 && r2 == 0 {
		r.addEvidence("cache_dead", "Prompt Cache 完全不生效",
			"cache_control 被剥离或多账号轮换导致缓存不共享", ClassWrapper, 2)
	}
	return r.finish(fmt.Sprintf("首次 write=%d，二次 read=%d，改首部 write=%d read=%d", w1, r2, w3, r3))
}

// checkHelloEntropy 10 次 Hello/Hi 采样。
// 官方模型每次措辞都不同（去重 8-10）；模板化或固定回复的伪池会塌缩到 1-4 种。
func checkHelloEntropy(ctx context.Context, c *Client, _ *runState) *CheckResult {
	meta, _ := checkByID("hello-entropy")
	r := newResult(meta)
	const rounds = 10
	seen := map[string]bool{}
	okCount := 0
	for i := 0; i < rounds; i++ {
		word := "Hello"
		if i%2 == 1 {
			word = "Hi"
		}
		ex := c.Post(ctx, KindMessages, map[string]any{
			"model": c.Target().Model, "max_tokens": 200,
			"messages": []map[string]any{{"role": "user", "content": word}},
		})
		r.Exchanges = append(r.Exchanges, ex)
		r.DurationMs += ex.DurationMs
		if !ex.OK() {
			continue
		}
		okCount++
		if text := squash(contentText(ex.JSON)); text != "" {
			seen[text] = true
		}
	}
	if okCount < rounds/2 {
		r.Status = StatusInconclusive
		r.diagnose("采样请求成功", false, fmt.Sprintf("仅 %d/%d 次成功", okCount, rounds))
		return r.finish("采样样本不足")
	}
	unique := len(seen)
	r.assert("采样多样性达到最低要求", helloEntropyPassed(unique), fmt.Sprintf("%d/%d 次不重复（成功 %d 次）", unique, rounds, okCount))
	switch {
	case helloEntropyPassed(unique):
		r.AuthScore = 1
	case unique <= 3:
		r.addEvidence("low_entropy", "回复高度模板化",
			fmt.Sprintf("%d 次采样只有 %d 种回复", okCount, unique), ClassWrapper, 1)
	}
	return r.finish(fmt.Sprintf("%d 次采样得到 %d 种不同回复", okCount, unique))
}

// checkThinkingGradient 三档难度的思考量是否随难度增长。
// 固定注入 thinking 的假实现往往三档一样长，或者干脆没有签名。
func checkThinkingGradient(ctx context.Context, c *Client, st *runState) *CheckResult {
	meta, _ := checkByID("thinking-gradient")
	r := newResult(meta)
	tasks := []struct {
		level int
		q     string
	}{
		{1, "What is 2+2?"},
		{2, "Find all integer solutions to x^2 - y^2 = 45 where x,y > 0. Show your work."},
		{3, "Design a lock-free MPMC queue in C++. Discuss the ABA problem, memory ordering, and a correctness proof sketch in detail."},
	}
	var chars []int
	sigCount := 0
	for _, t := range tasks {
		ex := c.PostStream(ctx, KindMessages, map[string]any{
			"model": c.Target().Model, "max_tokens": 8000, "stream": true,
			"thinking": st.profile.ThinkingParam("summarized"),
			"messages": []map[string]any{{"role": "user", "content": t.q}},
		})
		r.Exchanges = append(r.Exchanges, ex)
		r.DurationMs += ex.DurationMs
		if !ex.OK() {
			r.diagnose(fmt.Sprintf("第 %d 档请求成功", t.level), false, describeFailure(ex))
			chars = append(chars, -1)
			continue
		}
		thinkChars, sigLen := streamThinkingStats(ex.Events)
		chars = append(chars, thinkChars)
		if sigLen > 0 {
			sigCount++
		}
		r.diagnose(fmt.Sprintf("第 %d 档产生思考", t.level), thinkChars > 0,
			fmt.Sprintf("thinking %d 字符，签名 %d 字符，首字 %s", thinkChars, sigLen, fmtMsPtr(ex.TTFTMs)))
	}

	valid := len(chars) == 3 && chars[0] >= 0 && chars[1] >= 0 && chars[2] >= 0
	if !valid {
		r.Status = StatusInconclusive
		return r.finish("部分档位请求失败")
	}
	monotonic := meaningfulThinkingGradient(chars)
	r.assert("思考量随难度明显增长", monotonic, fmt.Sprintf("%v", chars))
	r.diagnose("各档均带签名", sigCount == 3, fmt.Sprintf("%d/3 档观察到签名", sigCount))
	if monotonic && sigCount >= 2 {
		r.AuthScore = 15
	}
	return r.finish(fmt.Sprintf("三档思考字符数 %v", chars))
}

func helloEntropyPassed(unique int) bool { return unique >= 4 }

func meaningfulThinkingGradient(chars []int) bool {
	if len(chars) != 3 || chars[0] <= 0 || chars[1] <= 0 || chars[2] <= 0 {
		return false
	}
	return chars[1] >= chars[0]*3/2 && chars[2] >= chars[1]*3/2
}

// streamThinkingStats 汇总流里的思考字符数与签名长度。
func streamThinkingStats(events []SSEEvent) (thinkChars, sigLen int) {
	for _, ev := range events {
		delta := mapOf(ev.Data["delta"])
		switch str(delta["type"]) {
		case "thinking_delta":
			thinkChars += len(str(delta["thinking"]))
		case "signature_delta":
			sigLen += len(str(delta["signature"]))
		}
	}
	return
}

// checkSlope 输出 token 与词数之比。
// 正常在 1.2-2.5；显著偏高说明渠道在未被请求的情况下强制注入了 thinking，
// 把用户的 max_tokens 烧在看不见的地方。
func checkSlope(ctx context.Context, c *Client, _ *runState) *CheckResult {
	meta, _ := checkByID("slope")
	r := newResult(meta)
	ex := c.Post(ctx, KindMessages, map[string]any{
		"model": c.Target().Model, "max_tokens": 1200,
		"messages": []map[string]any{{"role": "user",
			"content": "Write exactly 150 words about the ocean. Plain prose, no lists, no headings."}},
	})
	r.Exchanges = []*Exchange{ex}
	r.DurationMs = ex.DurationMs
	if !ex.OK() {
		r.Status = StatusInconclusive
		r.diagnose("斜率请求成功", false, describeFailure(ex))
		return r.finish("斜率请求失败")
	}
	text := contentText(ex.JSON)
	words := len(strings.Fields(text))
	out := intOf(usageOf(ex.JSON)["output_tokens"])
	if words == 0 || out == 0 {
		r.Status = StatusInconclusive
		return r.finish("无法计算斜率（无文本或无用量）")
	}
	ratio := float64(out) / float64(words)
	ok := ratio >= 1.0 && ratio <= 3.0
	r.assert("token/词 比例在正常区间", ok, fmt.Sprintf("%.2f tok/word（%d tokens / %d 词）", ratio, out, words))
	if ratio > 3.0 {
		r.addEvidence("forced_thinking", "输出 token 远超可见文本",
			fmt.Sprintf("%.2f tok/word，疑似强制注入 thinking", ratio), ClassWrapper, 2)
	}
	return r.finish(fmt.Sprintf("%.2f tok/word", ratio))
}
