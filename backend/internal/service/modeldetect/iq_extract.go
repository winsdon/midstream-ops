package modeldetect

import (
	"regexp"
	"strconv"
	"strings"
)

// 智商题作品的文档类型。
const (
	DocumentSVG  = "svg"
	DocumentHTML = "html"
)

// streamedReply 由 SSE 事件（或上游直接回的整段 JSON）还原出的一条回复。
type streamedReply struct {
	Text          string
	ThinkingChars int
	StopReason    string
	OutputTokens  int
	// Complete 见到 message_stop 且流内没有 error 事件。
	Complete bool
	// StreamError 流内 error 事件的说明（HTTP 200 之后才报的过载等）。
	StreamError string
}

// assembleStream 把 SSE 事件拼回完整回复。thinking 只计字数——判分只看可见回复。
func assembleStream(events []SSEEvent) streamedReply {
	var out streamedReply
	var text strings.Builder
	for _, ev := range events {
		switch ev.Type {
		case "content_block_delta":
			delta := mapOf(ev.Data["delta"])
			switch str(delta["type"]) {
			case "text_delta":
				text.WriteString(str(delta["text"]))
			case "thinking_delta":
				out.ThinkingChars += len([]rune(str(delta["thinking"])))
			}
		case "message_delta":
			if reason := str(mapOf(ev.Data["delta"])["stop_reason"]); reason != "" {
				out.StopReason = reason
			}
			if n := intOf(mapOf(ev.Data["usage"])["output_tokens"]); n > 0 {
				out.OutputTokens = n
			}
		case "message_stop":
			out.Complete = true
		case "error":
			out.StreamError = errorMessage(ev.Data)
			if out.StreamError == "" {
				out.StreamError = "流内出现 error 事件"
			}
		}
	}
	if out.StreamError != "" {
		out.Complete = false
	}
	out.Text = strings.TrimSpace(text.String())
	return out
}

// replyFromJSON 上游无视 stream:true、直接回整段 JSON 时，照样还原成一条回复。
func replyFromJSON(body map[string]any) streamedReply {
	if body == nil || str(body["type"]) == "error" {
		return streamedReply{}
	}
	return streamedReply{
		Text:          contentText(body),
		ThinkingChars: len([]rune(thinkingText(body))),
		StopReason:    str(body["stop_reason"]),
		OutputTokens:  intOf(usageOf(body)["output_tokens"]),
		Complete:      len(contentBlocks(body)) > 0,
	}
}

// artwork 回复里的一份完整作品。
type artwork struct {
	doc  string
	kind string
}

// extractDocument 从回复里取出完整的 SVG / HTML 作品（移植自 cockpit-tools 的 extract_html）。
//
// 先看 html / svg / xml / 无语言标注的代码块；有多份完整作品时优先取带动画的——模型常先给
// 静态版再给动画版。最后一个代码块没闭合、前面又没有完整作品，说明输出被截断，宁可不取也不把
// 半截文档当作品。没有代码块才在全文里找。只截取模型自己写完的文档，从不补全或修复。
func extractDocument(reply string) (doc, kind string) {
	var found []artwork
	inFence, eligible := false, false
	var block strings.Builder
	for _, line := range strings.Split(reply, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if inFence {
				if eligible {
					if d, k := findDocument(block.String()); d != "" {
						found = append(found, artwork{doc: d, kind: k})
					}
				}
				inFence = false
				block.Reset()
				continue
			}
			lang := asciiLower(strings.TrimSpace(strings.TrimLeft(trimmed, "`")))
			eligible = lang == "" || lang == "html" || lang == "svg" || lang == "xml"
			inFence = true
			continue
		}
		if inFence {
			block.WriteString(line)
			block.WriteByte('\n')
		}
	}
	if len(found) > 0 {
		for _, a := range found {
			if len(animationKinds(a.doc)) > 0 {
				return a.doc, a.kind
			}
		}
		return found[0].doc, found[0].kind
	}
	if inFence {
		return "", ""
	}
	return findDocument(reply)
}

// findDocument 在一段文本里找完整文档：闭合的 <html> 优先（连同前面的 doctype），其次 <svg>。
func findDocument(text string) (doc, kind string) {
	lower := asciiLower(text)
	if start := openTagIndex(lower, "<html"); start >= 0 {
		if end := strings.LastIndex(lower, "</html>"); end > start {
			if doctype := strings.LastIndex(lower[:start], "<!doctype html"); doctype >= 0 {
				start = doctype
			}
			return text[start : end+len("</html>")], DocumentHTML
		}
		// 没闭合的 <html>（如「可以嵌进 <html> 页面」这句说明）不作数，继续找独立 SVG
	}
	if start := openTagIndex(lower, "<svg"); start >= 0 {
		if end := strings.LastIndex(lower, "</svg>"); end > start {
			return text[start : end+len("</svg>")], DocumentSVG
		}
	}
	return "", ""
}

