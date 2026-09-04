package modeldetect

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
)

// checkParamStrict 用四个非法请求测参数校验严格性。
//
// 真正的 Anthropic 后端（含 Bedrock / Vertex）会在网关层就把它们拒掉；
// 逆向实现与 OpenAI 格式转换层普遍不做校验，照单全收。被接受得越多越可疑。
func checkParamStrict(ctx context.Context, c *Client, st *runState) *CheckResult {
	meta, _ := checkByID("param-strict")
	r := newResult(meta)
	model := c.Target().Model

	cases := []struct {
		key     string
		label   string
		body    map[string]any
		hint    func(string) bool
		soft    bool // soft 项不计真实性分，只作诊断
		comment string
	}{
		{
			key:   "thinking_budget_200",
			label: "thinking.budget_tokens=200 被拒",
			body: map[string]any{
				"model": model, "max_tokens": 400,
				"thinking": map[string]any{"type": "enabled", "budget_tokens": 200},
				"messages": []map[string]any{{"role": "user", "content": "Name your product in one sentence."}},
			},
			hint:    func(s string) bool { return minBudgetHintRe.MatchString(s) },
			comment: "官方要求 budget_tokens >= 1024",
		},
		{
			key:   "unknown_field",
			label: "未知顶层字段被拒",
			body: map[string]any{
				"model": model, "max_tokens": 8, "probe_unknown_field": 1,
				"messages": []map[string]any{{"role": "user", "content": "hi"}},
			},
			hint:    func(s string) bool { return extraFieldRejectRe.MatchString(s) },
			comment: "官方对未知字段报 Extra inputs are not permitted",
		},
		{
			key:   "max_tokens_zero",
			label: "max_tokens=0 被拒",
			body: map[string]any{
				"model": model, "max_tokens": 0,
				"messages": []map[string]any{{"role": "user", "content": "hi"}},
			},
			hint:    func(string) bool { return true },
			comment: "max_tokens 必须 >= 1",
		},
		{
			key:   "thinking_temperature",
			label: "thinking 同时设 temperature 被拒",
			body: map[string]any{
				"model": model, "max_tokens": 400, "temperature": 0.5,
				"thinking": st.profile.ThinkingParam("summarized"),
				"messages": []map[string]any{{"role": "user", "content": "hi"}},
			},
			hint:    func(string) bool { return true },
			soft:    true,
			comment: "thinking 开启时 temperature 只能为 1",
		},
	}

	rejected := 0
	accepted := make([]string, 0, len(cases))
	for _, tc := range cases {
		ex := c.Post(ctx, KindMessages, tc.body)
		r.Exchanges = append(r.Exchanges, ex)
		r.DurationMs += ex.DurationMs

		if ex.NetworkError != "" || ex.Status >= 500 {
			r.diagnose(tc.label, false, describeFailure(ex))
			continue
		}
		ok := ex.Status >= 400 && ex.Status < 500
		detail := fmt.Sprintf("HTTP %d", ex.Status)
		if msg := errorMessage(ex.JSON); msg != "" {
			detail += "：" + clip(msg, 160)
		}
		if ok && !tc.hint(ex.ErrorText()) {
			detail += "（报错未命中官方特征，仍按拒绝计）"
		}
		if tc.soft {
			r.diagnose(tc.label, ok, detail+"｜"+tc.comment)
		} else {
			r.assert(tc.label, ok, detail+"｜"+tc.comment)
		}
		if ok {
			if !tc.soft {
				rejected++
			}
			continue
		}
		accepted = append(accepted, tc.key)
		if tc.key == "thinking_budget_200" {
			r.addEvidence("budget_200_accepted", "thinking.budget_tokens=200 被接受",
				"非原样官方 thinking 口", ClassWrapper, 2)
		}
	}

	// 三个硬校验各值 5 分，真实性满分 15；软项不计分
	r.AuthScore = rejected * 5
	if len(accepted) >= 2 {
		r.addEvidence("loose_validation", "多个非法参数被放行",
			strings.Join(accepted, ", "), ClassWrapper, 3)
	}
	if len(accepted) == len(cases) {
		r.Status = StatusSuspicious
		r.AuthCapReason = "全部非法参数都被接受，后端不做官方参数校验"
	}
	return r.finish(fmt.Sprintf("%d/3 个硬校验按官方行为拒绝", rejected))
}

