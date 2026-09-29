package modeldetect

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// animatedPelican 一份带 SMIL 动画的最小作品。
const animatedPelican = `<svg viewBox="0 0 200 120" xmlns="http://www.w3.org/2000/svg">` +
	`<g><circle cx="60" cy="90" r="20"/><animateTransform attributeName="transform" type="translate" values="0 0;10 0;0 0" dur="1s" repeatCount="indefinite"/></g></svg>`

// sseBody 拼一条官方形态的 SSE 流：先一段 thinking，再按 chunks 分片输出正文。
// complete 为 false 时在正文中途断流（没有 message_delta / message_stop）。
func sseBody(chunks []string, stop string, complete bool) string {
	var sb strings.Builder
	write := func(event string, data map[string]any) {
		raw, _ := json.Marshal(data)
		fmt.Fprintf(&sb, "event: %s\ndata: %s\n\n", event, raw)
	}
	write("message_start", map[string]any{"type": "message_start", "message": map[string]any{
		"id": "msg_01ABCDEFGHIJKLMNOPQRSTUV", "type": "message", "role": "assistant", "model": "claude-opus-5",
		"content": []any{}, "usage": map[string]any{"input_tokens": 120, "output_tokens": 1}}})
	write("content_block_start", map[string]any{"type": "content_block_start", "index": 0,
		"content_block": map[string]any{"type": "thinking", "thinking": ""}})
	write("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0,
		"delta": map[string]any{"type": "thinking_delta", "thinking": "先想一想"}})
	write("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
	write("content_block_start", map[string]any{"type": "content_block_start", "index": 1,
		"content_block": map[string]any{"type": "text", "text": ""}})
	for _, c := range chunks {
		write("content_block_delta", map[string]any{"type": "content_block_delta", "index": 1,
			"delta": map[string]any{"type": "text_delta", "text": c}})
	}
	if !complete {
		return sb.String()
	}
	write("content_block_stop", map[string]any{"type": "content_block_stop", "index": 1})
	write("message_delta", map[string]any{"type": "message_delta",
		"delta": map[string]any{"stop_reason": stop}, "usage": map[string]any{"output_tokens": 42}})
	write("message_stop", map[string]any{"type": "message_stop"})
	return sb.String()
}

// iqUpstream 按题目回放预设的流式回复，并记下收到的请求体。
type iqUpstream struct {
	pelican   string // 鹈鹕题的正文回复
	candy     string // 糖果题的正文回复
	stop      string // stop_reason，默认 end_turn
	cut       bool   // true 时中途断流
	streamErr bool   // true 时正常收尾后再追加一条流内 error 事件（HTTP 200 之后才报的过载）
	json      bool   // true 时无视 stream:true，直接回整段 JSON
	status    int    // 非 0 时直接返回该 HTTP 状态

	mu     sync.Mutex
	bodies []map[string]any
}

