package repository

import (
	"context"
	"time"
)

// costExpr 本站视角的成本估算表达式，仅供日趋势的官价对照字段（及其日结列）使用。
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
		SELECT COALESCE(ul.group_id, 0), COALESCE(g.name,'(无分组)'), COALESCE(g.rate_multiplier,1),
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

// UserGroupAccountUsageRow 按「用户 × 分组 × 账号」聚合行。
//
// 上游实扣挂在账号上，一个账号可被多个用户、多个分组共用，拆到用户/分组
// 需要与 ByGroup 相同的分摊权重；本行提供 CostWeight 作为原料。
type UserGroupAccountUsageRow struct {
	UserID         int64
	UserName       string
	GroupID        int64
	GroupName      string
	RateMultiplier float64
	AccountID      int64
	AccountName    string
	Requests       int64
	Revenue        float64
	CostWeight     float64
}

// AggregateUsageByUserGroupAccount 在时间范围内按「用户 × 分组 × 账号」聚合。
// 不滤 deleted_at：已删用户/账号/分组的历史流量仍须归属。
func (p *PG) AggregateUsageByUserGroupAccount(ctx context.Context, start, end time.Time) ([]UserGroupAccountUsageRow, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT COALESCE(ul.user_id, 0), `+userNameExpr+`,
		       COALESCE(ul.group_id, 0), COALESCE(g.name,'(无分组)'), COALESCE(g.rate_multiplier,1),
		       ul.account_id, COALESCE(a.name,''), COUNT(*),
		       `+revenueExpr+`, COALESCE(SUM(ul.total_cost),0)
		FROM usage_logs ul
		LEFT JOIN users u ON u.id = ul.user_id
		LEFT JOIN groups g ON g.id = ul.group_id
		LEFT JOIN accounts a ON a.id = ul.account_id
		WHERE ul.created_at >= $1 AND ul.created_at < $2
		GROUP BY 1, 2, 3, 4, 5, 6, 7`, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserGroupAccountUsageRow
	for rows.Next() {
		var r UserGroupAccountUsageRow
		if err := rows.Scan(&r.UserID, &r.UserName, &r.GroupID, &r.GroupName, &r.RateMultiplier,
			&r.AccountID, &r.AccountName, &r.Requests, &r.Revenue, &r.CostWeight); err != nil {
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

// userNameExpr 用户展示名：username → email → #id；无用户记 (无用户)。
const userNameExpr = `COALESCE(NULLIF(u.username, ''), NULLIF(u.email, ''),
	CASE WHEN COALESCE(ul.user_id, 0) = 0 THEN '(无用户)' ELSE '#' || COALESCE(ul.user_id, 0)::text END)`

// UsageDailyRow 日结汇总行（日 × 用户 × 分组 × 账号）。usage_daily 表与日结聚合共用。
type UsageDailyRow struct {
	UsageDate    string // YYYY-MM-DD（站点时区）
	UserID       int64
	UserName     string
	GroupID      int64 // 0 = 无分组
	GroupName    string
	GroupRate    float64
	AccountID    int64
	AccountName  string
	Requests     int64
	Revenue      float64 // Σ actual_cost
	CostWeight   float64 // Σ total_cost 裸值（分摊权重，不乘倍率）
	OfficialCost float64 // Σ total_cost × account_rate_multiplier（官价对照）
}

// AggregateUsageForRollup 把 [start, end) 的 usage_logs 按「日 × 用户 × 分组 × 账号」聚合，供日结落库。
//
// 口径与 AggregateUsage* 四个实时查询逐列一致（收入/权重/官价表达式、名称兜底），
// 保证同一天读汇总与读实时结果相同。不滤 deleted_at：已删实体的历史流量仍须归属。
func (p *PG) AggregateUsageForRollup(ctx context.Context, tz string, start, end time.Time) ([]UsageDailyRow, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT (ul.created_at AT TIME ZONE $1)::date,
		       COALESCE(ul.user_id, 0), `+userNameExpr+`,
		       COALESCE(ul.group_id, 0), COALESCE(g.name,'(无分组)'), COALESCE(g.rate_multiplier,1),
		       ul.account_id, COALESCE(a.name,''), COUNT(*),
		       `+revenueExpr+`, COALESCE(SUM(ul.total_cost),0), `+costExpr+`
		FROM usage_logs ul
		LEFT JOIN users u ON u.id = ul.user_id
		LEFT JOIN groups g ON g.id = ul.group_id
		LEFT JOIN accounts a ON a.id = ul.account_id
		WHERE ul.created_at >= $2 AND ul.created_at < $3
		GROUP BY 1, 2, 3, 4, 5, 6, 7, 8`, tz, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UsageDailyRow
	for rows.Next() {
		var r UsageDailyRow
		var day time.Time
		if err := rows.Scan(&day, &r.UserID, &r.UserName, &r.GroupID, &r.GroupName, &r.GroupRate,
			&r.AccountID, &r.AccountName, &r.Requests, &r.Revenue, &r.CostWeight, &r.OfficialCost); err != nil {
			return nil, err
		}
		r.UsageDate = day.Format("2006-01-02")
		out = append(out, r)
	}
	return out, rows.Err()
}

