package modeldetect

import (
	"context"
	"encoding/json"
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
	// injectCache 为 true 时，裸请求也返回大段 cache_creation（号池注入特征）。
	injectCache     bool
	cacheWrite      int
	cacheReadOffset int
	callCount       int

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

// validate 复刻官方网关的参数校验；strictParams=false 的渠道全部放行。
func (up *fakeUpstream) validate(body map[string]any) error {
	if !up.strictParams {
		return nil
	}
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
	// 篡改过的签名必须被拒
	if !up.signatureChecked {
		return nil
	}
	for _, m := range sliceOf(body["messages"]) {
		msg := mapOf(m)
		for _, raw := range sliceOf(msg["content"]) {
			blk := mapOf(raw)
			if str(blk["type"]) == "thinking" && !strings.HasPrefix(str(blk["signature"]), "SIGVALID") {
				return errText("Invalid signature on thinking block")
			}
		}
	}
	return nil
}

func (up *fakeUpstream) reply(w http.ResponseWriter, body map[string]any) {
	id := "msg_01ABCDEFGHIJKLMNOPQRSTUV"
	toolID := "toolu_01ABCDEFGHIJKLMNOP"
	switch up.behaviour {
	case "bedrock":
		id = "msg_bdrk_01ABCDEFGHIJKLMNOPQR"
		toolID = "tooluse_ABCDEFGHIJKLMNOPQR"
	case "wrapper":
		id = "msg_" + "6f1c2b7e-1111-4b0e-9e2a-abcdef123456"
		toolID = "call_abc123"
	}
	// 只贴前缀不给旁证：id 顶着 msg_bdrk_，内核仍是第一方流水号。
	if up.bedrockPrefixOnly {
		id = "msg_bdrk_01ABCDEFGHIJKLMNOPQRSTUV"
	}

	usage := map[string]any{"input_tokens": 13, "output_tokens": 5}
	if up.injectCache {
		up.mu.Lock()
		usage["input_tokens"] = 14
		if up.cacheWrite == 0 {
			up.cacheWrite = 2400
			usage["cache_creation_input_tokens"] = 2400
		} else {
			usage["cache_read_input_tokens"] = 2400 + up.cacheReadOffset
		}
		up.mu.Unlock()
	}
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
		think := map[string]any{
			"type": "thinking", "thinking": "让我算一下……",
			"signature": "SIGVALID_" + strings.Repeat("x", 40),
		}
		switch {
		case up.forgeEmptyThinking:
			// 有签名、无正文：签名是贴上去的
			think = map[string]any{"type": "thinking", "thinking": "",
				"signature": "SIGVALID_" + strings.Repeat("x", 40)}
		case up.emptyThinkingOnce:
			up.mu.Lock()
			up.thinkingCount++
			first := up.thinkingCount == 1
			up.mu.Unlock()
			if first {
				think = map[string]any{"type": "thinking", "thinking": "",
					"signature": "SIGVALID_" + strings.Repeat("x", 40)}
			}
		case up.useRedactedThinking:
			// 官方的加密保护形态，本就没有明文正文，不得误杀
			think = map[string]any{"type": "redacted_thinking", "data": strings.Repeat("z", 40)}
		}
		writeJSON(w, 200, map[string]any{
			"id": id, "type": "message", "role": "assistant", "model": str(body["model"]),
			"stop_reason": "end_turn", "usage": usage,
			"content": []any{
				think,
				map[string]any{"type": "text", "text": "答案是 262。"},
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
	case strings.Contains(prompt, "secret code"):
		return "the code"
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
		t.Fatal("放行非法参数且不校验签名的渠道必须触发真实性封顶")
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

func TestCacheChainRequiresExactReplay(t *testing.T) {
	for _, tc := range []struct {
		name   string
		offset int
		status string
	}{
		{name: "exact", status: StatusPassed},
		{name: "short", offset: -1, status: StatusSuspicious},
		{name: "long", offset: 1, status: StatusSuspicious},
	} {
		t.Run(tc.name, func(t *testing.T) {
			up := &fakeUpstream{behaviour: "official", injectCache: true, cacheReadOffset: tc.offset}
			run := runAgainst(t, up, []string{"ping", "ping-again"})
			got := findCheckResult(run, "ping-again")
			if got == nil || got.Status != tc.status {
				t.Fatalf("缓存链路状态 = %#v，期望 %s", got, tc.status)
			}
		})
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
// 说明这条渠道根本不会思考，签名是凭空贴上去的。
func TestAuditRejectsEmptySignedThinking(t *testing.T) {
	up := &fakeUpstream{behaviour: "official", strictParams: true, signatureChecked: true,
		forgeEmptyThinking: true}
	run := runAgainst(t, up, []string{"ping", "thinking-sig"})

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
	run := runAgainst(t, up, []string{"ping", "thinking-sig", "thinking-gradient"})

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

// TestParseSignatureShape 用两族真实样本验证签名形态归类。
// 样本取自实测：Bedrock 内嵌公开模型 id + UUID，第一方内嵌内部代号。
func TestParseSignatureShape(t *testing.T) {
	const bedrockSig = "CAISuAIKpgEIERgCKkDwdNvB3RB+OxiKEVcsXwLxiSdHsXzQYtXXVyxj+5Aad9T+sxY0WZNF02RnQ0dI/5wXvLg3BCvqxHbkT3Rvx1dAMg1jbGF1ZGUtb3B1cy01OAFCCHRoaW5raW5nWiRkMWRlMTVlNy1jZWViLTRiYWYtOWI3MS1kOGE0MjNiY2IwZGVyEFmXpmOOcoaDAXn4SwAtQuSIAQGoAZil8dQGsAECEgxo1pRa0g58s4HHnw4aDG/l4Lo+E0n4vc+41iIw8aR5OZqyd3cPimrH9rqwVDmtjXLVZVOK7xmeOXb+vtHFfDWJnHoYfDDBDt5PYCJFKj/Lm/iV9VaFArzDVewwZ2Krummb6Ue5zzswWNUAd3JFATZRiDCPQ8FpmXlZA7uUk2zGzvqqljT/E8ysswbGWaAYAQ=="
	const firstPartySig = "EsIDCmIIDxABGAIqQDVYDjprY+zhcKXcGeGZt0T0bOtXnZ2BF+aUwREzpA8DLPqKgVDL2gJmd0v0UugRMd9cJq8VldT9YVUhAJnwNeEyDGNsYXVkZS1ob25leTgAQgh0aGlua2luZxIMISnSIxBxeBzdeQ2GGgxGorabR0lT738SvwciMMMdiNS3vDBwx+L1lme4DQRdlo0nrNlE1ODQXgGPfXGI7kMT+mDYRci6CGndngnZXCqNAkghB1kJU5HBaiSGnKP8Vh+1O3pWnhE3I1cj3NE7uCJRkg3C+teTs//u8d2MFPHn2bjQHtrbAfSAC4bEsh1BjIvmisYyhdkh4QdtjTHcKDjhaFOvtWuG2bvNUGFBWwuqFsf9Yq9NBhdehchdFAVX5OBvZCBd4eS2Qm8d1+2Zx6w4T5nrHUczJpAJpVGLjpjPRYFeYY5nlumI2ScLyUH3EpZWVD8yLwRZVgpRXp8/Wajuri8Hw6pYJ+f+AJt1WfMp16PKR/Wskb1e1RhMRq9Ld7siIwun1T8v3jZwZxvykn2RTM+rE7QhMGuuaN5TFGL9axd1Z16mPrVh9TQSo9d6jw+ADoyKmgIbDo+yTi2rGAE="

	cases := []struct {
		name       string
		sig        string
		wantFamily string
		wantLabel  string
	}{
		{"Bedrock 形态", bedrockSig, SigFamilyBedrock, "claude-opus-5"},
		{"第一方形态", firstPartySig, SigFamilyFirstParty, "claude-honey"},
		{"Vertex 前缀", "claude#abcdef", SigFamilyVertex, ""},
		{"空签名", "", SigFamilyUnknown, ""},
		{"非 base64", "!!!not-base64!!!", SigFamilyUnknown, ""},
		{"随机字节", "3q2+7w==", SigFamilyUnknown, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseSignatureShape(tc.sig)
			if got.Family != tc.wantFamily {
				t.Fatalf("family 应为 %s，实际 %s（key=%q ref=%q）",
					tc.wantFamily, got.Family, got.KeyLabel, got.RequestRef)
			}
			if tc.wantLabel != "" && got.KeyLabel != tc.wantLabel {
				t.Fatalf("KeyLabel 应为 %q，实际 %q", tc.wantLabel, got.KeyLabel)
			}
		})
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
