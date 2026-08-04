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

// PassiveStabilityRow 被动稳定性行（真实流量分位数）。
type PassiveStabilityRow struct {
	AccountID    int64
	AccountName  string
	Platform     string
	Requests     int64
	DurationP50  *float64
	DurationP95  *float64
	FirstTokP50  *float64
	FirstTokP95  *float64
}

// PassiveStability 近 N 小时按账号的耗时/首字分位数。
func (p *PG) PassiveStability(ctx context.Context, since time.Time) ([]PassiveStabilityRow, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT ul.account_id, COALESCE(a.name,''), COALESCE(a.platform,''), COUNT(*),
		       percentile_cont(0.5)  WITHIN GROUP (ORDER BY ul.duration_ms)    FILTER (WHERE ul.duration_ms    IS NOT NULL),
		       percentile_cont(0.95) WITHIN GROUP (ORDER BY ul.duration_ms)    FILTER (WHERE ul.duration_ms    IS NOT NULL),
		       percentile_cont(0.5)  WITHIN GROUP (ORDER BY ul.first_token_ms) FILTER (WHERE ul.first_token_ms IS NOT NULL),
		       percentile_cont(0.95) WITHIN GROUP (ORDER BY ul.first_token_ms) FILTER (WHERE ul.first_token_ms IS NOT NULL)
		FROM usage_logs ul LEFT JOIN accounts a ON a.id = ul.account_id
		WHERE ul.created_at >= $1
		GROUP BY 1, 2, 3`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PassiveStabilityRow
	for rows.Next() {
		var r PassiveStabilityRow
		if err := rows.Scan(&r.AccountID, &r.AccountName, &r.Platform, &r.Requests,
			&r.DurationP50, &r.DurationP95, &r.FirstTokP50, &r.FirstTokP95); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
