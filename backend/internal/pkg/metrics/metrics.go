// Package metrics 落地跨页面复用的指标口径。
//
// 【为什么单独成包】SLA 与缓存命中率原本只写在 handler/stability_handler.go 里，
// 是「稳定性页的展示逻辑」。渠道状态图要用同一口径时，把 handler 的函数导出给
// service 用会形成 service → handler 的反向依赖；复制一份公式则必然漂移 ——
// 两处数字不一致时没人能从代码看出哪个是对的。口径属于领域，不属于页面。
//
// 【为什么返回 *float64】这几个指标在「没有样本」时不是 0，而是「无意义」：
// 缓存率 0% 表示「完全没命中」，而没走 token 的请求根本不该参与这个比例。
// 用 nil 表达「无样本」，前端渲染成「-」，避免把缺失读成坏值。
package metrics

// SLAPercent 流量 SLA（0–100）。分母 = 成功 + SLA 口径失败；两边都为 0 返回 nil。
//
// 成功只来自 usage_logs（只记成功请求），失败来自 ops_error_logs 且已排除业务
// 限制与忽略状态码 —— 排除动作在 SQL 里完成，本函数不再关心。
func SLAPercent(success, errCount int64) *float64 {
	total := success + errCount
	if total <= 0 {
		return nil
	}
	v := float64(success) / float64(total) * 100
	return &v
}

// CacheHitRate 缓存命中率（0–100）：cache_read / (input + cache_read)。
// 两边都 0 返回 nil（没走 token 的请求，不是命中率 0）。
func CacheHitRate(cacheRead, input int64) *float64 {
	total := cacheRead + input
	if total <= 0 {
		return nil
	}
	v := float64(cacheRead) / float64(total) * 100
	return &v
}

// TokensPerSecond 输出吞吐：Σ output_tokens / Σ duration_s。与广场模型指标同一公式。
// 总耗时为 0（全无耗时数据）返回 nil。
func TokensPerSecond(outputTokens int64, durationMsSum float64) *float64 {
	if durationMsSum <= 0 {
		return nil
	}
	v := float64(outputTokens) / (durationMsSum / 1000.0)
	return &v
}
