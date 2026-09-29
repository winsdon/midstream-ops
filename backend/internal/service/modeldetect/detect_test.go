package modeldetect

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeUpstream 模拟一条上游渠道。behaviour 决定它像哪种后端。
type fakeUpstream struct {
	mu        sync.Mutex
	behaviour string // official | bedrock | wrapper
	// signatureChecked 为 false 时，篡改的签名也会被接受（伪装特征）。
	signatureChecked bool
	// strictParams 为 false 时，非法参数一律放行。
	strictParams bool
	// injectCache 为 true 时，裸请求也返回缓存写入 —— 只有网关自己拼了系统提示
	// 才会出现这种写入，所以它是号池注入特征，并同时带上订阅额度头。
	injectCache bool
	// cacheMode 决定缓存探针（ping-again）看到的链形态，见 cacheMode* 常量。
	// 空字符串等同 cacheModeStable。
	cacheMode        string
	cacheSeen        map[string]bool
	cacheReadOffset  int
	cacheProbeBodies []map[string]any
	callCount        int

	// forgeCacheRead 在极小输入上伪造缓存命中（低于最小可缓存长度，物理上不可能）。
	forgeCacheRead bool
	// forgeZeroInput 把输入用量整体记进 cache_read，input_tokens 置 0（字段错位）。
	forgeZeroInput bool
	// forgeEmptyThinking 每次都返回带签名但正文为空的 thinking 块。
	forgeEmptyThinking bool
	// emptyThinkingOnce 只让第一个 thinking 块为空，之后正常返回正文
	// （adaptive 的合法形态，不得误杀）。
	emptyThinkingOnce bool
	thinkingCount     int
	// useRedactedThinking 返回合法的 redacted_thinking 保护形态。
	useRedactedThinking bool
	// bedrockPrefixOnly 只贴 msg_bdrk_ 前缀，不给任何 Bedrock 旁证。
	bedrockPrefixOnly bool
	// replyCount 已回复的消息数，用来生成互不相同的消息 id。
	replyCount int
	// replayID 非空时每条回复都用这个 id（回放旧响应的网关）。
	replayID string
	// signature 非空时替换 thinking 块的签名（签名来源审计用）。
	signature string
	// geo 非空时写进 usage.inference_geo。
	geo string
	// stripInvalidThinking 网关把校验不过的 thinking 块剥掉再转发（常见的「签名错误自动重试」）。
	stripInvalidThinking bool
	// stripAllThinking 网关一律剥掉历史 thinking 块（后端从来见不到回传的签名）。
	stripAllThinking bool
	// issued 本渠道签发过的签名：原样回传才算有效（签名被篡改一位就对不上）。
	issued map[string]bool
}

// fakeThoughts 假渠道每次 thinking 的思考 token 数；回传时按它计入输入（Opus 4.5 起的官方行为）。
const fakeThoughts = 150

// fakeSig 每条回复一份不同的合法签名：真后端的签名内含随机 nonce，从不重复。
func fakeSig(n int) string { return "SIGVALID_" + fakeMsgCore(n) + strings.Repeat("x", 18) }

// issue 记下一份签发出去的签名。
func (up *fakeUpstream) issue(sig string) string {
	up.mu.Lock()
	defer up.mu.Unlock()
	if up.issued == nil {
		up.issued = map[string]bool{}
	}
	up.issued[sig] = true
	return sig
}

// validSig 签名是不是本渠道原样签发的。
func (up *fakeUpstream) validSig(sig string) bool {
	up.mu.Lock()
	defer up.mu.Unlock()
	return up.issued[sig]
}

// fakeMsgCore 把序号编成 22 位 base58，使每条回复的消息 id 互不相同且符合官方字母表。
func fakeMsgCore(n int) string {
	const alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
	b := []byte(strings.Repeat("A", 22))
	for i := len(b) - 1; i >= 0 && n > 0; i-- {
		b[i] = alphabet[n%len(alphabet)]
		n /= len(alphabet)
	}
	return string(b)
}

