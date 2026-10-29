package modeldetect

import (
	"strings"
	"testing"
)

func TestExtractDocument(t *testing.T) {
	svg := `<svg viewBox="0 0 200 120" xmlns="http://www.w3.org/2000/svg"><circle r="4"/></svg>`
	html := "<!DOCTYPE html>\n<html><body>鹈鹕" + svg + "</body></html>"
	tests := []struct {
		name     string
		reply    string
		wantDoc  string
		wantKind string
	}{
		{
			name:     "svg 代码块，前后有说明",
			reply:    "这是一只骑车的鹈鹕：\n\n```svg\n" + svg + "\n```\n\n动画用了 SMIL。",
			wantDoc:  svg,
			wantKind: DocumentSVG,
		},
		{
			name:     "html 代码块连同 doctype",
			reply:    "```html\n" + html + "\n```",
			wantDoc:  html,
			wantKind: DocumentHTML,
		},
		{
			name:     "无语言标注的代码块",
			reply:    "```\n" + svg + "\n```",
			wantDoc:  svg,
			wantKind: DocumentSVG,
		},
		{
			name:     "没有代码块的裸 SVG",
			reply:    "好的！" + svg + " 完成。",
			wantDoc:  svg,
			wantKind: DocumentSVG,
		},
		{
			name:     "大写标签",
			reply:    "```xml\n<SVG width=\"10\"></SVG>\n```",
			wantDoc:  `<SVG width="10"></SVG>`,
			wantKind: DocumentSVG,
		},
		{
			name:     "中文说明后下标仍对齐（ASCII 小写化不改字节长度）",
			reply:    "İİİ鹈鹕骑自行车\n```svg\n" + svg + "\n```",
			wantDoc:  svg,
			wantKind: DocumentSVG,
		},
		{
			name:  "代码块被截断，宁可不取",
			reply: "```svg\n<svg viewBox=\"0 0 1 1\"><g>",
		},
		{
			name:  "前一个代码块完整、最后一个未闭合，仍取完整的那个",
			reply: "```svg\n" + svg + "\n```\n再给一个版本：\n```svg\n<svg><g>",
			// 先遇到的完整代码块直接返回
			wantDoc:  svg,
			wantKind: DocumentSVG,
		},
		{
			name:  "只有开标签没有闭合",
			reply: `<svg viewBox="0 0 1 1"><circle/>`,
		},
		{
			name:  "<svgx 不是 svg 标签",
			reply: "<svgx></svg>",
		},
		{
			name:     "没闭合的 <html> 只是一句说明，继续找独立 SVG",
			reply:    svg + "\n可以直接嵌进 <html> 页面里使用。",
			wantDoc:  svg,
			wantKind: DocumentSVG,
		},
		{
			name: "先给静态版再给动画版，取带动画的",
			reply: "静态版：\n```svg\n<svg viewBox=\"0 0 1 1\"><circle r=\"1\"/></svg>\n```\n动画版：\n```svg\n" +
				`<svg viewBox="0 0 1 1"><animate attributeName="r"/></svg>` + "\n```",
			wantDoc:  `<svg viewBox="0 0 1 1"><animate attributeName="r"/></svg>`,
			wantKind: DocumentSVG,
		},
		{
			name:  "没有作品",
			reply: "抱歉，我无法生成图像。",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc, kind := extractDocument(tc.reply)
			if doc != tc.wantDoc || kind != tc.wantKind {
				t.Fatalf("得到 (%q, %q)，期望 (%q, %q)", doc, kind, tc.wantDoc, tc.wantKind)
			}
		})
	}
}

func TestAnimationKinds(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want string
	}{
		{"SMIL", `<svg><g><animateTransform attributeName="transform"/></g></svg>`, "SMIL"},
		{"CSS", `<svg><style>@keyframes spin{to{transform:rotate(1turn)}}</style></svg>`, "CSS"},
		{"CSS 属性", `<svg><g style="animation: bob 1s infinite"/></svg>`, "CSS"},
		{"JS", `<svg><script>requestAnimationFrame(tick)</script></svg>`, "JS"},
		{"Web Animations API", `<svg><script>el.animate([{opacity:0},{opacity:1}],{iterations:Infinity})</script></svg>`, "JS"},
		{"setTimeout 递归", `<svg><script>function f(){step();setTimeout(f,16)}</script></svg>`, "JS"},
		{"混合", `<svg><animate/><style>@keyframes a{}</style></svg>`, "SMIL/CSS"},
		{"静态", `<svg><circle r="3"/></svg>`, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := strings.Join(animationKinds(tc.doc), "/"); got != tc.want {
				t.Fatalf("得到 %q，期望 %q", got, tc.want)
			}
		})
	}
}
