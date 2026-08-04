package repository

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strconv"
	"time"
)

// PriceSource 参考价来源。
const (
	PriceSourcePrimary = "primary" // 指定主上游
	PriceSourceLowest  = "lowest"  // 所有数据源中最低
	PriceSourceHighest = "highest" // 所有数据源中最高
	PriceSourceAverage = "average" // 算术平均
)

// MarkupMode 加价方式。
const (
	MarkupFixed      = "fixed"      // 参考价 + markup
	MarkupPercentage = "percentage" // 参考价 × (1 + markup/100)
)

// PricingSource 一个上游数据源引用。
type PricingSource struct {
	ID            int64  `json:"id"`
	PricingID     int64  `json:"pricing_id"`
	ProviderID    int64  `json:"provider_id"`
	UpstreamGroup string `json:"upstream_group"`
}

// LocalGroupPricing 本站分组的调价规则（多上游聚合）。
type LocalGroupPricing struct {
	ID              int64    `json:"id"`
	LocalGroupID    int64    `json:"local_group_id"`
	LocalGroupName  string   `json:"local_group_name"`
	AutoEnabled     bool     `json:"auto_enabled"`
	PriceSource     string   `json:"price_source"`
	PrimaryProvider *int64   `json:"primary_provider_id"`
	PrimaryGroup    *string  `json:"primary_group"`
	MarkupMode      string   `json:"markup_mode"`
	MarkupValue     float64  `json:"markup_value"`
	FollowThreshold float64  `json:"follow_threshold"`
	MinRate         *float64 `json:"min_rate"`
	MaxRate         *float64 `json:"max_rate"`
	LastAppliedRate *float64 `json:"last_applied_rate"`
	Conflict        bool     `json:"conflict"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`

	Sources []PricingSource `json:"sources"`
}

// Reference 按 price_source 聚合参考价。
// rates 的 key 为 "providerID:group"，缺失的数据源直接跳过；无任何可用数据时 ok=false。
func (p *LocalGroupPricing) Reference(rates map[string]float64) (float64, bool) {
	switch p.PriceSource {
	case PriceSourcePrimary:
		if p.PrimaryProvider == nil || p.PrimaryGroup == nil {
			return 0, false
		}
		v, ok := rates[SourceKey(*p.PrimaryProvider, *p.PrimaryGroup)]
		return v, ok
	default:
		var vals []float64
		for _, s := range p.Sources {
			if v, ok := rates[SourceKey(s.ProviderID, s.UpstreamGroup)]; ok {
				vals = append(vals, v)
			}
		}
		if len(vals) == 0 {
			return 0, false
		}
		switch p.PriceSource {
		case PriceSourceLowest:
			m := vals[0]
			for _, v := range vals[1:] {
				if v < m {
					m = v
				}
			}
			return m, true
		case PriceSourceHighest:
			m := vals[0]
			for _, v := range vals[1:] {
				if v > m {
					m = v
				}
			}
			return m, true
		case PriceSourceAverage:
			var sum float64
			for _, v := range vals {
				sum += v
			}
			return sum / float64(len(vals)), true
		}
		return 0, false
	}
}

// Target 由参考价算目标倍率：加价 → 夹紧 → 四舍五入 4 位小数。
func (p *LocalGroupPricing) Target(ref float64) float64 {
	var v float64
	if p.MarkupMode == MarkupFixed {
		v = ref + p.MarkupValue
	} else {
		v = ref * (1 + p.MarkupValue/100)
	}
	if p.MinRate != nil && v < *p.MinRate {
		v = *p.MinRate
	}
	if p.MaxRate != nil && v > *p.MaxRate {
		v = *p.MaxRate
	}
	return math.Round(v*10000) / 10000
}

// SourceKey 构造 rates map 的键。
func SourceKey(providerID int64, group string) string {
	return strconv.FormatInt(providerID, 10) + ":" + group
}