func newFakeServer(t *testing.T, up *fakeUpstream) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up.mu.Lock()
		up.callCount++
		up.mu.Unlock()
		if r.URL.Path == "/v1/models" {
			writeJSON(w, 200, map[string]any{"data": []any{map[string]any{"id": "claude-opus-5"}}})
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)

		if r.URL.Path == "/v1/messages/count_tokens" {
			writeJSON(w, 200, map[string]any{"input_tokens": 12 + len(string(raw))/40})
			return
		}
		if up.behaviour == "bedrock" {
			w.Header().Set("x-amzn-requestid", "11111111-2222-3333-4444-555555555555")
		} else if up.behaviour == "official" {
			w.Header().Set("anthropic-ratelimit-requests-remaining", "999")
			if up.injectCache {
				// 真实号池带订阅额度头；号池判定靠它 + 裸请求注入两条独立旁证。
				w.Header().Set("anthropic-ratelimit-unified-5h-remaining", "42")
			}
		}

		if up.stripInvalidThinking || up.stripAllThinking {
			body = up.stripThinking(body, up.stripAllThinking)
		}
		if err := up.validate(body); err != nil {
			writeJSON(w, 400, map[string]any{
				"type": "error", "error": map[string]any{"type": "invalid_request_error", "message": err.Error()}})
			return
		}
		up.reply(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// stripThinking 去掉历史里的 thinking 块：all 为 false 时只去掉签名校验不过的（网关剥离后重发的效果）。
func (up *fakeUpstream) stripThinking(body map[string]any, all bool) map[string]any {
	msgs := sliceOf(body["messages"])
	out := make([]any, 0, len(msgs))
	for _, m := range msgs {
		msg := mapOf(m)
		blocks := sliceOf(msg["content"])
		if blocks == nil {
			out = append(out, m)
			continue
		}
		kept := make([]any, 0, len(blocks))
		for _, raw := range blocks {
			blk := mapOf(raw)
			if str(blk["type"]) == "thinking" && (all || !up.validSig(str(blk["signature"]))) {
				continue
			}
			kept = append(kept, raw)
		}
		out = append(out, map[string]any{"role": msg["role"], "content": kept})
	}
	cp := map[string]any{}
	for k, v := range body {
		cp[k] = v
	}
	cp["messages"] = out
	return cp
}

// 缓存探针的注入形态。默认 stable：前缀逐级增长，每次都读到已缓存的部分并写入新段。
const (
	cacheModeStable  = "stable"   // write₁ → (read₁+write₁, write₂) → (read₂+write₂, write₃)（默认）
	cacheModeOff     = "off"      // 每次都不返回缓存用量（网关把 cache_control 剥了）
	cacheModeDrop    = "drop"     // 首次写入后再也不命中
	cacheModePreHit  = "pre_hit"  // 首次就命中（复用了别人的缓存）
	cacheModeDrift   = "drift"    // 第 3 次没有把第 2 次写进去的量读回来
	cacheModeNoWrite = "no_write" // 第 2、3 次命中但零写入（前缀没有增长）
)

// fakeProbeSegments 稳定链上每段新增内容的 token 量：
// 第 n 次请求读前 n-1 段之和、写第 n 段。三次都非零 —— 这正是被测的形态。
var fakeProbeSegments = []int{2400, 900, 900}

// 是缓存探针请求：本次检测里只有 ping-again 会打显式 cache_control。
func isCacheProbeBody(body map[string]any) bool {
	for _, raw := range sliceOf(body["system"]) {
		if mapOf(raw)["cache_control"] != nil {
			return true
		}
	}
	return false
}

// probeChainUsage 按稳定链语义给出第 turn 次探针请求的 (read, write)。
func probeChainUsage(turn int) (read, write int) {
	for i := 0; i < turn-1 && i < len(fakeProbeSegments); i++ {
		read += fakeProbeSegments[i]
	}
	if turn-1 < len(fakeProbeSegments) {
		write = fakeProbeSegments[turn-1]
	}
	return
}

// applyInjection 按缓存形态写入本次响应的用量。调用方必须已持有 up.mu。
//
// 裸请求（不带 cache_control）代表「网关注入」：只有 injectCache 才写缓存，
// 因为裸请求上出现的缓存写入只可能来自网关自己拼进去的系统提示。
// 探针请求（ping-again）永远走缓存链模型 —— 前缀是我们自己送的。
func (up *fakeUpstream) applyInjection(usage map[string]any, body map[string]any) {
	isProbe := isCacheProbeBody(body)
	if up.cacheSeen == nil {
		up.cacheSeen = map[string]bool{}
	}
	if !isProbe {
		if !up.injectCache || up.cacheSeen["bare"] {
			return
		}
		up.cacheSeen["bare"] = true
		usage["cache_creation_input_tokens"] = 2400
		return
	}

	up.cacheProbeBodies = append(up.cacheProbeBodies, body)
	turn := len(up.cacheProbeBodies)

	switch up.cacheMode {
	case cacheModeOff:
		return
	case cacheModePreHit:
		usage["cache_read_input_tokens"] = 4030
		return
	case cacheModeDrop:
		// 第一次写进去，之后再也读不到。
		if turn == 1 {
			usage["cache_creation_input_tokens"] = fakeProbeSegments[0]
		}
		return
	case cacheModeDrift:
		read, write := probeChainUsage(turn)
		if turn == 3 {
			read = fakeProbeSegments[0] // 第 2 次写的那段没被读回来
		}
		read += up.probeReadOffset(turn)
		usage["cache_read_input_tokens"] = read
		usage["cache_creation_input_tokens"] = write
		return
	case cacheModeNoWrite:
		// 命中照旧，但新增内容没被写进缓存：后续读取停在第一段。
		if turn == 1 {
			usage["cache_creation_input_tokens"] = fakeProbeSegments[0]
			return
		}
		usage["cache_read_input_tokens"] = fakeProbeSegments[0] + up.probeReadOffset(turn)
		return
	}

	// stable：逐级推进。
	read, write := probeChainUsage(turn)
	usage["cache_read_input_tokens"] = read + up.probeReadOffset(turn)
	usage["cache_creation_input_tokens"] = write
}

// probeReadOffset 只偏移第 2 次起的读取量：首轮读到的必然是 0，
// 偏移它反而会变成「首轮就命中」这种另一种形态。
func (up *fakeUpstream) probeReadOffset(turn int) int {
	if turn <= 1 {
		return 0
	}
	return up.cacheReadOffset
}

// validate 复刻官方网关的参数与签名校验：strictParams=false 的渠道放行非法参数，
// signatureChecked=false 的渠道放行篡改签名，两者互不相干。
func (up *fakeUpstream) validate(body map[string]any) error {
	if up.strictParams {
		if err := validateParams(body); err != nil {
			return err
		}
	}
	// 篡改过的签名必须被拒
	if !up.signatureChecked {
		return nil
	}
	for _, m := range sliceOf(body["messages"]) {
		msg := mapOf(m)
		for _, raw := range sliceOf(msg["content"]) {
			blk := mapOf(raw)
			if str(blk["type"]) == "thinking" && !up.validSig(str(blk["signature"])) {
				return errText("Invalid signature on thinking block")
			}
		}
	}
	return nil
}

func validateParams(body map[string]any) error {
	if mt, ok := num(body["max_tokens"]); ok && mt < 1 {
		return errText("max_tokens: Input should be greater than or equal to 1")
	}
	if th := mapOf(body["thinking"]); th != nil {
		if b, ok := num(th["budget_tokens"]); ok && b < 1024 {
			return errText("thinking.budget_tokens: Input should be greater than or equal to 1024")
		}
		if _, hasTemp := body["temperature"]; hasTemp {
			return errText("temperature may only be set to 1 when thinking is enabled")
		}
	}
	known := map[string]bool{"model": true, "max_tokens": true, "messages": true, "system": true,
		"thinking": true, "tools": true, "tool_choice": true, "stream": true, "temperature": true,
		"output_config": true}
	for k := range body {
		if !known[k] {
			return errText("probe_unknown_field: Extra inputs are not permitted")
		}
	}
	return nil
}

func (up *fakeUpstream) reply(w http.ResponseWriter, body map[string]any) {
	up.mu.Lock()
	up.replyCount++
	n := up.replyCount
	up.mu.Unlock()
	core := fakeMsgCore(n)

	id := "msg_01" + core
	toolID := "toolu_01ABCDEFGHIJKLMNOP"
	switch up.behaviour {
	case "bedrock":
		id = "msg_bdrk_01" + core
		toolID = "tooluse_ABCDEFGHIJKLMNOPQR"
	case "wrapper":
		id = fmt.Sprintf("msg_6f1c2b7e-1111-4b0e-9e2a-%012d", n)
		toolID = "call_abc123"
	}
	// 只贴前缀不给旁证：id 顶着 msg_bdrk_，内核仍是第一方流水号。
	if up.bedrockPrefixOnly {
		id = "msg_bdrk_01" + core
	}
	if up.replayID != "" {
		id = up.replayID
	}

	usage := map[string]any{"input_tokens": 13, "output_tokens": 5}
	if up.geo != "" {
		usage["inference_geo"] = up.geo
	}
	// 探针（带 cache_control）永远走缓存链模型：前缀是我们自己送的，跟网关
	// 有没有注入无关。裸请求的缓存写入才只在 injectCache 时出现。
	up.mu.Lock()
	if up.injectCache {
		usage["input_tokens"] = 14
	}
	up.applyInjection(usage, body)
	up.mu.Unlock()
	// 极小输入上伪造缓存命中：总输入远低于 Opus 5 的 512 门槛。
	if up.forgeCacheRead {
		usage["input_tokens"] = 13
		usage["cache_read_input_tokens"] = 17
	}
	// 字段错位：输入被整体记成缓存读取。
	if up.forgeZeroInput {
		usage["input_tokens"] = 0
		usage["cache_read_input_tokens"] = 17
	}
	// 多轮对话（签名回传）的输入 = 首轮输入 + 之后每一轮：正文按长度折算，留在上下文里的
	// thinking 块按当初的思考量计（Opus 4.5 起的官方行为）。网关剥掉 thinking 时输入跟着变少。
	if msgs := sliceOf(body["messages"]); len(msgs) > 1 {
		usage["input_tokens"] = 13 + historyTokens(msgs[1:])
	}

	// 工具调用
	if tools := sliceOf(body["tools"]); len(tools) > 0 {
		tool := mapOf(tools[0])
		name := str(tool["name"])
		input := map[string]any{}
		text := userText(body)
		switch name {
		case "calculate_sum":
			a, b := twoNumbers(text)
			input = map[string]any{"a": a, "b": b}
		case "lookup_order":
			input = map[string]any{"order_id": firstToken(text, "ORDER_")}
		}
		writeJSON(w, 200, map[string]any{
			"id": id, "type": "message", "role": "assistant", "model": str(body["model"]),
			"stop_reason": "tool_use", "usage": usage,
			"content": []any{map[string]any{"type": "tool_use", "id": toolID, "name": name, "input": input}},
		})
		return
	}

	// thinking
	if mapOf(body["thinking"]) != nil && !hasThinkingBlock(body) {
		sig := fakeSig(n)
		if up.signature != "" {
			sig = up.signature
		}
		think := map[string]any{"type": "thinking", "thinking": "让我算一下……", "signature": up.issue(sig)}
		switch {
		case up.forgeEmptyThinking:
			// 有签名、无正文：签名是贴上去的
			think = map[string]any{"type": "thinking", "thinking": "", "signature": up.issue(fakeSig(n))}
		case up.emptyThinkingOnce:
			up.mu.Lock()
			up.thinkingCount++
			first := up.thinkingCount == 1
			up.mu.Unlock()
			if first {
				think = map[string]any{"type": "thinking", "thinking": "", "signature": up.issue(fakeSig(n))}
			}
		case up.useRedactedThinking:
			// 官方的加密保护形态，本就没有明文正文，不得误杀
			think = map[string]any{"type": "redacted_thinking", "data": strings.Repeat("z", 40)}
		}
		usage["output_tokens"] = fakeThoughts + 5
		usage["output_tokens_details"] = map[string]any{"thinking_tokens": fakeThoughts}
		answer := "答案是 262。"
		if len(sliceOf(body["messages"])) > 1 {
			// 多轮（历史里的 thinking 被网关剥掉后）照样回答最后一问。
			answer = replyFor(userText(body))
		}
		writeJSON(w, 200, map[string]any{
			"id": id, "type": "message", "role": "assistant", "model": str(body["model"]),
			"stop_reason": "end_turn", "usage": usage,
			"content": []any{
				think,
				map[string]any{"type": "text", "text": answer},
			},
		})
		return
	}

	limit := 4096
	if v, ok := num(body["max_tokens"]); ok {
		limit = int(v)
	}
	text := replyFor(userText(body))
	out := len(strings.Fields(text))
	stop := "end_turn"
	if out > limit {
		if up.behaviour == "wrapper" {
			// 包装渠道无视 max_tokens
			out = 800
		} else {
			text = strings.Join(strings.Fields(text)[:limit], " ")
			out = limit
			stop = "max_tokens"
		}
	}
	usage["output_tokens"] = out
	writeJSON(w, 200, map[string]any{
		"id": id, "type": "message", "role": "assistant", "model": str(body["model"]),
		"stop_reason": stop, "usage": usage,
		"content": []any{map[string]any{"type": "text", "text": text}},
	})
}

// replyFor 按提问内容给出足以让断言成立的回复。
func replyFor(prompt string) string {
	switch {
	case strings.Contains(prompt, "PONG"):
		return "PONG"
	case strings.Contains(prompt, "TOKEN"):
		return strings.TrimSpace(strings.Repeat("TOKEN ", 200))
	case strings.Contains(prompt, "校验串"):
		return firstToken(prompt, "CONT_")
	case strings.Contains(prompt, "session ID"):
		return firstToken(prompt, "SESSION_")
	case strings.Contains(prompt, "2+2"):
		return "Arrr! 4"
	}
	return "ok"
}

func userText(body map[string]any) string {
	var sb strings.Builder
	if s, ok := body["system"].(string); ok {
		sb.WriteString(s)
	}
	for _, m := range sliceOf(body["messages"]) {
		msg := mapOf(m)
		if s, ok := msg["content"].(string); ok {
			sb.WriteString(s + "\n")
		}
	}
	return sb.String()
}

func hasThinkingBlock(body map[string]any) bool {
	for _, m := range sliceOf(body["messages"]) {
		for _, raw := range sliceOf(mapOf(m)["content"]) {
			if str(mapOf(raw)["type"]) == "thinking" {
				return true
			}
		}
	}
	return false
}

// historyTokens 假渠道对首轮之后各轮的计量：正文按 JSON 长度折算，thinking 块按 fakeThoughts 计。
func historyTokens(msgs []any) int {
	total := 0
	for _, m := range msgs {
		msg := mapOf(m)
		blocks := sliceOf(msg["content"])
		if blocks == nil {
			raw, _ := json.Marshal(msg["content"])
			total += len(raw) / 4
			continue
		}
		for _, raw := range blocks {
			blk := mapOf(raw)
			if str(blk["type"]) == "thinking" {
				total += fakeThoughts
				continue
			}
			b, _ := json.Marshal(blk)
			total += len(b) / 4
		}
	}
	return total
}

// twoNumbers 从「计算 A 加 B」里取出两个数。
func twoNumbers(text string) (float64, float64) {
	var nums []float64
	for _, f := range strings.FieldsFunc(text, func(r rune) bool { return r < '0' || r > '9' }) {
		if v, err := strconv.ParseFloat(f, 64); err == nil {
			nums = append(nums, v)
		}
	}
	if len(nums) >= 2 {
		return nums[0], nums[1]
	}
	return 0, 0
}

func firstToken(text, prefix string) string {
	idx := strings.Index(text, prefix)
	if idx < 0 {
		return ""
	}
	rest := text[idx:]
	end := strings.IndexFunc(rest, func(r rune) bool {
		return !((r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_')
	})
	if end < 0 {
		return rest
	}
	return rest[:end]
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type textErr string

func (e textErr) Error() string { return string(e) }
func errText(s string) error    { return textErr(s) }

// ---- 测试 ----

func runAgainst(t *testing.T, up *fakeUpstream, checks []string) *TargetRun {
	t.Helper()
	srv := newFakeServer(t, up)
	target, err := Target{
		Name: "t", BaseURL: srv.URL, APIKey: "sk-test-key-1234567890", Model: "claude-opus-5",
	}.Normalize()
	if err != nil {
		t.Fatalf("归一化目标失败: %v", err)
	}
	return Run(context.Background(), target, checks, NewGate(4), nil)
}

func TestClassifyOfficialAPI(t *testing.T) {
	up := &fakeUpstream{behaviour: "official", strictParams: true, signatureChecked: true}
	run := runAgainst(t, up, []string{"models", "ping", "ping-again", "param-strict",
		"max-tokens-strict", "tool-use", "thinking-sig", "sig-tamper", "count-tokens"})

	if run.Verdict.Label != LabelOfficial {
		t.Fatalf("期望判定 %s，实际 %s（分数 %v）", LabelOfficial, run.Verdict.Label, run.Verdict.Scores)
	}
	if run.Verdict.Authenticity.Capped {
		t.Fatalf("干净官方后端不应触发真实性封顶：%v", run.Verdict.Authenticity.CapReason)
	}
	if run.Verdict.Authenticity.Score < 60 {
		t.Fatalf("期望真实性分 >= 60，实际 %d", run.Verdict.Authenticity.Score)
	}
}

func TestClassifyBedrock(t *testing.T) {
	up := &fakeUpstream{behaviour: "bedrock", strictParams: true, signatureChecked: true}
	run := runAgainst(t, up, []string{"ping", "tool-use"})

	if run.Verdict.Label != LabelBedrock {
		t.Fatalf("期望判定 %s，实际 %s（分数 %v）", LabelBedrock, run.Verdict.Label, run.Verdict.Scores)
	}
	if run.Verdict.Confidence != ConfidenceHigh {
		t.Fatalf("msg_bdrk_ 前缀应给出 high 置信度，实际 %s", run.Verdict.Confidence)
	}
}

func TestClassifyWrapperAndAuthenticityCap(t *testing.T) {
	up := &fakeUpstream{behaviour: "wrapper", strictParams: false, signatureChecked: false}
	run := runAgainst(t, up, []string{"ping", "param-strict", "max-tokens-strict",
		"tool-use", "thinking-sig", "sig-tamper"})

	if run.Verdict.Label != LabelWrapper {
		t.Fatalf("期望判定 %s，实际 %s（分数 %v）", LabelWrapper, run.Verdict.Label, run.Verdict.Scores)
	}
	if !run.Verdict.Authenticity.Capped {
		t.Fatal("不校验签名、无视 max_tokens 的渠道必须触发真实性封顶")
	}
	if run.Verdict.Authenticity.Score > authCapScore {
		t.Fatalf("封顶后真实性分应 <= %d，实际 %d", authCapScore, run.Verdict.Authenticity.Score)
	}
	if run.Verdict.Authenticity.Grade != GradeFake {
		t.Fatalf("期望等级 %s，实际 %s", GradeFake, run.Verdict.Authenticity.Grade)
	}
}

func TestMaxPoolCacheInjection(t *testing.T) {
	up := &fakeUpstream{behaviour: "official", strictParams: true, signatureChecked: true, injectCache: true}
	run := runAgainst(t, up, []string{"ping", "ping-again"})

	if run.Verdict.Label != LabelMaxPool {
		t.Fatalf("裸请求写入并命中大段缓存应判为号池，实际 %s（分数 %v）", run.Verdict.Label, run.Verdict.Scores)
	}
}

// 缓存链靠「前缀逐级增长」才测得出来：每一步都必须读到上次写的、并写入新段。
func TestCacheChainRequiresEveryStepToWrite(t *testing.T) {
	for _, tc := range []struct {
		name       string
		cacheMode  string
		offset     int
		status     string
		evidence   string
		wantWrites []int
	}{
		{name: "逐级推进", cacheMode: cacheModeStable, status: StatusPassed, evidence: "cache_chain_clean",
			wantWrites: []int{2400, 900, 900}},
		{name: "读取量偏短", cacheMode: cacheModeStable, offset: -1, status: StatusSuspicious, evidence: "cache_prefix_drift"},
		{name: "读取量偏长", cacheMode: cacheModeStable, offset: 1, status: StatusSuspicious, evidence: "cache_prefix_drift"},
		{name: "第三次没把第二次写进去读回来", cacheMode: cacheModeDrift, status: StatusSuspicious, evidence: "cache_prefix_drift"},
		{name: "后续请求零写入", cacheMode: cacheModeNoWrite, status: StatusSuspicious, evidence: "cache_not_extended"},
		{name: "写完不再命中", cacheMode: cacheModeDrop, status: StatusSuspicious, evidence: "cache_never_hits"},
		{name: "首轮就命中", cacheMode: cacheModePreHit, status: StatusSuspicious, evidence: "cache_probe_prehit"},
		{name: "探针被剥离", cacheMode: cacheModeOff, status: StatusInconclusive, evidence: "cache_probe_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mode := tc.cacheMode
			if mode == "" {
				mode = cacheModeStable
			}
			up := &fakeUpstream{behaviour: "official", cacheMode: mode, cacheReadOffset: tc.offset}
			run := runAgainst(t, up, []string{"ping", "ping-again"})
			got := findCheckResult(run, "ping-again")
			if got == nil {
				t.Fatal("缺少 ping-again 结果")
			}
			if got.Status != tc.status {
				t.Fatalf("状态 = %s（%s），期望 %s", got.Status, got.Summary, tc.status)
			}
			if !hasEvidence([]*CheckResult{got}, tc.evidence) {
				t.Fatalf("缺少证据 %s，实际 %+v", tc.evidence, got.Evidence)
			}
			if len(got.Exchanges) != 3 {
				t.Fatalf("缓存链应恰好发 3 次请求，实际 %d", len(got.Exchanges))
			}
			for i, want := range tc.wantWrites {
				if gotWrite := cacheCreationTokens(usageOf(got.Exchanges[i].JSON)); gotWrite != want {
					t.Fatalf("第 %d 次写入 = %d，期望 %d", i+1, gotWrite, want)
				}
			}
		})
	}
}

// 只发同一段前缀时第 2、3 次完全等价，链有没有推进根本看不出来 ——
// 这条测试钉住「三次请求必须是严格增长的前缀，且每次都写新段」。
func TestCacheProbeGrowsPrefixEachRequest(t *testing.T) {
	up := &fakeUpstream{behaviour: "official"}
	run := runAgainst(t, up, []string{"ping", "ping-again"})
	got := findCheckResult(run, "ping-again")
	if got == nil || got.Status != StatusPassed {
		t.Fatalf("逐级推进的缓存链应通过，实际 %#v", got)
	}

	up.mu.Lock()
	bodies := append([]map[string]any(nil), up.cacheProbeBodies...)
	up.mu.Unlock()
	if len(bodies) != 3 {
		t.Fatalf("带 cache_control 的探针请求应为 3 条，实际 %d", len(bodies))
	}

	var prev []string
	var prevTotal int
	for i, body := range bodies {
		blocks := sliceOf(body["system"])
		if len(blocks) != i+1 {
			t.Fatalf("第 %d 条探针应有 %d 个 system 块（前缀逐次加长），实际 %d", i+1, i+1, len(blocks))
		}
		if mapOf(blocks[len(blocks)-1])["cache_control"] == nil {
			t.Fatalf("第 %d 条探针的 cache_control 不在末段：%+v", i+1, blocks)
		}
		texts := make([]string, 0, len(blocks))
		total := 0
		for j, raw := range blocks {
			text := str(mapOf(raw)["text"])
			texts = append(texts, text)
			total += len(text)
			if j < i && mapOf(raw)["cache_control"] != nil {
				t.Fatalf("第 %d 条探针给旧段打了 cache_control，前缀就不再是纯增长：%+v", i+1, blocks)
			}
		}
		// 前面的段必须逐字节保留，否则前缀不是字节前缀，缓存也不可能命中。
		for j, old := range prev {
			if texts[j] != old {
				t.Fatalf("第 %d 条探针的第 %d 段被改写，前缀不再是增长的前缀", i+1, j+1)
			}
		}
		if total <= prevTotal {
			t.Fatalf("第 %d 条探针前缀没有变长（%d → %d）", i+1, prevTotal, total)
		}
		if i == 0 && len(texts[0]) < 1024 {
			t.Fatalf("首段只有 %d 字符，短于最小可缓存长度会被官方静默跳过", len(texts[0]))
		}
		prev, prevTotal = texts, total
	}
}

// 3 次带 cache_control 的请求都没缓存用量时，只能记「本轮无证据」。
// 旧的两次裸请求把它写成「符合干净 API Key / Bedrock 直连」并给 +1 官方分，
// 但那个观察实际区分不了「网关剥了 cache_control」与「干净直连」。
func TestCacheProbeWithoutCacheUsageIsInconclusive(t *testing.T) {
	up := &fakeUpstream{behaviour: "official", injectCache: true, cacheMode: cacheModeOff}
	run := runAgainst(t, up, []string{"ping", "ping-again"})
	got := findCheckResult(run, "ping-again")
	if got == nil || got.Status != StatusInconclusive {
		t.Fatalf("无缓存用量的探针应记证据不足，实际 %#v", got)
	}
	for _, ev := range got.Evidence {
		if ev.Weight > 0 && ev.Class != ClassOfficial {
			t.Fatalf("无缓存用量不该给任何渠道加分：%+v", ev)
		}
		if ev.Key == "cache_probe_unavailable" && ev.Weight != 0 {
			t.Fatalf("「没有缓存用量」本身权重必须为 0：%+v", ev)
		}
	}
	if !hasEvidence([]*CheckResult{got}, "cache_probe_unavailable") {
		t.Fatalf("缺少 cache_probe_unavailable 证据：%+v", got.Evidence)
	}
}

func findCheckResult(run *TargetRun, id string) *CheckResult {
	for _, result := range run.Checks {
		if result != nil && result.ID == id {
			return result
		}
	}
	return nil
}

func TestUnavailableTarget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 401, map[string]any{"type": "error",
			"error": map[string]any{"type": "authentication_error", "message": "invalid x-api-key"}})
	}))
	defer srv.Close()
	target, _ := Target{BaseURL: srv.URL, APIKey: "sk-test-key-1234567890", Model: "claude-opus-5"}.Normalize()
	run := Run(context.Background(), target, []string{"models", "ping"}, NewGate(2), nil)
	if run.Verdict.Label != LabelUnavailable {
		t.Fatalf("全部请求 401 应判不可用，实际 %s", run.Verdict.Label)
	}
}

