package modeldetect

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// nonce 生成随机校验串，防止渠道靠缓存或固定回复蒙混过关。
func nonce(prefix string) string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return prefix + "_FALLBACK"
	}
	return prefix + "_" + strings.ToUpper(hex.EncodeToString(b))
}

// bedrockModelRe Bedrock 的模型 id 形态（含跨区推理前缀）。
var bedrockModelRe = regexp.MustCompile(`(?i)^(us|eu|apac|global)?\.?anthropic\.claude`)

// checkModels 拉模型列表。它是最便宜的一次探测：既验证连通性，
// 又能在列表里直接看到 Bedrock 风格的模型 id。
func checkModels(ctx context.Context, c *Client, _ *runState) *CheckResult {
	meta, _ := checkByID("models")
	r := newResult(meta)
	ex := c.Get(ctx, KindModels)
	r.Exchanges = []*Exchange{ex}
	r.DurationMs = ex.DurationMs

	if !ex.OK() {
		r.Status = StatusUnsupported
		r.diagnose("模型列表可读", false, describeFailure(ex))
		return r.finish("目标未提供 /v1/models（不少中转会关掉，单独不作判据）")
	}

	var ids []string
	data := sliceOf(ex.JSON["data"])
	if data == nil {
		data = sliceOf(ex.JSON["models"])
	}
	for _, raw := range data {
		if m := mapOf(raw); m != nil {
			if id := str(m["id"]); id != "" {
				ids = append(ids, id)
			}
			continue
		}
		if s := str(raw); s != "" {
			ids = append(ids, s)
		}
	}

	r.assert("模型列表可读", true, fmt.Sprintf("%d 个模型", len(ids)))
	wanted := c.Target().Model
	hasWanted := false
	var bedrockStyle []string
	for _, id := range ids {
		if strings.EqualFold(id, wanted) {
			hasWanted = true
		}
		if bedrockModelRe.MatchString(id) {
			bedrockStyle = append(bedrockStyle, id)
		}
	}
	r.diagnose("列表包含所测模型", hasWanted || len(ids) == 0, wanted)

	if len(bedrockStyle) > 0 {
		r.addEvidence("bedrock_model_ids", "模型列表出现 Bedrock 形态 id",
			clip(strings.Join(bedrockStyle, ", "), 120), ClassBedrock, 3)
	}
	collectGatewayFingerprint(r, ex)
	return r.finish(fmt.Sprintf("拉到 %d 个模型", len(ids)))
}

// pingBody 最小非流请求：只要一个精确回声，成本极低但足以看清 usage 与响应头。
func pingBody(model string) map[string]any {
	return map[string]any{
		"model":      model,
		"max_tokens": 40,
		"messages": []map[string]any{
			{"role": "user", "content": "Reply with exactly PONG only."},
		},
	}
}

// checkPing 基础请求 + 网关指纹。本项承担最多的分类判据。
func checkPing(ctx context.Context, c *Client, st *runState) *CheckResult {
	meta, _ := checkByID("ping")
	r := newResult(meta)
	ex := c.Post(ctx, KindMessages, pingBody(c.Target().Model))
	r.Exchanges = []*Exchange{ex}
	r.DurationMs = ex.DurationMs

	if !ex.OK() {
		r.assert("基础补全成功", false, describeFailure(ex))
		return r.finish("基础请求失败，后续判定证据有限")
	}
	r.assert("基础补全成功", true, fmt.Sprintf("HTTP %d，耗时 %dms", ex.Status, ex.DurationMs))

	body := ex.JSON
	usage := usageOf(body)
	st.pingUsage = usage
	st.pingInput = intOf(usage["input_tokens"])
	st.pingCacheWrite = cacheCreationTokens(usage)
	st.pingSeen = true

	analyzeMessageID(r, str(body["id"]))
	analyzeModelEcho(r, str(body["model"]), c.Target().Model)
	analyzeUsage(r, usage, st)
	collectGatewayFingerprint(r, ex)

	text := contentText(body)
	r.diagnose("回声内容为 PONG", strings.Contains(strings.ToUpper(text), "PONG"), clip(text, 80))

	return r.finish(fmt.Sprintf("input=%d cache_write=%d cache_read=%d",
		st.pingInput, st.pingCacheWrite, cacheReadTokens(usage)))
}