// checkMaxTokens max_tokens 是否被严格执行。
// 参考案例里有渠道请求 10 却生成 800+，那说明 max_tokens 根本没往后端传。
func checkMaxTokens(ctx context.Context, c *Client, _ *runState) *CheckResult {
	meta, _ := checkByID("max-tokens-strict")
	r := newResult(meta)
	const limit = 10
	ex := c.Post(ctx, KindMessages, map[string]any{
		"model": c.Target().Model, "max_tokens": limit,
		"messages": []map[string]any{{"role": "user",
			"content": "连续输出 200 个英文单词 TOKEN，每个之间用一个空格分隔，不要提前结束，也不要输出其他内容。"}},
	})
	r.Exchanges = []*Exchange{ex}
	r.DurationMs = ex.DurationMs

	if !ex.OK() {
		r.assert("请求成功", false, describeFailure(ex))
		return r.finish("请求失败")
	}
	usage := usageOf(ex.JSON)
	out := intOf(usage["output_tokens"])
	stop := str(ex.JSON["stop_reason"])
	within := out > 0 && out <= limit

	r.assert("请求成功", true, fmt.Sprintf("HTTP %d", ex.Status))
	r.assert(fmt.Sprintf("输出不超过 max_tokens=%d", limit), within, fmt.Sprintf("output_tokens=%d", out))
	r.diagnose("因触顶而停止", stop == "max_tokens", "stop_reason="+stop)

	if within {
		r.AuthScore = 10
	} else if out > limit {
		r.AuthCapReason = fmt.Sprintf("max_tokens=%d 被忽略，实际输出 %d tokens", limit, out)
		r.addEvidence("max_tokens_ignored", "max_tokens 未被执行",
			fmt.Sprintf("请求 %d，输出 %d", limit, out), ClassWrapper, 3)
	}
	if within && stop != "max_tokens" {
		r.Status = StatusInconclusive
		return r.finish(fmt.Sprintf("输出未超限但 stop_reason=%s，未形成触顶证据", stop))
	}
	return r.finish(fmt.Sprintf("output_tokens=%d，stop_reason=%s", out, stop))
}

// checkToolUse 强制工具调用。tool_use id 的前缀是区分平台的强指纹：
// toolu_ 是官方形态（可被网关改写，单独不定罪），tooluse_ 是 Bedrock/Kiro 原生，
// tool_N 是 Vertex。同时用随机参数验证模型确实读懂了指令而不是套模板。
func checkToolUse(ctx context.Context, c *Client, _ *runState) *CheckResult {
	meta, _ := checkByID("tool-use")
	r := newResult(meta)
	model := c.Target().Model

	a := rand.Intn(70) + 11
	b := rand.Intn(70) + 11
	orderID := nonce("ORDER")

	cases := []struct {
		name     string
		schema   map[string]any
		prompt   string
		expected string
		verify   func(map[string]any) bool
	}{
		{
			name: "calculate_sum",
			schema: map[string]any{"type": "object",
				"properties": map[string]any{"a": map[string]any{"type": "number"}, "b": map[string]any{"type": "number"}},
				"required":   []string{"a", "b"}, "additionalProperties": false},
			prompt:   fmt.Sprintf("必须调用 calculate_sum 计算 %d 加 %d。", a, b),
			expected: fmt.Sprintf("a=%d, b=%d", a, b),
			verify: func(in map[string]any) bool {
				return intOf(in["a"]) == a && intOf(in["b"]) == b
			},
		},
		{
			name: "lookup_order",
			schema: map[string]any{"type": "object",
				"properties": map[string]any{"order_id": map[string]any{"type": "string"}},
				"required":   []string{"order_id"}, "additionalProperties": false},
			prompt:   fmt.Sprintf("必须调用 lookup_order 查询订单 %s。", orderID),
			expected: "order_id=" + orderID,
			verify:   func(in map[string]any) bool { return str(in["order_id"]) == orderID },
		},
	}

	passed := 0
	for _, tc := range cases {
		ex := c.Post(ctx, KindMessages, map[string]any{
			"model": model, "max_tokens": 512,
			"tools":       []map[string]any{{"name": tc.name, "description": "检测用工具", "input_schema": tc.schema}},
			"tool_choice": map[string]any{"type": "tool", "name": tc.name},
			"messages":    []map[string]any{{"role": "user", "content": tc.prompt}},
		})
		r.Exchanges = append(r.Exchanges, ex)
		r.DurationMs += ex.DurationMs
		if !ex.OK() {
			r.assert(tc.name+" 调用成功", false, describeFailure(ex))
			continue
		}

		var call map[string]any
		for _, raw := range contentBlocks(ex.JSON) {
			blk := mapOf(raw)
			if str(blk["type"]) == "tool_use" && str(blk["name"]) == tc.name {
				call = blk
				break
			}
		}
		if call == nil {
			r.assert(tc.name+" 调用成功", false, "响应中没有 tool_use 块")
			continue
		}
		analyzeToolUseID(r, str(call["id"]))
		input := mapOf(call["input"])
		ok := tc.verify(input) && str(ex.JSON["stop_reason"]) == "tool_use"
		r.assert(tc.name+" 调用参数与停止原因正确", ok,
			fmt.Sprintf("期望 %s，实际 %v，stop_reason=%s", tc.expected, input, str(ex.JSON["stop_reason"])))
		if ok {
			passed++
		}
	}
	if passed == len(cases) {
		r.AuthScore = 10
	}
	return r.finish(fmt.Sprintf("%d/%d 个工具调用完全正确", passed, len(cases)))
}

