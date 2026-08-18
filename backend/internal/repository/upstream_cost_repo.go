package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// UpstreamKeyCost 上游某 key 某一天的真实成本。
type UpstreamKeyCost struct {
	ProviderID    int64
	UpstreamKeyID int64
	KeyName       string
	AccountID     *int64
	UsageDate     string // YYYY-MM-DD
	ActualCost    float64
	OfficialCost  float64
	Requests      int64
}

// UpstreamKeyMapping 上游 key ↔ 本站账号映射。
type UpstreamKeyMapping struct {
	ProviderID     int64
	UpstreamKeyID  int64
	KeyName        string
	KeyFingerprint string
	AccountID      *int64
	AccountName    string
	RateMultiplier *float64
	Status         string
	GroupName      string
}

// CostSyncState 供应商成本同步状态。
type CostSyncState struct {
	ProviderID   int64      `json:"provider_id"`
	LastSyncedAt *time.Time `json:"last_synced_at"`
	LastError    *string    `json:"last_error"`
	KeysTotal    int64      `json:"keys_total"`
	KeysMatched  int64      `json:"keys_matched"`
	BackfilledAt *time.Time `json:"backfilled_at"`
}

// UpstreamCostRepo 上游真实成本存储。
type UpstreamCostRepo struct {
	db *DB
}

// NewUpstreamCostRepo 创建 UpstreamCostRepo。
func NewUpstreamCostRepo(s *Store) *UpstreamCostRepo { return &UpstreamCostRepo{db: s.DB()} }