// analyzeMessageID 消息 id 形态。网关可以改写它，所以单独一条不定罪，
// 但 msg_bdrk_ / req_vrtx_ 这类平台专属前缀几乎没人会主动伪造。
func analyzeMessageID(r *CheckResult, id string) {
	switch {
	case id == "":
		r.diagnose("响应含消息 id", false, "缺失")
		return
	case strings.HasPrefix(id, "msg_bdrk_"):
		r.addEvidence("msg_id_bedrock", "消息 id 为 msg_bdrk_ 前缀", id, ClassBedrock, 5)
	case strings.Contains(id, "req_vrtx_"):
		r.addEvidence("msg_id_vertex", "消息 id 含 req_vrtx_", id, ClassVertex, 5)
	case strings.HasPrefix(id, "msg_") && strings.Contains(id[4:], "-"):
		r.addEvidence("msg_id_uuid", "消息 id 带 UUID 连字符（非官方形态）", id, ClassWrapper, 2)
	case strings.HasPrefix(id, "msg_"):
		r.addEvidence("msg_id_official", "消息 id 为官方形态", id, ClassInfo, 0)
	default:
		r.addEvidence("msg_id_foreign", "消息 id 非 msg_ 前缀", id, ClassWrapper, 3)
	}
	r.diagnose("响应含消息 id", true, id)
}

// analyzeModelEcho 模型回显。回显与请求不一致，说明网关做了模型映射。
func analyzeModelEcho(r *CheckResult, got, want string) {
	if got == "" {
		r.diagnose("响应回显模型名", false, "缺失")
		r.addEvidence("model_echo_missing", "响应未回显模型名", "", ClassWrapper, 1)
		return
	}
	if bedrockModelRe.MatchString(got) {
		r.addEvidence("model_echo_bedrock", "回显模型为 Bedrock 形态 id", got, ClassBedrock, 4)
	}
	same := strings.EqualFold(got, want) || modelMatchesRequested(got, want)
	r.diagnose("模型回显与请求一致", same, fmt.Sprintf("请求 %s，回显 %s", want, got))
	if !same && !bedrockModelRe.MatchString(got) {
		r.addEvidence("model_echo_mismatch", "回显模型与请求不一致",
			fmt.Sprintf("请求 %s，回显 %s", want, got), ClassWrapper, 2)
	}
}

// analyzeUsage 从最小请求的用量反推网关注入。
//
// 一句 "Reply with exactly PONG only." 的官方基线在 10~15 tokens。
// 明显高出的部分只可能来自网关在前面拼了系统提示：
// 拼进 cache 的是 Claude Code 号池的典型做法，没拼进 cache 的更像 IDE 反代。
func analyzeUsage(r *CheckResult, usage map[string]any, st *runState) {
	in := st.pingInput
	cacheWrite := st.pingCacheWrite
	r.diagnose("input_tokens 在裸请求基线内", in > 0 && in < 300, fmt.Sprintf("input_tokens=%d", in))

	if in >= 300 {
		r.addEvidence("huge_input_tokens", "裸请求 input_tokens 异常高（未走缓存的系统提示）",
			fmt.Sprintf("input_tokens=%d", in), ClassKiro, 3)
	} else if in >= 60 {
		r.addEvidence("injected_input_tokens", "裸请求存在明显 system 注入",
			fmt.Sprintf("input_tokens=%d（基线约 10-15）", in), ClassWrapper, 1)
	}

	// 请求里没写 cache_control 却出现缓存写入，只能是网关自己加的
	if cacheWrite >= 1500 {
		r.addEvidence("cc_prompt_cached", "裸请求写入大段缓存（Claude Code 系统提示特征）",
			fmt.Sprintf("cache_creation=%d", cacheWrite), ClassMaxPool, 3)
	} else if cacheWrite > 0 {
		r.addEvidence("gateway_cache_write", "裸请求出现缓存写入（网关注入了 system）",
			fmt.Sprintf("cache_creation=%d", cacheWrite), ClassWrapper, 1)
	}

	if tier := str(usage["service_tier"]); tier != "" {
		r.addEvidence("service_tier", "usage 含 service_tier", tier, ClassInfo, 0)
	}
	switch geo := str(usage["inference_geo"]); geo {
	case "not_available":
		r.addEvidence("geo_placeholder", "inference_geo=not_available（占位注入）", geo, ClassWrapper, 2)
	case "global":
		r.addEvidence("geo_global", "inference_geo=global（可能是网关注入）", geo, ClassWrapper, 1)
	}
}