// analyzeToolUseID 判读 tool_use id 前缀。
func analyzeToolUseID(r *CheckResult, id string) {
	switch {
	case id == "":
		r.addEvidence("tool_id_missing", "tool_use 缺少 id", "", ClassWrapper, 2)
	case strings.HasPrefix(id, "toolu_"):
		r.addEvidence("tool_id_official", "tool_use id 为 toolu_（官方形态，可被网关改写）", id, ClassInfo, 0)
	case strings.HasPrefix(id, "tooluse_"):
		r.addEvidence("tool_id_bedrock", "tool_use id 为 tooluse_（Bedrock / Kiro 原生）", id, ClassBedrock, 4)
	case strings.HasPrefix(id, "tool_"):
		r.addEvidence("tool_id_vertex", "tool_use id 为 tool_N（Vertex 形态）", id, ClassVertex, 4)
	default:
		r.addEvidence("tool_id_foreign", "tool_use id 形态非官方", id, ClassWrapper, 2)
	}
}

// thinkingPrompt 需要多步推理才能答对的题，保证模型真的会产生思考块。
const thinkingPrompt = "请解决这个需要多步推理的问题并给出最终验证：求最小正整数 n，使 n 除以 7 余 3、除以 11 余 5、除以 13 余 9。"

// checkThinkingSignature 取一次带签名的 thinking 块。
// 签名前缀是 Vertex 的指纹；adaptive 模型本轮合法跳过 thinking 时记「证据不足」，不判负。
func checkThinkingSignature(ctx context.Context, c *Client, st *runState) *CheckResult {
	meta, _ := checkByID("thinking-sig")
	r := newResult(meta)
	param := st.profile.ThinkingParam("summarized")
	body := map[string]any{
		"model": c.Target().Model, "max_tokens": 4096, "thinking": param,
		"messages": []map[string]any{{"role": "user", "content": thinkingPrompt}},
	}
	ex := c.Post(ctx, KindMessages, body)
	r.Exchanges = []*Exchange{ex}
	r.DurationMs = ex.DurationMs

	if !ex.OK() {
		r.Status = StatusInconclusive
		r.diagnose("thinking 请求成功", false, describeFailure(ex))
		return r.finish("thinking 请求失败，无法验证签名")
	}

	blocks := contentBlocks(ex.JSON)
	idx, signature := -1, ""
	redacted := false
	for i, raw := range blocks {
		blk := mapOf(raw)
		switch str(blk["type"]) {
		case "thinking":
			if sig := str(blk["signature"]); sig != "" && idx < 0 {
				idx, signature = i, sig
			}
		case "redacted_thinking":
			redacted = true
		}
	}

	if idx < 0 {
		r.Status = StatusInconclusive
		reason := "未返回带签名的 thinking 块"
		if redacted {
			reason = "本轮返回 redacted_thinking（合法保护形态）"
		} else if st.profile.Adaptive {
			reason = "adaptive 本轮合法跳过 thinking"
		}
		r.diagnose("取得可回传的 thinking 签名", false, reason)
		return r.finish(reason)
	}

	st.thinkingContent = blocks
	st.thinkingIndex = idx
	st.thinkingParam = param
	st.thinkingPrompt = thinkingPrompt

	r.assert("thinking 请求成功", true, fmt.Sprintf("HTTP %d", ex.Status))
	r.assert("取得非空 thinking 签名", true, fmt.Sprintf("签名长度 %d（长度仅记录，不作真伪判据）", len(signature)))
	if strings.HasPrefix(signature, "claude#") {
		r.addEvidence("sig_prefix_vertex", "thinking 签名前缀为 claude#（Vertex）", clip(signature, 24), ClassVertex, 5)
	}
	return r.finish(fmt.Sprintf("取得签名，长度 %d", len(signature)))
}

