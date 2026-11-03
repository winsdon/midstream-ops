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

// isRefusal 安全分类器拒答：HTTP 200、stop_reason=refusal、没有正文。它不是渠道的回复，
// 不能当成样本参与「回复是否模板化」之类的统计。
func isRefusal(ex *Exchange) bool {
	return ex != nil && ex.OK() && str(ex.JSON["stop_reason"]) == "refusal"
}

// refusalCategory 拒答的类别（stop_details.category），取第一个读得到的。
func refusalCategory(exs []*Exchange) string {
	for _, ex := range exs {
		if isRefusal(ex) {
			if cat := str(mapOf(ex.JSON["stop_details"])["category"]); cat != "" {
				return cat
			}
		}
	}
	return "未注明类别"
}

// checkHelloEntropy 10 次 Hello/Hi 采样。
// 官方模型每次措辞都不同（去重 8-10）；模板化或固定回复的伪池会塌缩到 1-4 种。
func checkHelloEntropy(ctx context.Context, c *Client, _ *runState) *CheckResult {
	meta, _ := checkByID("hello-entropy")
	r := newResult(meta)
	const rounds = 10
	seen := map[string]bool{}
	okCount, refused := 0, 0
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
		if isRefusal(ex) {
			refused++
			continue
		}
		okCount++
		if text := squash(contentText(ex.JSON)); text != "" {
			seen[text] = true
		}
	}
	if okCount < rounds/2 {
		r.Status = StatusInconclusive
		r.diagnose("采样请求成功", false, fmt.Sprintf("仅 %d/%d 次拿到正常回复", okCount, rounds))
		if refused > 0 {
			// 拒答没有正文，算进去就成了「0 种回复」：2026-11 实测 Fable 5 有渠道把 Hello 一律按 cyber 拒答。
			r.diagnose("未被安全分类器拒答", false, fmt.Sprintf("%d 次 stop_reason=refusal（%s）", refused, refusalCategory(r.Exchanges)))
			return r.finish(fmt.Sprintf("%d/%d 次被安全分类器拒答，采样样本不足", refused, rounds))
		}
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

func helloEntropyPassed(unique int) bool { return unique >= 4 }