func (up *iqUpstream) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		up.mu.Lock()
		up.bodies = append(up.bodies, body)
		up.mu.Unlock()

		if up.status != 0 {
			writeJSON(w, up.status, map[string]any{"type": "error",
				"error": map[string]any{"type": "rate_limit_error", "message": "slow down"}})
			return
		}
		text := up.candy
		if strings.Contains(userText(body), "pelican") {
			text = up.pelican
		}
		stop := up.stop
		if stop == "" {
			stop = "end_turn"
		}
		if up.json {
			writeJSON(w, 200, map[string]any{
				"id": "msg_01ABCDEFGHIJKLMNOPQRSTUV", "type": "message", "role": "assistant", "model": "claude-opus-5",
				"stop_reason": stop, "usage": map[string]any{"input_tokens": 120, "output_tokens": 42},
				"content": []any{map[string]any{"type": "text", "text": text}},
			})
			return
		}
		w.Header().Set("content-type", "text/event-stream")
		// 按字符对半分两片写，模拟逐步到达的正文（按字节切会把中文切碎）
		runes := []rune(text)
		mid := len(runes) / 2
		_, _ = io.WriteString(w, sseBody([]string{string(runes[:mid]), string(runes[mid:])}, stop, !up.cut))
		if up.streamErr {
			_, _ = io.WriteString(w, "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n")
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func runIQ(t *testing.T, up *iqUpstream, checks ...string) *TargetRun {
	return runIQWithPrompt(t, up, "", checks...)
}

func runIQWithPrompt(t *testing.T, up *iqUpstream, prompt string, checks ...string) *TargetRun {
	t.Helper()
	target, err := Target{Name: "t", BaseURL: up.server(t).URL, APIKey: "sk-test-key-1234567890",
		Model: "claude-opus-5", PelicanPrompt: prompt}.Normalize()
	if err != nil {
		t.Fatalf("归一化目标失败: %v", err)
	}
	return Run(context.Background(), target, ResolveSuiteCheckIDs(SuiteIQ, checks), NewGate(2), nil)
}

func TestPelicanUsesCustomPrompt(t *testing.T) {
	const custom = "Generate an SVG of a pelican riding a bicycle in a rainy city and animate this"
	up := &iqUpstream{pelican: "```svg\n" + animatedPelican + "\n```"}
	res := findCheck(runIQWithPrompt(t, up, custom, "iq-pelican").Checks, "iq-pelican")
	if res == nil || res.Prompt != custom {
		t.Fatalf("应记录自定义提示词，实际 %+v", res)
	}
	if len(up.bodies) != 1 || userText(up.bodies[0]) != custom+"\n" {
		t.Fatalf("请求应使用自定义提示词，实际 %q", userText(up.bodies[0]))
	}
}

func TestPelicanPassesWithAnimatedSVG(t *testing.T) {
	up := &iqUpstream{pelican: "Here you go:\n\n```svg\n" + animatedPelican + "\n```\nEnjoy!"}
	run := runIQ(t, up, "iq-pelican")
	res := findCheck(run.Checks, "iq-pelican")
	if res == nil || res.Status != StatusPassed {
		t.Fatalf("带动画的完整 SVG 应通过，实际 %+v", res)
	}
	if res.Output == nil || res.Output.Document != animatedPelican || res.Output.DocumentKind != DocumentSVG {
		t.Fatalf("作品应原样保存，实际 %+v", res.Output)
	}
	if !strings.Contains(res.Summary, "SMIL") {
		t.Fatalf("摘要应注明动画手段，实际 %q", res.Summary)
	}

	body := up.bodies[0]
	if body["stream"] != true || mapOf(body["thinking"]) == nil {
		t.Fatalf("智商题应以流式 + thinking 请求，实际 %v", body)
	}
	if userText(body) != pelicanPrompt+"\n" {
		t.Fatalf("鹈鹕题提示词应一字不改，实际 %q", userText(body))
	}
}

func TestPelicanWithoutAnimationFails(t *testing.T) {
	static := `<svg viewBox="0 0 10 10"><circle r="3"/></svg>`
	res := findCheck(runIQ(t, &iqUpstream{pelican: static}, "iq-pelican").Checks, "iq-pelican")
	if res.Status != StatusSuspicious {
		t.Fatalf("没有动画应判未通过，实际 %s", res.Status)
	}
	if res.Output == nil || res.Output.Document != static {
		t.Fatal("没有动画的作品也应保存，方便人工对照")
	}
}

func TestPelicanWithoutSVGFailsAndKeepsReply(t *testing.T) {
	res := findCheck(runIQ(t, &iqUpstream{pelican: "I cannot draw."}, "iq-pelican").Checks, "iq-pelican")
	if res.Status != StatusSuspicious {
		t.Fatalf("没交出作品应判未通过，实际 %s", res.Status)
	}
	if res.Output == nil || res.Output.Text != "I cannot draw." {
		t.Fatalf("没抽到作品时应保留回复原文，实际 %+v", res.Output)
	}
}

func TestPelicanTruncatedIsInconclusive(t *testing.T) {
	up := &iqUpstream{pelican: "```svg\n<svg viewBox=\"0 0 1 1\"><g>", stop: "max_tokens"}
	res := findCheck(runIQ(t, up, "iq-pelican").Checks, "iq-pelican")
	if res.Status != StatusInconclusive {
		t.Fatalf("被 max_tokens 截断应记证据不足，实际 %s", res.Status)
	}
}

// 先交静态版、动画版没写完就被截断：不能判「没有动画」。
func TestPelicanTruncatedStaticVersionIsInconclusive(t *testing.T) {
	static := `<svg viewBox="0 0 10 10"><circle r="3"/></svg>`
	up := &iqUpstream{pelican: "```svg\n" + static + "\n```\n动画版：\n```svg\n<svg><g>", stop: "max_tokens"}
	res := findCheck(runIQ(t, up, "iq-pelican").Checks, "iq-pelican")
	if res.Status != StatusInconclusive {
		t.Fatalf("截断时没写出动画应记证据不足，实际 %s（%s）", res.Status, res.Summary)
	}
}

// 流在 HTTP 200 之后才断：记证据不足、可以重试，已收到的内容留着看。
func TestIQBrokenStreamIsRetryableAndKeepsPartial(t *testing.T) {
	res := findCheck(runIQ(t, &iqUpstream{candy: "最终答案：21", cut: true}, "iq-candy").Checks, "iq-candy")
	if res.Status != StatusInconclusive || !RequestFailed(res) {
		t.Fatalf("中途断流应记证据不足且可重试，实际 %s", res.Status)
	}
	if res.Output == nil || res.Output.Text != "最终答案：21" || res.Output.Answer != "" {
		t.Fatalf("应保留已收到的内容但不据此判分，实际 %+v", res.Output)
	}
}

func TestIQStreamErrorEventIsRetryable(t *testing.T) {
	up := &iqUpstream{pelican: animatedPelican, streamErr: true}
	res := findCheck(runIQ(t, up, "iq-pelican").Checks, "iq-pelican")
	if res.Status != StatusInconclusive || !RequestFailed(res) {
		t.Fatalf("流内 overloaded 应记证据不足且可重试，实际 %s", res.Status)
	}
	if res.Output == nil || res.Output.Document != animatedPelican {
		t.Fatal("已完整收到的作品应保留，方便查看")
	}
}

func TestIQAcceptsJSONWhenUpstreamIgnoresStream(t *testing.T) {
	res := findCheck(runIQ(t, &iqUpstream{candy: "……\n最终答案：21", json: true}, "iq-candy").Checks, "iq-candy")
	if res.Status != StatusPassed {
		t.Fatalf("上游直接回整段 JSON 时应照常判分，实际 %s（%s）", res.Status, res.Summary)
	}
}

// key 被切在两个 delta 里时，原始 SSE 里没有连续的 key，拼接后必须再擦一遍。
func TestIQRedactsKeySplitAcrossDeltas(t *testing.T) {
	const key = "sk-test-key-1234567890"
	reply := strings.Repeat("甲", 10) + key + strings.Repeat("乙", 10)
	res := findCheck(runIQ(t, &iqUpstream{candy: reply}, "iq-candy").Checks, "iq-candy")
	if res.Output == nil || strings.Contains(res.Output.Text, key) || !strings.Contains(res.Output.Text, redacted) {
		t.Fatalf("拼接后的回复里不应出现 key，实际 %+v", res.Output)
	}
}

func TestCandyGrading(t *testing.T) {
	tests := []struct {
		name       string
		reply      string
		wantStatus string
		wantAnswer string
	}{
		{"答对", "形状摸得出来：摸 12 颗五角星、9 颗圆的……\n最终答案：21", StatusPassed, "21"},
		// 29 是把形状当成摸不出来、只按总数算的结果，正是这道题要筛掉的答案
		{"漏读「手感可以分辨」", "最坏情况先摸光西瓜……\n最终答案：29", StatusSuspicious, "29"},
		{"没按格式也没有答案", "我觉得需要很多颗。", StatusSuspicious, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			up := &iqUpstream{candy: tc.reply}
			res := findCheck(runIQ(t, up, "iq-candy").Checks, "iq-candy")
			if res.Status != tc.wantStatus {
				t.Fatalf("状态 %s，期望 %s（%s）", res.Status, tc.wantStatus, res.Summary)
			}
			if res.Output == nil || res.Output.Answer != tc.wantAnswer || res.Output.Expected != "21" ||
				res.Output.Text != tc.reply {
				t.Fatalf("产出不符: %+v", res.Output)
			}
			if !strings.Contains(userText(up.bodies[0]), "最终答案：数字") {
				t.Fatal("糖果题末尾应带格式行")
			}
		})
	}
}

