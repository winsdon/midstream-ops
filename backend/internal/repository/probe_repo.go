package repository

import (
	"context"
	"database/sql"
	"time"
)

// ProbeResult 探测结果。
type ProbeResult struct {
	ID          int64
	ProviderID  *int64
	AccountID   int64
	AccountName string
	Platform    string
	Model       string
	BaseURL     string
	Source      string
	Success     bool
	StatusCode  *int
	TTFTMs      *int64
	TotalMs     *int64
	Error       *string
	CreatedAt   time.Time
}

// ProbeRepo 探测结果存储。
type ProbeRepo struct {
	db *DB
}

// NewProbeRepo 创建 ProbeRepo。
func NewProbeRepo(s *Store) *ProbeRepo { return &ProbeRepo{db: s.DB()} }

// Insert 写入一条探测结果。
func (r *ProbeRepo) Insert(ctx context.Context, pr *ProbeResult) error {
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO probe_results (provider_id, account_id, account_name, platform, model, base_url,
			source, success, status_code, ttft_ms, total_ms, error, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING id`,
		pr.ProviderID, pr.AccountID, pr.AccountName, pr.Platform, pr.Model, pr.BaseURL,
		pr.Source, pr.Success, pr.StatusCode, pr.TTFTMs, pr.TotalMs, pr.Error, nowUTC()).Scan(&pr.ID)
	return err
}

// ProbeFilter 探测明细查询过滤。
type ProbeFilter struct {
	AccountID *int64
	Page      int
	PageSize  int
}

// List 分页查询探测明细（按时间倒序）。
func (r *ProbeRepo) List(ctx context.Context, f ProbeFilter) ([]*ProbeResult, int64, error) {
	where := "1=1"
	args := []any{}
	if f.AccountID != nil {
		where += " AND account_id = ?"
		args = append(args, *f.AccountID)
	}

	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM probe_results WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 || f.PageSize > 1000 {
		f.PageSize = 20
	}
	offset := (f.Page - 1) * f.PageSize

	query := `SELECT id, provider_id, account_id, account_name, platform, model, base_url,
		source, success, status_code, ttft_ms, total_ms, error, created_at
		FROM probe_results WHERE ` + where + ` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`
	args = append(args, f.PageSize, offset)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*ProbeResult
	for rows.Next() {
		pr, err := scanProbe(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, pr)
	}
	return out, total, rows.Err()
}

func scanProbe(row interface{ Scan(...any) error }) (*ProbeResult, error) {
	var pr ProbeResult
	var providerID sql.NullInt64
	var statusCode sql.NullInt64
	var ttft, total sql.NullInt64
	var errStr sql.NullString
	err := row.Scan(&pr.ID, &providerID, &pr.AccountID, &pr.AccountName, &pr.Platform, &pr.Model, &pr.BaseURL,
		&pr.Source, &pr.Success, &statusCode, &ttft, &total, &errStr, &pr.CreatedAt)
	if err != nil {
		return nil, err
	}
	if providerID.Valid {
		id := providerID.Int64
		pr.ProviderID = &id
	}
	if statusCode.Valid {
		sc := int(statusCode.Int64)
		pr.StatusCode = &sc
	}
	if ttft.Valid {
		t := ttft.Int64
		pr.TTFTMs = &t
	}
	if total.Valid {
		t := total.Int64
		pr.TotalMs = &t
	}
	if errStr.Valid {
		e := errStr.String
		pr.Error = &e
	}
	return &pr, nil
}

// SummaryRow 探测汇总行（按账号）。
type SummaryRow struct {
	AccountID   int64
	AccountName string
	Platform    string
	Total       int64
	SuccessCnt  int64
	AvgTTFT     *float64
	AvgTotal    *float64
	LastSuccess *bool
	LastAt      *time.Time
}

// Summary 近 N 小时按账号的探测汇总。
func (r *ProbeRepo) Summary(ctx context.Context, since time.Time) ([]*SummaryRow, error) {
	sinceStr := since.UTC().Format(time.RFC3339)
	rows, err := r.db.QueryContext(ctx, `
		SELECT account_id, account_name, platform, COUNT(*) AS total,
		       COUNT(*) FILTER (WHERE success) AS success_cnt,
		       AVG(ttft_ms) AS avg_ttft,
		       AVG(total_ms) AS avg_total,
		       MAX(created_at) AS last_at
		FROM probe_results
		WHERE created_at >= ?
		GROUP BY account_id, account_name, platform
		ORDER BY total DESC`, sinceStr)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*SummaryRow
	for rows.Next() {
		var s SummaryRow
		var avgTTFT, avgTotal sql.NullFloat64
		var lastAt sql.NullTime
		if err := rows.Scan(&s.AccountID, &s.AccountName, &s.Platform, &s.Total, &s.SuccessCnt, &avgTTFT, &avgTotal, &lastAt); err != nil {
			return nil, err
		}
		if avgTTFT.Valid {
			v := avgTTFT.Float64
			s.AvgTTFT = &v
		}
		if avgTotal.Valid {
			v := avgTotal.Float64
			s.AvgTotal = &v
		}
		s.LastAt = timePtr(lastAt)
		out = append(out, &s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 补充每个账号「窗口内」最近一次探测的成功与否。
	//
	// 必须带 since：窗口档位下探到 5 分钟后，拿全历史最后一条会让
	// 「窗口内 0 次探测」的账号显示一个来自几天前的 ✓，误导性极强。
	//
	// 用窗口内 MAX(id) 而非 MAX(created_at)：同一秒内的多条记录靠 id 才能定序。
	// 一条 SQL 取代原先的逐账号单查（走 idx_probe_time）。
	lastRows, err := r.db.QueryContext(ctx, `
		SELECT account_id, success FROM probe_results
		WHERE id IN (SELECT MAX(id) FROM probe_results WHERE created_at >= ? GROUP BY account_id)`, sinceStr)
	if err != nil {
		return nil, err
	}
	defer lastRows.Close()
	lastByAccount := make(map[int64]bool, len(out))
	for lastRows.Next() {
		var aid int64
		var ok bool
		if err := lastRows.Scan(&aid, &ok); err != nil {
			return nil, err
		}
		lastByAccount[aid] = ok
	}
	if err := lastRows.Err(); err != nil {
		return nil, err
	}
	for _, s := range out {
		if b, ok := lastByAccount[s.AccountID]; ok {
			v := b
			s.LastSuccess = &v
		}
	}
	return out, nil
}

// ModelProbeRow 按模型聚合的探测汇总（模型广场状态指标用）。
type ModelProbeRow struct {
	Model       string
	Total       int64
	SuccessCnt  int64
	AvgTTFT     *float64
	AvgTotal    *float64
	LastSuccess *bool
}

// SummaryByModel 近 N 小时按模型的探测汇总。
//
// 与 Summary 的区别只是聚合维度换成 model；探测仅覆盖 probe_enabled 的账号，
// 样本量远小于 usage_logs，调用方对匹配不到的模型应显示"未接入监控"而非报错。
func (r *ProbeRepo) SummaryByModel(ctx context.Context, since time.Time) ([]*ModelProbeRow, error) {
	sinceStr := since.UTC().Format(time.RFC3339)
	rows, err := r.db.QueryContext(ctx, `
		SELECT model, COUNT(*) AS total,
		       COUNT(*) FILTER (WHERE success) AS success_cnt,
		       AVG(ttft_ms) AS avg_ttft,
		       AVG(total_ms) AS avg_total
		FROM probe_results
		WHERE created_at >= ? AND model <> ''
		GROUP BY model
		ORDER BY total DESC`, sinceStr)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*ModelProbeRow
	for rows.Next() {
		var m ModelProbeRow
		var avgTTFT, avgTotal sql.NullFloat64
		if err := rows.Scan(&m.Model, &m.Total, &m.SuccessCnt, &avgTTFT, &avgTotal); err != nil {
			return nil, err
		}
		if avgTTFT.Valid {
			v := avgTTFT.Float64
			m.AvgTTFT = &v
		}
		if avgTotal.Valid {
			v := avgTotal.Float64
			m.AvgTotal = &v
		}
		out = append(out, &m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 补充每个模型最近一次探测的成功与否（与 Summary 同样的逐行补充策略，
	// 模型数量级与账号相当，额外查询开销可接受）。
	for _, m := range out {
		var lastSuccess bool
		row := r.db.QueryRowContext(ctx, `
			SELECT success FROM probe_results WHERE model = ? ORDER BY created_at DESC, id DESC LIMIT 1`, m.Model)
		if err := row.Scan(&lastSuccess); err == nil {
			b := lastSuccess
			m.LastSuccess = &b
		}
	}
	return out, nil
}

// Trend 单账号近 N 小时探测点列（按时间升序）。
func (r *ProbeRepo) Trend(ctx context.Context, accountID int64, since time.Time) ([]*ProbeResult, error) {
	sinceStr := since.UTC().Format(time.RFC3339)
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, provider_id, account_id, account_name, platform, model, base_url,
			source, success, status_code, ttft_ms, total_ms, error, created_at
		FROM probe_results WHERE account_id = ? AND created_at >= ?
		ORDER BY created_at ASC, id ASC`, accountID, sinceStr)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ProbeResult
	for rows.Next() {
		pr, err := scanProbe(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, pr)
	}
	return out, rows.Err()
}

// DeleteOlderThan 清理 retention 之前的探测结果。
func (r *ProbeRepo) DeleteOlderThan(ctx context.Context, before time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM probe_results WHERE created_at < ?`, before.UTC().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
