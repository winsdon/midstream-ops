package modeldetect

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
)

// checkParamStrict 用四个请求测参数校验严格性。
//
// 真正的 Anthropic 后端（含 Bedrock / Vertex）会拒掉非法参数；逆向实现与 OpenAI 格式转换层
// 普遍照单全收。但中间隔着网关时，「被接受」多半是网关先把请求规范化了（Go 网关丢掉未知字段、
// 把 max_tokens=0 当成未设再补默认值、改写 thinking 配置），真后端根本没见到非法参数——实测
// 有渠道 budget_tokens=200 被接受却思考了 249 token，签名完整性照样通过。所以本项只说明
// 「这条链路改写了请求」，记包装分，不封顶真实性。
//
// max_tokens=0 不是非法参数：官方把它当缓存预热，返回空 content、stop_reason=max_tokens、
// 不生成输出。有输出才说明网关把它换成了别的值。
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
		// official 非空时由它判断是否符合官方行为；为空时 4xx 即符合。
		official func(ex *Exchange) bool
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
			comment: "官方要求 budget_tokens >= 1024，4.7 起的型号干脆不接受 type=enabled",
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
			label: "max_tokens=0 不生成输出",
			body: map[string]any{
				"model": model, "max_tokens": 0,
				"messages": []map[string]any{{"role": "user", "content": "hi"}},
			},
			hint:     func(string) bool { return true },
			comment:  "官方把 max_tokens=0 当缓存预热：空 content、零输出；有输出说明网关换掉了 max_tokens",
			official: noOutputPrewarm,
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
		rejectedByAPI := ex.Status >= 400 && ex.Status < 500
		ok := rejectedByAPI
		if tc.official != nil {
			ok = rejectedByAPI || tc.official(ex)
		}
		detail := fmt.Sprintf("HTTP %d", ex.Status)
		if msg := errorMessage(ex.JSON); msg != "" {
			detail += "：" + clip(msg, 160)
		} else if ex.OK() {
			detail += fmt.Sprintf("，output_tokens=%d", intOf(usageOf(ex.JSON)["output_tokens"]))
		}
		if rejectedByAPI && !tc.hint(ex.ErrorText()) {
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
	}

	// 三个硬校验各值 5 分，真实性满分 15；软项不计分。
	// 放行两项以上记一条包装证据；只放行 budget 一项时单独记——同一次观察不重复计分。
	r.AuthScore = rejected * 5
	switch {
	case len(accepted) >= 2:
		r.addEvidence("loose_validation", "多个非法参数被放行",
			strings.Join(accepted, ", ")+"（网关规范化了请求或后端未校验）", ClassWrapper, 3)
	case len(accepted) == 1 && accepted[0] == "thinking_budget_200":
		r.addEvidence("budget_200_accepted", "thinking.budget_tokens=200 被接受",
			"非原样官方 thinking 口", ClassWrapper, 2)
	}
	if len(accepted) == len(cases) {
		r.Status = StatusSuspicious
	}
	return r.finish(fmt.Sprintf("%d/3 个硬校验符合官方行为", rejected))
}