// 标准答案由题目数据穷举验证，不靠手算。
//
// 形状摸得出来：摸的人先定好圆的摸 x 颗、五角星摸 y 颗，每种分法都要扛住所有最坏的口味组合；
// 形状摸不出来时圆的、五角星各几颗也由最坏情况决定，得到的 29 就是漏读题干的陷阱答案。
func TestCandyAnswerMatchesPuzzle(t *testing.T) {
	round := [3]int{7, 9, 8} // 苹果、桃子、西瓜
	star := [3]int{7, 6, 4}
	// paired 手里的圆形 r 与五角星 s 能否凑出「圆苹果 + 星桃子」或「圆桃子 + 星苹果」
	paired := func(r, s [3]int) bool { return (r[0] > 0 && s[1] > 0) || (r[1] > 0 && s[0] > 0) }
	// draws 从 pool 里摸出 n 颗的所有口味组合
	draws := func(pool [3]int, n int) [][3]int {
		var out [][3]int
		for a := 0; a <= pool[0] && a <= n; a++ {
			for b := 0; b <= pool[1] && a+b <= n; b++ {
				if c := n - a - b; c <= pool[2] {
					out = append(out, [3]int{a, b, c})
				}
			}
		}
		return out
	}
	guaranteed := func(x, y int) bool {
		for _, r := range draws(round, x) {
			for _, s := range draws(star, y) {
				if !paired(r, s) {
					return false
				}
			}
		}
		return true
	}
	roundTotal, starTotal := round[0]+round[1]+round[2], star[0]+star[1]+star[2]

	byShape := -1
	for x := 0; x <= roundTotal; x++ {
		for y := 0; y <= starTotal; y++ {
			if (byShape < 0 || x+y < byShape) && guaranteed(x, y) {
				byShape = x + y
			}
		}
	}
	if byShape != candyAnswer {
		t.Fatalf("能摸出形状时最少要摸 %d 颗，常量 candyAnswer = %d", byShape, candyAnswer)
	}

	blind := -1
	for n := 1; n <= roundTotal+starTotal && blind < 0; n++ {
		all := true
		for x := n - starTotal; x <= n && all; x++ {
			if x >= 0 && x <= roundTotal {
				all = guaranteed(x, n-x)
			}
		}
		if all {
			blind = n
		}
	}
	if blind != 29 {
		t.Fatalf("摸不出形状时应得到陷阱答案 29，实际 %d", blind)
	}

	if meta, _ := checkByID("iq-candy"); !strings.Contains(meta.Note, "标准答案 "+strconv.Itoa(candyAnswer)) {
		t.Fatalf("勾选面板的说明应展示标准答案 %d，实际 %q", candyAnswer, meta.Note)
	}
}

