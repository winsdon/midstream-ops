package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// ErrDetectionNotFound 指定 id 的检测记录不存在。
var ErrDetectionNotFound = errors.New("model detection not found")

// ModelDetection 一次渠道检测的留痕。
//
// Report 是脱敏后的完整报告 JSON（含逐项断言与原始响应片段），
// API Key 明文既不在这里也不在任何列里——同一性只靠 TargetFP 串联。
type ModelDetection struct {
	ID                int64
	AccountID         *int64
	AccountName       string
	ProviderID        *int64
	TargetFP          string
	TargetName        string
	BaseURL           string
	Model             string
	Label             string
	Confidence        string
	AuthenticityScore int
	AuthenticityGrade string
	Scores            json.RawMessage
	Report            json.RawMessage
	CreatedAt         time.Time
}

// ModelDetectionRepo 检测历史存储。
type ModelDetectionRepo struct {
	db *DB
}

// NewModelDetectionRepo 创建 ModelDetectionRepo。
func NewModelDetectionRepo(s *Store) *ModelDetectionRepo { return &ModelDetectionRepo{db: s.DB()} }

// Insert 写入一条检测结果，回填 id 与 created_at。
func (r *ModelDetectionRepo) Insert(ctx context.Context, d *ModelDetection) error {
	scores := d.Scores
	if len(scores) == 0 {
		scores = json.RawMessage(`{}`)
	}
	report := d.Report
	if len(report) == 0 {
		report = json.RawMessage(`{}`)
	}
	return r.db.QueryRowContext(ctx, `
		INSERT INTO model_detections (account_id, account_name, provider_id, target_fp, target_name,
			base_url, model, label, confidence, authenticity_score, authenticity_grade, scores, report, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING id, created_at`,
		d.AccountID, d.AccountName, d.ProviderID, d.TargetFP, d.TargetName,
		d.BaseURL, d.Model, d.Label, d.Confidence, d.AuthenticityScore, d.AuthenticityGrade,
		string(scores), string(report), nowUTC()).Scan(&d.ID, &d.CreatedAt)
}

// DetectionFilter 历史查询条件。
type DetectionFilter struct {
	AccountID *int64
	TargetFP  string
	Label     string
	Page      int
	PageSize  int
}

// List 分页查询历史（按时间倒序，不含 report 体积大的列）。
func (r *ModelDetectionRepo) List(ctx context.Context, f DetectionFilter) ([]*ModelDetection, int64, error) {
	where := "1=1"
	args := []any{}
	if f.AccountID != nil {
		where += " AND account_id = ?"
		args = append(args, *f.AccountID)
	}
	if f.TargetFP != "" {
		where += " AND target_fp = ?"
		args = append(args, f.TargetFP)
	}
	if f.Label != "" {
		where += " AND label = ?"
		args = append(args, f.Label)
	}

	var total int64
	if err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM model_detections WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if f.PageSize <= 0 {
		f.PageSize = 20
	}
	if f.Page <= 0 {
		f.Page = 1
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT id, account_id, account_name, provider_id, target_fp, target_name, base_url, model,
		       label, confidence, authenticity_score, authenticity_grade, scores, created_at
		FROM model_detections WHERE `+where+`
		ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`,
		append(args, f.PageSize, (f.Page-1)*f.PageSize)...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()

	var out []*ModelDetection
	for rows.Next() {
		d := &ModelDetection{}
		var scores []byte
		if err := rows.Scan(&d.ID, &d.AccountID, &d.AccountName, &d.ProviderID, &d.TargetFP,
			&d.TargetName, &d.BaseURL, &d.Model, &d.Label, &d.Confidence,
			&d.AuthenticityScore, &d.AuthenticityGrade, &scores, &d.CreatedAt); err != nil {
			return nil, 0, err
		}
		d.Scores = json.RawMessage(scores)
		out = append(out, d)
	}
	return out, total, rows.Err()
}

// Get 读取单条（含完整 report）。
func (r *ModelDetectionRepo) Get(ctx context.Context, id int64) (*ModelDetection, error) {
	d := &ModelDetection{}
	var scores, report []byte
	err := r.db.QueryRowContext(ctx, `
		SELECT id, account_id, account_name, provider_id, target_fp, target_name, base_url, model,
		       label, confidence, authenticity_score, authenticity_grade, scores, report, created_at
		FROM model_detections WHERE id = ?`, id).
		Scan(&d.ID, &d.AccountID, &d.AccountName, &d.ProviderID, &d.TargetFP, &d.TargetName,
			&d.BaseURL, &d.Model, &d.Label, &d.Confidence, &d.AuthenticityScore,
			&d.AuthenticityGrade, &scores, &report, &d.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDetectionNotFound
	}
	if err != nil {
		return nil, err
	}
	d.Scores = json.RawMessage(scores)
	d.Report = json.RawMessage(report)
	return d, nil
}

// LatestByAccount 每个账号最近一次检测结果，供上游列表直接标注渠道类型。
func (r *ModelDetectionRepo) LatestByAccount(ctx context.Context) (map[int64]*ModelDetection, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT DISTINCT ON (account_id)
		       id, account_id, account_name, provider_id, target_fp, target_name, base_url, model,
		       label, confidence, authenticity_score, authenticity_grade, created_at
		FROM model_detections
		WHERE account_id IS NOT NULL
		ORDER BY account_id, created_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := map[int64]*ModelDetection{}
	for rows.Next() {
		d := &ModelDetection{}
		if err := rows.Scan(&d.ID, &d.AccountID, &d.AccountName, &d.ProviderID, &d.TargetFP,
			&d.TargetName, &d.BaseURL, &d.Model, &d.Label, &d.Confidence,
			&d.AuthenticityScore, &d.AuthenticityGrade, &d.CreatedAt); err != nil {
			return nil, err
		}
		if d.AccountID != nil {
			out[*d.AccountID] = d
		}
	}
	return out, rows.Err()
}

// DeleteOlderThan 清理过期历史，返回删除行数。
func (r *ModelDetectionRepo) DeleteOlderThan(ctx context.Context, before time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM model_detections WHERE created_at < ?`, before.UTC())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