// noOutputPrewarm max_tokens=0 的官方行为：200、带完整 usage、不生成任何输出（缓存预热，content 为空）。
// 只看有没有生成，不苛求 content 是空数组：重组响应的网关常补一个空 text 块。
func noOutputPrewarm(ex *Exchange) bool {
	usage := usageOf(ex.JSON)
	_, hasOutput := usage["output_tokens"]
	return ex.OK() && hasOutput && intOf(usage["output_tokens"]) == 0 && contentText(ex.JSON) == ""
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
		body := map[string]any{
			"model": model, "max_tokens": 512,
			"tools":       []map[string]any{{"name": tc.name, "description": "检测用工具", "input_schema": tc.schema}},
			"tool_choice": map[string]any{"type": "tool", "name": tc.name},
			"messages":    []map[string]any{{"role": "user", "content": tc.prompt}},
		}
		ex := c.Post(ctx, KindMessages, body)
		r.Exchanges = append(r.Exchanges, ex)
		r.DurationMs += ex.DurationMs
		// Opus 5.5、Fable 5.1 起官方不支持强制工具调用（tool_choice any / tool 返回 400）。
		// 这是型号行为不是渠道问题：改用 auto 重发一次，提示词本就要求调用这个工具。
		if ex.Status == 400 && forcedToolChoiceRe.MatchString(ex.ErrorText()) {
			r.diagnose(tc.name+" 支持强制工具调用", false, "该型号不支持 tool_choice any / tool（官方行为），改用 auto 重发")
			body["tool_choice"] = map[string]any{"type": "auto"}
			ex = c.Post(ctx, KindMessages, body)
			r.Exchanges = append(r.Exchanges, ex)
			r.DurationMs += ex.DurationMs
		}
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
	st.thinkingUsage = thinkingUsageOf(usageOf(ex.JSON))

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
//
// 正负样本用各自的校验串：请求体不同，按内容缓存的网关就没法拿正样本的回复顶替负样本；
// 真顶替了，回复里是正样本的校验串、签名也与正样本相同，一眼能认出来。
func checkSignatureTamper(ctx context.Context, c *Client, st *runState) *CheckResult {
	meta, _ := checkByID("sig-tamper")
	r := newResult(meta)
	if len(st.thinkingContent) == 0 {
		r.Status = StatusInconclusive
		return r.finish("上一步未取得可回传签名，跳过完整性验证")
	}

	challenge := nonce("CONT")
	positive := c.Post(ctx, KindMessages, map[string]any{
		"model": c.Target().Model, "max_tokens": 512,
		"thinking": st.thinkingParam, "messages": tamperMessages(st, st.thinkingContent, challenge),
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
	tamperChallenge := nonce("CONT")
	negative := c.Post(ctx, KindMessages, map[string]any{
		"model": c.Target().Model, "max_tokens": 512,
		"thinking": st.thinkingParam, "messages": tamperMessages(st, tampered, tamperChallenge),
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
		return judgeTamperAccepted(r, st, positive, negative, challenge, tamperChallenge)
	default:
		r.Status = StatusInconclusive
		return r.finish("签名连续性未形成完整证据链")
	}
}

// tamperMessages 签名回传的三轮对话：原题、带签名的 assistant 回复、要求只回校验串。
func tamperMessages(st *runState, assistant []any, challenge string) []map[string]any {
	return []map[string]any{
		{"role": "user", "content": st.thinkingPrompt},
		{"role": "assistant", "content": assistant},
		{"role": "user", "content": "只回复校验串 " + challenge + "。"},
	}
}

// tamperInputTolerance 正负样本输入总量的允许误差：两者只差校验串里的 12 位随机十六进制。
const tamperInputTolerance = 16

// minArrivalThoughts 取签名那次的思考少于这么多 token，就分不清回传续写多出来的输入是 thinking
// 还是新那句提问（十几二十个 token），计量上确认不了 thinking 进了上下文。
const minArrivalThoughts = 100

// judgeTamperAccepted 篡改后的签名拿到了 200：先认清这个 200 从哪来，再决定要不要定罪。
//
// 顺序有讲究：
//  1. 看回复里的校验串。回的是正样本的校验串 → 网关回放了缓存的响应；两个都没有 → 无从判断。
//  2. 回复是新生成的，签名却和原样回传那次（或回传的原签名）相同 → 签名与内容不绑定，是伪造
//     或贴上去的旧签名。这条不看 usage：偷懒的假后端每条回复都贴同一个签名，usage 也未必可信。
//  3. usage 缺失或不可能（续写的输入不比首轮大）→ 记包装证据，无法确认篡改块到达后端。
//  4. 负样本输入比正样本少一截 → 网关剥掉了校验不过的 thinking 块再重发。
//  5. 正样本的输入里得有回传的 thinking：Opus 4.5 与 4.6 起的型号把历史 thinking 留在上下文、
//     按输入计费；网关一律剥掉、号池路由到别的型号（其他型号会静默丢掉）时，正负样本的输入一样少，
//     「两次一致」证明不了篡改块到过后端。Sonnet 4.5、Haiku 4.5 及更早的型号 API 自己就剥掉，计量上
//     无从确认，一律不封顶。
//
// 实测 23 个「篡改被接受」里有 2 个是回放、12 个输入骤降（剥离后重发）——这些后端都没见过篡改的签名。
func judgeTamperAccepted(r *CheckResult, st *runState, positive, negative *Exchange, challenge, tamperChallenge string) *CheckResult {
	reply := contentText(negative.JSON)
	fresh := strings.Contains(reply, tamperChallenge)
	if !fresh {
		r.Status = StatusInconclusive
		if strings.Contains(reply, challenge) {
			r.diagnose("篡改请求拿到的是新生成的回复", false,
				"回复里是原样回传那次的校验串：网关回放了缓存的响应，篡改请求没有到达后端")
			r.addEvidence("sig_replayed", "回放缓存的响应", "签名完整性：篡改请求拿到的是原样回传那次的回复", ClassWrapper, 3)
			return r.finish("篡改请求被缓存的响应顶替，签名校验没有到达后端")
		}
		r.diagnose("篡改请求拿到的是新生成的回复", false, "回复里没有本次的校验串："+clip(reply, 60))
		return r.finish("篡改签名被接受，但回复没有照做，无法判断")
	}

	known := append(exchangeSignatures(positive), str(mapOf(st.thinkingContent[st.thinkingIndex])["signature"]))
	if sharesSignature(known, exchangeSignatures(negative)) {
		r.Status = StatusSuspicious
		r.AuthCapReason = "篡改请求拿到新生成的回复，签名却是之前出现过的：签名与内容不绑定，是伪造或贴上去的"
		r.addEvidence("sig_unbound", "签名与回复内容不绑定（伪造或贴旧签名）",
			"篡改请求的回复照做了新校验串，thinking 签名却与之前的响应相同", ClassWrapper, 4)
		return r.finish("篡改签名被接受，且新回复带着旧签名：签名是伪造或贴上去的")
	}

	posIn, _, posOK := inputTotal(usageOf(positive.JSON))
	negIn, _, negOK := inputTotal(usageOf(negative.JSON))
	first := st.thinkingUsage.input
	switch {
	case !posOK || !negOK:
		r.Status = StatusInconclusive
		r.diagnose("篡改块原样到达后端", false, "usage 缺 input_tokens，无法确认篡改后的 thinking 块到达了后端")
		return r.finish("篡改签名被接受，但 usage 缺输入用量，无法确认它到达了后端")
	case posIn <= first:
		r.Status = StatusInconclusive
		detail := fmt.Sprintf("回传续写的输入 %d 不大于取签名那次的 %d（续写包含首轮全文，不可能更少）", posIn, first)
		r.diagnose("篡改块原样到达后端", false, detail)
		r.addEvidence("usage_implausible", "usage 不可能成立", detail, ClassWrapper, 2)
		return r.finish("篡改签名被接受，但 usage 不可信，无法确认它到达了后端")
	case posIn-negIn > tamperInputTolerance:
		r.Status = StatusInconclusive
		detail := fmt.Sprintf("篡改请求输入 %d，比原样回传的 %d 少 %d", negIn, posIn, posIn-negIn)
		r.diagnose("篡改块原样到达后端", false, detail+"：网关剥掉了校验不过的 thinking 块")
		r.addEvidence("thinking_stripped", "网关剥掉了回传的 thinking 块", detail+"，签名校验没有到达后端", ClassWrapper, 2)
		return r.finish("网关剥掉了篡改过的 thinking 块，签名校验没有到达后端")
	case negIn-posIn > tamperInputTolerance:
		r.Status = StatusInconclusive
		r.diagnose("篡改块原样到达后端", false, fmt.Sprintf("篡改请求输入 %d，比原样回传的 %d 多 %d：计量对不上", negIn, posIn, negIn-posIn))
		return r.finish("篡改签名被接受，但两次输入对不上，无法确认它到达了后端")
	}

	arrived, detail := thinkingArrived(st, posIn)
	r.diagnose("回传的 thinking 进了上下文", arrived, detail)
	if !arrived {
		r.Status = StatusInconclusive
		if st.profile.KeepsThinking {
			r.addEvidence("thinking_stripped", "网关剥掉了回传的 thinking 块",
				detail+"：回传的 thinking 没进上下文（被网关剥掉，或路由到了别的型号）", ClassWrapper, 2)
		}
		return r.finish("篡改签名被接受，但回传的 thinking 没有进上下文，签名校验没有到达后端")
	}

	r.diagnose("篡改块原样到达后端", true, fmt.Sprintf("原样回传输入 %d，篡改请求输入 %d", posIn, negIn))
	r.Status = StatusSuspicious
	r.AuthCapReason = "篡改后的 thinking 签名原样到达后端却被接受，后端未做签名校验"
	r.addEvidence("signature_not_verified", "篡改签名被接受", "篡改块原样到达后端且未被拒绝", ClassWrapper, 4)
	return r.finish("原样签名可续写，但篡改签名也被接受，完整性可疑")
}

// thinkingArrived 原样回传那次的输入里有没有回传的 thinking。
//
// 续写的输入 = 首轮输入 + 回传的 assistant 轮 + 新的一问。thinking 留在上下文时，回传的 assistant
// 轮按首轮的全部输出（思考 + 正文）计费；被剥掉或丢掉时只剩正文。拆得出 thinking_tokens 就看扣掉
// 正文后剩下的是否盖得住一半思考；拆不出就看是否不少于首轮全部输出。
func thinkingArrived(st *runState, posIn int) (bool, string) {
	if !st.profile.KeepsThinking {
		return false, "该型号不把历史轮次的 thinking 放进上下文（Sonnet 4.5、Haiku 4.5 及更早），计量上无从确认篡改块到过后端"
	}
	u := st.thinkingUsage
	extra := posIn - u.input
	if !u.split {
		return extra >= u.output, fmt.Sprintf("原样回传的输入比首轮多 %d，首轮输出 %d（渠道没拆分 thinking_tokens）", extra, u.output)
	}
	if u.thoughts < minArrivalThoughts {
		return false, fmt.Sprintf("取签名那次只思考了 %d token，计量上分不清它有没有进上下文", u.thoughts)
	}
	carried := extra - (u.output - u.thoughts)
	return carried*2 >= u.thoughts, fmt.Sprintf("原样回传的输入比首轮多 %d，扣掉首轮正文 %d 还剩 %d，首轮思考 %d",
		extra, u.output-u.thoughts, carried, u.thoughts)
}

// sharesSignature 两次响应里有没有同一份签名。
func sharesSignature(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

// tamperSignature 深拷贝 content 并篡改指定 thinking 块的签名（结构保持有效，见 tamperSig）。
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
			cp["signature"] = tamperSig(sig)
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
