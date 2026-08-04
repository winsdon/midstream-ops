package repository

import (
	"context"
	"database/sql"
	"time"
)

// BalanceSnapshot 余额快照。
type BalanceSnapshot struct {
	ID         int64
	ProviderID int64
	Balance    *float64
	Currency   string
	Source     string
	Metrics    *string // JSON
	Error      *string
	CreatedAt  time.Time
}

// BalanceRepo 余额快照存储。
type BalanceRepo struct {
	db *sql.DB
}

// NewBalanceRepo 创建 BalanceRepo。
func NewBalanceRepo(s *SQLite) *BalanceRepo { return &BalanceRepo{db: s.DB()} }

// InsertSnapshot 写入一条快照。
func (r *BalanceRepo) InsertSnapshot(ctx context.Context, snap *BalanceSnapshot) error {
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO balance_snapshots (provider_id, balance, currency, source, metrics, error, created_at)
		VALUES (?,?,?,?,?,?,?)`,
		snap.ProviderID, snap.Balance, snap.Currency, snap.Source, snap.Metrics, snap.Error, nowUTC())
	if err != nil {
		return err
	}
	snap.ID, _ = res.LastInsertId()
	return nil
}

// LatestSnapshot 返回供应商最新一条快照。
func (r *BalanceRepo) LatestSnapshot(ctx context.Context, providerID int64) (*BalanceSnapshot, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, provider_id, balance, currency, source, metrics, error, created_at
		FROM balance_snapshots WHERE provider_id = ? ORDER BY created_at DESC, id DESC LIMIT 1`, providerID)
	return scanSnapshot(row)
}

// LatestSnapshots 批量返回每个供应商的最新快照（列表页用，避免 N+1 查询）。
func (r *BalanceRepo) LatestSnapshots(ctx context.Context) (map[int64]*BalanceSnapshot, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, provider_id, balance, currency, source, metrics, error, created_at
		FROM balance_snapshots b
		WHERE id = (
			SELECT id FROM balance_snapshots
			WHERE provider_id = b.provider_id
			ORDER BY created_at DESC, id DESC LIMIT 1
		)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]*BalanceSnapshot)
	for rows.Next() {
		s, err := scanSnapshot(rows)
		if err != nil {
			return nil, err
		}
		out[s.ProviderID] = s
	}
	return out, rows.Err()
}

// History 返回供应商近 N 天快照（按时间升序）。
func (r *BalanceRepo) History(ctx context.Context, providerID int64, days int) ([]*BalanceSnapshot, error) {
	if days <= 0 {
		days = 7
	}
	since := time.Now().UTC().AddDate(0, 0, -days).Format(time.RFC3339)
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, provider_id, balance, currency, source, metrics, error, created_at
		FROM balance_snapshots WHERE provider_id = ? AND created_at >= ?
		ORDER BY created_at ASC, id ASC`, providerID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*BalanceSnapshot
	for rows.Next() {
		s, err := scanSnapshot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func scanSnapshot(row interface{ Scan(...any) error }) (*BalanceSnapshot, error) {
	var s BalanceSnapshot
	var bal sql.NullFloat64
	var metrics, errStr sql.NullString
	var createdAt string
	err := row.Scan(&s.ID, &s.ProviderID, &bal, &s.Currency, &s.Source, &metrics, &errStr, &createdAt)
	if err != nil {
		return nil, err
	}
	if bal.Valid {
		b := bal.Float64
		s.Balance = &b
	}
	if metrics.Valid {
		m := metrics.String
		s.Metrics = &m
	}
	if errStr.Valid {
		e := errStr.String
		s.Error = &e
	}
	s.CreatedAt = parseTime(createdAt)
	return &s, nil
}

// DeleteOlderThan 清理 retention 之前的快照，返回删除行数。
func (r *BalanceRepo) DeleteOlderThan(ctx context.Context, before time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM balance_snapshots WHERE created_at < ?`, before.UTC().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
