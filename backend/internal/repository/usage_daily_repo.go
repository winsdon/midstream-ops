package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// UsageDailyRepo 收入日结汇总（usage_daily）与结算进度（usage_rollup_state）。
//
// sub2api 的 usage_logs 只保留约一个月，这张表是收入侧唯一的长期数据源，永久保留。
// 读方法一律按闭区间日期串（YYYY-MM-DD）查询，返回值复用实时查询的行类型，
// 使服务层的分摊/组装逻辑对两种数据源无感。
type UsageDailyRepo struct {
	db *DB
}

// NewUsageDailyRepo 创建 UsageDailyRepo。
func NewUsageDailyRepo(s *Store) *UsageDailyRepo { return &UsageDailyRepo{db: s.DB()} }

// rollupStateID usage_rollup_state 是单行表。
const rollupStateID = 1

// Upsert 幂等写入日结行。
//
// 只增不减：usage_logs 只追加，同一组合的请求数变少只可能是日志被清了，
// 此时整行保留旧值（WHERE 守卫）；新结果里没有的组合也不删。
func (r *UsageDailyRepo) Upsert(ctx context.Context, rows []UsageDailyRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO usage_daily
			(usage_date, user_id, group_id, account_id, user_name, group_name, group_rate, account_name,
			 requests, revenue, cost_weight, official_cost, settled_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(usage_date, user_id, group_id, account_id) DO UPDATE SET
			user_name     = excluded.user_name,
			group_name    = excluded.group_name,
			group_rate    = excluded.group_rate,
			account_name  = excluded.account_name,
			requests      = excluded.requests,
			revenue       = excluded.revenue,
			cost_weight   = excluded.cost_weight,
			official_cost = excluded.official_cost,
			settled_at    = excluded.settled_at
		WHERE excluded.requests >= usage_daily.requests`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	now := nowUTC()
	for _, d := range rows {
		if _, err := stmt.ExecContext(ctx, d.UsageDate, d.UserID, d.GroupID, d.AccountID,
			d.UserName, d.GroupName, d.GroupRate, d.AccountName,
			d.Requests, d.Revenue, d.CostWeight, d.OfficialCost, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SettledThrough 返回已结算到哪一天（含，YYYY-MM-DD）；从未结算返回空串。
func (r *UsageDailyRepo) SettledThrough(ctx context.Context) (string, error) {
	var d sql.NullTime
	err := r.db.QueryRowContext(ctx,
		`SELECT settled_through FROM usage_rollup_state WHERE id = ?`, rollupStateID).Scan(&d)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !d.Valid) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return d.Time.Format(dateLayout), nil
}

// SetSettledThrough 推进结算进度（只进不退）。
func (r *UsageDailyRepo) SetSettledThrough(ctx context.Context, date string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO usage_rollup_state (id, settled_through, updated_at) VALUES (?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			settled_through = GREATEST(excluded.settled_through, usage_rollup_state.settled_through),
			updated_at      = excluded.updated_at`, rollupStateID, date, nowUTC())
	return err
}

// 名称/倍率取区间内最新一天的快照：改名后统计页显示当前名，而不是任意一天的旧名。
const (
	latestUserName    = `(array_agg(user_name ORDER BY usage_date DESC))[1]`
	latestGroupName   = `(array_agg(group_name ORDER BY usage_date DESC))[1]`
	latestGroupRate   = `(array_agg(group_rate ORDER BY usage_date DESC))[1]`
	latestAccountName = `(array_agg(account_name ORDER BY usage_date DESC))[1]`
)

// ByAccount 闭区间内按账号上卷（对应实时 AggregateUsageByAccount）。
func (r *UsageDailyRepo) ByAccount(ctx context.Context, startDate, endDate string) ([]AccountUsageRow, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT account_id, `+latestAccountName+`, SUM(requests), SUM(revenue)
		FROM usage_daily WHERE usage_date >= ? AND usage_date <= ?
		GROUP BY account_id`, startDate, endDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AccountUsageRow
	for rows.Next() {
		var a AccountUsageRow
		if err := rows.Scan(&a.AccountID, &a.AccountName, &a.Requests, &a.Revenue); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ByGroupAccount 闭区间内按「分组 × 账号」上卷（对应实时 AggregateUsageByGroupAccount）。
func (r *UsageDailyRepo) ByGroupAccount(ctx context.Context, startDate, endDate string) ([]GroupAccountUsageRow, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT group_id, `+latestGroupName+`, `+latestGroupRate+`,
		       account_id, `+latestAccountName+`,
		       SUM(requests), SUM(revenue), SUM(cost_weight)
		FROM usage_daily WHERE usage_date >= ? AND usage_date <= ?
		GROUP BY group_id, account_id`, startDate, endDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GroupAccountUsageRow
	for rows.Next() {
		var g GroupAccountUsageRow
		if err := rows.Scan(&g.GroupID, &g.GroupName, &g.RateMultiplier, &g.AccountID, &g.AccountName,
			&g.Requests, &g.Revenue, &g.CostWeight); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// ByUserGroupAccount 闭区间内按「用户 × 分组 × 账号」上卷（对应实时 AggregateUsageByUserGroupAccount）。
func (r *UsageDailyRepo) ByUserGroupAccount(ctx context.Context, startDate, endDate string) ([]UserGroupAccountUsageRow, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT user_id, `+latestUserName+`,
		       group_id, `+latestGroupName+`, `+latestGroupRate+`,
		       account_id, `+latestAccountName+`,
		       SUM(requests), SUM(revenue), SUM(cost_weight)
		FROM usage_daily WHERE usage_date >= ? AND usage_date <= ?
		GROUP BY user_id, group_id, account_id`, startDate, endDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserGroupAccountUsageRow
	for rows.Next() {
		var u UserGroupAccountUsageRow
		if err := rows.Scan(&u.UserID, &u.UserName, &u.GroupID, &u.GroupName, &u.RateMultiplier,
			&u.AccountID, &u.AccountName, &u.Requests, &u.Revenue, &u.CostWeight); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// Daily 闭区间内按日上卷（对应实时 AggregateUsageDaily），按日升序。
func (r *UsageDailyRepo) Daily(ctx context.Context, startDate, endDate string) ([]DailyTrendRow, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT usage_date, SUM(requests), SUM(revenue), SUM(official_cost)
		FROM usage_daily WHERE usage_date >= ? AND usage_date <= ?
		GROUP BY usage_date ORDER BY usage_date`, startDate, endDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DailyTrendRow
	for rows.Next() {
		var d DailyTrendRow
		var day time.Time
		if err := rows.Scan(&day, &d.Requests, &d.Revenue, &d.OfficialCost); err != nil {
			return nil, err
		}
		d.Day = day.Format(dateLayout)
		out = append(out, d)
	}
	return out, rows.Err()
}

// ActiveUserIDs 闭区间内有请求的去重用户 id（不含无用户的 0）。
func (r *UsageDailyRepo) ActiveUserIDs(ctx context.Context, startDate, endDate string) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT DISTINCT user_id FROM usage_daily
		WHERE usage_date >= ? AND usage_date <= ? AND user_id <> 0`, startDate, endDate)
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
