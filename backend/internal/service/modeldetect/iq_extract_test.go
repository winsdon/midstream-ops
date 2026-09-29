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

func TestExtractFinalAnswer(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		want  int
		found bool
	}{
		{"格式行", "推理……\n最终答案：21", 21, true},
		{"半角冒号与加粗", "**最终答案: 21**", 21, true},
		{"只加粗标签", "推理……\n**最终答案**：21", 21, true},
		{"LaTeX 包裹", "最终答案：$21$", 21, true},
		{"行内代码包裹", "最终答案：`21`", 21, true},
		{"格式行取最后一次", "若形状摸不出来，最终答案：29；\n但题目说形状摸得出来\n最终答案：21", 21, true},
		{"格式行优先于其他写法", "答案是 29？不对。\n最终答案：21", 21, true},
		{"算式里的中间数不算答案", "……所以答案是 12+9=21。\n\n**最终答案**：21", 21, true},
		{"格式行后面换行接列表不算算式", "最终答案：21\n- 验证见上", 21, true},
		{"boxed 兜底", `所以 \boxed{21}`, 21, true},
		{"答案是 兜底", "综上，答案是 **21** 个。", 21, true},
		{"兜底只看末尾几行", "开头猜一下：答案是 5？\n一\n二\n三\n四\n五\n六\n结论见上", 0, false},
		{"兜底也不认算式", "所以答案是 12+9=21", 0, false},
		{"推理里的中间数不算答案", "至少要摸 9 颗圆的，再加上 12 颗五角星……", 0, false},
		{"没有答案", "这道题需要更多信息。", 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, found := extractFinalAnswer(tc.text)
			if got != tc.want || found != tc.found {
				t.Fatalf("得到 (%d, %v)，期望 (%d, %v)", got, found, tc.want, tc.found)
			}
		})
	}
}

func TestAnswerOnLastLine(t *testing.T) {
	if n, ok := answerOnLastLine("推理……\n\n**最终答案**：21\n"); !ok || n != 21 {
		t.Fatalf("格式行在最后一行应取出答案，实际 (%d, %v)", n, ok)
	}
	if _, ok := answerOnLastLine("最终答案：16？不对，再想想……\n如果先摸圆形"); ok {
		t.Fatal("被截断的推理里出现过格式行，但最后一行不是它，不应算答案")
	}
}

func TestReplyFromJSON(t *testing.T) {
	got := replyFromJSON(map[string]any{
		"type": "message", "stop_reason": "end_turn",
		"usage": map[string]any{"output_tokens": 7},
		"content": []any{
			map[string]any{"type": "thinking", "thinking": "想一想"},
			map[string]any{"type": "text", "text": "最终答案：21"},
		},
	})
	if !got.Complete || got.Text != "最终答案：21" || got.ThinkingChars != 3 || got.OutputTokens != 7 {
		t.Fatalf("整段 JSON 回复还原错误: %+v", got)
	}
	if replyFromJSON(map[string]any{"type": "error"}).Complete {
		t.Fatal("错误体不应算完整回复")
	}
}

func TestAssembleStream(t *testing.T) {
	complete := parseSSE(sseBody([]string{"最终答案：", "21"}, "end_turn", true))
	got := assembleStream(complete)
	if !got.Complete || got.Text != "最终答案：21" || got.StopReason != "end_turn" || got.OutputTokens != 42 {
		t.Fatalf("完整流还原错误: %+v", got)
	}
	if got.ThinkingChars != len([]rune("先想一想")) {
		t.Fatalf("thinking 字数应按字符计，实际 %d", got.ThinkingChars)
	}

	cut := assembleStream(parseSSE(sseBody([]string{"<svg>"}, "", false)))
	if cut.Complete {
		t.Fatal("缺 message_stop 的流不应算完整")
	}

	withErr := assembleStream(parseSSE(sseBody([]string{"hi"}, "end_turn", true) +
		"event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n"))
	if withErr.Complete || withErr.StreamError != "Overloaded" {
		t.Fatalf("流内 error 事件应判不完整并带出原因: %+v", withErr)
	}
}