// checkSignatureTamper 签名完整性：真实性评分里权重最高的一项。
//
// 只有真正持有签名密钥的 Anthropic 后端（含 Bedrock / Vertex）才能同时做到
// 「原样回传可继续对话」与「改一个字符必须拒绝」。伪装渠道要么两边都放行，
// 要么根本无法续写。
func checkSignatureTamper(ctx context.Context, c *Client, st *runState) *CheckResult {
	meta, _ := checkByID("sig-tamper")
	r := newResult(meta)
	if len(st.thinkingContent) == 0 {
		r.Status = StatusInconclusive
		return r.finish("上一步未取得可回传签名，跳过完整性验证")
	}

	challenge := nonce("CONT")
	baseMessages := []map[string]any{
		{"role": "user", "content": st.thinkingPrompt},
		{"role": "assistant", "content": st.thinkingContent},
		{"role": "user", "content": "只回复校验串 " + challenge + "。"},
	}

	positive := c.Post(ctx, KindMessages, map[string]any{
		"model": c.Target().Model, "max_tokens": 512,
		"thinking": st.thinkingParam, "messages": baseMessages,
	})
	r.Exchanges = append(r.Exchanges, positive)
	r.DurationMs += positive.DurationMs
	positiveOK := positive.OK() && strings.Contains(contentText(positive.JSON), challenge)
	r.assert("原样回传签名后可继续对话", positiveOK,
		fmt.Sprintf("HTTP %d，回复 %s", positive.Status, clip(contentText(positive.JSON), 60)))

	tampered := tamperSignature(st.thinkingContent, st.thinkingIndex)
	if tampered == nil {
		r.Status = StatusInconclusive
		return r.finish("无法构造篡改样本")
	}
	tamperedMessages := []map[string]any{
		{"role": "user", "content": st.thinkingPrompt},
		{"role": "assistant", "content": tampered},
		{"role": "user", "content": "只回复校验串 " + challenge + "。"},
	}
	negative := c.Post(ctx, KindMessages, map[string]any{
		"model": c.Target().Model, "max_tokens": 512,
		"thinking": st.thinkingParam, "messages": tamperedMessages,
	})
	r.Exchanges = append(r.Exchanges, negative)
	r.DurationMs += negative.DurationMs

	rejected := negative.Status >= 400 && negative.Status < 500 && signatureRejectRe.MatchString(negative.ErrorText())
	looseReject := negative.Status >= 400 && negative.Status < 500
	r.assert("篡改一个字符后被拒绝", rejected,
		fmt.Sprintf("HTTP %d：%s", negative.Status, clip(errorMessage(negative.JSON)+negative.Raw, 160)))

	switch {
	case positiveOK && rejected:
		r.AuthScore = 40
		r.addEvidence("signature_verified", "签名可原样回传且篡改被拒",
			"后端持有 Anthropic 签名密钥", ClassInfo, 0)
		return r.finish("签名完整性验证通过（真实性最强证据）")
	case positiveOK && looseReject:
		r.AuthScore = 30
		r.Status = StatusInconclusive
		return r.finish("篡改被拒但报错未提签名，证据略弱")
	case positiveOK && negative.OK():
		r.Status = StatusSuspicious
		r.AuthCapReason = "篡改后的 thinking 签名被接受，后端未做签名校验"
		r.addEvidence("signature_not_verified", "篡改签名被接受", "后端不校验签名", ClassWrapper, 4)
		return r.finish("原样签名可续写，但篡改签名也被接受，完整性可疑")
	default:
		r.Status = StatusInconclusive
		return r.finish("签名连续性未形成完整证据链")
	}
}

