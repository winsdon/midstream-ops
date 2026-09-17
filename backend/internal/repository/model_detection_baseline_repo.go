package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

var ErrBaselineNotFound = errors.New("model detection baseline not found")

type ModelDetectionBaseline struct {
	ID              int64
	Owner           string
	AccountID       *int64
	TargetFP        string
	TargetName      string
	BaseURL         string
	Model           string
	TemplateVersion string
	Status          string
	QualityOK       bool
	InputTokens     int
	OutputTokens    int
	ThinkingTokens  int
	ThinkingChars   int
	TTFTMs          int64
	DurationMs      int64
	ResponseSummary string
	Report          json.RawMessage
	Error           string
	CreatedAt       time.Time
}

type ModelDetectionBaselineRepo struct{ db *DB }

func NewModelDetectionBaselineRepo(s *Store) *ModelDetectionBaselineRepo {
	return &ModelDetectionBaselineRepo{db: s.DB()}
}

func (r *ModelDetectionBaselineRepo) Upsert(ctx context.Context, b *ModelDetectionBaseline) error {
	report := b.Report
	if len(report) == 0 {
		report = json.RawMessage(`{}`)
	}
	return r.db.QueryRowContext(ctx, `
      INSERT INTO model_detection_baselines (owner, account_id, target_fp, target_name, base_url, model, template_version, status, quality_ok, input_tokens, output_tokens, thinking_tokens, thinking_chars, ttft_ms, duration_ms, response_summary, report, error, created_at)
      VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
      ON CONFLICT(owner) DO UPDATE SET account_id=excluded.account_id, target_fp=excluded.target_fp, target_name=excluded.target_name, base_url=excluded.base_url, model=excluded.model, template_version=excluded.template_version, status=excluded.status, quality_ok=excluded.quality_ok, input_tokens=excluded.input_tokens, output_tokens=excluded.output_tokens, thinking_tokens=excluded.thinking_tokens, thinking_chars=excluded.thinking_chars, ttft_ms=excluded.ttft_ms, duration_ms=excluded.duration_ms, response_summary=excluded.response_summary, report=excluded.report, error=excluded.error, created_at=excluded.created_at
      RETURNING id, created_at`, b.Owner, b.AccountID, b.TargetFP, b.TargetName, b.BaseURL, b.Model, b.TemplateVersion, b.Status, b.QualityOK, b.InputTokens, b.OutputTokens, b.ThinkingTokens, b.ThinkingChars, b.TTFTMs, b.DurationMs, b.ResponseSummary, string(report), b.Error, nowUTC()).Scan(&b.ID, &b.CreatedAt)
}

func (r *ModelDetectionBaselineRepo) Get(ctx context.Context, owner string) (*ModelDetectionBaseline, error) {
	b := &ModelDetectionBaseline{}
	var report []byte
	err := r.db.QueryRowContext(ctx, `SELECT id,owner,account_id,target_fp,target_name,base_url,model,template_version,status,quality_ok,input_tokens,output_tokens,thinking_tokens,thinking_chars,ttft_ms,duration_ms,response_summary,report,error,created_at FROM model_detection_baselines WHERE owner=?`, owner).Scan(&b.ID, &b.Owner, &b.AccountID, &b.TargetFP, &b.TargetName, &b.BaseURL, &b.Model, &b.TemplateVersion, &b.Status, &b.QualityOK, &b.InputTokens, &b.OutputTokens, &b.ThinkingTokens, &b.ThinkingChars, &b.TTFTMs, &b.DurationMs, &b.ResponseSummary, &report, &b.Error, &b.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrBaselineNotFound
	}
	if err != nil {
		return nil, err
	}
	b.Report = json.RawMessage(report)
	return b, nil
}