// RateAction 一次调价动作审计。
type RateAction struct {
	ID        int64     `json:"id"`
	PricingID int64     `json:"pricing_id"`
	TriggerBy string    `json:"trigger_by"`
	OldRate   *float64  `json:"old_rate"`
	NewRate   float64   `json:"new_rate"`
	Status    string    `json:"status"`
	Error     *string   `json:"error"`
	CreatedAt time.Time `json:"created_at"`
}

// PricingRepo 调价规则与审计存储。
type PricingRepo struct {
	db *sql.DB
}

// NewPricingRepo 创建 PricingRepo。
func NewPricingRepo(s *SQLite) *PricingRepo { return &PricingRepo{db: s.DB()} }

const pricingCols = `id, local_group_id, local_group_name, auto_enabled, price_source,
	primary_provider_id, primary_group, markup_mode, markup_value, follow_threshold,
	min_rate, max_rate, last_applied_rate, conflict, created_at, updated_at`

func scanPricing(row interface{ Scan(...any) error }) (*LocalGroupPricing, error) {
	var p LocalGroupPricing
	var autoEnabled, conflict int
	var primaryProvider sql.NullInt64
	var primaryGroup sql.NullString
	var minRate, maxRate, lastApplied sql.NullFloat64
	var createdAt, updatedAt string
	if err := row.Scan(&p.ID, &p.LocalGroupID, &p.LocalGroupName, &autoEnabled, &p.PriceSource,
		&primaryProvider, &primaryGroup, &p.MarkupMode, &p.MarkupValue, &p.FollowThreshold,
		&minRate, &maxRate, &lastApplied, &conflict, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	p.AutoEnabled = autoEnabled != 0
	p.Conflict = conflict != 0
	if primaryProvider.Valid {
		v := primaryProvider.Int64
		p.PrimaryProvider = &v
	}
	if primaryGroup.Valid {
		v := primaryGroup.String
		p.PrimaryGroup = &v
	}
	if minRate.Valid {
		v := minRate.Float64
		p.MinRate = &v
	}
	if maxRate.Valid {
		v := maxRate.Float64
		p.MaxRate = &v
	}
	if lastApplied.Valid {
		v := lastApplied.Float64
		p.LastAppliedRate = &v
	}
	p.CreatedAt = parseTime(createdAt)
	p.UpdatedAt = parseTime(updatedAt)
	return &p, nil
}

// List 返回全部规则（含数据源）。
func (r *PricingRepo) List(ctx context.Context) ([]*LocalGroupPricing, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+pricingCols+` FROM local_group_pricing ORDER BY local_group_name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*LocalGroupPricing
	byID := make(map[int64]*LocalGroupPricing)
	for rows.Next() {
		p, err := scanPricing(rows)
		if err != nil {
			return nil, err
		}
		p.Sources = []PricingSource{}
		out = append(out, p)
		byID[p.ID] = p
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, r.attachSources(ctx, byID)
}

// attachSources 批量加载数据源，避免 N+1。
func (r *PricingRepo) attachSources(ctx context.Context, byID map[int64]*LocalGroupPricing) error {
	if len(byID) == 0 {
		return nil
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id, pricing_id, provider_id, upstream_group FROM pricing_sources`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var s PricingSource
		if err := rows.Scan(&s.ID, &s.PricingID, &s.ProviderID, &s.UpstreamGroup); err != nil {
			return err
		}
		if p, ok := byID[s.PricingID]; ok {
			p.Sources = append(p.Sources, s)
		}
	}
	return rows.Err()
}

// GetByID 按 ID 查询（含数据源）。
func (r *PricingRepo) GetByID(ctx context.Context, id int64) (*LocalGroupPricing, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+pricingCols+` FROM local_group_pricing WHERE id = ?`, id)
	p, err := scanPricing(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p.Sources = []PricingSource{}
	return p, r.attachSources(ctx, map[int64]*LocalGroupPricing{p.ID: p})
}