// tamperSignature 深拷贝 content 并改掉指定 thinking 块签名的首字符。
func tamperSignature(blocks []any, idx int) []any {
	if idx < 0 || idx >= len(blocks) {
		return nil
	}
	out := make([]any, 0, len(blocks))
	for i, raw := range blocks {
		blk := mapOf(raw)
		if blk == nil {
			out = append(out, raw)
			continue
		}
		cp := make(map[string]any, len(blk))
		for k, v := range blk {
			cp[k] = v
		}
		if i == idx {
			sig := str(cp["signature"])
			if sig == "" {
				return nil
			}
			first := "A"
			if sig[0] == 'A' {
				first = "B"
			}
			cp["signature"] = first + sig[1:]
		}
		out = append(out, cp)
	}
	return out
}

// checkStream SSE 协议完整性。OpenAI 格式转换层最常在这里露馅：
// 缺 message_start.usage、块索引不闭合、事件名与 data.type 对不上。
func checkStream(ctx context.Context, c *Client, _ *runState) *CheckResult {
	meta, _ := checkByID("stream")
	r := newResult(meta)
	challenge := nonce("SSE")
	ex := c.PostStream(ctx, KindMessages, map[string]any{
		"model": c.Target().Model, "max_tokens": 512, "stream": true,
		"messages": []map[string]any{{"role": "user",
			"content": fmt.Sprintf("请把校验串 %s 连续重复 20 次，每次之间用一个空格分隔，不要输出其他内容。", challenge)}},
	})
	r.Exchanges = []*Exchange{ex}
	r.DurationMs = ex.DurationMs

	if !ex.OK() {
		r.assert("流式请求成功", false, describeFailure(ex))
		return r.finish("流式请求失败")
	}
	r.assert("流式请求成功", true, fmt.Sprintf("HTTP %d", ex.Status))
	r.assert("Content-Type 为 text/event-stream",
		strings.Contains(ex.Headers["content-type"], "event-stream"), ex.Headers["content-type"])

	flow := validateSSE(ex.Events)
	r.assert("SSE 核心状态机完整", flow.valid, flow.detail())

	var text strings.Builder
	for _, ev := range ex.Events {
		delta := mapOf(ev.Data["delta"])
		if str(delta["type"]) == "text_delta" {
			text.WriteString(str(delta["text"]))
		}
	}
	occurrences := strings.Count(text.String(), challenge)
	r.assert("随机校验串通过流式返回", occurrences >= 20, fmt.Sprintf("观察到 %d/20 次", occurrences))
	r.diagnose("message_start 携带 usage", flow.startHasUsage, "OpenAI 转换层常缺此字段")
	r.diagnose("观察到多个网络数据块", ex.ChunkCount >= 2,
		fmt.Sprintf("chunks=%d，首字节 %s", ex.ChunkCount, fmtMsPtr(ex.TTFTMs)))

	if flow.valid && occurrences >= 20 {
		r.AuthScore = 10
	}
	if !flow.startHasUsage && flow.valid {
		r.addEvidence("sse_missing_usage", "message_start 缺少 usage", "疑似格式转换层重组", ClassWrapper, 1)
	}
	return r.finish(fmt.Sprintf("%d 个事件，校验串 %d/20 次", len(ex.Events), occurrences))
}

// sseFlow SSE 状态机校验结果。
type sseFlow struct {
	valid         bool
	startHasUsage bool
	violations    []string
	blocks        int
}

func (f sseFlow) detail() string {
	if len(f.violations) == 0 {
		return fmt.Sprintf("%d 个内容块均闭合", f.blocks)
	}
	return strings.Join(f.violations, "；")
}

