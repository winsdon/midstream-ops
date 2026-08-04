package repository

import (
	"context"
	"database/sql"
	"time"
)

// RateSnapshot 一行倍率快照：每行代表一次「真实的倍率状态」。
// first_seen_at = 该倍率首次出现（变化时刻）；last_seen_at = 最后一次确认。
type RateSnapshot struct {
	ID         int64      `json:"id"`
	Scope      string     `json:"scope"`       // local | upstream
	ProviderID int64      `json:"provider_id"` // upstream 时有效
	EntityType string     `json:"entity_type"` // group | account
	EntityID   string     `json:"entity_id"`
	Name       string     `json:"name"`
	Rate       float64    `json:"rate"`
	// Platform 分组所属平台（anthropic|openai|gemini|...），空串表示上游未提供。
	// 仅作展示分类用，不参与变化判定（见 RateService.Reconcile）。
	Platform    string    `json:"platform"`
	FirstSeenAt time.Time `json:"first_seen_at"`
	LastSeenAt time.Time  `json:"last_seen_at"`
	Deleted    bool       `json:"deleted"`

	// PrevRate 上一次不同的倍率（查询时由 LAG 推导，非表列）。
	PrevRate *float64 `json:"prev_rate,omitempty"`
}

// RateRepo 倍率快照存储（变更驱动：insert-on-change + touch-on-same + mark-deleted）。
type RateRepo struct {
	db *sql.DB
}

// NewRateRepo 创建 RateRepo。
func NewRateRepo(s *SQLite) *RateRepo { return &RateRepo{db: s.DB()} }

// CurrentRow 某实体的最新一行（diff 基准）。
type CurrentRow struct {
	ID      int64
	Name    string
	Rate    float64
	Deleted bool
}

// CurrentRows 返回 (scope, provider, entityType) 下每个实体的最新一行。
// key = entity_type + ":" + entity_id。
//
// 必须按 entityType 过滤：group 与 account 分两次 Reconcile，若返回全类型行，
// 另一类型会被误判为「本轮未出现」而标记 deleted，下一轮又「复活」插新行，
// 两者每轮互踩导致快照表疯长。
func (r *RateRepo) CurrentRows(ctx context.Context, scope string, providerID int64, entityType string) (map[string]CurrentRow, error) {
	// 每实体按 first_seen_at 最大取一行（id 最大兜底同刻并列）
	rows, err := r.db.QueryContext(ctx, `
		SELECT entity_type, entity_id, id, name, rate, deleted FROM rate_snapshots rs
		WHERE scope=? AND provider_id=? AND entity_type=? AND id = (
			SELECT id FROM rate_snapshots
			WHERE scope=rs.scope AND provider_id=rs.provider_id
			  AND entity_type=rs.entity_type AND entity_id=rs.entity_id
			ORDER BY first_seen_at DESC, id DESC LIMIT 1
		)`, scope, providerID, entityType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]CurrentRow)
	for rows.Next() {
		var et, eid string
		var row CurrentRow
		var deleted int
		if err := rows.Scan(&et, &eid, &row.ID, &row.Name, &row.Rate, &deleted); err != nil {
			return nil, err
		}
		row.Deleted = deleted != 0
		out[et+":"+eid] = row
	}
	return out, rows.Err()
}

// Insert 插入新快照行（新实体 / 倍率变化 / 删除后复活）。
func (r *RateRepo) Insert(ctx context.Context, scope string, providerID int64, entityType, entityID, name string, rate float64, platform string) error {
	now := nowUTC()
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO rate_snapshots (scope, provider_id, entity_type, entity_id, name, rate, platform, first_seen_at, last_seen_at, deleted)
		VALUES (?,?,?,?,?,?,?,?,?,0)`,
		scope, providerID, entityType, entityID, name, rate, platform, now, now)
	return err
}

// Touch 倍率未变：延长 last_seen_at（顺带同步名称与平台）。
//
// platform 与 name 一样是「随行描述」，上游改标签时就地更新即可；
// 若让它触发插行，历史时间线会出现 1.0 → 1.0 的假变化。
func (r *RateRepo) Touch(ctx context.Context, id int64, name, platform string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE rate_snapshots SET last_seen_at=?, name=?, platform=? WHERE id=?`, nowUTC(), name, platform, id)
	return err
}

// MarkDeleted 标记实体已从上游消失。
func (r *RateRepo) MarkDeleted(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `UPDATE rate_snapshots SET deleted=1, last_seen_at=? WHERE id=?`, nowUTC(), id)
	return err
}

// SnapshotFilter 快照查询过滤。
type SnapshotFilter struct {
	Scope      string // local | upstream | ""（全部）
	ProviderID *int64
	EntityType string
	EntityID   string
	Page       int
	PageSize   int
}

