package modeldetect

import "strings"

// 鹈鹕作品的文档类型。
const (
	DocumentSVG  = "svg"
	DocumentHTML = "html"
)

// ExtractArtwork returns only a complete document supplied by the model.
// It never repairs or completes the generated source.
func ExtractArtwork(text string) (string, string) { return extractDocument(text) }

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