// openTagIndex 找第一个真正的开标签：标签名后必须紧跟空白或 >，排除 <svgx 之类。
func openTagIndex(lower, tag string) int {
	from := 0
	for {
		i := strings.Index(lower[from:], tag)
		if i < 0 {
			return -1
		}
		i += from
		if next := i + len(tag); next < len(lower) {
			switch lower[next] {
			case '>', ' ', '\t', '\n', '\r':
				return i
			}
		}
		from = i + len(tag)
	}
}

// asciiLower 只把 A-Z 转小写。
//
// 不用 strings.ToLower：个别 Unicode 字符小写后字节长度会变，拿小写串里的下标去切原文会错位，
// 而作品前后常有中文说明。
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// animationKinds 识别作品里用到的动画手段。
func animationKinds(doc string) []string {
	lower := asciiLower(doc)
	var kinds []string
	if strings.Contains(lower, "<animate") || strings.Contains(lower, "<set ") {
		kinds = append(kinds, "SMIL")
	}
	if strings.Contains(lower, "@keyframes") || strings.Contains(lower, "animation:") ||
		strings.Contains(lower, "animation-name") {
		kinds = append(kinds, "CSS")
	}
	for _, api := range []string{"requestanimationframe", "setinterval(", "settimeout(", ".animate("} {
		if strings.Contains(lower, api) {
			kinds = append(kinds, "JS")
			break
		}
	}
	return kinds
}

// answerNoise 模型常把答案包在 Markdown / LaTeX 记号里（**最终答案**：21、`21`、$21$），匹配前先剥掉。
var answerNoise = strings.NewReplacer("*", "", "_", "", "`", "", "$", "")

// formatLineRe 题目要求的「最终答案：N」格式行。
// 第二个分组捕获数字后紧跟的运算符（只跨空格、不跨行）：「12+9=21」里的 12 是算式的一部分，不是结论。
var formatLineRe = regexp.MustCompile(`最终答案\s*(?:是|为)?\s*[:：]?\s*(\d+)[ \t]*([+\-×*/=＋－＝]?)`)

// fallbackAnswerRes 模型没照格式作答时的兜底：\boxed{N} 与「答案是 N」。
// 「最少……N 个」这类写法不认——推理过程里的「至少 1 个」会被误抽。
var fallbackAnswerRes = []*regexp.Regexp{
	regexp.MustCompile(`\\boxed\{\s*(\d+)\s*\}`),
	regexp.MustCompile(`答案\s*(?:是|为)?\s*[:：]?\s*(\d+)[ \t]*([+\-×*/=＋－＝]?)`),
}

// answerTailLines 兜底规则只看回复最后几行：结论在末尾，前面的推理里满是中间数。
const answerTailLines = 5

// extractFinalAnswer 取回复里的最终数字答案。格式行取最后一次出现（推理里常先写中间结果）；
// 没有格式行才在末尾几行里找兜底写法。
func extractFinalAnswer(text string) (int, bool) {
	clean := answerNoise.Replace(text)
	if n, ok := lastAnswer(formatLineRe, clean); ok {
		return n, true
	}
	tail := lastLines(clean, answerTailLines)
	for _, re := range fallbackAnswerRes {
		if n, ok := lastAnswer(re, tail); ok {
			return n, true
		}
	}
	return 0, false
}

// answerOnLastLine 最后一个非空行就是格式行时返回其中的答案。
// 输出被截断时只认它：截断的回答没有真正的结尾，「取最后一次出现」的前提不成立。
func answerOnLastLine(text string) (int, bool) {
	return lastAnswer(formatLineRe, lastLines(answerNoise.Replace(text), 1))
}

// lastAnswer 取最后一个有效匹配里的数字；数字后紧跟运算符的是算式里的中间数，跳过。
func lastAnswer(re *regexp.Regexp, text string) (int, bool) {
	matches := re.FindAllStringSubmatch(text, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		m := matches[i]
		if len(m) > 2 && m[2] != "" {
			continue
		}
		if n, err := strconv.Atoi(m[1]); err == nil {
			return n, true
		}
	}
	return 0, false
}

// lastLines 取最后 n 个非空行，保持原顺序。
func lastLines(text string, n int) string {
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, n)
	for i := len(lines) - 1; i >= 0 && len(kept) < n; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			kept = append([]string{line}, kept...)
		}
	}
	return strings.Join(kept, "\n")
}