// UpsertCosts 按 (provider_id, upstream_key_id, usage_date) 幂等写入成本。
// 同一天重复采集覆写而非追加，使定时同步与历史回补可共用一张表。
func (r *UpstreamCostRepo) UpsertCosts(ctx context.Context, costs []UpstreamKeyCost) error {
	if len(costs) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO upstream_key_costs
			(provider_id, upstream_key_id, key_name, account_id, usage_date, actual_cost, official_cost, requests, synced_at)
		VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT(provider_id, upstream_key_id, usage_date) DO UPDATE SET
			key_name      = excluded.key_name,
			account_id    = excluded.account_id,
			actual_cost   = excluded.actual_cost,
			official_cost = GREATEST(excluded.official_cost, upstream_key_costs.official_cost),
			requests      = GREATEST(excluded.requests, upstream_key_costs.requests),
			synced_at     = excluded.synced_at`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	now := nowUTC()
	for _, c := range costs {
		if _, err := stmt.ExecContext(ctx, c.ProviderID, c.UpstreamKeyID, c.KeyName, c.AccountID,
			c.UsageDate, c.ActualCost, c.OfficialCost, c.Requests, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UpsertMappings 幂等写入 key ↔ 账号映射。
func (r *UpstreamCostRepo) UpsertMappings(ctx context.Context, maps []UpstreamKeyMapping) error {
	if len(maps) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO upstream_key_map
			(provider_id, upstream_key_id, key_name, key_fingerprint, account_id, account_name, rate_multiplier, status, group_name, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(provider_id, upstream_key_id) DO UPDATE SET
			key_name        = excluded.key_name,
			key_fingerprint = excluded.key_fingerprint,
			account_id      = excluded.account_id,
			account_name    = excluded.account_name,
			rate_multiplier = excluded.rate_multiplier,
			status          = excluded.status,
			group_name      = excluded.group_name,
			updated_at      = excluded.updated_at`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	now := nowUTC()
	for _, m := range maps {
		if _, err := stmt.ExecContext(ctx, m.ProviderID, m.UpstreamKeyID, m.KeyName, m.KeyFingerprint,
			m.AccountID, m.AccountName, m.RateMultiplier, m.Status, m.GroupName, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// actualCostExpr 自营站剔除后的实扣求和表达式（三个聚合函数共用，口径必须一致）。
//
// 自营站的上游实扣是左手倒右手，不是真实支出，故置 0。用 CASE 置零而非 WHERE
// 过滤行：过滤会让账号从结果 map 里消失，StatsService 随即判成 CostMatched=false，
// 触发前端「成本不完整、利润被高估 ⚠」告警。自营站成本为 0 是有意为之，不是数据缺失。
//
// official_cost 不做剔除：它是官价对照口径，与「这笔钱付给了谁」无关。
const actualCostExpr = `COALESCE(SUM(CASE WHEN p.self_operated THEN 0 ELSE c.actual_cost END),0)`

// AccountCost 账号在查询区间内的真实成本汇总。
type AccountCost struct {
	AccountID    int64
	ActualCost   float64
	OfficialCost float64
}

// CostByAccount 返回 [startDate, endDate] 内按本站账号汇总的真实成本（仅已匹配账号）。
// 日期为闭区间的 YYYY-MM-DD。自营站实扣计 0，见 actualCostExpr。
func (r *UpstreamCostRepo) CostByAccount(ctx context.Context, startDate, endDate string) (map[int64]AccountCost, error) {
	// JOIN 而非 LEFT JOIN：provider_id 有 FK CASCADE，孤儿行不存在
	rows, err := r.db.QueryContext(ctx, `
		SELECT c.account_id, `+actualCostExpr+`, COALESCE(SUM(c.official_cost),0)
		FROM upstream_key_costs c
		JOIN providers p ON p.id = c.provider_id
		WHERE c.account_id IS NOT NULL AND c.usage_date >= ? AND c.usage_date <= ?
		GROUP BY c.account_id`, startDate, endDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]AccountCost)
	for rows.Next() {
		var c AccountCost
		if err := rows.Scan(&c.AccountID, &c.ActualCost, &c.OfficialCost); err != nil {
			return nil, err
		}
		out[c.AccountID] = c
	}
	return out, rows.Err()
}

// DailyCost 某一天的真实成本汇总。
type DailyCost struct {
	UsageDate    string
	ActualCost   float64
	OfficialCost float64
}

// CostByDay 返回 [startDate, endDate] 内按天汇总的真实成本（全部 key，含未匹配）。
// 自营站实扣计 0，见 actualCostExpr。
func (r *UpstreamCostRepo) CostByDay(ctx context.Context, startDate, endDate string) (map[string]DailyCost, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT c.usage_date, `+actualCostExpr+`, COALESCE(SUM(c.official_cost),0)
		FROM upstream_key_costs c
		JOIN providers p ON p.id = c.provider_id
		WHERE c.usage_date >= ? AND c.usage_date <= ?
		GROUP BY c.usage_date`, startDate, endDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]DailyCost)
	for rows.Next() {
		var d DailyCost
		// usage_date 是 DATE，pgx 返回 time.Time；对外仍是 YYYY-MM-DD 字符串。
		var usageDate time.Time
		if err := rows.Scan(&usageDate, &d.ActualCost, &d.OfficialCost); err != nil {
			return nil, err
		}
		d.UsageDate = usageDate.Format(dateLayout)
		out[d.UsageDate] = d
	}
	return out, rows.Err()
}

// ProviderCost 供应商在区间内的真实成本汇总。
type ProviderCost struct {
	ProviderID   int64
	ActualCost   float64
	OfficialCost float64
}

// CostByProvider 返回 [startDate, endDate] 内按供应商汇总的真实成本。
// 含未匹配到本站账号的 key，故供应商总额不等于其账号明细之和。
// 自营站实扣计 0，见 actualCostExpr。
func (r *UpstreamCostRepo) CostByProvider(ctx context.Context, startDate, endDate string) (map[int64]ProviderCost, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT c.provider_id, `+actualCostExpr+`, COALESCE(SUM(c.official_cost),0)
		FROM upstream_key_costs c
		JOIN providers p ON p.id = c.provider_id
		WHERE c.usage_date >= ? AND c.usage_date <= ?
		GROUP BY c.provider_id`, startDate, endDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]ProviderCost)
	for rows.Next() {
		var c ProviderCost
		if err := rows.Scan(&c.ProviderID, &c.ActualCost, &c.OfficialCost); err != nil {
			return nil, err
		}
		out[c.ProviderID] = c
	}
	return out, rows.Err()
}

// KeyCostRow 上游 key 成本明细（供应商详情展示）。
type KeyCostRow struct {
	UpstreamKeyID  int64    `json:"upstream_key_id"`
	KeyName        string   `json:"key_name"`
	AccountID      *int64   `json:"account_id"`
	AccountName    string   `json:"account_name"`
	RateMultiplier *float64 `json:"rate_multiplier"`
	ActualCost     float64  `json:"actual_cost"`
	OfficialCost   float64  `json:"official_cost"`
	Matched        bool     `json:"matched"`
}

