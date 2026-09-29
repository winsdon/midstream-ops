package modeldetect

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// AuditCheckID 跨项审计的结果 id。它不在 Checks 目录里——审计不发请求，
// 也就不该出现在勾选面板与成本估算里。
const AuditCheckID = "cross-audit"

// AuditCheckTitle 审计项标题。
const AuditCheckTitle = "跨请求一致性审计"

var (
	// bedrockErrRe Bedrock 转发层特有的错误报文形态。
	bedrockErrRe = regexp.MustCompile(`(?i)(invokemodel|bedrock runtime|operation error bedrock)`)
	// firstPartyErrRe 第一方 Messages API 的签名报错形态。
	firstPartyErrRe = regexp.MustCompile("(?i)invalid `?signature`? in `?thinking`? block")
	// bedrockMsgIDRe Bedrock 的消息 id 形态：msg_bdrk_ 后跟一长串 base32。
	bedrockMsgIDRe = regexp.MustCompile(`^msg_bdrk_[a-z0-9]{40,}$`)
	// firstPartyMsgCoreRe 第一方消息 id 内核：01 开头的流水号（只看形态，不管字母表）。
	firstPartyMsgCoreRe = regexp.MustCompile(`^01[A-Za-z0-9]{20,}$`)
	// officialMsgIDRe 第一方消息 id：msg_01 + base58（不含 0 O I l）。
	officialMsgIDRe = regexp.MustCompile(`^msg_01[1-9A-HJ-NP-Za-km-z]{20,}$`)
	// platformMsgIDRe 带平台标记的消息 id（msg_bdrk_、msg_vrtx_ 等），形态由各平台自己的证据负责。
	platformMsgIDRe = regexp.MustCompile(`^msg_[a-z]+_`)
	// messageIDRe 响应原文里的消息 id。流式响应只有原文，id 在 message_start 里。
	messageIDRe = regexp.MustCompile(`"id"\s*:\s*"(msg_[A-Za-z0-9_-]+)"`)
)

// sigClockTolerance 签名签发时间与本机时钟的容差。只防时钟小偏差——
// 实测回放渠道的签名比请求早了数小时到数天。
const sigClockTolerance = 30 * time.Minute

// AuditRun 跨检测项一致性审计。
//
// 它不发任何请求，只回读已完成检测项留下的响应。存在的理由是单个 CheckFunc 拿不到
// 别的检测项的响应，而「第一轮就报缓存命中」「同一份签名出现两次」这类判据
// 必须跨请求比对才成立。
//
// 三类基于公开协议的判据（缓存门槛、usage 守恒、thinking 自洽）会设 AuthCapReason
// 直接封顶真实性评分；消息 id、签名复用、签名来源与平台形态只加分类分，其中签名字段是
// 未公开编码，解析不出来只记诊断。startedAt 是本轮检测的开始时间，签名签发时间以它为界。
//
// 没有任何可读响应时返回 nil，调用方直接跳过。
func AuditRun(checks []*CheckResult, profile ThinkingProfile, startedAt time.Time) *CheckResult {
	bodies := auditBodies(checks)
	if len(bodies) == 0 {
		return nil
	}

	r := &CheckResult{ID: AuditCheckID, Title: AuditCheckTitle, Group: GroupProtocol}
	auditCacheFloor(r, bodies, profile)
	auditUsageConservation(r, bodies)
	auditThinkingCoherence(r, bodies)
	auditMessageIDs(r, checks)
	auditSignatureReuse(r, checks)
	auditSignatureProvenance(r, bodies, startedAt, time.Now())
	auditPlatformShape(r, checks, bodies)
	auditBedrockCorroboration(r, checks)

	return r.finish(auditSummary(r))
}

// auditable 该检测项的响应是否参与审计。只检测不计分的项（如「是否 0 注入」）不参与，
// 否则它们的响应会经由审计间接影响分类与真实性评分。
func auditable(c *CheckResult) bool {
	return c != nil && c.ID != AuditCheckID && !c.Informational
}