func TestIQRequestFailureIsRetryable(t *testing.T) {
	res := findCheck(runIQ(t, &iqUpstream{status: 429}, "iq-candy").Checks, "iq-candy")
	if res.Status != StatusInconclusive || !RequestFailed(res) {
		t.Fatalf("429 应记证据不足且可重试，实际 %s", res.Status)
	}
}

// 截断的回答没有真正的结尾：只认恰好落在最后一行的格式行，推理里出现过的不算。
func TestCandyTruncatedOnlyTrustsLastLine(t *testing.T) {
	cut := &iqUpstream{candy: "最终答案：16？不对，重新想\n如果先摸圆形……", stop: "max_tokens"}
	if res := findCheck(runIQ(t, cut, "iq-candy").Checks, "iq-candy"); res.Status != StatusInconclusive {
		t.Fatalf("截断且最后一行不是格式行应记证据不足，实际 %s（%s）", res.Status, res.Summary)
	}
	done := &iqUpstream{candy: "推理……\n最终答案：21", stop: "max_tokens"}
	if res := findCheck(runIQ(t, done, "iq-candy").Checks, "iq-candy"); res.Status != StatusPassed {
		t.Fatalf("格式行恰好写完再截断应照常判分，实际 %s（%s）", res.Status, res.Summary)
	}
}

