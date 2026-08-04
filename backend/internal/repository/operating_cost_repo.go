package repository

import (
	"context"
	"database/sql"
	"errors"
)

// OperatingCost 自营站的一笔运营成本（买账号、订阅费、服务器等）。
//
// 与 upstream_key_costs 的区别：那张表是上游按 key 记录的变动成本（自动同步），
// 本表是站点级固定成本（人工录入），两者在同一时间轴上相加构成真实总成本。
type OperatingCost struct {
	ID         int64
	ProviderID int64
	Category   string
	Amount     float64
	Currency   string
	OccurredOn string // YYYY-MM-DD（本地时区日历日）
	Note       string
	Operator   string
	CreatedAt  string
}

// OperatingCostParams 新建运营成本参数（校验由 service 层单一入口完成）。
type OperatingCostParams struct {
	ProviderID int64
	Category   string
	Amount     float64
	OccurredOn string
	Note       string
	Operator   string
}

// OperatingCostRepo 运营成本存储。
type OperatingCostRepo struct {
	db *sql.DB
}

// NewOperatingCostRepo 创建 OperatingCostRepo。
func NewOperatingCostRepo(s *SQLite) *OperatingCostRepo {
	return &OperatingCostRepo{db: s.DB()}
}

const operatingCostCols = `id, provider_id, category, amount, currency, occurred_on, note, operator, created_at`

func scanOperatingCost(row interface{ Scan(...any) error }) (*OperatingCost, error) {
	var c OperatingCost
	err := row.Scan(&c.ID, &c.ProviderID, &c.Category, &c.Amount, &c.Currency,
		&c.OccurredOn, &c.Note, &c.Operator, &c.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// ListByProvider 返回某站点在 [startDate, endDate] 内的明细（发生日降序，同日按录入时间降序）。
// 日期为闭区间的 YYYY-MM-DD；两者留空表示不限区间。
func (r *OperatingCostRepo) ListByProvider(ctx context.Context, providerID int64, startDate, endDate string) ([]OperatingCost, error) {
	query := `SELECT ` + operatingCostCols + ` FROM provider_operating_costs WHERE provider_id = ?`
	args := []any{providerID}
	if startDate != "" {
		query += ` AND occurred_on >= ?`
		args = append(args, startDate)
	}
	if endDate != "" {
		query += ` AND occurred_on <= ?`
		args = append(args, endDate)
	}
	query += ` ORDER BY occurred_on DESC, id DESC`

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []OperatingCost
	for rows.Next() {
		c, err := scanOperatingCost(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// GetByID 按 ID 查询（删除前校验归属用）。
func (r *OperatingCostRepo) GetByID(ctx context.Context, id int64) (*OperatingCost, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+operatingCostCols+` FROM provider_operating_costs WHERE id = ?`, id)
	c, err := scanOperatingCost(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

// Create 写入一笔运营成本。
//
// 币种恒为 USD：必须与上游实扣同币种才能直接相加。前端按 recharge_rate 折 CNY 展示。
func (r *OperatingCostRepo) Create(ctx context.Context, p OperatingCostParams) (*OperatingCost, error) {
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO provider_operating_costs (provider_id, category, amount, currency, occurred_on, note, operator)
		VALUES (?,?,?,'USD',?,?,?)`,
		p.ProviderID, p.Category, p.Amount, p.OccurredOn, p.Note, p.Operator)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return r.GetByID(ctx, id)
}

// Delete 删除一笔。影响 0 行返回 ErrNotFound，让调用方能回 404 而非静默成功。
//
// 与授信台账不同，这里用硬删除而非冲正：运营成本是自己给自己记的备忘，
// 记错了改掉即可，不存在需要留审计轨迹的对外债权关系。
func (r *OperatingCostRepo) Delete(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM provider_operating_costs WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SumByProvider 返回区间内 provider_id → 运营成本合计（统计页按供应商用）。
func (r *OperatingCostRepo) SumByProvider(ctx context.Context, startDate, endDate string) (map[int64]float64, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT provider_id, COALESCE(SUM(amount),0)
		FROM provider_operating_costs
		WHERE occurred_on >= ? AND occurred_on <= ?
		GROUP BY provider_id`, startDate, endDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]float64)
	for rows.Next() {
		var pid int64
		var sum float64
		if err := rows.Scan(&pid, &sum); err != nil {
			return nil, err
		}
		out[pid] = sum
	}
	return out, rows.Err()
}

// SumByDay 返回区间内 YYYY-MM-DD → 运营成本合计（趋势图用）。
func (r *OperatingCostRepo) SumByDay(ctx context.Context, startDate, endDate string) (map[string]float64, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT occurred_on, COALESCE(SUM(amount),0)
		FROM provider_operating_costs
		WHERE occurred_on >= ? AND occurred_on <= ?
		GROUP BY occurred_on`, startDate, endDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]float64)
	for rows.Next() {
		var day string
		var sum float64
		if err := rows.Scan(&day, &sum); err != nil {
			return nil, err
		}
		out[day] = sum
	}
	return out, rows.Err()
}

// TotalByProvider 返回某站点在区间内的合计（弹窗顶部「本期合计」用）。
func (r *OperatingCostRepo) TotalByProvider(ctx context.Context, providerID int64, startDate, endDate string) (float64, error) {
	var sum float64
	err := r.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(amount),0) FROM provider_operating_costs
		WHERE provider_id = ? AND occurred_on >= ? AND occurred_on <= ?`,
		providerID, startDate, endDate).Scan(&sum)
	return sum, err
}
