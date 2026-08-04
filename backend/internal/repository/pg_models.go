package repository

import (
	"context"
	"encoding/json"
	"time"
)

// PGGroupModels 一个分组及其实际可用的模型列表。
type PGGroupModels struct {
	GroupID          int64
	GroupName        string
	Platform         string
	RateMultiplier   float64
	SubscriptionType string
	IsExclusive      bool
	// Models 是该分组下所有可调度账号 credentials->'model_mapping' 的 KEY 并集。
	// 为空表示账号都没配映射，调用方应回落到平台默认模型表。
	Models []string
}

// ListGroupAvailableModels 返回每个公开分组实际可用的模型。
//
// 口径对齐 sub2api 的 GatewayService.GetAvailableModels —— 即 GET /v1/models 的
// 权威来源：该分组下所有可调度账号 model_mapping 的 KEY 并集。KEY 是用户请求名
// （VALUE 才是上游真实名），因此可与 usage_logs 的 COALESCE(requested_model, model) join。
//
// 刻意不用 channel_model_pricing.models：那只是「配了自定义价的模型」，
// 不等于「可用模型」——绝大多数模型走 LiteLLM 全局价，不在该表里。
//
// 专属分组（is_exclusive）被整体排除：它们是管理员授权给特定用户的，分组名与
// 倍率属于内部定价策略，不应出现在面向全体用户的广场里。只存在于专属分组的模型
// 因此也不会出现。
//
// 账号过滤只保留持久状态（未删除 / active / schedulable），忽略限流、过载、临时
// 下线等瞬时运行态：广场是价格目录，不应因账号临时限流而让模型消失。
func (p *PG) ListGroupAvailableModels(ctx context.Context) ([]PGGroupModels, error) {
	rows, err := p.pool.Query(ctx, `
		WITH grp_accounts AS (
		    SELECT g.id AS group_id, a.credentials
		    FROM groups g
		    JOIN account_groups ag ON ag.group_id = g.id
		    JOIN accounts a        ON a.id = ag.account_id
		    WHERE g.deleted_at IS NULL AND g.status = 'active'
		      AND COALESCE(g.is_exclusive, false) = false
		      -- composite 分组聚合全部平台的账号，其余按平台匹配
		      AND (g.platform = 'composite' OR a.platform = g.platform)
		      AND a.deleted_at IS NULL AND a.status = 'active' AND a.schedulable = TRUE
		),
		account_models AS (
		    SELECT ga.group_id, jsonb_object_keys(ga.credentials -> 'model_mapping') AS model
		    FROM grp_accounts ga
		    WHERE jsonb_typeof(ga.credentials -> 'model_mapping') = 'object'
		      AND ga.credentials -> 'model_mapping' <> '{}'::jsonb
		)
		SELECT g.id, COALESCE(g.name,''), COALESCE(g.platform,''),
		       COALESCE(g.rate_multiplier,1), COALESCE(g.subscription_type,'standard'),
		       COALESCE(g.is_exclusive,false),
		       COALESCE(
		           ARRAY_AGG(DISTINCT am.model ORDER BY am.model)
		               FILTER (WHERE am.model IS NOT NULL AND am.model NOT LIKE '%*'),
		           '{}'
		       ) AS models
		FROM groups g
		LEFT JOIN account_models am ON am.group_id = g.id
		WHERE g.deleted_at IS NULL AND g.status = 'active'
		  AND COALESCE(g.is_exclusive, false) = false
		GROUP BY g.id, g.name, g.platform, g.rate_multiplier, g.subscription_type, g.is_exclusive
		ORDER BY g.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PGGroupModels
	for rows.Next() {
		var g PGGroupModels
		if err := rows.Scan(&g.GroupID, &g.GroupName, &g.Platform, &g.RateMultiplier,
			&g.SubscriptionType, &g.IsExclusive, &g.Models); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// PGChannelPricingRow 渠道模型定价 × 分组 的一行原始结果。
//
// 一条 channel_model_pricing 可绑定多个模型（models JSONB 数组），且其所属渠道
// 可关联多个分组，因此同一 pricing 会随分组数展开成多行；service 层负责按
// 模型名去重聚合。
type PGChannelPricingRow struct {
	PricingID        int64
	ChannelID        int64
	ChannelName      string
	ChannelDesc      string
	Platform         string
	Models           []string // models JSONB 展开
	BillingMode      string   // token | per_request | image
	InputPrice       *float64 // 每 token 价（USD），NULL 表示未配置
	OutputPrice      *float64
	CacheWritePrice  *float64
	CacheReadPrice   *float64
	ImageOutputPrice *float64
	PerRequestPrice  *float64

	// 分组信息（渠道未关联分组时为零值，GroupID = 0）
	GroupID          int64
	GroupName        string
	GroupPlatform    string
	GroupRate        float64
	SubscriptionType string
	IsExclusive      bool
}

// ListChannelModelPricing 读取所有启用渠道的模型定价及其关联分组。
//
// 只取 status='active' 的渠道与未删除、启用的分组；channel_groups.group_id 有唯一
// 索引，即一个分组最多属于一个渠道，因此这里的展开不会产生分组重复。
func (p *PG) ListChannelModelPricing(ctx context.Context) ([]PGChannelPricingRow, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT cmp.id, cmp.channel_id, COALESCE(ch.name,''), COALESCE(ch.description,''),
		       COALESCE(cmp.platform,''), COALESCE(cmp.models,'[]'::jsonb),
		       COALESCE(cmp.billing_mode,'token'),
		       cmp.input_price, cmp.output_price, cmp.cache_write_price,
		       cmp.cache_read_price, cmp.image_output_price, cmp.per_request_price,
		       COALESCE(g.id,0), COALESCE(g.name,''), COALESCE(g.platform,''),
		       COALESCE(g.rate_multiplier,1), COALESCE(g.subscription_type,'standard'),
		       COALESCE(g.is_exclusive,false)
		FROM channel_model_pricing cmp
		JOIN channels ch ON ch.id = cmp.channel_id AND ch.status = 'active'
		LEFT JOIN channel_groups cg ON cg.channel_id = cmp.channel_id
		LEFT JOIN groups g ON g.id = cg.group_id AND g.deleted_at IS NULL AND g.status = 'active'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PGChannelPricingRow
	for rows.Next() {
		var r PGChannelPricingRow
		var modelsRaw []byte
		if err := rows.Scan(
			&r.PricingID, &r.ChannelID, &r.ChannelName, &r.ChannelDesc,
			&r.Platform, &modelsRaw, &r.BillingMode,
			&r.InputPrice, &r.OutputPrice, &r.CacheWritePrice,
			&r.CacheReadPrice, &r.ImageOutputPrice, &r.PerRequestPrice,
			&r.GroupID, &r.GroupName, &r.GroupPlatform,
			&r.GroupRate, &r.SubscriptionType, &r.IsExclusive,
		); err != nil {
			return nil, err
		}
		// models 是 JSONB 数组；解析失败按空处理，不让单条脏数据拖垮整页。
		if len(modelsRaw) > 0 {
			_ = json.Unmarshal(modelsRaw, &r.Models)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PGPricingInterval 阶梯定价区间。
type PGPricingInterval struct {
	PricingID       int64
	MinTokens       int
	MaxTokens       *int // NULL 表示无上限
	TierLabel       string
	InputPrice      *float64
	OutputPrice     *float64
	CacheWritePrice *float64
	CacheReadPrice  *float64
	PerRequestPrice *float64
}

// ListPricingIntervals 批量读取全部阶梯定价区间（按 pricing_id 分组，避免 N+1）。
func (p *PG) ListPricingIntervals(ctx context.Context) ([]PGPricingInterval, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT pricing_id, COALESCE(min_tokens,0), max_tokens, COALESCE(tier_label,''),
		       input_price, output_price, cache_write_price, cache_read_price, per_request_price
		FROM channel_pricing_intervals
		ORDER BY pricing_id, sort_order`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PGPricingInterval
	for rows.Next() {
		var iv PGPricingInterval
		if err := rows.Scan(&iv.PricingID, &iv.MinTokens, &iv.MaxTokens, &iv.TierLabel,
			&iv.InputPrice, &iv.OutputPrice, &iv.CacheWritePrice,
			&iv.CacheReadPrice, &iv.PerRequestPrice); err != nil {
			return nil, err
		}
		out = append(out, iv)
	}
	return out, rows.Err()
}

// PGModelMetric 站点级模型延迟/吞吐/成功率聚合。
type PGModelMetric struct {
	Model           string
	RequestCount    int64
	AvgDurationMs   int64
	TokensPerSecond float64
	// SuccessCount 成功请求数（usage_logs 只记录成功请求）。
	SuccessCount int64
	// ErrorCount 失败请求数（ops_error_logs，已排除业务限制类）。
	ErrorCount int64
}

// modelMetricsLimit 兜底上限，防止异常数据导致结果集膨胀。
const modelMetricsLimit = 500

// AggregateModelMetrics 按模型聚合 [start, end) 窗口内的请求数、平均耗时、输出吞吐与成败计数。
//
// 模型维度用 COALESCE(requested_model, model) —— 与用户请求名空间对齐，才能和
// 分组可用模型 join 上。
//
// 成功率口径对齐 sub2api 的 vNext 指标定义：分母排除 is_business_limited
// （余额不足、限额等业务限制不算服务故障）。usage_logs 只记录成功请求，
// 失败请求在 ops_error_logs，因此两表分别统计后在应用层合成。
func (p *PG) AggregateModelMetrics(ctx context.Context, start, end time.Time) ([]PGModelMetric, error) {
	rows, err := p.pool.Query(ctx, `
		WITH usage_agg AS (
		    SELECT COALESCE(NULLIF(TRIM(requested_model), ''), model) AS model_name,
		           COUNT(*) AS request_count,
		           COALESCE(AVG(duration_ms) FILTER (WHERE duration_ms > 0), 0)::bigint AS avg_duration_ms,
		           COALESCE(
		               SUM(output_tokens) FILTER (WHERE duration_ms > 0)::float8
		               / NULLIF(SUM(duration_ms) FILTER (WHERE duration_ms > 0)::float8 / 1000.0, 0),
		               0
		           ) AS tokens_per_second
		    FROM usage_logs
		    WHERE created_at >= $1 AND created_at < $2
		    GROUP BY 1
		),
		error_agg AS (
		    SELECT COALESCE(NULLIF(TRIM(model), ''), '') AS model_name,
		           COUNT(*) AS error_count
		    FROM ops_error_logs
		    WHERE created_at >= $1 AND created_at < $2
		      AND is_business_limited = FALSE
		    GROUP BY 1
		)
		SELECT COALESCE(u.model_name, e.model_name) AS model_name,
		       COALESCE(u.request_count, 0),
		       COALESCE(u.avg_duration_ms, 0),
		       COALESCE(u.tokens_per_second, 0),
		       COALESCE(u.request_count, 0) AS success_count,
		       COALESCE(e.error_count, 0)
		FROM usage_agg u
		FULL OUTER JOIN error_agg e ON e.model_name = u.model_name
		WHERE COALESCE(u.model_name, e.model_name) <> ''
		ORDER BY (COALESCE(u.request_count,0) + COALESCE(e.error_count,0)) DESC
		LIMIT $3`, start, end, modelMetricsLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PGModelMetric
	for rows.Next() {
		var m PGModelMetric
		if err := rows.Scan(&m.Model, &m.RequestCount, &m.AvgDurationMs, &m.TokensPerSecond,
			&m.SuccessCount, &m.ErrorCount); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