// EarliestUsageAt 返回 usage_logs 最早一条的时间；表为空时 ok=false。
// 首次日结从这一天开始抢救，更早的已被 sub2api 清掉。
func (p *PG) EarliestUsageAt(ctx context.Context) (t time.Time, ok bool, err error) {
	var v *time.Time
	if err := p.pool.QueryRow(ctx, `SELECT MIN(created_at) FROM usage_logs`).Scan(&v); err != nil {
		return time.Time{}, false, err
	}
	if v == nil {
		return time.Time{}, false, nil
	}
	return *v, true, nil
}

// ActiveUserIDs 返回 [start, end) 内有请求的去重用户 id（不含无用户的 0）。
func (p *PG) ActiveUserIDs(ctx context.Context, start, end time.Time) ([]int64, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT DISTINCT ul.user_id FROM usage_logs ul
		WHERE ul.created_at >= $1 AND ul.created_at < $2 AND COALESCE(ul.user_id, 0) <> 0`, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// CountUsersCreated 返回 [start, end) 内注册的用户数。
// 软删的也算：注册确实发生过；users 表不随日志清理，可以实时查任意历史区间。
func (p *PG) CountUsersCreated(ctx context.Context, start, end time.Time) (int64, error) {
	var n int64
	err := p.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM users WHERE created_at >= $1 AND created_at < $2`, start, end).Scan(&n)
	return n, err
}

// PassiveStabilityRow 被动稳定性行（真实流量分位数 + SLA 口径失败数）。
type PassiveStabilityRow struct {
	AccountID           int64
	AccountName         string
	Platform            string
	Requests            int64 // 成功请求数（usage_logs 只记成功）
	ErrorCount          int64 // SLA 口径失败（ops_error_logs，已排除业务限制）
	DurationAvg         *float64
	DurationP50         *float64
	DurationP90         *float64
	FirstTokAvg         *float64
	FirstTokP50         *float64
	FirstTokP90         *float64
	OutputTokens        int64
	DurationMsSum       float64 // 仅用于算 TPS，不进 DTO
	InputTokens         int64
	CacheReadTokens     int64
	CacheCreationTokens int64
}

// normalizeIgnoredStatusCodes 保证传给 PG 的是非 nil 切片。
//
// nil 切片会被 pgx 编码成 SQL NULL，而 `x <> ALL(NULL)` 求值为 NULL ——
// 那会把 error_agg 里所有行静默滤掉，失败数恒为 0、SLA 恒为 100。
// 空数组才是「不排除任何状态码」。
func normalizeIgnoredStatusCodes(codes []int32) []int32 {
	if codes == nil {
		return []int32{}
	}
	return codes
}

// PassiveStability 近 N 分钟按账号的耗时/首字分位数与 SLA 成败计数。
//
// 成功率口径对齐 sub2api 运维监控：分母排除 is_business_limited。
// ignoredStatusCodes 里的状态码同样不计入失败数（空 = 不过滤）：400/403/429/529
// 这类由调用方或配额触发的响应不代表上游服务故障，计进 SLA 会让活跃账号
// 长期挂着一条与服务质量无关的失败尾巴。
// usage_logs 只记录成功请求，失败在 ops_error_logs，两表 FULL OUTER JOIN
// 后在应用层合成 —— 窗口内只有失败的账号也必须出现，否则 100% 挂掉反而从表上消失。
func (p *PG) PassiveStability(ctx context.Context, since time.Time, ignoredStatusCodes []int32) ([]PassiveStabilityRow, error) {
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
		           COALESCE(SUM(ul.cache_read_tokens), 0) AS cache_read_tokens,
		           COALESCE(SUM(ul.cache_creation_tokens), 0) AS cache_creation_tokens
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
		      AND COALESCE(status_code, 0) <> ALL($2::int[])
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
		       COALESCE(u.cache_read_tokens, 0),
		       COALESCE(u.cache_creation_tokens, 0)
		FROM usage_agg u
		FULL OUTER JOIN error_agg e ON e.account_id = u.account_id
		LEFT JOIN accounts a ON a.id = COALESCE(u.account_id, e.account_id)`, since, normalizeIgnoredStatusCodes(ignoredStatusCodes))
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
			&r.OutputTokens, &r.DurationMsSum, &r.InputTokens, &r.CacheReadTokens, &r.CacheCreationTokens); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PassiveTimelinePoint 单账号单时间桶的成败 + 延迟/吞吐（空桶不返回）。
type PassiveTimelinePoint struct {
	AccountID           int64
	Bucket              time.Time
	Ok                  int64
	Err                 int64
	DurationAvg         *float64
	DurationP50         *float64
	DurationP90         *float64
	FirstTokAvg         *float64
	FirstTokP50         *float64
	FirstTokP90         *float64
	OutputTokens        int64
	DurationMsSum       float64
	InputTokens         int64
	CacheReadTokens     int64
	CacheCreationTokens int64
}

// PassiveStabilityTimeline 按账号 + date_bin 分桶。
//
// 口径与 PassiveStability 完全相同（含 ignoredStatusCodes）：成功来自 usage_logs，
// 失败来自 ops_error_logs 且排除业务限制与忽略状态码。分位数只来自成功请求。
// origin 取 since。
func (p *PG) PassiveStabilityTimeline(ctx context.Context, since time.Time, bucket time.Duration, ignoredStatusCodes []int32) ([]PassiveTimelinePoint, error) {
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
		           COALESCE(SUM(cache_read_tokens), 0) AS cache_read_tokens,
		           COALESCE(SUM(cache_creation_tokens), 0) AS cache_creation_tokens
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
		      AND COALESCE(status_code, 0) <> ALL($3::int[])
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
		       COALESCE(u.cache_read_tokens, 0),
		       COALESCE(u.cache_creation_tokens, 0)
		FROM usage_b u
		FULL OUTER JOIN error_b e
		  ON e.account_id = u.account_id AND e.bucket = u.bucket
		ORDER BY 1, 2`, since, bucket.Milliseconds(), normalizeIgnoredStatusCodes(ignoredStatusCodes))
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
			&r.OutputTokens, &r.DurationMsSum, &r.InputTokens, &r.CacheReadTokens, &r.CacheCreationTokens,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GroupStatusTimelinePoint 单分组单时间桶的成败 + 首字/缓存量（空桶不返回）。