// GetByLocalGroup 按本站分组 id 查询；不存在返回 ErrNotFound。
func (r *PricingRepo) GetByLocalGroup(ctx context.Context, localGroupID int64) (*LocalGroupPricing, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+pricingCols+` FROM local_group_pricing WHERE local_group_id = ?`, localGroupID)
	p, err := scanPricing(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p.Sources = []PricingSource{}
	return p, r.attachSources(ctx, map[int64]*LocalGroupPricing{p.ID: p})
}

// ListByUpstream 返回引用了指定上游分组的全部规则（自动调价按此匹配）。
func (r *PricingRepo) ListByUpstream(ctx context.Context, providerID int64, group string) ([]*LocalGroupPricing, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+pricingCols+` FROM local_group_pricing
		WHERE id IN (SELECT pricing_id FROM pricing_sources WHERE provider_id = ? AND upstream_group = ?)`,
		providerID, group)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*LocalGroupPricing
	byID := make(map[int64]*LocalGroupPricing)
	for rows.Next() {
		p, err := scanPricing(rows)
		if err != nil {
			return nil, err
		}
		p.Sources = []PricingSource{}
		out = append(out, p)
		byID[p.ID] = p
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, r.attachSources(ctx, byID)
}

// PricingParams 新建/编辑规则参数。
type PricingParams struct {
	LocalGroupID    int64
	LocalGroupName  string
	AutoEnabled     bool
	PriceSource     string
	PrimaryProvider *int64
	PrimaryGroup    *string
	MarkupMode      string
	MarkupValue     float64
	FollowThreshold float64
	MinRate         *float64
	MaxRate         *float64
	Sources         []PricingSource // 只用 ProviderID + UpstreamGroup
}

// Upsert 按本站分组新建或更新规则（含数据源全量替换），返回最新规则。
func (r *PricingRepo) Upsert(ctx context.Context, p PricingParams) (*LocalGroupPricing, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	autoEnabled := 0
	if p.AutoEnabled {
		autoEnabled = 1
	}
	if p.PriceSource == "" {
		p.PriceSource = PriceSourcePrimary
	}
	if p.MarkupMode == "" {
		p.MarkupMode = MarkupPercentage
	}

	var id int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM local_group_pricing WHERE local_group_id = ?`, p.LocalGroupID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		res, insErr := tx.ExecContext(ctx, `
			INSERT INTO local_group_pricing (local_group_id, local_group_name, auto_enabled, price_source,
				primary_provider_id, primary_group, markup_mode, markup_value, follow_threshold,
				min_rate, max_rate, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			p.LocalGroupID, p.LocalGroupName, autoEnabled, p.PriceSource,
			p.PrimaryProvider, p.PrimaryGroup, p.MarkupMode, p.MarkupValue, p.FollowThreshold,
			p.MinRate, p.MaxRate, nowUTC(), nowUTC())
		if insErr != nil {
			return nil, insErr
		}
		if id, err = res.LastInsertId(); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	} else {
		if _, err := tx.ExecContext(ctx, `
			UPDATE local_group_pricing SET local_group_name=?, auto_enabled=?, price_source=?,
				primary_provider_id=?, primary_group=?, markup_mode=?, markup_value=?, follow_threshold=?,
				min_rate=?, max_rate=?, updated_at=?
			WHERE id=?`,
			p.LocalGroupName, autoEnabled, p.PriceSource,
			p.PrimaryProvider, p.PrimaryGroup, p.MarkupMode, p.MarkupValue, p.FollowThreshold,
			p.MinRate, p.MaxRate, nowUTC(), id); err != nil {
			return nil, err
		}
	}

	// 数据源全量替换
	if _, err := tx.ExecContext(ctx, `DELETE FROM pricing_sources WHERE pricing_id = ?`, id); err != nil {
		return nil, err
	}
	for _, s := range p.Sources {
		if s.UpstreamGroup == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO pricing_sources (pricing_id, provider_id, upstream_group) VALUES (?,?,?)`,
			id, s.ProviderID, s.UpstreamGroup); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r.GetByID(ctx, id)
}

// Delete 删除规则及其数据源与审计。
func (r *PricingRepo) Delete(ctx context.Context, id int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM rate_actions WHERE pricing_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM pricing_sources WHERE pricing_id = ?`, id); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM local_group_pricing WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