func TestAPIKeyNeverLeaksIntoReport(t *testing.T) {
	const key = "sk-secret-value-should-never-appear"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 故意把 key 回显进响应体，验证脱敏生效
		writeJSON(w, 200, map[string]any{"id": "msg_01x", "type": "message", "role": "assistant",
			"model": "claude-opus-5", "stop_reason": "end_turn",
			"usage":   map[string]any{"input_tokens": 12, "output_tokens": 2},
			"content": []any{map[string]any{"type": "text", "text": "echo " + r.Header.Get("x-api-key")}}})
	}))
	defer srv.Close()

	target, _ := Target{BaseURL: srv.URL, APIKey: key, Model: "claude-opus-5"}.Normalize()
	run := Run(context.Background(), target, []string{"ping"}, NewGate(1), nil)

	blob, err := json.Marshal(run)
	if err != nil {
		t.Fatalf("序列化报告失败: %v", err)
	}
	if strings.Contains(string(blob), key) {
		t.Fatal("报告中出现了 API Key 明文")
	}
	if !strings.Contains(string(blob), redacted) {
		t.Fatal("报告中未见脱敏占位符，脱敏可能未生效")
	}
}

func TestResolveCheckIDsAddsDependencies(t *testing.T) {
	got := ResolveCheckIDs([]string{"sig-tamper", "ping-again"})
	want := map[string]bool{"ping": true, "ping-again": true, "thinking-sig": true, "sig-tamper": true}
	if len(got) != len(want) {
		t.Fatalf("期望补齐 %d 项依赖，实际 %v", len(want), got)
	}
	for _, id := range got {
		if !want[id] {
			t.Fatalf("出现意外检测项 %s", id)
		}
	}
	// 顺序必须让前置项先跑
	if indexOf(got, "ping") > indexOf(got, "ping-again") ||
		indexOf(got, "thinking-sig") > indexOf(got, "sig-tamper") {
		t.Fatalf("前置项未排在依赖项之前: %v", got)
	}
}

