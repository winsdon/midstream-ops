package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// ErrIQRunNotFound 指定 id 的智商测试记录不存在。
var ErrIQRunNotFound = errors.New("model iq run not found")

// ModelIQRun 一次智商测试的留痕（单个目标）。
//
// Results 是每道题的简要结论，列表页直接用；Report 是含作品与回复全文的完整报告，
// 体积大，只在详情里取。与检测历史一样，API Key 明文不在任何列里。
type ModelIQRun struct {
	ID          int64
	AccountID   *int64
	AccountName string
	ProviderID  *int64
	TargetFP    string
	TargetName  string
	BaseURL     string
	Model       string
	Passed      int
	Total       int
	Results     json.RawMessage
	Report      json.RawMessage
	CreatedAt   time.Time
}

// ModelIQRepo 智商测试历史存储。
type ModelIQRepo struct {
	db *DB
}

// NewModelIQRepo 创建 ModelIQRepo。
func NewModelIQRepo(s *Store) *ModelIQRepo { return &ModelIQRepo{db: s.DB()} }

// Insert 写入一条智商测试结果，回填 id 与 created_at。
func (r *ModelIQRepo) Insert(ctx context.Context, run *ModelIQRun) error {
	results := run.Results
	if len(results) == 0 {
		results = json.RawMessage(`[]`)
	}
	report := run.Report
	if len(report) == 0 {
		report = json.RawMessage(`{}`)
	}
	return r.db.QueryRowContext(ctx, `
		INSERT INTO model_iq_runs (account_id, account_name, provider_id, target_fp, target_name,
			base_url, model, passed, total, results, report, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?) RETURNING id, created_at`,
		run.AccountID, run.AccountName, run.ProviderID, run.TargetFP, run.TargetName,
		run.BaseURL, run.Model, run.Passed, run.Total, string(results), string(report), nowUTC()).
		Scan(&run.ID, &run.CreatedAt)
}

// List 分页查询历史（按时间倒序，不含 report）。
func (r *ModelIQRepo) List(ctx context.Context, page, pageSize int) ([]*ModelIQRun, int64, error) {
	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM model_iq_runs`).Scan(&total); err != nil {
		return nil, 0, err
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if page <= 0 {
		page = 1
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT id, account_id, account_name, provider_id, target_fp, target_name, base_url, model,
		       passed, total, results, created_at
		FROM model_iq_runs
		ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()

	var out []*ModelIQRun
	for rows.Next() {
		run := &ModelIQRun{}
		var results []byte
		if err := rows.Scan(&run.ID, &run.AccountID, &run.AccountName, &run.ProviderID, &run.TargetFP,
			&run.TargetName, &run.BaseURL, &run.Model, &run.Passed, &run.Total, &results, &run.CreatedAt); err != nil {
			return nil, 0, err
		}
		run.Results = json.RawMessage(results)
		out = append(out, run)
	}
	return out, total, rows.Err()
}

// Get 读取单条（含完整 report）。
func (r *ModelIQRepo) Get(ctx context.Context, id int64) (*ModelIQRun, error) {
	run := &ModelIQRun{}
	var results, report []byte
	err := r.db.QueryRowContext(ctx, `
		SELECT id, account_id, account_name, provider_id, target_fp, target_name, base_url, model,
		       passed, total, results, report, created_at
		FROM model_iq_runs WHERE id = ?`, id).
		Scan(&run.ID, &run.AccountID, &run.AccountName, &run.ProviderID, &run.TargetFP, &run.TargetName,
			&run.BaseURL, &run.Model, &run.Passed, &run.Total, &results, &report, &run.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrIQRunNotFound
	}
	if err != nil {
		return nil, err
	}
	run.Results = json.RawMessage(results)
	run.Report = json.RawMessage(report)
	return run, nil
}

// DeleteOlderThan 清理过期历史，返回删除行数。
func (r *ModelIQRepo) DeleteOlderThan(ctx context.Context, before time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM model_iq_runs WHERE created_at < ?`, before.UTC())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteBeyondLatest 只保留最新 keep 条，返回删除行数。
//
// 排序与列表一致（created_at DESC, id DESC）。keep <= 0 表示不限制。
// 智商报告含作品与回复全文，和真伪检测历史一样按条数封顶。
func (r *ModelIQRepo) DeleteBeyondLatest(ctx context.Context, keep int) (int64, error) {
	if keep <= 0 {
		return 0, nil
	}
	res, err := r.db.ExecContext(ctx, `
		WITH stale AS (
			SELECT id FROM model_iq_runs
			ORDER BY created_at DESC, id DESC
			OFFSET ?
		)
		DELETE FROM model_iq_runs WHERE id IN (SELECT id FROM stale)`, keep)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Delete 删除一条智商测试历史。影响 0 行返回 ErrIQRunNotFound。
func (r *ModelIQRepo) Delete(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM model_iq_runs WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrIQRunNotFound
	}
	return nil
}