// auditBedrockCorroboration Bedrock 旁证补分。
//
// msg_bdrk_ 前缀本身只值 2 分（见 analyzeMessageID），够不到分类阈值——网关贴一个
// 前缀字符串的成本太低。真 Bedrock 必然还留下别的痕迹：x-amzn-* 响应头、
// anthropic.claude 形态的模型回显、tooluse_ 工具 id、Bedrock 形态的错误报文。
// 任一旁证同向就补足 3 分，与降权前的判定行为等价。签名内部认不出平台，不作旁证。
func auditBedrockCorroboration(r *CheckResult, checks []*CheckResult) {
	if !hasEvidence(checks, "msg_id_bedrock") {
		return
	}

	var found []string
	if hasEvidence(checks, "amzn_headers") {
		found = append(found, "x-amzn-* 响应头")
	}
	if hasEvidence(checks, "model_echo_bedrock") {
		found = append(found, "Bedrock 形态模型回显")
	}
	if hasEvidence(checks, "tool_id_bedrock") {
		found = append(found, "tooluse_ 工具 id")
	}
	if tamperErrFamily(checks) == SigFamilyBedrock {
		found = append(found, "Bedrock 形态篡改报错")
	}

	if len(found) == 0 {
		r.diagnose("msg_bdrk_ 前缀有独立旁证", false,
			"只有消息 id 前缀像 Bedrock，无响应头 / 模型回显 / 工具 id / 报错佐证")
		r.addEvidence("bedrock_prefix_only", "仅凭消息 id 前缀声称 Bedrock",
			"缺少任何独立旁证，前缀可能是网关贴上的", ClassWrapper, 2)
		return
	}
	detail := strings.Join(found, "、")
	r.diagnose("msg_bdrk_ 前缀有独立旁证", true, detail)
	r.addEvidence("bedrock_corroborated", "Bedrock 判定获得独立旁证", detail, ClassBedrock, 3)
}

// auditBody 一条参与审计的成功响应。
type auditBody struct {
	checkID string
	body    map[string]any
	request map[string]any
}

// auditBodies 收集所有成功响应的消息体。
//
// ex.JSON 只在进程内存在（不入库），重试路径下从 previous 恢复的结果只剩 Raw，
// 因此这里回退到解析 Raw。Raw 截断到 8000 字符会让长响应解析失败，跳过即可——
// 审计判据不依赖某一条特定响应。
func auditBodies(checks []*CheckResult) []auditBody {
	var out []auditBody
	for _, c := range checks {
		if !auditable(c) {
			continue
		}
		for _, ex := range c.Exchanges {
			if ex == nil || !ex.OK() {
				continue
			}
			body := ex.JSON
			if body == nil {
				body = parseJSONObject(ex.Raw)
			}
			if body == nil || str(body["type"]) == "error" {
				continue
			}
			req, _ := ex.RequestBody.(map[string]any)
			out = append(out, auditBody{checkID: c.ID, body: body, request: req})
		}
	}
	return out
}

// auditCacheFloor 缓存门槛：低于模型最小可缓存前缀却报缓存命中，物理上不可能。
//
// 判据来自 AWS 与 Anthropic 一致的公开约定：cache checkpoint 有最小 token 数
// （Opus 5 = 512），且总输入 = input + cache_read + cache_creation。短于门槛的前缀
// 官方静默不缓存，绝不会返回非零 cache_read。认不出型号时不判：门槛按型号而定，
// 拿默认值去比会把真缓存判成「不可能」。
func auditCacheFloor(r *CheckResult, bodies []auditBody, profile ThinkingProfile) {
	floor := profile.MinCacheTokens
	if floor <= 0 || !profile.Recognized {
		return
	}
	worst := -1
	worstTotal := 0
	worstRead := 0
	var worstCheck string
	for _, b := range bodies {
		usage := usageOf(b.body)
		if usage == nil {
			continue
		}
		read := cacheReadTokens(usage)
		if read <= 0 {
			continue
		}
		total := intOf(usage["input_tokens"]) + read + cacheCreationTokens(usage)
		if total >= floor {
			continue
		}
		if worst < 0 || total < worstTotal {
			worst, worstTotal, worstRead, worstCheck = total, total, read, b.checkID
		}
	}
	if worst < 0 {
		r.diagnose("缓存命中均满足最小可缓存长度", true,
			fmt.Sprintf("模型最小可缓存前缀 %d tokens", floor))
		return
	}

	detail := fmt.Sprintf("总输入 %d tokens（input+cache_read+cache_creation）低于 %s 的最小可缓存前缀 %d，却报 cache_read=%d｜来自 %s",
		worstTotal, profile.Family, floor, worstRead, worstCheck)
	r.assert("缓存命中满足最小可缓存长度", false, detail)
	r.AuthCapReason = "响应在低于最小可缓存长度的输入上报告缓存命中，usage 为伪造"
	r.addEvidence("usage_impossible_cache", "缓存命中低于最小可缓存长度（不可能）",
		detail, ClassWrapper, 4)
}

