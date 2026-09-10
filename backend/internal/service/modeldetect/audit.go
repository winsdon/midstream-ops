package modeldetect

import (
	"fmt"
	"regexp"
	"strings"
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
	// firstPartyMsgCoreRe 第一方消息 id 内核：01 开头的 base58 流水号。
	firstPartyMsgCoreRe = regexp.MustCompile(`^01[A-Za-z0-9]{20,}$`)
)

// AuditRun 跨检测项一致性审计。
//
// 它不发任何请求，只回读已完成检测项留下的响应。存在的理由是单个 CheckFunc 拿不到
// 别的检测项的响应，而「第一轮就报缓存命中」「消息 id 族与签名族互斥」这类判据
// 必须跨请求比对才成立。
//
// 三类基于公开协议的判据（缓存门槛、usage 守恒、thinking 自洽）会设 AuthCapReason
// 直接封顶真实性评分；平台形态交叉比对依赖未公开的签名编码，只作低权诊断线索。
//
// 没有任何可读响应时返回 nil，调用方直接跳过。
func AuditRun(checks []*CheckResult, profile ThinkingProfile) *CheckResult {
	bodies := auditBodies(checks)
	if len(bodies) == 0 {
		return nil
	}

	r := &CheckResult{ID: AuditCheckID, Title: AuditCheckTitle, Group: GroupProtocol}
	auditCacheFloor(r, bodies, profile)
	auditUsageConservation(r, bodies)
	auditThinkingCoherence(r, bodies)
	auditPlatformShape(r, checks, bodies)
	auditBedrockCorroboration(r, checks, bodies)

	return r.finish(auditSummary(r))
}

// auditBedrockCorroboration Bedrock 旁证补分。
//
// msg_bdrk_ 前缀本身只值 2 分（见 analyzeMessageID），够不到分类阈值——网关贴一个
// 前缀字符串的成本太低。真 Bedrock 必然还留下别的痕迹：x-amzn-* 响应头、
// anthropic.claude 形态的模型回显、tooluse_ 工具 id、Bedrock 形态的签名或错误报文。
// 任一旁证同向就补足 3 分，与降权前的判定行为等价。
func auditBedrockCorroboration(r *CheckResult, checks []*CheckResult, bodies []auditBody) {
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
	for _, b := range bodies {
		for _, raw := range contentBlocks(b.body) {
			blk := mapOf(raw)
			if str(blk["type"]) != "thinking" {
				continue
			}
			if sig := str(blk["signature"]); sig != "" &&
				ParseSignatureShape(sig).Family == SigFamilyBedrock {
				found = append(found, "Bedrock 形态签名")
				break
			}
		}
		if len(found) > 0 && found[len(found)-1] == "Bedrock 形态签名" {
			break
		}
	}

	if len(found) == 0 {
		r.diagnose("msg_bdrk_ 前缀有独立旁证", false,
			"只有消息 id 前缀像 Bedrock，无响应头 / 模型回显 / 工具 id / 签名 / 报错佐证")
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
}

// auditBodies 收集所有成功响应的消息体。
//
// ex.JSON 只在进程内存在（不入库），重试路径下从 previous 恢复的结果只剩 Raw，
// 因此这里回退到解析 Raw。Raw 截断到 8000 字符会让长响应解析失败，跳过即可——
// 审计判据不依赖某一条特定响应。
func auditBodies(checks []*CheckResult) []auditBody {
	var out []auditBody
	for _, c := range checks {
		if c == nil || c.ID == AuditCheckID {
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
			out = append(out, auditBody{checkID: c.ID, body: body})
		}
	}
	return out
}

// auditCacheFloor 缓存门槛：低于模型最小可缓存前缀却报缓存命中，物理上不可能。
//
// 判据来自 AWS 与 Anthropic 一致的公开约定：cache checkpoint 有最小 token 数
// （Opus 5 = 512），且总输入 = input + cache_read + cache_creation。短于门槛的前缀
// 官方静默不缓存，绝不会返回非零 cache_read。
func auditCacheFloor(r *CheckResult, bodies []auditBody, profile ThinkingProfile) {
	floor := profile.MinCacheTokens
	if floor <= 0 {
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
// 输入用量，算不上造假。
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
			fmt.Sprintf("%d 条响应 input_tokens=0 但有输出（网关未回填输入用量）", missing))
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
// redacted_thinking 是官方的加密保护形态，本就没有明文正文，全程排除。
func auditThinkingCoherence(r *CheckResult, bodies []auditBody) {
	signed := 0
	empty := 0
	withText := 0
	var firstEmpty string
	for _, b := range bodies {
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
			fmt.Sprintf("%d 个签名块中 %d 个有正文（空正文是 adaptive 的合法形态）", signed, withText))
		return
	}

	detail := fmt.Sprintf("%d 个带签名的 thinking 块全部无正文，整轮检测未见任何思考内容｜首见于 %s",
		empty, firstEmpty)
	r.assert("整轮检测出现过思考正文", false, detail)
	r.AuthCapReason = "thinking 块全程带签名却无任何思考正文，签名与内容不匹配"
	r.addEvidence("thinking_empty_signed", "带签名的 thinking 块全程无正文", detail, ClassWrapper, 4)
}

// auditPlatformShape 平台形态交叉一致性。
//
// 四个正交维度各自归一出一个平台族：消息 id 内核、模型回显、签名内部编码、篡改报错格式。
// 真渠道四项必然同向；只有 msg_bdrk_ 这一个字符串像 Bedrock、其余全是第一方形态的，
// 说明前缀是贴上去的。
//
// 签名解析依赖未公开编码，因此本项只作低权诊断线索，不设 AuthCapReason。另外三个维度
// 不依赖它，签名解析失效时判据仍部分成立。
func auditPlatformShape(r *CheckResult, checks []*CheckResult, bodies []auditBody) {
	shapes := map[string]string{}
	note := map[string]string{}
	record := func(dim, family, detail string) {
		if family == "" || family == SigFamilyUnknown {
			return
		}
		if _, seen := shapes[dim]; seen {
			return
		}
		shapes[dim] = family
		note[dim] = detail
	}

	for _, b := range bodies {
		if id := str(b.body["id"]); id != "" {
			record("消息 id", msgIDFamily(id), id)
		}
		if m := str(b.body["model"]); m != "" {
			record("模型回显", modelEchoFamily(m), m)
		}
		for _, raw := range contentBlocks(b.body) {
			blk := mapOf(raw)
			if str(blk["type"]) != "thinking" {
				continue
			}
			if sig := str(blk["signature"]); sig != "" {
				sh := ParseSignatureShape(sig)
				if sh.Family != SigFamilyUnknown {
					record("签名内部", sh.Family, sh.KeyLabel)
				}
			}
		}
	}
	record("篡改报错", tamperErrFamily(checks), "")

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
	case strings.Contains(id, "req_vrtx_"):
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
	order := []string{"消息 id", "模型回显", "签名内部", "篡改报错"}
	parts := make([]string, 0, len(shapes))
	for _, dim := range order {
		if fam, ok := shapes[dim]; ok {
			parts = append(parts, fmt.Sprintf("%s=%s", dim, fam))
		}
	}
	return strings.Join(parts, "，")
}

// auditSummary 汇总一句话结论。
func auditSummary(r *CheckResult) string {
	if r.AuthCapReason != "" {
		return r.AuthCapReason
	}
	for _, ev := range r.Evidence {
		if ev.Key == "platform_shape_conflict" {
			return "平台形态跨维度矛盾：" + ev.Detail
		}
	}
	return "跨请求 usage、thinking 与平台形态自洽"
}