// 智商测试不出判定：没有渠道分类、没有跨项审计，每道题也不产出证据与真实性分。
func TestIQSuiteSkipsVerdict(t *testing.T) {
	up := &iqUpstream{pelican: animatedPelican, candy: "最终答案：21"}
	run := runIQ(t, up, "iq-pelican", "iq-candy")
	if run.Suite != SuiteIQ {
		t.Fatalf("run.Suite = %q，期望 %q", run.Suite, SuiteIQ)
	}
	if run.Verdict != nil {
		t.Fatalf("智商测试不应产出判定，实际 %+v", run.Verdict)
	}
	if findCheck(run.Checks, AuditCheckID) != nil {
		t.Fatal("智商测试不应跑跨项审计")
	}
	for _, c := range run.Checks {
		if len(c.Evidence) > 0 || c.AuthScore != 0 || c.AuthCapReason != "" {
			t.Fatalf("%s 不应产出证据或真实性分: %+v", c.ID, c)
		}
	}
}

func TestResolveSuiteCheckIDsDoesNotMixSuites(t *testing.T) {
	iq := ResolveSuiteCheckIDs(SuiteIQ, []string{"ping", "iq-candy"})
	if strings.Join(iq, ",") != "iq-candy" {
		t.Fatalf("智商套件只应保留智商题，实际 %v", iq)
	}
	auth := ResolveSuiteCheckIDs(SuiteAuthenticity, []string{"ping", "iq-candy"})
	if strings.Join(auth, ",") != "ping" {
		t.Fatalf("真伪套件不应带上智商题，实际 %v", auth)
	}
	if got := ResolveSuiteCheckIDs(SuiteIQ, nil); strings.Join(got, ",") != "iq-pelican,iq-candy" {
		t.Fatalf("智商套件空选应回退到两道内置题，实际 %v", got)
	}
	for _, id := range DefaultCheckIDs(SuiteAuthenticity) {
		if meta, _ := checkByID(id); meta.InSuite(SuiteIQ) {
			t.Fatalf("真伪推荐项不应含智商题 %s", id)
		}
	}
	if NormalizeSuite("") != SuiteAuthenticity || NormalizeSuite("bogus") != SuiteAuthenticity ||
		NormalizeSuite(SuiteIQ) != SuiteIQ {
		t.Fatal("NormalizeSuite 应把未知值归为真伪检测")
	}
}

func TestPresetsOnlyReferAuthenticityChecks(t *testing.T) {
	for _, p := range Presets {
		for _, id := range p.Checks {
			if meta, _ := checkByID(id); !meta.InSuite(SuiteAuthenticity) {
				t.Fatalf("预设 %s 不应引用智商题 %s", p.ID, id)
			}
		}
	}
}

func TestIQChecksCarryPromptAndGrading(t *testing.T) {
	for _, id := range []string{"iq-pelican", "iq-candy"} {
		meta, ok := checkByID(id)
		if !ok || meta.Suite != SuiteIQ || meta.Prompt == "" || meta.Grading == "" || handlers[id] == nil {
			t.Fatalf("%s 目录信息不完整: %+v", id, meta)
		}
	}
}