//
// 只保留渠道状态图需要的字段：色块分数只看 SLA 与首字 P50，底部两个数看
// SLA 与缓存率（分母含缓存写入，见 metrics.CacheHitRate），所以不取耗时分位数、TPS 那些用不上的列。
type GroupStatusTimelinePoint struct {
	GroupID             int64
	Bucket              time.Time
	Ok                  int64
	Err                 int64
	FirstTokP50         *float64
	InputTokens         int64
	CacheReadTokens     int64
	CacheCreationTokens int64
}

// GroupStatusTimeline 按分组 + date_bin 分桶。
//
// 口径与 PassiveStabilityTimeline 完全一致，只是把 account_id 换成 group_id：
// 成功来自 usage_logs（只记成功请求），失败来自 ops_error_logs 且排除业务限制与
// 忽略状态码，首字分位数只来自成功请求。origin 取 since。
//
// 【为什么要 FULL OUTER JOIN】窗口内只有失败的分组也必须在图上出现，
// 否则「整组挂掉」的表现是从看板上消失，比标红更糟。
//
// usage_logs.group_id 可能为 NULL（老数据 / 无分组请求），这类行不进结果：
// 看板按分组出卡，没有分组就没有归属，硬塞进某个桶只会让数字对不上。
func (p *PG) GroupStatusTimeline(ctx context.Context, since time.Time, bucket time.Duration, ignoredStatusCodes []int32) ([]GroupStatusTimelinePoint, error) {
	if bucket <= 0 {
		bucket = time.Minute
	}
	rows, err := p.pool.Query(ctx, `
		WITH usage_b AS (
		    SELECT group_id,
		           date_bin(($2 * INTERVAL '1 millisecond'), created_at, $1) AS bucket,
		           COUNT(*) AS ok,
		           percentile_cont(0.5) WITHIN GROUP (ORDER BY first_token_ms)
		               FILTER (WHERE first_token_ms IS NOT NULL) AS first_tok_p50,
		           COALESCE(SUM(input_tokens), 0) AS input_tokens,
		           COALESCE(SUM(cache_read_tokens), 0) AS cache_read_tokens,
		           COALESCE(SUM(cache_creation_tokens), 0) AS cache_creation_tokens
		    FROM usage_logs
		    WHERE created_at >= $1 AND group_id IS NOT NULL
		    GROUP BY 1, 2
		),
		error_b AS (
		    SELECT group_id,
		           date_bin(($2 * INTERVAL '1 millisecond'), created_at, $1) AS bucket,
		           COUNT(*) AS err
		    FROM ops_error_logs
		    WHERE created_at >= $1
		      AND is_business_limited = FALSE
		      AND COALESCE(status_code, 0) >= 400
		      AND COALESCE(status_code, 0) <> ALL($3::int[])
		      AND group_id IS NOT NULL
		    GROUP BY 1, 2
		)
		SELECT COALESCE(u.group_id, e.group_id),
		       COALESCE(u.bucket, e.bucket),
		       COALESCE(u.ok, 0),
		       COALESCE(e.err, 0),
		       u.first_tok_p50,
		       COALESCE(u.input_tokens, 0),
		       COALESCE(u.cache_read_tokens, 0),
		       COALESCE(u.cache_creation_tokens, 0)
		FROM usage_b u
		FULL OUTER JOIN error_b e
		  ON e.group_id = u.group_id AND e.bucket = u.bucket
		ORDER BY 1, 2`, since, bucket.Milliseconds(), normalizeIgnoredStatusCodes(ignoredStatusCodes))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GroupStatusTimelinePoint
	for rows.Next() {
		var r GroupStatusTimelinePoint
		if err := rows.Scan(
			&r.GroupID, &r.Bucket, &r.Ok, &r.Err,
			&r.FirstTokP50, &r.InputTokens, &r.CacheReadTokens, &r.CacheCreationTokens,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