// validateSSE 校验事件顺序与块闭合。ping 与未知控制事件按官方兼容规则放过。
func validateSSE(events []SSEEvent) sseFlow {
	f := sseFlow{}
	open := map[int]bool{}
	closed := map[int]bool{}
	starts, stops, deltas := 0, 0, 0
	startPos, stopPos := -1, -1

	for i, ev := range events {
		if ev.Data == nil {
			f.violations = append(f.violations, fmt.Sprintf("第 %d 个事件 data 不是 JSON", i+1))
			continue
		}
		if ev.Event != "" && ev.Type != "" && ev.Event != ev.Type {
			f.violations = append(f.violations, fmt.Sprintf("事件名 %s 与 data.type=%s 不一致", ev.Event, ev.Type))
		}
		switch ev.Type {
		case "message_start":
			starts++
			startPos = i
			if msg := mapOf(ev.Data["message"]); msg != nil && mapOf(msg["usage"]) != nil {
				f.startHasUsage = true
			}
		case "content_block_start":
			idx := intOf(ev.Data["index"])
			if open[idx] {
				f.violations = append(f.violations, fmt.Sprintf("index=%d 重复 start", idx))
			}
			open[idx] = true
		case "content_block_delta":
			idx := intOf(ev.Data["index"])
			if !open[idx] || closed[idx] {
				f.violations = append(f.violations, fmt.Sprintf("index=%d 的 delta 不在开放块内", idx))
			}
		case "content_block_stop":
			idx := intOf(ev.Data["index"])
			if !open[idx] || closed[idx] {
				f.violations = append(f.violations, fmt.Sprintf("index=%d 非法 stop", idx))
			}
			closed[idx] = true
		case "message_delta":
			deltas++
		case "message_stop":
			stops++
			stopPos = i
		case "error":
			f.violations = append(f.violations, "流内出现 error 事件")
		}
	}

	for idx := range open {
		if !closed[idx] {
			f.violations = append(f.violations, fmt.Sprintf("index=%d 未闭合", idx))
		}
	}
	f.blocks = len(open)
	if starts != 1 {
		f.violations = append(f.violations, fmt.Sprintf("message_start 数量=%d", starts))
	}
	if stops != 1 {
		f.violations = append(f.violations, fmt.Sprintf("message_stop 数量=%d", stops))
	}
	if deltas == 0 {
		f.violations = append(f.violations, "缺少 message_delta")
	}
	if startPos >= 0 && stopPos >= 0 && startPos > stopPos {
		f.violations = append(f.violations, "message_start 出现在 message_stop 之后")
	}
	f.valid = len(f.violations) == 0 && f.blocks > 0
	return f
}

// checkCountTokens count_tokens 端点的稳定性、单调性，以及与基础请求 input_tokens 的一致性。
// 后者能证明计数与推理走的是同一个 tokenizer——套壳国产模型通常对不上。
func checkCountTokens(ctx context.Context, c *Client, st *runState) *CheckResult {
	meta, _ := checkByID("count-tokens")
	r := newResult(meta)
	model := c.Target().Model
	shortBody := map[string]any{"model": model,
		"messages": []map[string]any{{"role": "user", "content": "Reply with exactly PONG only."}}}
	longBody := map[string]any{"model": model,
		"messages": []map[string]any{{"role": "user",
			"content": "长文本 " + strings.Repeat("token counting monotonicity probe. ", 200)}}}

	first := c.Post(ctx, KindCountTokens, shortBody)
	repeat := c.Post(ctx, KindCountTokens, shortBody)
	long := c.Post(ctx, KindCountTokens, longBody)
	r.Exchanges = []*Exchange{first, repeat, long}
	r.DurationMs = first.DurationMs + repeat.DurationMs + long.DurationMs

	if !first.OK() {
		r.Status = StatusUnsupported
		r.diagnose("count_tokens 可用", false, describeFailure(first))
		return r.finish("目标未提供 count_tokens（网关常不透传，单独不作判据）")
	}

	a := intOf(first.JSON["input_tokens"])
	b := intOf(repeat.JSON["input_tokens"])
	long1 := intOf(long.JSON["input_tokens"])
	r.assert("count_tokens 可用", true, fmt.Sprintf("短文本=%d", a))
	r.assert("相同输入计数稳定", a > 0 && a == b, fmt.Sprintf("%d vs %d", a, b))
	r.assert("长文本计数严格更大", long1 > a, fmt.Sprintf("短=%d，长=%d", a, long1))

	aligned := true
	if st.pingSeen && st.pingInput > 0 && st.pingCacheWrite == 0 {
		diff := st.pingInput - a
		if diff < 0 {
			diff = -diff
		}
		aligned = diff <= 3
		r.diagnose("与基础请求 input_tokens 对齐", aligned,
			fmt.Sprintf("count=%d，基础请求 input=%d（差 %d）", a, st.pingInput, diff))
		if !aligned && st.pingInput > a {
			r.addEvidence("tokenizer_gap", "推理路径比计数多出 token（system 注入或不同 tokenizer）",
				fmt.Sprintf("count=%d，实际 input=%d", a, st.pingInput), ClassWrapper, 1)
		}
	}
	if a > 0 && a == b && long1 > a {
		r.AuthScore = 5
	}
	return r.finish(fmt.Sprintf("短=%d 重复=%d 长=%d", a, b, long1))
}

// fmtMsPtr 格式化可空毫秒。
func fmtMsPtr(v *int64) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf("%dms", *v)
}
