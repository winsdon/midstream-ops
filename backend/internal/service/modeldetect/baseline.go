package modeldetect

import (
	"context"
	"fmt"
	"strings"
)

const BaselineTemplateVersion = "ccmax-v1"

type BaselineStats struct {
	Model           string `json:"model,omitempty"`
	QualityOK       bool   `json:"quality_ok"`
	InputTokens     int    `json:"input_tokens"`
	OutputTokens    int    `json:"output_tokens"`
	ThinkingTokens  int    `json:"thinking_tokens"`
	ThinkingChars   int    `json:"thinking_chars"`
	TTFTMs          int64  `json:"ttft_ms"`
	DurationMs      int64  `json:"duration_ms"`
	ResponseSummary string `json:"response_summary"`
}

func SameBaselineModel(baseline, actual string) bool {
	return strings.EqualFold(strings.TrimSpace(baseline), strings.TrimSpace(actual))
}

func BaselineRequest(model string) map[string]any {
	return map[string]any{
		"model": model, "max_tokens": 32000,
		"thinking":      map[string]any{"type": "adaptive", "display": "summarized"},
		"output_config": map[string]any{"effort": "high"},
		"stream":        false,
		"messages":      []map[string]any{{"role": "user", "content": "深度分析一下为啥1+1=2？？？"}},
	}
}

func CaptureBaseline(ctx context.Context, c *Client) (*BaselineStats, *Exchange) {
	ex := c.Post(ctx, KindMessages, BaselineRequest(c.Target().Model))
	if !ex.OK() {
		return nil, ex
	}
	stats := StatsFromExchange(ex)
	stats.QualityOK = strings.TrimSpace(contentText(ex.JSON)) != "" && str(ex.JSON["stop_reason"]) == "end_turn"
	stats.ResponseSummary = clip(contentText(ex.JSON), 1000)
	return stats, ex
}

func StatsFromExchange(ex *Exchange) *BaselineStats {
	if ex == nil {
		return nil
	}
	usage := usageOf(ex.JSON)
	thinkChars, _ := responseThinkingStats(ex.JSON)
	return &BaselineStats{InputTokens: intOf(usage["input_tokens"]), OutputTokens: intOf(usage["output_tokens"]), ThinkingTokens: intOf(mapOf(usage["output_tokens_details"])["thinking_tokens"]), ThinkingChars: thinkChars, TTFTMs: valueInt64(ex.TTFTMs), DurationMs: ex.DurationMs}
}

func valueInt64(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

func compareBaseline(actual, base *BaselineStats) (qualityOK, tokenOK, thinkingOK bool, detail string) {
	if actual == nil || base == nil {
		return false, false, false, "缺少基准统计"
	}
	qualityOK = actual.QualityOK
	tokenOK = withinRatio(actual.OutputTokens, base.OutputTokens, 0.35, 32)
	thinkingBase := base.ThinkingTokens
	thinkingActual := actual.ThinkingTokens
	if thinkingBase <= 0 {
		thinkingBase = base.ThinkingChars
		thinkingActual = actual.ThinkingChars
	}
	thinkingOK = withinRatio(thinkingActual, thinkingBase, 0.45, 64)
	detail = fmt.Sprintf("output_tokens 基准=%d 实际=%d；thinking 基准=%d 实际=%d；quality=%v", base.OutputTokens, actual.OutputTokens, thinkingBase, thinkingActual, qualityOK)
	return
}

func withinRatio(actual, base int, ratio float64, minAbs int) bool {
	if base <= 0 || actual <= 0 {
		return false
	}
	diff := actual - base
	if diff < 0 {
		diff = -diff
	}
	if diff < minAbs {
		return true
	}
	return float64(diff) <= float64(base)*ratio
}