// auditUsageConservation usage 守恒：有输出必有输入。
//
// input_tokens=0 而 output_tokens>0 不合法。若同时 cache_read>0，说明网关把输入
// 整个记进了 cache_read（字段错位），这是伪造 usage 的典型形态；否则只是网关没回填
// 输入用量，算不上造假。usage 里干脆没有 input_tokens 也按没回填算——官方每条响应都带它。
func auditUsageConservation(r *CheckResult, bodies []auditBody) {
	var badCheck string
	var badRead int
	missing := 0
	for _, b := range bodies {
		usage := usageOf(b.body)
		if usage == nil {
			continue
		}
		if _, ok := usage["input_tokens"]; !ok {
			missing++
			continue
		}
		if intOf(usage["input_tokens"]) != 0 || intOf(usage["output_tokens"]) <= 0 {
			continue
		}
		if read := cacheReadTokens(usage); read > 0 {
			if badCheck == "" {
				badCheck, badRead = b.checkID, read
			}
			continue
		}
		missing++
	}

	switch {
	case badCheck != "":
		detail := fmt.Sprintf("input_tokens=0 但 output_tokens>0 且 cache_read=%d，输入被整体记入缓存读取｜来自 %s",
			badRead, badCheck)
		r.assert("usage 输入输出守恒", false, detail)
		r.AuthCapReason = "usage 字段错位：输入用量被整体伪装成缓存读取"
		r.addEvidence("usage_zero_input", "input_tokens=0 却有输出与缓存读取", detail, ClassWrapper, 4)
	case missing > 0:
		r.diagnose("usage 输入输出守恒", false,
			fmt.Sprintf("%d 条响应没回填输入用量（缺 input_tokens，或为 0 却有输出）", missing))
		r.addEvidence("usage_input_missing", "响应未回填 input_tokens",
			fmt.Sprintf("%d 条响应缺输入用量", missing), ClassWrapper, 1)
	default:
		r.diagnose("usage 输入输出守恒", true, "")
	}
}

// auditThinkingCoherence thinking 块自洽。
//
// 空正文 + 非空签名单独不是造假：adaptive 模式下模型可以给出「已签名但无摘要正文」的
// 思考块，实测真 Bedrock 渠道与第一方渠道都会这样返回。因此只在整轮检测里从未出现过
// 任何思考正文时才定罪——那意味着这条渠道根本不会思考，签名是凭空贴上去的。
//
// 只看显式要了摘要（display: summarized）的请求：Opus 4.7 起、Opus 5 / 5.5、Sonnet 5 与 Fable 的
// display 默认是 omitted，不要摘要时 thinking 块本就是空的。整轮没有这种请求（例如只勾了
// ping）就不判，否则真渠道会被封顶（历史 #60 即如此）。
//
// redacted_thinking 是官方的加密保护形态，本就没有明文正文，全程排除。
func auditThinkingCoherence(r *CheckResult, bodies []auditBody) {
	signed := 0
	empty := 0
	withText := 0
	var firstEmpty string
	for _, b := range bodies {
		if !summaryRequested(b.request) {
			continue
		}
		for _, raw := range contentBlocks(b.body) {
			blk := mapOf(raw)
			if str(blk["type"]) != "thinking" {
				continue
			}
			if str(blk["signature"]) == "" {
				continue
			}
			signed++
			if strings.TrimSpace(str(blk["thinking"])) == "" {
				empty++
				if firstEmpty == "" {
					firstEmpty = b.checkID
				}
				continue
			}
			withText++
		}
	}
	if signed == 0 {
		return
	}
	if withText > 0 {
		r.diagnose("带签名的 thinking 块存在思考正文", true,
			fmt.Sprintf("要了摘要的 %d 个签名块中 %d 个有正文（空正文是 adaptive 的合法形态）", signed, withText))
		return
	}
	if signed < minCoherenceSamples {
		r.diagnose("带签名的 thinking 块存在思考正文", false,
			fmt.Sprintf("要了摘要的签名块只有 %d 个且为空，样本太少，不据此定罪", signed))
		return
	}

	detail := fmt.Sprintf("要了摘要的 %d 个带签名 thinking 块全部无正文，整轮检测未见任何思考内容｜首见于 %s",
		empty, firstEmpty)
	r.assert("整轮检测出现过思考正文", false, detail)
	r.AuthCapReason = "thinking 块全程带签名却无任何思考正文，签名与内容不匹配"
	r.addEvidence("thinking_empty_signed", "带签名的 thinking 块全程无正文", detail, ClassWrapper, 4)
}