func TestRequestFailed(t *testing.T) {
	if RequestFailed(&CheckResult{Status: StatusFailed, Exchanges: []*Exchange{{Status: 400}}}) {
		t.Fatal("协议 400 不应算请求失败")
	}
	if RequestFailed(&CheckResult{Status: StatusPassed, Exchanges: []*Exchange{{Status: 200}}}) {
		t.Fatal("成功项不应算请求失败")
	}
	if !RequestFailed(&CheckResult{Status: StatusInconclusive, Exchanges: []*Exchange{{Status: 429}}}) {
		t.Fatal("429 应可重试")
	}
	if !RequestFailed(&CheckResult{Status: StatusInconclusive, Exchanges: []*Exchange{{Status: 502}}}) {
		t.Fatal("5xx 应可重试")
	}
	if !RequestFailed(&CheckResult{Status: StatusInconclusive, Exchanges: []*Exchange{{NetworkError: "timeout"}}}) {
		t.Fatal("网络错误应可重试")
	}
	if RequestFailed(&CheckResult{Status: StatusRunning, Exchanges: []*Exchange{{NetworkError: "x"}}}) {
		t.Fatal("执行中的项还没结束，不能标成可重试")
	}
}

func TestExpandRetryIDsFollowsSkippedDependents(t *testing.T) {
	existing := []*CheckResult{
		{ID: "ping", Status: StatusInconclusive, Exchanges: []*Exchange{{Status: 502}}},
		{ID: "ping-again", Status: StatusInconclusive},
	}
	got := ExpandRetryIDs([]string{"ping"}, existing, []string{"ping", "ping-again", "stream"})
	if indexOf(got, "ping") < 0 || indexOf(got, "ping-again") < 0 {
		t.Fatalf("重试 ping 应带上因前置失败而没发出请求的 ping-again，实际 %v", got)
	}
	if indexOf(got, "stream") >= 0 {
		t.Fatalf("未跑过的 stream 不应被带上: %v", got)
	}
}

func TestRetryChecksReplacesOnlyRequested(t *testing.T) {
	fails := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fails {
			writeJSON(w, 502, map[string]any{"type": "error", "error": map[string]any{"type": "api_error", "message": "boom"}})
			return
		}
		writeJSON(w, 200, map[string]any{
			"id": "msg_01TESTTESTTESTTESTTEST", "type": "message", "role": "assistant",
			"model": "claude-opus-5", "stop_reason": "end_turn",
			"usage":   map[string]any{"input_tokens": 12, "output_tokens": 1},
			"content": []any{map[string]any{"type": "text", "text": "PONG"}},
		})
	}))
	defer srv.Close()
	target, _ := Target{Name: "t", BaseURL: srv.URL, APIKey: "sk-test-key-1234567890", Model: "claude-opus-5"}.Normalize()
	first := Run(context.Background(), target, []string{"ping"}, NewGate(1), nil)
	if !RequestFailed(first.Checks[0]) {
		t.Fatalf("首次应是请求失败，实际 %+v", first.Checks[0])
	}
	fails = false
	second := RetryChecks(context.Background(), target, []string{"ping"}, first, NewGate(1), nil)
	ping := findCheck(second.Checks, "ping")
	if ping == nil {
		t.Fatalf("重试后应仍有 ping 项，实际 %v", checkIDs(second.Checks))
	}
	if RequestFailed(ping) || ping.Status != StatusPassed {
		t.Fatalf("重试成功后应通过，实际 status=%s", ping.Status)
	}
	// 重试不得把同一项叠成两份（跨项审计除外，它每次重算并替换）。
	if got := countCheck(second.Checks, "ping"); got != 1 {
		t.Fatalf("ping 应只有 1 份，实际 %d", got)
	}
	if got := countCheck(second.Checks, AuditCheckID); got > 1 {
		t.Fatalf("审计项应只有 1 份，实际 %d", got)
	}
}

