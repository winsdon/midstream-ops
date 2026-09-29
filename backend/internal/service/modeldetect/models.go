package modeldetect

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// str 安全取字符串。
func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// num 安全取数值。JSON 解析后数字一律是 float64。
func num(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case string:
		f, err := strconv.ParseFloat(n, 64)
		return f, err == nil
	}
	return 0, false
}

// intOf 取整数，缺失或非数值返回 0。
func intOf(v any) int {
	f, ok := num(v)
	if !ok {
		return 0
	}
	return int(f)
}

// mapOf 安全取对象。
func mapOf(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

// sliceOf 安全取数组。
func sliceOf(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}

// contentBlocks 取消息的 content 数组。
func contentBlocks(body map[string]any) []any {
	if body == nil {
		return nil
	}
	return sliceOf(body["content"])
}

// contentText 拼接所有 text 块。
func contentText(body map[string]any) string {
	var sb strings.Builder
	for _, raw := range contentBlocks(body) {
		b := mapOf(raw)
		if str(b["type"]) == "text" {
			sb.WriteString(str(b["text"]))
		}
	}
	return strings.TrimSpace(sb.String())
}

// thinkingText 拼接所有 thinking 块正文（部分渠道把身份信息漏在思考里）。
func thinkingText(body map[string]any) string {
	var sb strings.Builder
	for _, raw := range contentBlocks(body) {
		b := mapOf(raw)
		if str(b["type"]) == "thinking" {
			sb.WriteString(str(b["thinking"]))
			sb.WriteString("\n")
		}
	}
	return strings.TrimSpace(sb.String())
}

// usageOf 取 usage 对象。
func usageOf(body map[string]any) map[string]any {
	if body == nil {
		return nil
	}
	return mapOf(body["usage"])
}

// cacheCreationTokens 兼容新版嵌套用量字段（ephemeral_5m / ephemeral_1h）。
func cacheCreationTokens(usage map[string]any) int {
	if usage == nil {
		return 0
	}
	if v, ok := num(usage["cache_creation_input_tokens"]); ok {
		return int(v)
	}
	nested := mapOf(usage["cache_creation"])
	return intOf(nested["ephemeral_5m_input_tokens"]) + intOf(nested["ephemeral_1h_input_tokens"])
}

// cacheReadTokens 取缓存命中 token 数。
func cacheReadTokens(usage map[string]any) int { return intOf(usage["cache_read_input_tokens"]) }

// inputTotal 一次请求的全部输入：input + cache_creation + cache_read。
//
// 网关把注入的提示拼进缓存时，注入量落在缓存字段里，只看 input_tokens 会漏掉。
// usage 里没有 input_tokens 时 ok=false：缺字段读到的 0 不能当成真实用量对账。
func inputTotal(usage map[string]any) (total int, cached, ok bool) {
	if _, has := usage["input_tokens"]; !has {
		return 0, false, false
	}
	cache := cacheCreationTokens(usage) + cacheReadTokens(usage)
	return intOf(usage["input_tokens"]) + cache, cache > 0, true
}

// thinkingTokens 取 usage.output_tokens_details.thinking_tokens；渠道没拆分时返回 ok=false。
func thinkingTokens(usage map[string]any) (int, bool) {
	v, ok := num(mapOf(usage["output_tokens_details"])["thinking_tokens"])
	return int(v), ok
}

// hasThinkingOutput 响应里有没有 thinking / redacted_thinking 块。
func hasThinkingOutput(body map[string]any) bool {
	for _, raw := range contentBlocks(body) {
		switch str(mapOf(raw)["type"]) {
		case "thinking", "redacted_thinking":
			return true
		}
	}
	return false
}

// errorType 取标准错误响应的 error.type。
func errorType(body map[string]any) string {
	return str(mapOf(body["error"])["type"])
}

// errorMessage 取标准错误响应的 error.message。
func errorMessage(body map[string]any) string {
	return str(mapOf(body["error"])["message"])
}

// ---- 模型档案 ----

// ThinkingProfile 描述模型的 thinking 形态与最小可缓存前缀长度。
type ThinkingProfile struct {
	Family string
	// Adaptive 为 true 时用 {"type":"adaptive"}，否则用 {"type":"enabled","budget_tokens":1024}。
	Adaptive bool
	// DefaultThinking 不传 thinking 也会思考：Opus / Sonnet 5 起与 Fable / Mythos 5 起（Opus 5.5、
	// Fable 5.1 更是关不掉）。Opus 4.6–4.8、Sonnet 4.6 虽然用 adaptive，不传就不思考。
	DefaultThinking bool
	// KeepsThinking 历史轮次的 thinking 块留在上下文、按输入计费：Opus 4.5 与 4.6 起的全部型号。
	// Sonnet 4.5、Haiku 4.5 及更早的型号由 API 自己剥掉，回传的 thinking 不计输入。
	KeepsThinking bool
	// BudgetTokens 手动档的预算，Adaptive 时为 0。
	BudgetTokens int
	// MinCacheTokens 官方最小可缓存前缀长度（短于此值静默不缓存）。
	MinCacheTokens int
	// Recognized 为 false 表示型号无法识别，按保守默认处理。
	Recognized bool
}

var (
	modelVersionRe = regexp.MustCompile(`(?i)(opus|sonnet|haiku|fable|mythos)[-_. ]*(\d+)(?:[-_. ]*(\d+))?([a-z]?)`)
	// legacyModelRe 3.x 时代版本号写在族名前面：claude-3-7-sonnet-20250219、claude-3-5-haiku-20241022。
	legacyModelRe = regexp.MustCompile(`(?i)claude[-_. ]*(\d)(?:[-_. ](\d))?[-_. ]*(opus|sonnet|haiku)`)
)

// modelVersion 型号的族与版本号。
type modelVersion struct {
	family       string
	major, minor int
	hasMinor     bool
}

// parseModelVersion 从型号 id 读出族与版本号。小版本最多两位，更长的数字是日期或长度后缀：
// claude-sonnet-4-20250514 是 Sonnet 4.0，不是 4.20250514；紧跟字母的数字是渠道后缀不是版本：
// claude-opus-5-1m 是 1M 上下文的 Opus 5，不是 Opus 5.1。
func parseModelVersion(model string) (modelVersion, bool) {
	if m := legacyModelRe.FindStringSubmatch(model); m != nil {
		v := modelVersion{family: strings.ToLower(m[3]), hasMinor: m[2] != ""}
		v.major, _ = strconv.Atoi(m[1])
		v.minor, _ = strconv.Atoi(m[2])
		return v, true
	}
	m := modelVersionRe.FindStringSubmatch(model)
	if m == nil || len(m[2]) >= 8 {
		return modelVersion{}, false
	}
	v := modelVersion{family: strings.ToLower(m[1])}
	v.major, _ = strconv.Atoi(m[2])
	if m[3] != "" && len(m[3]) <= 2 && m[4] == "" {
		v.minor, _ = strconv.Atoi(m[3])
		v.hasMinor = true
	}
	return v, true
}

// atLeast 版本号不低于 major.minor。
func (v modelVersion) atLeast(major, minor int) bool {
	return v.major > major || (v.major == major && v.minor >= minor)
}

// ResolveProfile 解析模型的 thinking 形态与缓存阈值。
//
// 判据来自官方文档：Opus/Sonnet 4.6 起与 Fable/Mythos 5 用 adaptive thinking；
// 更早的型号继续用手动 budget_tokens。最小可缓存前缀按官方的型号表分档，短于阈值静默不缓存，
// 缓存检测必须超过它，否则会把「前缀太短」误判成「缓存失效」。
func ResolveProfile(model string) ThinkingProfile {
	p := ThinkingProfile{Family: "unknown", BudgetTokens: 1024, MinCacheTokens: 4096}
	v, ok := parseModelVersion(model)
	if !ok {
		return p
	}
	p.Recognized = true
	p.Family = v.family

	switch p.Family {
	case "opus":
		p.Adaptive = v.atLeast(4, 6)
		p.DefaultThinking = v.major >= 5
		p.KeepsThinking = v.atLeast(4, 5)
	case "sonnet":
		p.Adaptive = v.atLeast(4, 6)
		p.DefaultThinking = v.major >= 5
		p.KeepsThinking = v.atLeast(4, 6)
	case "haiku":
		p.KeepsThinking = v.atLeast(4, 6)
	case "fable", "mythos":
		p.Adaptive = v.major >= 5
		p.DefaultThinking = v.major >= 5
		p.KeepsThinking = v.major >= 5
	}
	if p.Adaptive {
		p.BudgetTokens = 0
	}
	p.MinCacheTokens = minCacheTokens(v)
	return p
}

// minCacheTokens 官方最小可缓存前缀（2026-09 的型号表）。
func minCacheTokens(v modelVersion) int {
	switch v.family {
	case "opus":
		switch {
		case v.major >= 5:
			return 512
		case v.atLeast(4, 8):
			return 1024
		case v.atLeast(4, 7):
			return 2048
		case v.atLeast(4, 5):
			return 4096
		default: // Opus 4.1 / 4
			return 1024
		}
	case "fable", "mythos":
		if v.major >= 5 {
			return 512
		}
	case "sonnet":
		return 1024
	case "haiku":
		if v.major >= 4 {
			return 4096
		}
		return 2048
	}
	return 4096
}

// ThinkingParam 按档案生成 thinking 参数。
func (p ThinkingProfile) ThinkingParam(display string) map[string]any {
	if p.Adaptive {
		return map[string]any{"type": "adaptive", "display": display}
	}
	return map[string]any{"type": "enabled", "budget_tokens": p.BudgetTokens, "display": display}
}

// OfficialCutoff 官方公布的知识截止（reliable = 最可靠知识，training = 训练数据结束）。
type OfficialCutoff struct {
	Name     string
	Reliable string
	Training string
}

// officialCutoffs 用于核对渠道自报的截止日期。国产模型套壳最常在这里露馅。
var officialCutoffs = map[string]OfficialCutoff{
	"claude-opus-5":     {"Claude Opus 5", "2026-05", "2026-05"},
	"claude-opus-4-8":   {"Claude Opus 4.8", "2026-01", "2026-01"},
	"claude-opus-4-7":   {"Claude Opus 4.7", "2026-01", "2026-01"},
	"claude-opus-4-6":   {"Claude Opus 4.6", "2025-05", "2025-08"},
	"claude-opus-4-5":   {"Claude Opus 4.5", "2025-05", "2025-08"},
	"claude-opus-4-1":   {"Claude Opus 4.1", "2025-01", "2025-03"},
	"claude-sonnet-5":   {"Claude Sonnet 5", "2026-01", "2026-01"},
	"claude-sonnet-4-6": {"Claude Sonnet 4.6", "2025-08", "2025-08"},
	"claude-sonnet-4-5": {"Claude Sonnet 4.5", "2025-01", "2025-07"},
	"claude-haiku-4-5":  {"Claude Haiku 4.5", "2025-02", "2025-07"},
	"claude-fable-5":    {"Claude Fable 5", "2026-01", "2026-01"},
}

// LookupCutoff 按模型 id 查官方截止日期。带日期后缀的 id（claude-opus-4-5-20251101）
// 会退化到不带后缀的主键，避免因为日期版本查不到就放弃核对。
//
// 不按同族同代回退：渠道别名（claude-opus-5-5）借用 claude-opus-5 的截止日期去比模型自报，
// 只会凭空多出一条「自报截止与官方不符」。
func LookupCutoff(model string) (OfficialCutoff, bool) {
	for _, key := range modelKeys(model) {
		if c, ok := officialCutoffs[key]; ok {
			return c, true
		}
	}
	return OfficialCutoff{}, false
}

// modelKeys 按查表优先级返回型号 id 的候选键：原样小写，其次去掉日期后缀
// （claude-opus-4-5-20251101 → claude-opus-4-5）。
func modelKeys(model string) []string {
	key := strings.ToLower(strings.TrimSpace(model))
	keys := []string{key}
	if i := strings.LastIndex(key, "-20"); i > 0 {
		keys = append(keys, key[:i])
	}
	return keys
}

// familyModelKeys 同族同代的主型号 id，先带小版本再只到大版本：
// claude-opus-4-8-thinking → claude-opus-4-8、claude-opus-4；claude-opus-5-5 → …、claude-opus-5；
// anthropic.claude-opus-5-v1:0 → claude-opus-5。认不出族与代时返回 nil。
func familyModelKeys(model string) []string {
	v, ok := parseModelVersion(model)
	if !ok {
		return nil
	}
	major := fmt.Sprintf("claude-%s-%d", v.family, v.major)
	if !v.hasMinor {
		return []string{major}
	}
	return []string{fmt.Sprintf("%s-%d", major, v.minor), major}
}
