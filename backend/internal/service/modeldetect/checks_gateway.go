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

// analyzeMessageID 消息 id 形态。网关可以改写它，所以单独一条不定罪。
//
// msg_bdrk_ 只值 2 分（够不到分类阈值）：贴一个前缀字符串的成本太低，实测有渠道
// 顶着它却在模型回显、篡改报错两处全是第一方形态。真 Bedrock 由
// auditBedrockCorroboration 凭独立旁证补足 3 分。msg_vrtx_ 同理只值 2 分，真 Vertex
// 另有 claude# 签名前缀与 tool_N 工具 id 可以补足。req_vrtx_ 保持 5 分——它没出现过被
// 冒用的情况。其余 msg_ 开头的 id 按官方字母表判断是否被网关重新生成，与跨项审计用
// 同一个证据键，同一件事只计一次分。
func analyzeMessageID(r *CheckResult, id string) {
	switch {
	case id == "":
		r.diagnose("响应含消息 id", false, "缺失")
		return
	case strings.HasPrefix(id, "msg_bdrk_"):
		r.addEvidence("msg_id_bedrock", "消息 id 为 msg_bdrk_ 前缀", id, ClassBedrock, 2)
	case strings.Contains(id, "req_vrtx_"):
		r.addEvidence("msg_id_vertex", "消息 id 含 req_vrtx_", id, ClassVertex, 5)
	case strings.HasPrefix(id, "msg_vrtx_"):
		r.addEvidence("msg_id_vrtx_prefix", "消息 id 为 msg_vrtx_ 前缀", id, ClassVertex, 2)
	case msgIDRewritten(id):
		r.addEvidence("msg_id_rewritten", "消息 id 不是官方形态（网关重新组装了响应）",
			id+"（官方为 msg_01 + base58，不含 0 O I l）", ClassWrapper, 2)
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
	same := sameModel(got, want)
	r.diagnose("模型回显与请求一致", same, fmt.Sprintf("请求 %s，回显 %s", want, got))
	// 平台形态的 id 已由 sameModel 归一；只有读不出版本号的 Bedrock 回显才只记平台证据。
	if _, parsed := parseModelVersion(got); !same && (parsed || !bedrockModelRe.MatchString(got)) {
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
	// inference_geo 只记录不打分：官方直连同样会返回 not_available 与 global
	// （2026-09 实测 3 条官方直连渠道），据此给包装分会误伤干净渠道。
	if geo := str(usage["inference_geo"]); geo != "" {
		r.addEvidence("inference_geo", "usage 含 inference_geo", geo, ClassInfo, 0)
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

// cacheProbeTurns 缓存链的请求次数。
//
// 三次是能看出「链有没有逐级推进」的最小值：第一次只知道有没有写入，第二次
// 才知道有没有命中，第三次才知道新写入的那段有没有被后续请求接上。
const cacheProbeTurns = 3

// cacheProbeSegment 缓存探针的填充句（约 19 token）。按型号最小可缓存长度重复。
const cacheProbeSegment = "This is a stable prompt cache probe segment that must stay byte identical. "

// cacheProbeSegments 生成三次请求各自「新增」的那一段内容。
//
// 关键：三次请求的前缀必须严格增长（prefix₁ ⊂ prefix₂ ⊂ prefix₃），不能发三次
// 一模一样的体。发一样的前缀时第 2、3 次完全等价 —— 只要第一次写过缓存，后面
// 两次就都是「纯命中、零写入」，这个结果既看不出网关有没有继续把新内容写进去，
// 也看不出缓存链有没有真的推进，等于只测了一次。逐级增长之后，「本次写入」就
// 必须等于「下次读取相对上次读取的增量」，这才是可证伪的链式判据。
//
// 每段用各自的 nonce：首轮必然冷写，且不会被上一轮检测留下的缓存顶掉。
// 长度按型号最小可缓存阈值放大 1.5 倍——短于阈值官方会静默不缓存。
func cacheProbeSegments(st *runState) []string {
	base := st.profile.MinCacheTokens*3/2/12 + 20
	segs := make([]string, 0, cacheProbeTurns)
	for i := 0; i < cacheProbeTurns; i++ {
		repeat := base
		if i > 0 {
			// 后续增量不必和首段一样长，但必须明显超过噪声，否则「写没写」看不出来。
			repeat = base/2 + 8
		}
		segs = append(segs, nonce(fmt.Sprintf("CCACH%d", i+1))+" "+strings.Repeat(cacheProbeSegment, repeat))
	}
	return segs
}

// cacheProbeBody 拼第 turn 次（1 基）请求的体：system 为前 turn 段，cache_control 落在末段。
//
// 断点始终压在最后一段上，于是可缓存前缀逐次加长：第 2 次请求读第 1 次写的量、
// 再写第 2 段；第 3 次请求读前两段之和、再写第 3 段。「本次写入」因此永远非零。
//
// 必须显式打 cache_control。裸请求（不带 cache_control）官方根本不写缓存，
// 拿它比两次只会得到 "0 == 0" —— 既证明不了官方缓存，也证明不了网关没做缓存。
func cacheProbeBody(model string, segments []string, turn int) map[string]any {
	blocks := make([]map[string]any, 0, turn)
	for i, seg := range segments[:turn] {
		block := map[string]any{"type": "text", "text": seg}
		if i == turn-1 {
			block["cache_control"] = map[string]any{"type": "ephemeral"}
		}
		blocks = append(blocks, block)
	}
	return map[string]any{
		"model": model, "max_tokens": 32,
		"system":   blocks,
		"messages": []map[string]any{{"role": "user", "content": "只回复 CACHE_OK。"}},
	}
}

// checkPingAgain 官方缓存链路：前缀逐级增长地发 3 次请求，看缓存链有没有真的推进。
//
// 判据是链式不变式：readₙ₊₁ = readₙ + writeₙ，且每一步 writeₙ 都必须大于 0。
// 前半条保证「上次写进去的确实被下次读到」，后半条保证「每次都在把新内容写进去」。
// 只要求前半条是不够的：第 2、3 次都不写入时 read₃ = read₂ 也满足等式，那等于
// 三次请求测的是同一件事。
//
// 判据只回答「这条链路有没有逐级推进的官方缓存链」，不回答「后端是不是真
// Claude」：缓存命中同样能用网关自建的缓存伪造，真伪由签名等密钥级证据负责。
func checkPingAgain(ctx context.Context, c *Client, st *runState) *CheckResult {
	meta, _ := checkByID("ping-again")
	r := newResult(meta)
	if !st.pingSeen {
		r.Status = StatusInconclusive
		return r.finish("基础请求未成功，无法做缓存复现")
	}

	segments := cacheProbeSegments(st)

	var chain cacheChain
	for turn := 1; turn <= cacheProbeTurns; turn++ {
		ex := c.Post(ctx, KindMessages, cacheProbeBody(c.Target().Model, segments, turn))
		r.Exchanges = append(r.Exchanges, ex)
		r.DurationMs += ex.DurationMs
		if !ex.OK() {
			r.Status = StatusInconclusive
			r.diagnose(fmt.Sprintf("第 %d 次缓存探针请求成功", turn), false, describeFailure(ex))
			return r.finish(fmt.Sprintf("第 %d 次缓存探针请求失败，无法判定缓存链", turn))
		}
		chain.record(turn, usageOf(ex.JSON))
	}
	r.assert("3 次缓存探针请求均成功", true, fmt.Sprintf("HTTP %d", r.Exchanges[0].Status))
	r.diagnose("3 次响应均含缓存用量字段",
		hasCacheUsage(chain.usage1) && hasCacheUsage(chain.usage2) && hasCacheUsage(chain.usage3),
		"缺字段时读到的 0 无法与「真的没缓存」区分")
	chain.detail = fmt.Sprintf("write_1=%d read_1=%d read_2=%d write_2=%d read_3=%d write_3=%d",
		chain.write1, chain.read1, chain.read2, chain.write2, chain.read3, chain.write3)

	outcome, reason := chain.outcome()
	// 缓存语义的观察一律只作诊断断言：可用性由上面那条断言负责，
	// 「不稳定」是结果可疑（suspicious），不是协议失败（failed）。
	r.diagnose("缓存链逐级推进（readₙ₊₁ = readₙ + writeₙ 且每步都有写入）",
		outcome == cacheChainPassed, chain.detail)

	switch outcome {
	case cacheChainUnavailable:
		r.Status = StatusInconclusive
		r.addEvidence("cache_probe_unavailable", "带 cache_control 的请求未返回任何缓存用量",
			"3 次均无 cache_read/cache_creation；无法据此证明目标使用了官方缓存", ClassInfo, 0)
		return r.finish("本轮未观察到缓存读写：" + chain.detail)

	case cacheChainPassed:
		r.Status = StatusPassed
		// 这里只给 1 分弱正面证据。链子干净说明这条链路支持前缀缓存且没有改写
		// 请求，但它证明不了后端身份 —— 缓存内容是我们自己送上去的前缀，不是
		// 网关注入的系统提示，所以不能像旧版那样据此加号池分。
		// 1 分够不到 official 的 4 分门槛，必须靠限流头等旁证补足。
		r.addEvidence("cache_chain_clean", "缓存链逐级推进：每步都命中上次写入并写入了新段",
			chain.detail, ClassOfficial, 1)
		return r.finish("缓存链逐级推进：" + chain.detail)
	}

	// 剩余结局都是「链没有按官方语义推进」，按观察到的形态给不同证据键，便于报告直读。
	r.Status = StatusSuspicious
	key, label := "cache_chain_unstable", "缓存链没有逐级推进"
	switch outcome {
	case cacheChainPreHit:
		key, label = "cache_probe_prehit", "首轮就命中缓存（前缀是每轮新 nonce，本不该命中）"
	case cacheChainNeverHits:
		key, label = "cache_never_hits", "写了缓存但下一次完全不命中（前缀被改写或缓存被剥离）"
	case cacheChainDrift:
		key, label = "cache_prefix_drift", "上次写入的内容没有被下次完整读到（前缀逐轮变化）"
	case cacheChainMismatch:
		key, label = "cache_chain_mismatch", "读取量与已有缓存不符（缓存内容与本次前缀不一致）"
	case cacheChainNotExtended:
		key, label = "cache_not_extended", "后续请求没有把新增内容写进缓存（前缀没有增长）"
	}
	r.addEvidence(key, label, reason+"｜"+chain.detail, ClassWrapper, 2)
	return r.finish("缓存链没有逐级推进（" + reason + "）：" + chain.detail)
}

// cacheChain 缓存链上三次请求的读写用量。
// write = cache_creation_input_tokens，read = cache_read_input_tokens。
type cacheChain struct {
	usage1, usage2, usage3 map[string]any
	write1, read1          int
	write2, read2          int
	write3, read3          int
	detail                 string
}

// record 记下一次请求的缓存用量，index 从 1 开始。
func (ch *cacheChain) record(index int, usage map[string]any) {
	write := cacheCreationTokens(usage)
	read := cacheReadTokens(usage)
	switch index {
	case 1:
		ch.usage1, ch.write1, ch.read1 = usage, write, read
	case 2:
		ch.usage2, ch.write2, ch.read2 = usage, write, read
	case 3:
		ch.usage3, ch.write3, ch.read3 = usage, write, read
	}
}

// 缓存链的结局。每种「没推进」的形态单独一类，报告据此给出可直读的证据键。
const (
	cacheChainPassed      = "passed"       // 逐步读上次写的、并写入新段
	cacheChainPreHit      = "pre_hit"      // 首轮（新 nonce）就命中，链路在复用别人的缓存
	cacheChainUnavailable = "unavailable"  // 3 次都没返回缓存用量，本轮无证据
	cacheChainNeverHits   = "never_hits"   // 写进去了，但下一次完全不命中
	cacheChainDrift       = "drift"        // 上次写的没被下次完整读到（前缀在变）
	cacheChainMismatch    = "mismatch"     // 读取量与已有缓存不符（起点就不对）
	cacheChainNotExtended = "not_extended" // 后续请求没有把新增内容写进缓存
)

// outcome 按 3 次请求的缓存用量给出结局与可读原因。
//
// 判据是链式不变式：readₙ₊₁ = readₙ + writeₙ，并且每一步 writeₙ > 0。
//
// 两个条件缺一不可。只查不变式的话，「第 2、3 次都零写入」也会通过（read₃ = read₂
// 天然成立），而那说明三次请求测的是同一件事 —— 网关根本没把新增内容写进去，
// 前缀没有增长。反过来只查「有没有写入」也不行：写入量对不上就说明每次拼进去的
// 内容不同，那条链根本没有被复用。
func (ch cacheChain) outcome() (string, string) {
	if ch.read1 > 0 {
		return cacheChainPreHit, fmt.Sprintf("首轮 read=%d 且前缀为每轮新 nonce", ch.read1)
	}
	if ch.write1 == 0 && ch.read2 == 0 && ch.read3 == 0 {
		return cacheChainUnavailable, "3 次请求均未返回 cache_read/cache_creation"
	}
	if ch.write1 == 0 {
		return cacheChainMismatch, fmt.Sprintf("首轮前缀是全新内容却没写入，而第二次读到 %d", ch.read2)
	}
	if ch.read2 == 0 {
		return cacheChainNeverHits, fmt.Sprintf("首次写入 %d，第二次完全没有命中", ch.write1)
	}
	if ch.read2 != ch.read1+ch.write1 {
		return cacheChainDrift, fmt.Sprintf("第二次读取 %d ≠ 首次读写合计 %d（上次写的没被完整读到）",
			ch.read2, ch.read1+ch.write1)
	}
	if ch.write2 == 0 {
		return cacheChainNotExtended, fmt.Sprintf("第二次请求没有写入（读取停在 %d，前缀没有增长）", ch.read2)
	}
	if ch.read3 != ch.read2+ch.write2 {
		return cacheChainDrift, fmt.Sprintf("第三次读取 %d ≠ 第二次读写合计 %d（上次写的没被完整读到）",
			ch.read3, ch.read2+ch.write2)
	}
	if ch.write3 == 0 {
		return cacheChainNotExtended, fmt.Sprintf("第三次请求没有写入（读取停在 %d，前缀没有增长）", ch.read3)
	}
	return cacheChainPassed, ""
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