// findCheck 按 id 取结果，缺失返回 nil。
func findCheck(checks []*CheckResult, id string) *CheckResult {
	for _, c := range checks {
		if c != nil && c.ID == id {
			return c
		}
	}
	return nil
}

// countCheck 统计某个 id 出现几次。
func countCheck(checks []*CheckResult, id string) int {
	n := 0
	for _, c := range checks {
		if c != nil && c.ID == id {
			n++
		}
	}
	return n
}

// checkIDs 取所有结果的 id，用于失败信息。
func checkIDs(checks []*CheckResult) []string {
	out := make([]string, 0, len(checks))
	for _, c := range checks {
		if c != nil {
			out = append(out, c.ID)
		}
	}
	return out
}

func indexOf(list []string, v string) int {
	for i, s := range list {
		if s == v {
			return i
		}
	}
	return -1
}

func TestRunIndependentChecksOverlap(t *testing.T) {
	var mu sync.Mutex
	inflight, maxInflight := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		inflight++
		if inflight > maxInflight {
			maxInflight = inflight
		}
		mu.Unlock()
		time.Sleep(80 * time.Millisecond)
		mu.Lock()
		inflight--
		mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/models") {
			writeJSON(w, 200, map[string]any{"data": []any{}})
			return
		}
		writeJSON(w, 200, map[string]any{
			"id": "msg_01TESTTESTTESTTESTTEST", "type": "message", "role": "assistant",
			"model": "claude-opus-5", "stop_reason": "end_turn",
			"usage":   map[string]any{"input_tokens": 12, "output_tokens": 1},
			"content": []any{map[string]any{"type": "text", "text": "PONG"}},
		})
	}))
	defer srv.Close()

	target, err := Target{Name: "t", BaseURL: srv.URL, APIKey: "sk-test-key-1234567890", Model: "claude-opus-5"}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	run := Run(context.Background(), target, []string{"models", "ping"}, NewGate(2), nil)
	if run.Verdict == nil {
		t.Fatal("缺少判定")
	}
	mu.Lock()
	got := maxInflight
	mu.Unlock()
	if got < 2 {
		t.Fatalf("无依赖的检测项应并行，最大在途 %d", got)
	}
}

func TestRunWaitsForDependencies(t *testing.T) {
	var mu sync.Mutex
	pingLive := false
	pingAgainOverlapped := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			writeJSON(w, 200, map[string]any{"data": []any{}})
			return
		}
		mu.Lock()
		if pingLive {
			pingAgainOverlapped = true
		}
		pingLive = true
		mu.Unlock()
		time.Sleep(50 * time.Millisecond)
		mu.Lock()
		pingLive = false
		mu.Unlock()
		writeJSON(w, 200, map[string]any{
			"id": "msg_01TESTTESTTESTTESTTEST", "type": "message", "role": "assistant",
			"model": "claude-opus-5", "stop_reason": "end_turn",
			"usage":   map[string]any{"input_tokens": 12, "output_tokens": 1},
			"content": []any{map[string]any{"type": "text", "text": "PONG"}},
		})
	}))
	defer srv.Close()

	target, _ := Target{Name: "t", BaseURL: srv.URL, APIKey: "sk-test-key-1234567890", Model: "claude-opus-5"}.Normalize()
	Run(context.Background(), target, []string{"ping", "ping-again"}, NewGate(3), nil)
	if pingAgainOverlapped {
		t.Fatal("ping-again 不得与 ping 重叠")
	}
}

func TestRunEmitsRunningThenDone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(30 * time.Millisecond)
		writeJSON(w, 200, map[string]any{
			"id": "msg_01TESTTESTTESTTESTTEST", "type": "message", "role": "assistant",
			"model": "claude-opus-5", "stop_reason": "end_turn",
			"usage":   map[string]any{"input_tokens": 12, "output_tokens": 1},
			"content": []any{map[string]any{"type": "text", "text": "PONG"}},
		})
	}))
	defer srv.Close()

	target, _ := Target{Name: "t", BaseURL: srv.URL, APIKey: "sk-test-key-1234567890", Model: "claude-opus-5"}.Normalize()
	var mu sync.Mutex
	var events []string
	run := Run(context.Background(), target, []string{"ping"}, NewGate(1), func(res *CheckResult) {
		mu.Lock()
		events = append(events, res.ID+":"+res.Status)
		mu.Unlock()
	})
	if ping := findCheck(run.Checks, "ping"); ping == nil || ping.Status == StatusRunning {
		t.Fatalf("最终结果不应停留在 running: %+v", run.Checks)
	}
	mu.Lock()
	got := append([]string(nil), events...)
	mu.Unlock()
	if len(got) < 2 || got[0] != "ping:running" || got[len(got)-1] == "ping:running" {
		t.Fatalf("应先回调 running 再回调终态，实际 %v", got)
	}
}