// minCoherenceSamples 「整轮从未出现思考正文」至少要在这么多个要了摘要的签名块上观察到才定罪。
const minCoherenceSamples = 2

// summaryRequested 请求显式要了 thinking 摘要。
func summaryRequested(req map[string]any) bool {
	return str(mapOf(req["thinking"])["display"]) == "summarized"
}

// messageIDOf 取一次成功响应的消息 id。流式与重试恢复的结果没有 JSON，回退到原文里找。
func messageIDOf(ex *Exchange) string {
	if ex.JSON != nil {
		if id := str(ex.JSON["id"]); id != "" {
			return id
		}
	}
	if m := messageIDRe.FindStringSubmatch(ex.Raw); m != nil {
		return m[1]
	}
	return ""
}

// msgIDRewritten 第一方形态的消息 id 是否出自网关之手。
//
// 第一方 id 是 msg_01 + base58，不含 0 O I l：2026-09 实测 3 条官方直连渠道共 75 个 id
// 无一例外，而随机串 20 位里一个都不出现的概率只有约 1/4。带平台标记的 id（msg_bdrk_、
// msg_vrtx_ 等）由各平台自己的证据负责，这里不管。
func msgIDRewritten(id string) bool {
	if !strings.HasPrefix(id, "msg_") || platformMsgIDRe.MatchString(id) || strings.Contains(id, "req_vrtx_") {
		return false
	}
	return !officialMsgIDRe.MatchString(id)
}

// vertexMsgID 消息 id 带 Vertex 标记：msg_vrtx_ 前缀，或内嵌 req_vrtx_ 请求号。
func vertexMsgID(id string) bool {
	return strings.HasPrefix(id, "msg_vrtx_") || strings.Contains(id, "req_vrtx_")
}

// auditMessageIDs 消息 id 的字母表与唯一性。
//
// id 不是官方形态，说明响应被网关重新组装过——这不等于后端是假的：实测有渠道签名完整性
// 通过、id 却是网关生成的。同一个 id 出现在两次不同的请求里，说明网关在回放旧响应，
// 官方后端不会给两次请求同一个 id。
func auditMessageIDs(r *CheckResult, checks []*CheckResult) {
	seen := map[string][]string{}
	var ids []string
	rewritten := 0
	var sample string
	for _, c := range checks {
		if !auditable(c) {
			continue
		}
		for _, ex := range c.Exchanges {
			if ex == nil || !ex.OK() || ex.Kind != KindMessages {
				continue
			}
			id := messageIDOf(ex)
			if id == "" {
				continue
			}
			if _, dup := seen[id]; !dup {
				ids = append(ids, id)
			}
			seen[id] = append(seen[id], c.ID)
			if msgIDRewritten(id) {
				rewritten++
				if sample == "" {
					sample = id
				}
			}
		}
	}
	total := 0
	for _, where := range seen {
		total += len(where)
	}
	if total == 0 {
		return
	}

	if rewritten > 0 {
		detail := fmt.Sprintf("%d/%d 条响应的 id 不是 msg_01 + base58（如 %s）", rewritten, total, sample)
		r.diagnose("消息 id 均为官方形态", false, detail)
		r.addEvidence("msg_id_rewritten", "消息 id 不是官方形态（网关重新组装了响应）", detail, ClassWrapper, 2)
	} else {
		r.diagnose("消息 id 均为官方形态", true, fmt.Sprintf("%d 条响应", total))
	}

	var dups []string
	for _, id := range ids {
		if where := seen[id]; len(where) > 1 {
			dups = append(dups, fmt.Sprintf("%s 出现 %d 次（%s）", id, len(where), tallyLabels(where)))
		}
	}
	if len(dups) == 0 {
		r.diagnose("消息 id 互不重复", true, fmt.Sprintf("%d 条响应", total))
		return
	}
	detail := strings.Join(dups, "；")
	r.assert("消息 id 互不重复", false, detail)
	r.addEvidence("msg_id_duplicate", "同一消息 id 出现在不同请求里（回放旧响应）", detail, ClassWrapper, 3)
}

