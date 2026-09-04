package modeldetect

import (
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
	// BudgetTokens 手动档的预算，Adaptive 时为 0。
	BudgetTokens int
	// MinCacheTokens 官方最小可缓存前缀长度（短于此值静默不缓存）。
	MinCacheTokens int
	// Recognized 为 false 表示型号无法识别，按保守默认处理。
	Recognized bool
}

var modelVersionRe = regexp.MustCompile(`(?i)(opus|sonnet|haiku|fable|mythos)[-_. ]*(\d+)(?:[-_. ]*(\d+))?`)

// ResolveProfile 解析模型的 thinking 形态与缓存阈值。
//
// 判据来自官方文档：Opus/Sonnet 4.6 起与 Fable/Mythos 5 用 adaptive thinking；
// 更早的型号继续用手动 budget_tokens。最小可缓存前缀按型号分档，短于阈值静默不缓存，
// 缓存检测必须超过它，否则会把「前缀太短」误判成「缓存失效」。
func ResolveProfile(model string) ThinkingProfile {
	p := ThinkingProfile{Family: "unknown", BudgetTokens: 1024, MinCacheTokens: 4096}
	m := modelVersionRe.FindStringSubmatch(model)
	if m == nil {
		return p
	}
	p.Recognized = true
	p.Family = strings.ToLower(m[1])
	major, _ := strconv.Atoi(m[2])
	minor := 0
	if m[3] != "" {
		minor, _ = strconv.Atoi(m[3])
	}

	switch p.Family {
	case "opus", "sonnet":
		p.Adaptive = major > 4 || (major == 4 && minor >= 6)
	case "fable", "mythos":
		p.Adaptive = major >= 5
	}
	if p.Adaptive {
		p.BudgetTokens = 0
	}

	switch {
	case (p.Family == "opus" || p.Family == "fable" || p.Family == "mythos") && major >= 5:
		p.MinCacheTokens = 512
	case p.Family == "sonnet" && major >= 5:
		p.MinCacheTokens = 1024
	case p.Family == "opus" && major == 4 && minor >= 8:
		p.MinCacheTokens = 1024
	case p.Family == "sonnet" && major == 4 && minor >= 5:
		p.MinCacheTokens = 1024
	case p.Family == "opus" && major == 4 && minor == 7:
		p.MinCacheTokens = 2048
	case p.Family == "haiku" && major == 3:
		p.MinCacheTokens = 2048
	default:
		p.MinCacheTokens = 4096
	}
	return p
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
func LookupCutoff(model string) (OfficialCutoff, bool) {
	key := strings.ToLower(strings.TrimSpace(model))
	if c, ok := officialCutoffs[key]; ok {
		return c, true
	}
	if i := strings.LastIndex(key, "-20"); i > 0 {
		if c, ok := officialCutoffs[key[:i]]; ok {
			return c, true
		}
	}
	return OfficialCutoff{}, false
}