func TestNormalizeBaseURL(t *testing.T) {
	cases := []struct {
		in, want string
		wantErr  bool
	}{
		{in: "https://api.example.com/", want: "https://api.example.com"},
		{in: "https://api.example.com/v1/messages", want: "https://api.example.com"},
		{in: "https://api.example.com/v1/messages/count_tokens", want: "https://api.example.com"},
		{in: "https://api.example.com/v1", want: "https://api.example.com/v1"},
		{in: "api.example.com", wantErr: true},
		{in: "https://user:pass@api.example.com", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, tc := range cases {
		got, err := NormalizeBaseURL(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("%q 期望报错，实际得到 %q", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q 意外报错: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("%q 期望 %q，实际 %q", tc.in, tc.want, got)
		}
	}
}

func TestEndpointDoesNotDoubleV1(t *testing.T) {
	if got := endpoint("https://x.com/v1", KindMessages); got != "https://x.com/v1/messages" {
		t.Fatalf("已带 /v1 的地址被重复拼接: %s", got)
	}
	if got := endpoint("https://x.com", KindCountTokens); got != "https://x.com/v1/messages/count_tokens" {
		t.Fatalf("count_tokens 端点错误: %s", got)
	}
}

func TestResolveProfile(t *testing.T) {
	cases := []struct {
		model     string
		adaptive  bool
		minCache  int
		recognize bool
	}{
		{"claude-opus-5", true, 512, true},
		{"claude-fable-5", true, 512, true},
		{"claude-sonnet-4-6", true, 1024, true},
		{"claude-opus-4-5-20251101", false, 4096, true},
		{"claude-haiku-4-5", false, 4096, true},
		{"某国产模型", false, 4096, false},
	}
	for _, tc := range cases {
		p := ResolveProfile(tc.model)
		if p.Adaptive != tc.adaptive || p.MinCacheTokens != tc.minCache || p.Recognized != tc.recognize {
			t.Fatalf("%s: 期望 adaptive=%v minCache=%d recognized=%v，实际 %+v",
				tc.model, tc.adaptive, tc.minCache, tc.recognize, p)
		}
	}
}

func TestValidateSSERejectsBrokenFlow(t *testing.T) {
	good := parseSSE(strings.Join([]string{
		`event: message_start` + "\n" + `data: {"type":"message_start","message":{"usage":{"input_tokens":3}}}`,
		`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":0}`,
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
		`event: content_block_stop` + "\n" + `data: {"type":"content_block_stop","index":0}`,
		`event: message_delta` + "\n" + `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
		`event: message_stop` + "\n" + `data: {"type":"message_stop"}`,
	}, "\n\n"))
	if flow := validateSSE(good); !flow.valid || !flow.startHasUsage {
		t.Fatalf("合法 SSE 流被判非法: %s", flow.detail())
	}

	broken := parseSSE(strings.Join([]string{
		`event: message_start` + "\n" + `data: {"type":"message_start"}`,
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
		`event: message_stop` + "\n" + `data: {"type":"message_stop"}`,
	}, "\n\n"))
	flow := validateSSE(broken)
	if flow.valid {
		t.Fatal("缺 content_block_start / message_delta 的流应被判非法")
	}
	if flow.startHasUsage {
		t.Fatal("message_start 无 usage 时不应报告 startHasUsage")
	}
}

func TestNormalizeCutoff(t *testing.T) {
	cases := map[string]string{
		"2026-05":    "2026-05",
		"2026年5月":    "2026-05",
		"May 2026":   "2026-05",
		"January 26": "",
	}
	for in, want := range cases {
		if got := normalizeCutoff(in); got != want {
			t.Fatalf("%q 期望 %q，实际 %q", in, want, got)
		}
	}
}

func TestModelMatchesRequested(t *testing.T) {
	if !modelMatchesRequested("Claude Opus 5", "claude-opus-5") {
		t.Fatal("别名应与模型 id 匹配")
	}
	if !modelMatchesRequested("claude-opus-4-5", "claude-opus-4-5-20251101") {
		t.Fatal("带日期后缀的 id 应与主键匹配")
	}
	if modelMatchesRequested("claude-sonnet-4-5", "claude-opus-5") {
		t.Fatal("不同型号不应匹配")
	}
}

func TestThinkingGradientRemoved(t *testing.T) {
	if _, ok := checkByID("thinking-gradient"); ok {
		t.Fatal("thinking-gradient 不应继续出现在检测目录")
	}
	if _, ok := handlers["thinking-gradient"]; ok {
		t.Fatal("thinking-gradient 不应继续注册 handler")
	}
	for _, preset := range Presets {
		for _, id := range preset.Checks {
			if id == "thinking-gradient" {
				t.Fatalf("预设 %s 不应引用 thinking-gradient", preset.ID)
			}
		}
	}
}
func TestPresetsReferKnownChecks(t *testing.T) {
	known := map[string]bool{}
	for _, c := range Checks {
		known[c.ID] = true
	}
	if len(Presets) != 2 {
		t.Fatalf("场景预设应为 CC Max 与 AWS Bedrock 两套，实际 %d", len(Presets))
	}
	seen := map[string]bool{}
	for _, p := range Presets {
		if p.ID == "" || p.Title == "" {
			t.Fatalf("预设缺 id 或标题: %+v", p)
		}
		if seen[p.ID] {
			t.Fatalf("预设 id 重复: %s", p.ID)
		}
		seen[p.ID] = true
		if len(p.Checks) == 0 {
			t.Fatalf("预设 %s 没有检测项", p.ID)
		}
		for _, id := range p.Checks {
			if !known[id] {
				t.Fatalf("预设 %s 引用了不存在的检测项 %s", p.ID, id)
			}
		}
	}
	if !seen["cc_max"] || !seen["aws_bedrock"] {
		t.Fatalf("缺少约定预设 id，实际 %v", seen)
	}
}

// ---- 跨项审计 ----

// auditOf 取审计结果，缺失即失败。
func auditOf(t *testing.T, run *TargetRun) *CheckResult {
	t.Helper()
	res := findCheck(run.Checks, AuditCheckID)
	if res == nil {
		t.Fatalf("应产出跨项审计结果，实际 %v", checkIDs(run.Checks))
	}
	return res
}

// hasEvidenceKey 判断某个结果里是否出现指定证据。
func hasEvidenceKey(res *CheckResult, key string) bool {
	for _, ev := range res.Evidence {
		if ev.Key == key {
			return true
		}
	}
	return false
}

// TestAuditRejectsImpossibleCacheRead 总输入低于最小可缓存长度却报缓存命中，
// 物理上不可能，必须封顶真实性评分。这是客户样本里最硬的一条伤。
func TestAuditRejectsImpossibleCacheRead(t *testing.T) {
	up := &fakeUpstream{behaviour: "official", strictParams: true, signatureChecked: true,
		forgeCacheRead: true}
	run := runAgainst(t, up, []string{"ping"})

	res := auditOf(t, run)
	if !hasEvidenceKey(res, "usage_impossible_cache") {
		t.Fatalf("应给出 usage_impossible_cache 证据，实际 %+v", res.Evidence)
	}
	if res.AuthCapReason == "" {
		t.Fatal("不可能的缓存命中应封顶真实性评分")
	}
	if got := run.Verdict.Authenticity.Grade; got != GradeFake {
		t.Fatalf("真实性应判 fake，实际 %s（score=%d）", got, run.Verdict.Authenticity.Score)
	}
}

// TestAuditRejectsZeroInputWithCacheRead input_tokens=0 却有输出与缓存读取，
// 说明输入被整体记进了 cache_read（字段错位）。
func TestAuditRejectsZeroInputWithCacheRead(t *testing.T) {
	up := &fakeUpstream{behaviour: "official", strictParams: true, signatureChecked: true,
		forgeZeroInput: true}
	run := runAgainst(t, up, []string{"ping"})

	res := auditOf(t, run)
	if !hasEvidenceKey(res, "usage_zero_input") {
		t.Fatalf("应给出 usage_zero_input 证据，实际 %+v", res.Evidence)
	}
	if run.Verdict.Authenticity.Grade != GradeFake {
		t.Fatalf("真实性应判 fake，实际 %s", run.Verdict.Authenticity.Grade)
	}
}

// TestAuditRejectsEmptySignedThinking 整轮检测里带签名的 thinking 块全部无正文，
// 说明这条渠道根本不会思考，签名是凭空贴上去的。只看要了摘要的请求，且至少两个样本
// （thinking-sig 与 param-strict 里带 thinking 的那一问）。
func TestAuditRejectsEmptySignedThinking(t *testing.T) {
	up := &fakeUpstream{behaviour: "official", strictParams: false, signatureChecked: true,
		forgeEmptyThinking: true}
	run := runAgainst(t, up, []string{"ping", "thinking-sig", "param-strict"})

	res := auditOf(t, run)
	if !hasEvidenceKey(res, "thinking_empty_signed") {
		t.Fatalf("应给出 thinking_empty_signed 证据，实际 %+v", res.Evidence)
	}
	if run.Verdict.Authenticity.Grade != GradeFake {
		t.Fatalf("真实性应判 fake，实际 %s", run.Verdict.Authenticity.Grade)
	}
}

// TestAuditAllowsSomeEmptyThinking adaptive 模式下「已签名但无摘要正文」是合法形态。
// 只要整轮里出现过思考正文，就不得因为某几次为空而定罪。
func TestAuditAllowsSomeEmptyThinking(t *testing.T) {
	up := &fakeUpstream{behaviour: "official", strictParams: true, signatureChecked: true,
		emptyThinkingOnce: true}
	run := runAgainst(t, up, []string{"ping", "thinking-sig", "quality-baseline"})

	res := auditOf(t, run)
	if hasEvidenceKey(res, "thinking_empty_signed") {
		t.Fatalf("出现过思考正文就不应定罪，实际 %+v", res.Evidence)
	}
	if run.Verdict.Authenticity.Capped {
		t.Fatalf("不应封顶，原因 %v", run.Verdict.Authenticity.CapReason)
	}
}

// TestAuditAllowsRedactedThinking redacted_thinking 是官方的加密保护形态，
// 本就没有明文正文，不得误杀。
func TestAuditAllowsRedactedThinking(t *testing.T) {
	up := &fakeUpstream{behaviour: "official", strictParams: true, signatureChecked: true,
		useRedactedThinking: true}
	run := runAgainst(t, up, []string{"ping", "thinking-sig"})

	res := auditOf(t, run)
	if hasEvidenceKey(res, "thinking_empty_signed") {
		t.Fatal("redacted_thinking 不应被判成空签名块")
	}
	if run.Verdict.Authenticity.Capped {
		t.Fatalf("合法保护形态不应封顶，原因 %v", run.Verdict.Authenticity.CapReason)
	}
}

// TestAuditPassesCleanOfficial 干净的官方渠道不得触发任何审计告警（防误杀）。
func TestAuditPassesCleanOfficial(t *testing.T) {
	up := &fakeUpstream{behaviour: "official", strictParams: true, signatureChecked: true}
	run := runAgainst(t, up, []string{"ping", "thinking-sig", "sig-tamper"})

	res := auditOf(t, run)
	if res.AuthCapReason != "" {
		t.Fatalf("干净渠道不应被封顶：%s", res.AuthCapReason)
	}
	for _, key := range []string{"usage_impossible_cache", "usage_zero_input", "thinking_empty_signed"} {
		if hasEvidenceKey(res, key) {
			t.Fatalf("干净渠道不应出现 %s", key)
		}
	}
}

// TestBedrockPrefixAloneIsNotEnough 只贴 msg_bdrk_ 前缀、无任何旁证的渠道，
// 不得被判成 Bedrock——贴一个前缀字符串的成本太低。
func TestBedrockPrefixAloneIsNotEnough(t *testing.T) {
	up := &fakeUpstream{behaviour: "official", strictParams: true, signatureChecked: true,
		bedrockPrefixOnly: true}
	run := runAgainst(t, up, []string{"ping", "thinking-sig", "sig-tamper"})

	if run.Verdict.Label == LabelBedrock {
		t.Fatalf("仅凭前缀不应判 Bedrock，scores=%v", run.Verdict.Scores)
	}
	res := auditOf(t, run)
	if !hasEvidenceKey(res, "bedrock_prefix_only") {
		t.Fatalf("应指出前缀缺旁证，实际 %+v", res.Evidence)
	}
}

// TestBedrockWithCorroborationStillClassifies 回归保护：带 x-amzn-* 与 tooluse_ 的
// 真 Bedrock 渠道必须仍判 Bedrock，降权不能误杀真渠道。
func TestBedrockWithCorroborationStillClassifies(t *testing.T) {
	up := &fakeUpstream{behaviour: "bedrock", strictParams: true, signatureChecked: true}
	run := runAgainst(t, up, []string{"ping", "tool-use", "thinking-sig", "sig-tamper"})

	if run.Verdict.Label != LabelBedrock {
		t.Fatalf("有旁证的 Bedrock 应判 aws_bedrock，实际 %s scores=%v",
			run.Verdict.Label, run.Verdict.Scores)
	}
	res := auditOf(t, run)
	if !hasEvidenceKey(res, "bedrock_corroborated") {
		t.Fatalf("应给出 bedrock_corroborated 证据，实际 %+v", res.Evidence)
	}
	if run.Verdict.Confidence != ConfidenceHigh {
		t.Fatalf("有旁证时置信度应为 high，实际 %s", run.Verdict.Confidence)
	}
}

// TestParseSignatureShape 用实测样本验证签名字段解析。
//
// 两份样本分别是 schema v17（opus-5，内嵌模型 + 账号 UUID + 签发时间）与 v15（内部代号、
// 无账号与时间）。早先把它们当作 Bedrock / 第一方两族，后来证实第一方 CC Max 渠道的 opus-5
// 签名同样是 v17 形态——差别在 schema 版本，签名认不出平台，所以 Family 只认 Vertex。
func TestParseSignatureShape(t *testing.T) {
	const v17Sig = "CAISuAIKpgEIERgCKkDwdNvB3RB+OxiKEVcsXwLxiSdHsXzQYtXXVyxj+5Aad9T+sxY0WZNF02RnQ0dI/5wXvLg3BCvqxHbkT3Rvx1dAMg1jbGF1ZGUtb3B1cy01OAFCCHRoaW5raW5nWiRkMWRlMTVlNy1jZWViLTRiYWYtOWI3MS1kOGE0MjNiY2IwZGVyEFmXpmOOcoaDAXn4SwAtQuSIAQGoAZil8dQGsAECEgxo1pRa0g58s4HHnw4aDG/l4Lo+E0n4vc+41iIw8aR5OZqyd3cPimrH9rqwVDmtjXLVZVOK7xmeOXb+vtHFfDWJnHoYfDDBDt5PYCJFKj/Lm/iV9VaFArzDVewwZ2Krummb6Ue5zzswWNUAd3JFATZRiDCPQ8FpmXlZA7uUk2zGzvqqljT/E8ysswbGWaAYAQ=="
	const v15Sig = "EsIDCmIIDxABGAIqQDVYDjprY+zhcKXcGeGZt0T0bOtXnZ2BF+aUwREzpA8DLPqKgVDL2gJmd0v0UugRMd9cJq8VldT9YVUhAJnwNeEyDGNsYXVkZS1ob25leTgAQgh0aGlua2luZxIMISnSIxBxeBzdeQ2GGgxGorabR0lT738SvwciMMMdiNS3vDBwx+L1lme4DQRdlo0nrNlE1ODQXgGPfXGI7kMT+mDYRci6CGndngnZXCqNAkghB1kJU5HBaiSGnKP8Vh+1O3pWnhE3I1cj3NE7uCJRkg3C+teTs//u8d2MFPHn2bjQHtrbAfSAC4bEsh1BjIvmisYyhdkh4QdtjTHcKDjhaFOvtWuG2bvNUGFBWwuqFsf9Yq9NBhdehchdFAVX5OBvZCBd4eS2Qm8d1+2Zx6w4T5nrHUczJpAJpVGLjpjPRYFeYY5nlumI2ScLyUH3EpZWVD8yLwRZVgpRXp8/Wajuri8Hw6pYJ+f+AJt1WfMp16PKR/Wskb1e1RhMRq9Ld7siIwun1T8v3jZwZxvykn2RTM+rE7QhMGuuaN5TFGL9axd1Z16mPrVh9TQSo9d6jw+ADoyKmgIbDo+yTi2rGAE="

	cases := []struct {
		name    string
		sig     string
		want    SigShape
		wantTS  int64
		wantFam string
	}{
		{"v17 opus-5", v17Sig, SigShape{Version: 17, Model: "claude-opus-5",
			AccountRef: "d1de15e7-ceeb-4baf-9b71-d8a423bcb0de"}, 1788629656, SigFamilyUnknown},
		{"v15 内部代号", v15Sig, SigShape{Version: 15, Model: "claude-honey"}, 0, SigFamilyUnknown},
		{"OAuth 订阅号带 uprof_", buildSig(17, "claude-opus-5", "f6f20bdd-e3c9-40ec-8dc1-76efec8a4d04",
			"uprof_011CesupVavWSZKUj9Phofyt", 1788976924), SigShape{Version: 17, Model: "claude-opus-5",
			AccountRef: "f6f20bdd-e3c9-40ec-8dc1-76efec8a4d04", Profile: "uprof_011CesupVavWSZKUj9Phofyt"},
			1788976924, SigFamilyUnknown},
		{"Vertex 前缀", "claude#abcdef", SigShape{}, 0, SigFamilyVertex},
		{"空签名", "", SigShape{}, 0, SigFamilyUnknown},
		{"非 base64", "!!!not-base64!!!", SigShape{}, 0, SigFamilyUnknown},
		{"随机字节", "3q2+7w==", SigShape{}, 0, SigFamilyUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseSignatureShape(tc.sig)
			if got.Family != tc.wantFam {
				t.Fatalf("family 应为 %s，实际 %s", tc.wantFam, got.Family)
			}
			if got.Version != tc.want.Version || got.Model != tc.want.Model ||
				got.AccountRef != tc.want.AccountRef || got.Profile != tc.want.Profile {
				t.Fatalf("字段解析不符：%+v", got)
			}
			if tc.wantTS == 0 && !got.IssuedAt.IsZero() || tc.wantTS != 0 && got.IssuedAt.Unix() != tc.wantTS {
				t.Fatalf("签发时间应为 %d，实际 %v", tc.wantTS, got.IssuedAt)
			}
			if got.UUIDForged() {
				t.Fatalf("合法 v4 UUID 不应判为编造：%s", got.AccountRef)
			}
		})
	}
}

// TestSigUUIDForged 外形像 UUID、版本位非法的账号标识是编造的（实测样本来自一条签名完整性可疑的渠道）。
func TestSigUUIDForged(t *testing.T) {
	for _, ref := range []string{"a7daefd5-841f-bec3-7e1d-e9147d04d0f6", "660fdce0-3a76-cd11-7d81-35fbc41fbaf9"} {
		if !(SigShape{AccountRef: ref}).UUIDForged() {
			t.Fatalf("%s 版本位非法，应判为编造", ref)
		}
	}
	for _, ref := range []string{"549299f0-23d6-4e73-84be-f1a8558c34d2", "592720655719", ""} {
		if (SigShape{AccountRef: ref}).UUIDForged() {
			t.Fatalf("%q 不应判为编造", ref)
		}
	}
}

// buildSig 按实测字段号拼一个签名：f1=2，f2.f1 内含版本、模型、账号、档案与签发时间。
func buildSig(version int, model, account, profile string, issued int64) string {
	var inner []byte
	inner = protoVarint(inner, 1, uint64(version))
	inner = protoBytes(inner, 6, []byte(model))
	inner = protoBytes(inner, 8, []byte("thinking"))
	if account != "" {
		inner = protoBytes(inner, 11, []byte(account))
	}
	if profile != "" {
		inner = protoBytes(inner, 15, []byte(profile))
	}
	if issued > 0 {
		inner = protoVarint(inner, 21, uint64(issued))
	}
	mid := protoBytes(nil, 1, inner)
	var out []byte
	out = protoVarint(out, 1, 2)
	out = protoBytes(out, 2, mid)
	out = protoVarint(out, 3, 1)
	return base64.StdEncoding.EncodeToString(out)
}

func protoVarint(buf []byte, field int, v uint64) []byte {
	buf = appendUvarint(buf, uint64(field)<<3)
	return appendUvarint(buf, v)
}

func protoBytes(buf []byte, field int, v []byte) []byte {
	buf = appendUvarint(buf, uint64(field)<<3|2)
	buf = appendUvarint(buf, uint64(len(v)))
	return append(buf, v...)
}

func appendUvarint(buf []byte, v uint64) []byte {
	for v >= 0x80 {
		buf = append(buf, byte(v)|0x80)
		v >>= 7
	}
	return append(buf, byte(v))
}

// TestAuditFlagsRewrittenMessageIDs id 含 0 O I l 说明是网关生成的：ping 与审计用同一证据键，只计一次分。
func TestAuditFlagsRewrittenMessageIDs(t *testing.T) {
	up := &fakeUpstream{behaviour: "official", strictParams: true, signatureChecked: true,
		replayID: "msg_011CetJoWHwZlDD3gjO5Hf5a"}
	run := runAgainst(t, up, []string{"ping"})
	res := auditOf(t, run)
	if !hasEvidenceKey(res, "msg_id_rewritten") {
		t.Fatalf("应指出消息 id 被网关重写，实际 %+v", res.Evidence)
	}
	if got := run.Verdict.Scores[ClassWrapper]; got != 2 {
		t.Fatalf("同一件事在 ping 与审计各报一次，只应计 2 分，实际 %d", got)
	}
}

// TestAuditFlagsDuplicateMessageIDs 同一个 id 出现在不同请求里是回放旧响应。
func TestAuditFlagsDuplicateMessageIDs(t *testing.T) {
	up := &fakeUpstream{behaviour: "official", strictParams: true, signatureChecked: true,
		replayID: "msg_011CetJZkDjw9DiCC1x7RKgh"}
	run := runAgainst(t, up, []string{"ping", "max-tokens-strict"})
	res := auditOf(t, run)
	if !hasEvidenceKey(res, "msg_id_duplicate") {
		t.Fatalf("应指出消息 id 重复，实际 %+v", res.Evidence)
	}
	if hasEvidenceKey(res, "msg_id_rewritten") {
		t.Fatal("官方形态的 id 不应判为重写")
	}
}

// TestAuditPassesUniqueOfficialIDs 每条响应一个官方形态 id，不得告警。
func TestAuditPassesUniqueOfficialIDs(t *testing.T) {
	up := &fakeUpstream{behaviour: "official", strictParams: true, signatureChecked: true}
	run := runAgainst(t, up, []string{"ping", "max-tokens-strict", "stream"})
	res := auditOf(t, run)
	for _, key := range []string{"msg_id_rewritten", "msg_id_duplicate"} {
		if hasEvidenceKey(res, key) {
			t.Fatalf("干净渠道不应出现 %s：%+v", key, res.Evidence)
		}
	}
}

// TestAuditFlagsStaleSignature 签名签发时间早于本轮开始，说明 thinking 块是回放的。
func TestAuditFlagsStaleSignature(t *testing.T) {
	stale := buildSig(17, "claude-opus-5", "54a0d54a-e856-4dd3-9019-65a8aabaa28d", "",
		time.Now().Add(-8*time.Hour).Unix())
	run := runAgainst(t, &fakeUpstream{behaviour: "official", signature: stale}, []string{"ping", "thinking-sig"})
	res := auditOf(t, run)
	if !hasEvidenceKey(res, "sig_stale") {
		t.Fatalf("应指出签名签发时间不在本轮内，实际 %+v", res.Evidence)
	}
	if res.AuthCapReason != "" {
		t.Fatalf("签名字段是未公开编码，不应封顶真实性：%s", res.AuthCapReason)
	}
}

// TestAuditRecordsFreshSignatureAccount 本轮新签发的签名不告警，只记录账号标识。
func TestAuditRecordsFreshSignatureAccount(t *testing.T) {
	fresh := buildSig(17, "claude-opus-5", "1548b6db-46e0-4854-94f7-cd5b29b949b1", "", time.Now().Unix())
	run := runAgainst(t, &fakeUpstream{behaviour: "official", signature: fresh}, []string{"ping", "thinking-sig"})
	res := auditOf(t, run)
	for _, key := range []string{"sig_stale", "sig_uuid_forged"} {
		if hasEvidenceKey(res, key) {
			t.Fatalf("新签发的合法签名不应出现 %s：%+v", key, res.Evidence)
		}
	}
	if !hasEvidenceKey(res, "sig_accounts") {
		t.Fatalf("应记录签名账号标识，实际 %+v", res.Evidence)
	}
}

// TestAuditFlagsForgedSignatureUUID 账号标识版本位非法：签名是编造的。
func TestAuditFlagsForgedSignatureUUID(t *testing.T) {
	forged := buildSig(17, "claude-opus-5", "a7daefd5-841f-bec3-7e1d-e9147d04d0f6", "", time.Now().Unix())
	run := runAgainst(t, &fakeUpstream{behaviour: "official", signature: forged}, []string{"ping", "thinking-sig"})
	if !hasEvidenceKey(auditOf(t, run), "sig_uuid_forged") {
		t.Fatal("版本位非法的账号标识应判为编造")
	}
}

// TestAuditFutureSignatureIsDiagnosticOnly 签发时间在未来不可能是回放，只记诊断、不打分。
func TestAuditFutureSignatureIsDiagnosticOnly(t *testing.T) {
	future := buildSig(17, "claude-opus-5", "1548b6db-46e0-4854-94f7-cd5b29b949b1", "",
		time.Now().Add(6*time.Hour).Unix())
	run := runAgainst(t, &fakeUpstream{behaviour: "official", signature: future}, []string{"ping", "thinking-sig"})
	res := auditOf(t, run)
	for _, ev := range res.Evidence {
		if ev.Weight > 0 {
			t.Fatalf("未来的签发时间不应加分：%+v", ev)
		}
	}
	found := false
	for _, a := range res.Assertions {
		if a.Label == "签名签发时间不在未来" {
			found = !a.OK && a.Diagnostic
		}
	}
	if !found {
		t.Fatalf("应留下一条未通过的诊断，实际 %+v", res.Assertions)
	}
}

// TestSigFieldsOnlyReadForVerifiedSchema 字段含义只在 v17 / v18 上实测过，其他版本不读账号与时间。
func TestSigFieldsOnlyReadForVerifiedSchema(t *testing.T) {
	got := ParseSignatureShape(buildSig(16, "claude-opus-5", "9ede73ac-10b1-49f5-b941-00353d7e6eb0", "",
		time.Now().Add(-48*time.Hour).Unix()))
	if got.Version != 16 || got.Model != "claude-opus-5" {
		t.Fatalf("版本号与模型名照常读取，实际 %+v", got)
	}
	if got.AccountRef != "" || !got.IssuedAt.IsZero() {
		t.Fatalf("未验证的 schema 不应读账号与时间，实际 %+v", got)
	}
}

// TestMessageIDFromSSERaw 流式响应只有原文，消息 id 要从 message_start 里取。
func TestMessageIDFromSSERaw(t *testing.T) {
	raw := "event: message_start\n" +
		`data: {"type":"message_start","message":{"model":"claude-opus-5","id":"msg_011CetJZkDjw9DiCC1x7RKgh","type":"message","content":[]}}` +
		"\n\nevent: content_block_start\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_01ABC","name":"x","input":{}}}` + "\n\n"
	if got := messageIDOf(&Exchange{Status: 200, Raw: raw}); got != "msg_011CetJZkDjw9DiCC1x7RKgh" {
		t.Fatalf("应从 message_start 取到消息 id，实际 %q", got)
	}
}

// TestPlatformMsgIDsAreNotRewritten 带平台标记的 id（msg_bdrk_ / msg_vrtx_）不按第一方字母表判。
func TestPlatformMsgIDsAreNotRewritten(t *testing.T) {
	for _, id := range []string{"msg_vrtx_01UDKZG8PWPj9mjajje8d7u7", "msg_bdrk_2fy2vtmqqob7eovjbannognxcdxpmoyalk33jxgye6m4coc7terh"} {
		if msgIDRewritten(id) {
			t.Fatalf("%s 是平台形态，不应判为网关重写", id)
		}
	}
	for _, id := range []string{"msg_011CetJoWHwZlDD3gjO5Hf5a", "msg_63d3678f98664e189dfbc851ec4cf898"} {
		if !msgIDRewritten(id) {
			t.Fatalf("%s 不是官方形态，应判为网关重写", id)
		}
	}
	r := &CheckResult{}
	analyzeMessageID(r, "msg_vrtx_01UDKZG8PWPj9mjajje8d7u7")
	if !hasEvidenceKey(r, "msg_id_vrtx_prefix") || hasEvidenceKey(r, "msg_id_rewritten") {
		t.Fatalf("msg_vrtx_ 应记为 Vertex 前缀，实际 %+v", r.Evidence)
	}
}

// TestInferenceGeoIsNotScored 官方直连同样返回 not_available / global，不得据此加包装分。
func TestInferenceGeoIsNotScored(t *testing.T) {
	for _, geo := range []string{"not_available", "global"} {
		run := runAgainst(t, &fakeUpstream{behaviour: "official", geo: geo}, []string{"ping"})
		if got := run.Verdict.Scores[ClassWrapper]; got != 0 {
			t.Fatalf("inference_geo=%s 不应加包装分，实际 %d（%+v）", geo, got, run.Verdict.Reasons)
		}
	}
}

// TestAuditIsNotSelectable 审计项不发请求，不得出现在勾选目录与成本估算里。
func TestAuditIsNotSelectable(t *testing.T) {
	if _, ok := checkByID(AuditCheckID); ok {
		t.Fatal("审计项不应出现在 Checks 目录里")
	}
	// 混在正常勾选里时应被静默丢弃（单独传它会退回默认集，那是既有的兜底行为）。
	got := ResolveCheckIDs([]string{"ping", AuditCheckID})
	if indexOf(got, AuditCheckID) >= 0 {
		t.Fatalf("审计项不应被解析成可执行项，实际 %v", got)
	}
	if got := TotalRequests([]string{AuditCheckID}); got != 0 {
		t.Fatalf("审计项不应计入请求成本，实际 %d", got)
	}
}