// tallyLabels 按出现顺序合并重复标签：[a a b] → "a×2、b"。
func tallyLabels(labels []string) string {
	counts := map[string]int{}
	var order []string
	for _, l := range labels {
		if counts[l] == 0 {
			order = append(order, l)
		}
		counts[l]++
	}
	parts := make([]string, 0, len(order))
	for _, l := range order {
		if counts[l] > 1 {
			parts = append(parts, fmt.Sprintf("%s×%d", l, counts[l]))
			continue
		}
		parts = append(parts, l)
	}
	return strings.Join(parts, "、")
}

// auditSignatureReuse 同一份 thinking 签名出现在不同请求的响应里：网关在回放缓存的响应。
//
// 签名内含逐次随机的 nonce 与数据密钥：2026-09 回扫 687 个签名，同一请求体发多次的 38 组里
// 34 组签名全不同，重复只出现在三条回放渠道上。回放时网关每次重写消息 id、延迟也与真实生成
// 相当，按 id 去重与看耗时都认不出；签名它造不出来，只能原样复用。回放说明渠道不纯、按次计费
// 却没有生成，不说明后端是假的——被回放的内容本来就出自真后端，所以只加分类分、不封顶真实性。
func auditSignatureReuse(r *CheckResult, checks []*CheckResult) {
	where := map[string][]string{}
	var order []string
	total := 0
	for _, c := range checks {
		if !auditable(c) {
			continue
		}
		for _, ex := range c.Exchanges {
			if ex == nil || ex.Kind != KindMessages {
				continue
			}
			for _, sig := range exchangeSignatures(ex) {
				if _, seen := where[sig]; !seen {
					order = append(order, sig)
				}
				where[sig] = append(where[sig], c.ID)
				total++
			}
		}
	}
	if total == 0 {
		return
	}

	var dups []string
	for _, sig := range order {
		if w := where[sig]; len(w) > 1 {
			dups = append(dups, fmt.Sprintf("%s 出现 %d 次（%s）", clip(sig, 16), len(w), tallyLabels(w)))
		}
	}
	if len(dups) == 0 {
		r.diagnose("thinking 签名互不重复（未见回放）", true, fmt.Sprintf("%d 个签名", total))
		return
	}
	detail := strings.Join(dups, "；")
	r.assert("thinking 签名互不重复（未见回放）", false, detail)
	r.addEvidence("sig_replayed", "回放缓存的响应", "同一份 thinking 签名出现在不同请求里（回放或贴用旧签名）："+detail, ClassWrapper, 3)
}