// collectGatewayFingerprint 从响应头提取平台与网关线索。
//
// 限流头是这里最有价值的一组：unified-5h 系列只出现在 OAuth 订阅额度上，
// 普通 API Key 走的是 requests/tokens 系列，Bedrock 则完全没有而带 x-amzn-*。
func collectGatewayFingerprint(r *CheckResult, ex *Exchange) {
	if ex == nil || len(ex.Headers) == 0 {
		return
	}
	unified, ratelimit, amzn := false, false, false
	for k := range ex.Headers {
		switch {
		case strings.HasPrefix(k, "anthropic-ratelimit-unified"):
			unified = true
			ratelimit = true
		case strings.HasPrefix(k, "anthropic-ratelimit"):
			ratelimit = true
		case strings.HasPrefix(k, "x-amzn-"):
			amzn = true
		}
	}
	if unified {
		r.addEvidence("unified_ratelimit", "响应带 anthropic-ratelimit-unified-5h-*（订阅额度头）",
			"OAuth 订阅（Max/Pro）专属", ClassMaxPool, 4)
	} else if ratelimit {
		r.addEvidence("anthropic_ratelimit", "响应带 anthropic-ratelimit-*（官方链路）", "", ClassOfficial, 3)
	}
	if amzn {
		r.addEvidence("amzn_headers", "响应带 x-amzn-* 头", "", ClassBedrock, 4)
	}
	if v := ex.Headers["x-oneapi-request-id"]; v != "" {
		r.addEvidence("oneapi_gateway", "检测到 OneAPI 网关", "x-oneapi-request-id", ClassInfo, 0)
	}
	if v := ex.Headers["x-new-api-version"]; v != "" {
		r.addEvidence("newapi_gateway", "检测到 new-api 网关", v, ClassInfo, 0)
	}
	if v := ex.Headers["server"]; v != "" {
		r.addEvidence("server_header", "网关 server 头", v, ClassInfo, 0)
	}
}