// KeyCosts 返回某供应商在区间内的 per-key 成本明细（按实扣降序）。
func (r *UpstreamCostRepo) KeyCosts(ctx context.Context, providerID int64, startDate, endDate string) ([]KeyCostRow, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT c.upstream_key_id, c.key_name, c.account_id,
		       COALESCE(m.account_name,''), m.rate_multiplier,
		       COALESCE(SUM(c.actual_cost),0), COALESCE(SUM(c.official_cost),0)
		FROM upstream_key_costs c
		LEFT JOIN upstream_key_map m
		       ON m.provider_id = c.provider_id AND m.upstream_key_id = c.upstream_key_id
		WHERE c.provider_id = ? AND c.usage_date >= ? AND c.usage_date <= ?
		GROUP BY c.upstream_key_id, c.key_name, c.account_id, m.account_name, m.rate_multiplier
		ORDER BY 6 DESC`, providerID, startDate, endDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KeyCostRow
	for rows.Next() {
		var k KeyCostRow
		var accID sql.NullInt64
		var rate sql.NullFloat64
		if err := rows.Scan(&k.UpstreamKeyID, &k.KeyName, &accID, &k.AccountName, &rate,
			&k.ActualCost, &k.OfficialCost); err != nil {
			return nil, err
		}
		if accID.Valid {
			id := accID.Int64
			k.AccountID = &id
			k.Matched = true
		}
		if rate.Valid {
			v := rate.Float64
			k.RateMultiplier = &v
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// MappedGroupHit 成本映射里「已匹配到本站账号」的一行。
type MappedGroupHit struct {
	AccountID   int64
	AccountName string
}

// MappedAccountsByGroup 上次成本同步留下的「上游分组 → 本站账号」。
// 只认有 group_name 且已匹配到账号的行；旧数据没分组名的不会出现。
func (r *UpstreamCostRepo) MappedAccountsByGroup(ctx context.Context, providerID int64) (map[string][]MappedGroupHit, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT group_name, account_id, COALESCE(account_name,'')
		FROM upstream_key_map
		WHERE provider_id = ? AND account_id IS NOT NULL AND COALESCE(group_name,'') <> ''
		ORDER BY group_name, account_id`, providerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]MappedGroupHit{}
	seen := map[string]map[int64]struct{}{}
	for rows.Next() {
		var group string
		var accID int64
		var accName string
		if err := rows.Scan(&group, &accID, &accName); err != nil {
			return nil, err
		}
		if seen[group] == nil {
			seen[group] = map[int64]struct{}{}
		}
		if _, ok := seen[group][accID]; ok {
			continue
		}
		seen[group][accID] = struct{}{}
		out[group] = append(out[group], MappedGroupHit{AccountID: accID, AccountName: accName})
	}
	return out, rows.Err()
}

func (r *UpstreamCostRepo) SaveSyncState(ctx context.Context, st CostSyncState) error {
	var syncedAt any
	if st.LastSyncedAt != nil {
		syncedAt = st.LastSyncedAt.UTC().Format(time.RFC3339)
	}
	var backfilledAt any
	if st.BackfilledAt != nil {
		backfilledAt = st.BackfilledAt.UTC().Format(time.RFC3339)
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO cost_sync_state
			(provider_id, last_synced_at, last_error, keys_total, keys_matched, backfilled_at, updated_at)
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(provider_id) DO UPDATE SET
			last_synced_at = excluded.last_synced_at,
			last_error     = excluded.last_error,
			keys_total     = excluded.keys_total,
			keys_matched   = excluded.keys_matched,
			backfilled_at  = COALESCE(excluded.backfilled_at, cost_sync_state.backfilled_at),
			updated_at     = excluded.updated_at`,
		st.ProviderID, syncedAt, st.LastError, st.KeysTotal, st.KeysMatched, backfilledAt, nowUTC())
	return err
}

// SyncStates 返回所有供应商的同步状态。
func (r *UpstreamCostRepo) SyncStates(ctx context.Context) (map[int64]CostSyncState, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT provider_id, last_synced_at, last_error, keys_total, keys_matched, backfilled_at
		FROM cost_sync_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]CostSyncState)
	for rows.Next() {
		var st CostSyncState
		var syncedAt, backfilledAt sql.NullTime
		var errStr sql.NullString
		if err := rows.Scan(&st.ProviderID, &syncedAt, &errStr, &st.KeysTotal, &st.KeysMatched, &backfilledAt); err != nil {
			return nil, err
		}
		st.LastSyncedAt = timePtr(syncedAt)
		if errStr.Valid {
			e := errStr.String
			st.LastError = &e
		}
		st.BackfilledAt = timePtr(backfilledAt)
		out[st.ProviderID] = st
	}
	return out, rows.Err()
}

// NeedsBackfill 返回尚未完成历史回补的供应商 id 集合。
func (r *UpstreamCostRepo) NeedsBackfill(ctx context.Context, providerIDs []int64) (map[int64]bool, error) {
	out := make(map[int64]bool, len(providerIDs))
	for _, id := range providerIDs {
		out[id] = true
	}
	if len(providerIDs) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(providerIDs))
	args := make([]any, len(providerIDs))
	for i, id := range providerIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	query := fmt.Sprintf(
		`SELECT provider_id FROM cost_sync_state WHERE backfilled_at IS NOT NULL AND provider_id IN (%s)`,
		strings.Join(placeholders, ","))
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = false
	}
	return out, rows.Err()
}

// DeleteOlderThan 清理 date 之前的成本记录，返回删除行数。
func (r *UpstreamCostRepo) DeleteOlderThan(ctx context.Context, beforeDate string) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM upstream_key_costs WHERE usage_date < ?`, beforeDate)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