// auditSignatureProvenance 签名来源：账号标识、签发时间、标识合法性。
//
// 签名里的账号标识同一账号跨请求不变：整轮只有一个就是单账号，多个就是号池——只记录，
// 不打分。签发时间早于本轮开始，说明 thinking 块不是为这次请求生成的，网关在回放缓存的
// 响应；账号标识外形像 UUID 而版本位非法，是签名被凭空编造。后两条依赖未公开字段，
// 所以只加分类分、不封顶真实性。签发时间在未来不可能是回放，只能是解析错位或编造，
// 只记诊断、不打分。签名头里读不出这些字段时（2026-09-28 起有渠道的签名头只剩版本号）
// 记一条未通过的诊断，免得报告对「这项其实没核对」只字不提。
func auditSignatureProvenance(r *CheckResult, bodies []auditBody, startedAt, now time.Time) {
	accounts := map[string]bool{}
	signed, oauth := 0, 0
	unread := map[int]int{} // 读不出来源字段的签名，按 schema 版本计数
	var stale, future, forged []string
	for _, b := range bodies {
		for _, raw := range contentBlocks(b.body) {
			blk := mapOf(raw)
			if str(blk["type"]) != "thinking" || str(blk["signature"]) == "" {
				continue
			}
			sh := ParseSignatureShape(str(blk["signature"]))
			if sh.AccountRef == "" && sh.IssuedAt.IsZero() {
				if sh.Family != SigFamilyVertex {
					unread[sh.Version]++
				}
				continue
			}
			signed++
			if sh.AccountRef != "" {
				accounts[sh.AccountRef] = true
			}
			if sh.Profile != "" {
				oauth++
			}
			if sh.UUIDForged() {
				forged = append(forged, fmt.Sprintf("%s：%s", b.checkID, sh.AccountRef))
			}
			if sh.IssuedAt.IsZero() || startedAt.IsZero() {
				continue
			}
			switch {
			case sh.IssuedAt.Before(startedAt.Add(-sigClockTolerance)):
				stale = append(stale, fmt.Sprintf("%s 早 %s", b.checkID, humanSpan(startedAt.Sub(sh.IssuedAt))))
			case sh.IssuedAt.After(now.Add(sigClockTolerance)):
				future = append(future, fmt.Sprintf("%s 晚 %s", b.checkID, humanSpan(sh.IssuedAt.Sub(now))))
			}
		}
	}
	if signed == 0 {
		if len(unread) > 0 {
			r.diagnose("签名来源字段可读", false, fmt.Sprintf(
				"%s 的签名头里没有账号标识与签发时间：号池、回放时间与同货源无法从签名核对（签名格式可能已变）",
				describeVersions(unread)))
		}
		return
	}

	pool := "单账号"
	if len(accounts) > 1 {
		pool = fmt.Sprintf("号池（至少 %d 个账号）", len(accounts))
	}
	detail := fmt.Sprintf("%d 个签名，%d 个账号标识：%s", signed, len(accounts), pool)
	if oauth > 0 {
		detail += fmt.Sprintf("；%d 个签名带 uprof_（OAuth 订阅号）", oauth)
	}
	if len(unread) > 0 {
		detail += fmt.Sprintf("；另有 %s 读不出来源字段", describeVersions(unread))
	}
	r.diagnose("签名账号标识", true, detail)
	r.addEvidence("sig_accounts", fmt.Sprintf("thinking 签名来自 %d 个账号", len(accounts)), detail, ClassInfo, 0)

	if len(stale) > 0 {
		d := fmt.Sprintf("签发时间比本轮开始（%s，本机时钟）早：%s", startedAt.Format(time.DateTime), strings.Join(stale, "；"))
		r.assert("thinking 签名为本轮新签发", false, d)
		r.addEvidence("sig_stale", "thinking 签名早于本轮签发（回放缓存的响应）", d, ClassWrapper, 4)
	}
	if len(future) > 0 {
		r.diagnose("签名签发时间不在未来", false,
			"签发时间比当前晚（字段解析错位或签名为编造，不据此打分）："+strings.Join(future, "；"))
	}
	if len(forged) > 0 {
		d := strings.Join(forged, "；")
		r.assert("签名账号标识是合法 UUID", false, d)
		r.addEvidence("sig_uuid_forged", "签名账号标识的 UUID 版本位非法（签名为编造）", d, ClassWrapper, 3)
	}
}

// humanSpan 把时长写成「x.x 天 / x.x 小时 / x 分钟」。
func humanSpan(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%.1f 天", d.Hours()/24)
	case d >= time.Hour:
		return fmt.Sprintf("%.1f 小时", d.Hours())
	}
	return fmt.Sprintf("%d 分钟", int(d.Minutes()))
}

// describeVersions 把各 schema 版本的签名个数写成「v17×41、v18×4」，读不出版本号的记为「未知格式」。
func describeVersions(counts map[int]int) string {
	versions := make([]int, 0, len(counts))
	for v := range counts {
		versions = append(versions, v)
	}
	sort.Ints(versions)
	parts := make([]string, 0, len(versions))
	for _, v := range versions {
		name := fmt.Sprintf("v%d", v)
		if v == 0 {
			name = "未知格式"
		}
		parts = append(parts, fmt.Sprintf("%s×%d", name, counts[v]))
	}
	return strings.Join(parts, "、")
}

