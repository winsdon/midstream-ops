package repository

import (
	"context"
	"time"
)

// costExpr 本站视角的成本估算表达式，仅供日趋势的官价对照字段使用。
//
// 真实成本以上游 actual_cost（倍率折后实扣）为准，见 upstream_key_costs 表。
// 本表达式在 accounts.rate_multiplier 未维护（全为 1）时等于原始官价，会显著高于真实支出，
// 因此不能用作利润口径。收益统计页已不再展示官价，只剩 AggregateUsageDaily 一个使用点。
const costExpr = `COALESCE(SUM(ul.total_cost * COALESCE(ul.account_rate_multiplier,1)),0)`

// revenueExpr 收益：用户端实扣。
const revenueExpr = `COALESCE(SUM(ul.actual_cost),0)`

// AccountUsageRow 按账号聚合的收益行。
type AccountUsageRow struct {
	AccountID   int64
	AccountName string
	Requests    int64
	Revenue     float64 // Σ actual_cost（用户实扣 = 我们的收入）
}

// AggregateUsageByAccount 在时间范围内按账号聚合收益。
// 成本不在这里出：真实成本取上游实扣，由服务层按 account_id join 本地库。
// 不滤 deleted_at：已删账号的历史流量仍须归属。
func (p *PG) AggregateUsageByAccount(ctx context.Context, start, end time.Time) ([]AccountUsageRow, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT ul.account_id, COALESCE(a.name,''), COUNT(*),
		       `+revenueExpr+`
		FROM usage_logs ul LEFT JOIN accounts a ON a.id = ul.account_id
		WHERE ul.created_at >= $1 AND ul.created_at < $2
		GROUP BY 1, 2`, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AccountUsageRow
	for rows.Next() {
		var r AccountUsageRow
		if err := rows.Scan(&r.AccountID, &r.AccountName, &r.Requests, &r.Revenue); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GroupAccountUsageRow 按「分组 × 账号」聚合行。
//
// 上游只按 key（≈本站账号）给一笔实扣，拆到分组需要一个分摊权重；本行提供的
// CostWeight 就是该权重的原料，服务层据此把账号实扣摊到它服务过的各个分组。
type GroupAccountUsageRow struct {
	GroupID        int64
	GroupName      string
	RateMultiplier float64
	AccountID      int64
	AccountName    string
	Requests       int64
	Revenue        float64 // Σ actual_cost（用户实扣 = 我们的收入，已含分组倍率）
	// CostWeight 为裸 Σ total_cost，不乘任何倍率：分摊权重必须反映真实资源消耗，
	// 用含倍率的 Revenue 当权重会让高倍率分组虚背成本。
	CostWeight float64
}

// AggregateUsageByGroupAccount 在时间范围内按「分组 × 账号」聚合，供成本分摊使用。
// 不滤 deleted_at：已删账号/分组的历史流量仍须归属。
func (p *PG) AggregateUsageByGroupAccount(ctx context.Context, start, end time.Time) ([]GroupAccountUsageRow, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT ul.group_id, COALESCE(g.name,'(无分组)'), COALESCE(g.rate_multiplier,1),
		       ul.account_id, COALESCE(a.name,''), COUNT(*),
		       `+revenueExpr+`, COALESCE(SUM(ul.total_cost),0)
		FROM usage_logs ul
		LEFT JOIN groups g ON g.id = ul.group_id
		LEFT JOIN accounts a ON a.id = ul.account_id
		WHERE ul.created_at >= $1 AND ul.created_at < $2
		GROUP BY 1, 2, 3, 4, 5`, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GroupAccountUsageRow
	for rows.Next() {
		var r GroupAccountUsageRow
		if err := rows.Scan(&r.GroupID, &r.GroupName, &r.RateMultiplier, &r.AccountID, &r.AccountName,
			&r.Requests, &r.Revenue, &r.CostWeight); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DailyTrendRow 日趋势行。
type DailyTrendRow struct {
	Day          string // YYYY-MM-DD（按指定时区）
	Requests     int64
	Revenue      float64
	OfficialCost float64
}

// AggregateUsageDaily 按日（指定时区）聚合趋势。
func (p *PG) AggregateUsageDaily(ctx context.Context, tz string, start, end time.Time) ([]DailyTrendRow, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT (ul.created_at AT TIME ZONE $1)::date AS day, COUNT(*),
		       `+revenueExpr+`, `+costExpr+`
		FROM usage_logs ul
		WHERE ul.created_at >= $2 AND ul.created_at < $3
		GROUP BY 1 ORDER BY 1`, tz, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DailyTrendRow
	for rows.Next() {
		var r DailyTrendRow
		var day time.Time
		if err := rows.Scan(&day, &r.Requests, &r.Revenue, &r.OfficialCost); err != nil {
			return nil, err
		}
		r.Day = day.Format("2006-01-02")
		out = append(out, r)
	}
	return out, rows.Err()
}

// PassiveStabilityRow 被动稳定性行（真实流量分位数 + SLA 口径失败数）。
type PassiveStabilityRow struct {
	AccountID       int64
	AccountName     string
	Platform        string
	Requests        int64 // 成功请求数（usage_logs 只记成功）
	ErrorCount      int64 // SLA 口径失败（ops_error_logs，已排除业务限制）
	DurationAvg     *float64
	DurationP50     *float64
	DurationP90     *float64
	FirstTokAvg     *float64
	FirstTokP50     *float64
	FirstTokP90     *float64
	OutputTokens    int64
	DurationMsSum   float64 // 仅用于算 TPS，不进 DTO
	InputTokens     int64
	CacheReadTokens int64
}

// PassiveStability 近 N 分钟按账号的耗时/首字分位数与 SLA 成败计数。
//
// 成功率口径对齐 sub2api 运维监控：分母排除 is_business_limited。
// usage_logs 只记录成功请求，失败在 ops_error_logs，两表 FULL OUTER JOIN
// 后在应用层合成 —— 窗口内只有失败的账号也必须出现，否则 100% 挂掉反而从表上消失。
func (p *PG) PassiveStability(ctx context.Context, since time.Time) ([]PassiveStabilityRow, error) {
	rows, err := p.pool.Query(ctx, `
		WITH usage_agg AS (
		    SELECT ul.account_id,
		           COUNT(*) AS requests,
		           AVG(ul.duration_ms)    FILTER (WHERE ul.duration_ms    IS NOT NULL) AS duration_avg,
		           percentile_cont(0.5)  WITHIN GROUP (ORDER BY ul.duration_ms)    FILTER (WHERE ul.duration_ms    IS NOT NULL) AS duration_p50,
		           percentile_cont(0.9)   WITHIN GROUP (ORDER BY ul.duration_ms)    FILTER (WHERE ul.duration_ms    IS NOT NULL) AS duration_p90,
		           AVG(ul.first_token_ms) FILTER (WHERE ul.first_token_ms IS NOT NULL) AS first_tok_avg,
		           percentile_cont(0.5)  WITHIN GROUP (ORDER BY ul.first_token_ms) FILTER (WHERE ul.first_token_ms IS NOT NULL) AS first_tok_p50,
		           percentile_cont(0.9)   WITHIN GROUP (ORDER BY ul.first_token_ms) FILTER (WHERE ul.first_token_ms IS NOT NULL) AS first_tok_p90,
		           COALESCE(SUM(ul.output_tokens) FILTER (WHERE ul.duration_ms > 0), 0) AS output_tokens,
		           COALESCE(SUM(ul.duration_ms) FILTER (WHERE ul.duration_ms > 0), 0)::float8 AS duration_ms_sum,
		           COALESCE(SUM(ul.input_tokens), 0) AS input_tokens,
		           COALESCE(SUM(ul.cache_read_tokens), 0) AS cache_read_tokens
		    FROM usage_logs ul
		    WHERE ul.created_at >= $1
		    GROUP BY 1
		),
		error_agg AS (
		    SELECT account_id, COUNT(*) AS error_count
		    FROM ops_error_logs
		    WHERE created_at >= $1
		      AND is_business_limited = FALSE
		      AND COALESCE(status_code, 0) >= 400
		      AND account_id IS NOT NULL
		    GROUP BY 1
		)
		SELECT COALESCE(u.account_id, e.account_id),
		       COALESCE(a.name, ''),
		       COALESCE(a.platform, ''),
		       COALESCE(u.requests, 0),
		       COALESCE(e.error_count, 0),
		       u.duration_avg,
		       u.duration_p50,
		       u.duration_p90,
		       u.first_tok_avg,
		       u.first_tok_p50,
		       u.first_tok_p90,
		       COALESCE(u.output_tokens, 0),
		       COALESCE(u.duration_ms_sum, 0),
		       COALESCE(u.input_tokens, 0),
		       COALESCE(u.cache_read_tokens, 0)
		FROM usage_agg u
		FULL OUTER JOIN error_agg e ON e.account_id = u.account_id
		LEFT JOIN accounts a ON a.id = COALESCE(u.account_id, e.account_id)`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PassiveStabilityRow
	for rows.Next() {
		var r PassiveStabilityRow
		if err := rows.Scan(&r.AccountID, &r.AccountName, &r.Platform, &r.Requests, &r.ErrorCount,
			&r.DurationAvg, &r.DurationP50, &r.DurationP90,
			&r.FirstTokAvg, &r.FirstTokP50, &r.FirstTokP90,
			&r.OutputTokens, &r.DurationMsSum, &r.InputTokens, &r.CacheReadTokens); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PassiveTimelinePoint 单账号单时间桶的成败 + 延迟/吞吐（空桶不返回）。
type PassiveTimelinePoint struct {
	AccountID       int64
	Bucket          time.Time
	Ok              int64
	Err             int64
	DurationAvg     *float64
	DurationP50     *float64
	DurationP90     *float64
	FirstTokAvg     *float64
	FirstTokP50     *float64
	FirstTokP90     *float64
	OutputTokens    int64
	DurationMsSum   float64
	InputTokens     int64
	CacheReadTokens int64
}

// PassiveStabilityTimeline 按账号 + date_bin 分桶。
//
// 口径与 PassiveStability 相同：成功来自 usage_logs，失败来自 ops_error_logs
// 且排除业务限制。分位数只来自成功请求。origin 取 since。
func (p *PG) PassiveStabilityTimeline(ctx context.Context, since time.Time, bucket time.Duration) ([]PassiveTimelinePoint, error) {
	if bucket <= 0 {
		bucket = time.Minute
	}
	rows, err := p.pool.Query(ctx, `
		WITH usage_b AS (
		    SELECT account_id,
		           date_bin(($2 * INTERVAL '1 millisecond'), created_at, $1) AS bucket,
		           COUNT(*) AS ok,
		           AVG(duration_ms)    FILTER (WHERE duration_ms    IS NOT NULL) AS duration_avg,
		           percentile_cont(0.5) WITHIN GROUP (ORDER BY duration_ms)    FILTER (WHERE duration_ms    IS NOT NULL) AS duration_p50,
		           percentile_cont(0.9) WITHIN GROUP (ORDER BY duration_ms)    FILTER (WHERE duration_ms    IS NOT NULL) AS duration_p90,
		           AVG(first_token_ms) FILTER (WHERE first_token_ms IS NOT NULL) AS first_tok_avg,
		           percentile_cont(0.5) WITHIN GROUP (ORDER BY first_token_ms) FILTER (WHERE first_token_ms IS NOT NULL) AS first_tok_p50,
		           percentile_cont(0.9) WITHIN GROUP (ORDER BY first_token_ms) FILTER (WHERE first_token_ms IS NOT NULL) AS first_tok_p90,
		           COALESCE(SUM(output_tokens) FILTER (WHERE duration_ms > 0), 0) AS output_tokens,
		           COALESCE(SUM(duration_ms) FILTER (WHERE duration_ms > 0), 0)::float8 AS duration_ms_sum,
		           COALESCE(SUM(input_tokens), 0) AS input_tokens,
		           COALESCE(SUM(cache_read_tokens), 0) AS cache_read_tokens
		    FROM usage_logs
		    WHERE created_at >= $1
		    GROUP BY 1, 2
		),
		error_b AS (
		    SELECT account_id,
		           date_bin(($2 * INTERVAL '1 millisecond'), created_at, $1) AS bucket,
		           COUNT(*) AS err
		    FROM ops_error_logs
		    WHERE created_at >= $1
		      AND is_business_limited = FALSE
		      AND COALESCE(status_code, 0) >= 400
		      AND account_id IS NOT NULL
		    GROUP BY 1, 2
		)
		SELECT COALESCE(u.account_id, e.account_id),
		       COALESCE(u.bucket, e.bucket),
		       COALESCE(u.ok, 0),
		       COALESCE(e.err, 0),
		       u.duration_avg,
		       u.duration_p50,
		       u.duration_p90,
		       u.first_tok_avg,
		       u.first_tok_p50,
		       u.first_tok_p90,
		       COALESCE(u.output_tokens, 0),
		       COALESCE(u.duration_ms_sum, 0),
		       COALESCE(u.input_tokens, 0),
		       COALESCE(u.cache_read_tokens, 0)
		FROM usage_b u
		FULL OUTER JOIN error_b e
		  ON e.account_id = u.account_id AND e.bucket = u.bucket
		ORDER BY 1, 2`, since, bucket.Milliseconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PassiveTimelinePoint
	for rows.Next() {
		var r PassiveTimelinePoint
		if err := rows.Scan(
			&r.AccountID, &r.Bucket, &r.Ok, &r.Err,
			&r.DurationAvg, &r.DurationP50, &r.DurationP90,
			&r.FirstTokAvg, &r.FirstTokP50, &r.FirstTokP90,
			&r.OutputTokens, &r.DurationMsSum, &r.InputTokens, &r.CacheReadTokens,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