// checkPingAgain 重复同一请求，看网关注入的系统提示是否命中自己写的缓存。
//
// 这是区分「Claude Code 号池」与「干净 API Key」的关键一步：号池每次都会把
// 同一段 CC 系统提示送上去，第二次必然读缓存；干净直连两次都是 0。
func checkPingAgain(ctx context.Context, c *Client, st *runState) *CheckResult {
	meta, _ := checkByID("ping-again")
	r := newResult(meta)
	if !st.pingSeen {
		r.Status = StatusInconclusive
		return r.finish("基础请求未成功，无法做缓存复现")
	}

	ex := c.Post(ctx, KindMessages, pingBody(c.Target().Model))
	r.Exchanges = []*Exchange{ex}
	r.DurationMs = ex.DurationMs
	if !ex.OK() {
		r.Status = StatusInconclusive
		r.diagnose("第二次基础补全成功", false, describeFailure(ex))
		return r.finish("第二次请求失败")
	}

	usage := usageOf(ex.JSON)
	read := cacheReadTokens(usage)
	write := cacheCreationTokens(usage)
	r.assert("第二次基础补全成功", true, fmt.Sprintf("HTTP %d", ex.Status))
	firstRead := cacheReadTokens(st.pingUsage)
	firstWrite := st.pingCacheWrite
	if !hasCacheUsage(st.pingUsage) || !hasCacheUsage(usage) {
		if !hasCacheUsage(st.pingUsage) && !hasCacheUsage(usage) {
			r.addEvidence("no_injection_cache", "两次请求均未返回缓存读写",
				"符合干净 API Key / Bedrock 直连；无法证明缓存链路", ClassOfficial, 1)
		}
		r.Status = StatusInconclusive
		r.diagnose("两次响应均包含缓存用量", false,
			fmt.Sprintf("首次 read=%d creation=%d，二次 read=%d creation=%d", firstRead, firstWrite, read, write))
		return r.finish("响应未提供完整缓存用量，无法验证官方缓存链路")
	}

	expected := firstRead + firstWrite
	chainStatus, exact := cacheChainOutcome(firstRead, firstWrite, read)
	if chainStatus == StatusInconclusive {
		r.Status = StatusInconclusive
		r.diagnose("缓存读写链路有实际写入或读取", false,
			fmt.Sprintf("首次 read=%d creation=%d，二次 read=%d", firstRead, firstWrite, read))
		return r.finish("缓存字段存在但本轮没有发生缓存读写")
	}
	r.assert("缓存读写链路严格复现", exact,
		fmt.Sprintf("cache_read_1=%d + cache_creation_1=%d = %d，cache_read_2=%d",
			firstRead, firstWrite, expected, read))
	if chainStatus == StatusSuspicious {
		r.Status = StatusSuspicious
		r.addEvidence("cache_chain_mismatch", "下一次缓存读取未严格覆盖上次读写总量",
			fmt.Sprintf("期望 %d，实际 %d", expected, read), ClassWrapper, 2)
		return r.finish(fmt.Sprintf("cache_read_1=%d cache_creation_1=%d cache_read_2=%d（不相等）",
			firstRead, firstWrite, read))
	}

	switch {
	case read >= 1000 && firstWrite >= 1000:
		r.addEvidence("cc_cache_hit", "注入的系统提示稳定命中缓存",
			fmt.Sprintf("首次读写合计 %d，第二次读取 %d", expected, read), ClassMaxPool, 3)
	case read > 0:
		r.addEvidence("gateway_cache_hit", "网关注入内容命中缓存",
			fmt.Sprintf("cache_read=%d", read), ClassWrapper, 1)
	default:
		r.addEvidence("cache_chain_empty", "缓存链路严格一致但没有发生缓存写入",
			"无法据此证明目标使用了官方缓存", ClassInfo, 0)
	}
	return r.finish(fmt.Sprintf("cache_read_1=%d cache_creation_1=%d cache_read_2=%d",
		firstRead, firstWrite, read))
}

func hasCacheUsage(usage map[string]any) bool {
	if usage == nil {
		return false
	}
	if _, ok := usage["cache_read_input_tokens"]; ok {
		return true
	}
	if _, ok := usage["cache_creation_input_tokens"]; ok {
		return true
	}
	creation := mapOf(usage["cache_creation"])
	_, has5m := creation["ephemeral_5m_input_tokens"]
	_, has1h := creation["ephemeral_1h_input_tokens"]
	return has5m || has1h
}

// describeFailure 汇总一次失败请求的可读原因。
func describeFailure(ex *Exchange) string {
	if ex.NetworkError != "" {
		return ex.NetworkError
	}
	if msg := errorMessage(ex.JSON); msg != "" {
		return fmt.Sprintf("HTTP %d: %s", ex.Status, clip(msg, 200))
	}
	return fmt.Sprintf("HTTP %d: %s", ex.Status, clip(ex.Raw, 200))
}