// auditPlatformShape 平台形态交叉一致性。
//
// 三个正交维度各自归一出一个平台族：消息 id 内核、模型回显、篡改报错格式。真渠道必然
// 同向；只有 msg_bdrk_ 这一个字符串像 Bedrock、其余全是第一方形态的，说明前缀是贴上去的。
// 签名内部认不出平台（见平台族常量的注释），不参与比对。
func auditPlatformShape(r *CheckResult, checks []*CheckResult, bodies []auditBody) {
	shapes := map[string]string{}
	record := func(dim, family string) {
		if family == "" || family == SigFamilyUnknown {
			return
		}
		if _, seen := shapes[dim]; seen {
			return
		}
		shapes[dim] = family
	}

	for _, b := range bodies {
		if id := str(b.body["id"]); id != "" {
			record("消息 id", msgIDFamily(id))
		}
		if m := str(b.body["model"]); m != "" {
			record("模型回显", modelEchoFamily(m))
		}
	}
	record("篡改报错", tamperErrFamily(checks))

	if len(shapes) < 2 {
		return
	}
	families := map[string][]string{}
	for dim, fam := range shapes {
		families[fam] = append(families[fam], dim)
	}
	if len(families) < 2 {
		r.diagnose("平台形态跨维度一致", true, describeShapes(shapes))
		return
	}

	detail := describeShapes(shapes)
	r.diagnose("平台形态跨维度一致", false, detail)
	r.addEvidence("platform_shape_conflict", "平台形态在不同维度上互相矛盾", detail, ClassWrapper, 1)
}

// msgIDFamily 从消息 id 归一平台族。
//
// Bedrock 的 id 是 msg_bdrk_ 加一长串 base32；第一方是 01 开头的 base58 流水号。
// 顶着 msg_bdrk_ 前缀却是第一方内核的，按第一方算——正是这个组合暴露了贴前缀行为。
func msgIDFamily(id string) string {
	switch {
	case bedrockMsgIDRe.MatchString(id):
		return SigFamilyBedrock
	case vertexMsgID(id):
		return SigFamilyVertex
	case strings.HasPrefix(id, "msg_bdrk_"):
		if firstPartyMsgCoreRe.MatchString(strings.TrimPrefix(id, "msg_bdrk_")) {
			return SigFamilyFirstParty
		}
	case strings.HasPrefix(id, "msg_"):
		if firstPartyMsgCoreRe.MatchString(strings.TrimPrefix(id, "msg_")) {
			return SigFamilyFirstParty
		}
	}
	return SigFamilyUnknown
}

// modelEchoFamily 从模型回显归一平台族。Bedrock 回显带 anthropic.claude 形态的 id。
func modelEchoFamily(model string) string {
	if bedrockModelRe.MatchString(model) {
		return SigFamilyBedrock
	}
	if modelIDRe.MatchString(model) {
		return SigFamilyFirstParty
	}
	return SigFamilyUnknown
}

// tamperErrFamily 从签名篡改的拒绝报文归一平台族。
//
// 这个维度不依赖任何未公开编码：Bedrock 转发层报 InvokeModel 错误，
// 第一方报 Invalid signature in thinking block。
func tamperErrFamily(checks []*CheckResult) string {
	for _, c := range checks {
		if c == nil || c.ID != "sig-tamper" {
			continue
		}
		for _, ex := range c.Exchanges {
			if ex == nil || ex.OK() {
				continue
			}
			text := ex.Raw + " " + ex.NetworkError
			switch {
			case bedrockErrRe.MatchString(text):
				return SigFamilyBedrock
			case firstPartyErrRe.MatchString(text):
				return SigFamilyFirstParty
			}
		}
	}
	return SigFamilyUnknown
}

// describeShapes 把各维度的归一结果拼成可读说明。
func describeShapes(shapes map[string]string) string {
	order := []string{"消息 id", "模型回显", "篡改报错"}
	parts := make([]string, 0, len(shapes))
	for _, dim := range order {
		if fam, ok := shapes[dim]; ok {
			parts = append(parts, fmt.Sprintf("%s=%s", dim, fam))
		}
	}
	return strings.Join(parts, "，")
}

// auditSummary 汇总一句话结论：封顶原因优先，其次列出所有加了分的发现。
func auditSummary(r *CheckResult) string {
	if r.AuthCapReason != "" {
		return r.AuthCapReason
	}
	var found []string
	for _, ev := range r.Evidence {
		if ev.Weight > 0 {
			found = append(found, ev.Label)
		}
	}
	if len(found) > 0 {
		return "跨请求比对发现：" + strings.Join(found, "；")
	}
	return "跨请求 usage、thinking、消息 id 与签名自洽"
}