// SetApplied 应用成功：记录基准值。
func (r *PricingRepo) SetApplied(ctx context.Context, id int64, rate float64) error {
	_, err := r.db.ExecContext(ctx, `UPDATE local_group_pricing SET last_applied_rate=?, updated_at=? WHERE id=?`,
		rate, nowUTC(), id)
	return err
}

// SetConflict 检测到人工改动：标记冲突并停止自动覆盖。
func (r *PricingRepo) SetConflict(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `UPDATE local_group_pricing SET conflict=1, updated_at=? WHERE id=?`, nowUTC(), id)
	return err
}

// ResolveConflict 人工确认后清除冲突（以当前值为新基准）。
func (r *PricingRepo) ResolveConflict(ctx context.Context, id int64, currentRate float64) error {
	_, err := r.db.ExecContext(ctx, `UPDATE local_group_pricing SET conflict=0, last_applied_rate=?, updated_at=? WHERE id=?`,
		currentRate, nowUTC(), id)
	return err
}

// InsertAction 写入调价审计（返回 action id）。
func (r *PricingRepo) InsertAction(ctx context.Context, a RateAction) (int64, error) {
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO rate_actions (pricing_id, trigger_by, old_rate, new_rate, status, error, created_at)
		VALUES (?,?,?,?,?,?,?)`,
		a.PricingID, a.TriggerBy, a.OldRate, a.NewRate, a.Status, a.Error, nowUTC())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateActionStatus 更新审计状态（pending → applied/failed）。
func (r *PricingRepo) UpdateActionStatus(ctx context.Context, id int64, status string, errMsg *string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE rate_actions SET status=?, error=? WHERE id=?`, status, errMsg, id)
	return err
}

// Actions 返回某规则的审计历史（倒序）。
func (r *PricingRepo) Actions(ctx context.Context, pricingID int64, limit int) ([]*RateAction, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, pricing_id, trigger_by, old_rate, new_rate, status, error, created_at
		FROM rate_actions WHERE pricing_id = ? ORDER BY created_at DESC, id DESC LIMIT ?`, pricingID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*RateAction
	for rows.Next() {
		var a RateAction
		var oldRate sql.NullFloat64
		var errMsg sql.NullString
		var createdAt string
		if err := rows.Scan(&a.ID, &a.PricingID, &a.TriggerBy, &oldRate, &a.NewRate, &a.Status, &errMsg, &createdAt); err != nil {
			return nil, err
		}
		if oldRate.Valid {
			v := oldRate.Float64
			a.OldRate = &v
		}
		if errMsg.Valid {
			e := errMsg.String
			a.Error = &e
		}
		a.CreatedAt = parseTime(createdAt)
		out = append(out, &a)
	}
	return out, rows.Err()
}

// DeleteByProvider 供应商删除时清理其作为数据源的引用；
// 规则本身保留（可能还引用了其它上游），但会丢失该数据源。
func (r *PricingRepo) DeleteByProvider(ctx context.Context, providerID int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM pricing_sources WHERE provider_id = ?`, providerID)
	return err
}

// MappedUpstreams 返回已被引用的上游分组集合（分组倍率页展示「已对接」用）。
// key = "providerID:group"。
func (r *PricingRepo) MappedUpstreams(ctx context.Context) (map[string]bool, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT DISTINCT provider_id, upstream_group FROM pricing_sources`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]bool)
	for rows.Next() {
		var pid int64
		var g string
		if err := rows.Scan(&pid, &g); err != nil {
			return nil, err
		}
		out[SourceKey(pid, g)] = true
	}
	return out, rows.Err()
}