// History 分页查询倍率变化历史（新行即变化；LAG 推导 prev_rate）。
// 只返回「构成变化」的行：每实体首行（prev 为 NULL）也返回，前端可选择过滤。
func (r *RateRepo) History(ctx context.Context, f SnapshotFilter) ([]*RateSnapshot, int64, error) {
	where := "1=1"
	args := []any{}
	if f.Scope != "" {
		where += " AND scope = ?"
		args = append(args, f.Scope)
	}
	if f.ProviderID != nil {
		where += " AND provider_id = ?"
		args = append(args, *f.ProviderID)
	}
	if f.EntityType != "" {
		where += " AND entity_type = ?"
		args = append(args, f.EntityType)
	}
	if f.EntityID != "" {
		where += " AND entity_id = ?"
		args = append(args, f.EntityID)
	}

	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM rate_snapshots WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 || f.PageSize > 1000 {
		f.PageSize = 20
	}
	offset := (f.Page - 1) * f.PageSize

	// LAG 按实体分区推导上一次倍率
	query := `
		SELECT id, scope, provider_id, entity_type, entity_id, name, rate, platform, first_seen_at, last_seen_at, deleted, prev_rate FROM (
			SELECT rs.*, LAG(rate) OVER (
				PARTITION BY scope, provider_id, entity_type, entity_id ORDER BY first_seen_at, id
			) AS prev_rate
			FROM rate_snapshots rs WHERE ` + where + `
		) ORDER BY first_seen_at DESC, id DESC LIMIT ? OFFSET ?`
	args = append(args, f.PageSize, offset)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*RateSnapshot
	for rows.Next() {
		s, err := scanRateSnapshot(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, s)
	}
	return out, total, rows.Err()
}

// CurrentList 返回 (scope[, provider]) 当前生效的全部倍率（含 prev_rate 与生效时长）。
// 分组倍率页主列表用。
func (r *RateRepo) CurrentList(ctx context.Context, scope string, providerID *int64, includeDeleted bool) ([]*RateSnapshot, error) {
	where := "scope = ?"
	args := []any{scope}
	if providerID != nil {
		where += " AND provider_id = ?"
		args = append(args, *providerID)
	}
	delCond := ""
	if !includeDeleted {
		delCond = " AND deleted = 0"
	}
	// 取每实体最新行 + LAG 推导 prev
	query := `
		SELECT id, scope, provider_id, entity_type, entity_id, name, rate, platform, first_seen_at, last_seen_at, deleted, prev_rate FROM (
			SELECT rs.*,
				LAG(rate) OVER (PARTITION BY scope, provider_id, entity_type, entity_id ORDER BY first_seen_at, id) AS prev_rate,
				ROW_NUMBER() OVER (PARTITION BY scope, provider_id, entity_type, entity_id ORDER BY first_seen_at DESC, id DESC) AS rn
			FROM rate_snapshots rs WHERE ` + where + `
		) WHERE rn = 1` + delCond + ` ORDER BY provider_id, entity_type, name COLLATE NOCASE`

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*RateSnapshot
	for rows.Next() {
		s, err := scanRateSnapshot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func scanRateSnapshot(rows *sql.Rows) (*RateSnapshot, error) {
	var s RateSnapshot
	var firstSeen, lastSeen string
	var deleted int
	var prev sql.NullFloat64
	if err := rows.Scan(&s.ID, &s.Scope, &s.ProviderID, &s.EntityType, &s.EntityID, &s.Name, &s.Rate, &s.Platform,
		&firstSeen, &lastSeen, &deleted, &prev); err != nil {
		return nil, err
	}
	s.FirstSeenAt = parseTime(firstSeen)
	s.LastSeenAt = parseTime(lastSeen)
	s.Deleted = deleted != 0
	if prev.Valid {
		v := prev.Float64
		s.PrevRate = &v
	}
	return &s, nil
}

// DeleteOlderThan 清理 retention 之前的「已封存」历史行。
// 保护每实体的当前行（最新一行）不被删，即使它很老 —— 当前倍率必须始终可查。
func (r *RateRepo) DeleteOlderThan(ctx context.Context, before time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, `
		DELETE FROM rate_snapshots WHERE last_seen_at < ? AND id NOT IN (
			SELECT id FROM (
				SELECT id, ROW_NUMBER() OVER (
					PARTITION BY scope, provider_id, entity_type, entity_id ORDER BY first_seen_at DESC, id DESC
				) AS rn FROM rate_snapshots
			) WHERE rn = 1
		)`, before.UTC().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteByProvider 供应商删除时清理其上游快照。
func (r *RateRepo) DeleteByProvider(ctx context.Context, providerID int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM rate_snapshots WHERE scope='upstream' AND provider_id=?`, providerID)
	return err
}
