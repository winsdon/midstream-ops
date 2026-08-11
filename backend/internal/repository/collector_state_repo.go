package repository

import (
	"context"
	"database/sql"
	"time"
)

// CollectorState 采集任务健康状态（provider_id=0 表示全局任务）。
type CollectorState struct {
	ProviderID          int64      `json:"provider_id"`
	Task                string     `json:"task"` // sync | probe | rate
	LastRunAt           *time.Time `json:"last_run_at"`
	LastSuccessAt       *time.Time `json:"last_success_at"`
	LastError           *string    `json:"last_error"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	NextEligibleAt      *time.Time `json:"next_eligible_at"`
}

// CollectorStateRepo 采集健康状态存储。
type CollectorStateRepo struct {
	db *DB
}

// NewCollectorStateRepo 创建 CollectorStateRepo。
func NewCollectorStateRepo(s *Store) *CollectorStateRepo { return &CollectorStateRepo{db: s.DB()} }

// RecordSuccess 记录一次成功：清零失败计数与退避。
func (r *CollectorStateRepo) RecordSuccess(ctx context.Context, providerID int64, task string) error {
	now := nowUTC()
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO collector_state (provider_id, task, last_run_at, last_success_at, last_error, consecutive_failures, next_eligible_at)
		VALUES (?,?,?,?,NULL,0,NULL)
		ON CONFLICT(provider_id, task) DO UPDATE SET
			last_run_at=excluded.last_run_at, last_success_at=excluded.last_success_at,
			last_error=NULL, consecutive_failures=0, next_eligible_at=NULL`,
		providerID, task, now, now)
	return err
}

// RecordFailure 记录一次失败：递增计数并写入退避解禁时刻。
// 返回递增后的连续失败次数（供调用方决定退避）。
func (r *CollectorStateRepo) RecordFailure(ctx context.Context, providerID int64, task, errMsg string, nextEligible *time.Time) (int, error) {
	var nextStr *string
	if nextEligible != nil {
		s := nextEligible.UTC().Format(time.RFC3339)
		nextStr = &s
	}
	// 用 RETURNING 一条语句拿到自增后的值，而非「先 UPDATE 再 SELECT 回读」。
	//
	// SQLite 时代靠 SetMaxOpenConns(1) 让两条语句必然连续，回读拿到的一定是自己
	// 写的值。PG 连接池下两条语句可能落在不同连接，中间插入同一 provider 的另一次
	// 失败记录，回读就会拿到别人的计数，导致退避时长算错。
	var n int
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO collector_state (provider_id, task, last_run_at, last_error, consecutive_failures, next_eligible_at)
		VALUES (?,?,?,?,1,?)
		ON CONFLICT(provider_id, task) DO UPDATE SET
			last_run_at=excluded.last_run_at, last_error=excluded.last_error,
			consecutive_failures=collector_state.consecutive_failures+1,
			next_eligible_at=excluded.next_eligible_at
		RETURNING consecutive_failures`,
		providerID, task, nowUTC(), errMsg, nextStr).Scan(&n)
	return n, err
}

// UpdateBackoff 更新退避解禁时刻（RecordFailure 后根据最新失败次数二次写入）。
func (r *CollectorStateRepo) UpdateBackoff(ctx context.Context, providerID int64, task string, nextEligible *time.Time) error {
	var nextStr *string
	if nextEligible != nil {
		s := nextEligible.UTC().Format(time.RFC3339)
		nextStr = &s
	}
	_, err := r.db.ExecContext(ctx, `UPDATE collector_state SET next_eligible_at=? WHERE provider_id=? AND task=?`,
		nextStr, providerID, task)
	return err
}

// Get 读取单个状态；无记录时返回零值。
func (r *CollectorStateRepo) Get(ctx context.Context, providerID int64, task string) (CollectorState, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT provider_id, task, last_run_at, last_success_at, last_error, consecutive_failures, next_eligible_at
		FROM collector_state WHERE provider_id=? AND task=?`, providerID, task)
	st, err := scanCollectorState(row)
	if err == sql.ErrNoRows {
		return CollectorState{ProviderID: providerID, Task: task}, nil
	}
	return st, err
}

// ListByTask 返回某任务全部供应商的状态。
func (r *CollectorStateRepo) ListByTask(ctx context.Context, task string) (map[int64]CollectorState, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT provider_id, task, last_run_at, last_success_at, last_error, consecutive_failures, next_eligible_at
		FROM collector_state WHERE task=?`, task)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]CollectorState)
	for rows.Next() {
		st, err := scanCollectorState(rows)
		if err != nil {
			return nil, err
		}
		out[st.ProviderID] = st
	}
	return out, rows.Err()
}

// ListByProvider 返回某供应商全部任务的状态。
func (r *CollectorStateRepo) ListByProvider(ctx context.Context, providerID int64) ([]CollectorState, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT provider_id, task, last_run_at, last_success_at, last_error, consecutive_failures, next_eligible_at
		FROM collector_state WHERE provider_id=?`, providerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CollectorState
	for rows.Next() {
		st, err := scanCollectorState(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func scanCollectorState(row interface{ Scan(...any) error }) (CollectorState, error) {
	var lastSuccess, nextEligible, lastRun sql.NullTime
	var st CollectorState
	var lastErr sql.NullString
	if err := row.Scan(&st.ProviderID, &st.Task, &lastRun, &lastSuccess, &lastErr, &st.ConsecutiveFailures, &nextEligible); err != nil {
		return st, err
	}
	st.LastRunAt = timePtr(lastRun)
	st.LastSuccessAt = timePtr(lastSuccess)
	if lastErr.Valid {
		e := lastErr.String
		st.LastError = &e
	}
	st.NextEligibleAt = timePtr(nextEligible)
	return st, nil
}
