package repository

import (
	"context"
	"database/sql"
	"time"

	"sub2api-account-monitor/internal/pkg/health"
)

// HealthState 账号健康状态行。
type HealthState struct {
	AccountID            int64        `json:"account_id"`
	AccountName          string       `json:"account_name"`
	ProviderID           *int64       `json:"provider_id"`
	State                health.State `json:"state"`
	ConsecutiveFailures  int          `json:"consecutive_failures"`
	ConsecutiveSuccesses int          `json:"consecutive_successes"`
	WeightPercent        int          `json:"weight_percent"`
	CooldownUntil        *time.Time   `json:"cooldown_until"`
	ObservingUntil       *time.Time   `json:"observing_until"`
	LastProbeAt          *time.Time   `json:"last_probe_at"`
	UpdatedAt            time.Time    `json:"updated_at"`
}

// Snapshot 转为状态机快照。
func (h *HealthState) Snapshot() health.Snapshot {
	return health.Snapshot{
		State:                h.State,
		ConsecutiveFailures:  h.ConsecutiveFailures,
		ConsecutiveSuccesses: h.ConsecutiveSuccesses,
		WeightPercent:        h.WeightPercent,
		CooldownUntil:        h.CooldownUntil,
		ObservingUntil:       h.ObservingUntil,
	}
}

// HealthEvent 状态迁移事件。
type HealthEvent struct {
	ID        int64     `json:"id"`
	AccountID int64     `json:"account_id"`
	FromState string    `json:"from_state"`
	ToState   string    `json:"to_state"`
	Reason    string    `json:"reason"`
	Detail    *string   `json:"detail"`
	CreatedAt time.Time `json:"created_at"`
}

// HealthRepo 健康状态与事件存储。
type HealthRepo struct {
	db *sql.DB
}

// NewHealthRepo 创建 HealthRepo。
func NewHealthRepo(s *SQLite) *HealthRepo { return &HealthRepo{db: s.DB()} }

// Get 读取单账号状态；无记录时返回 healthy 零值。
func (r *HealthRepo) Get(ctx context.Context, accountID int64) (HealthState, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT account_id, account_name, provider_id, state, consecutive_failures, consecutive_successes,
			weight_percent, cooldown_until, observing_until, last_probe_at, updated_at
		FROM health_states WHERE account_id = ?`, accountID)
	st, err := scanHealthState(row)
	if err == sql.ErrNoRows {
		return HealthState{AccountID: accountID, State: health.StateHealthy, WeightPercent: 100}, nil
	}
	return st, err
}

// List 返回全部健康状态。
func (r *HealthRepo) List(ctx context.Context) ([]HealthState, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT account_id, account_name, provider_id, state, consecutive_failures, consecutive_successes,
			weight_percent, cooldown_until, observing_until, last_probe_at, updated_at
		FROM health_states ORDER BY account_name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HealthState
	for rows.Next() {
		st, err := scanHealthState(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// Upsert 写回状态机推进结果。
func (r *HealthRepo) Upsert(ctx context.Context, st HealthState) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO health_states (account_id, account_name, provider_id, state, consecutive_failures,
			consecutive_successes, weight_percent, cooldown_until, observing_until, last_probe_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(account_id) DO UPDATE SET
			account_name=excluded.account_name, provider_id=excluded.provider_id, state=excluded.state,
			consecutive_failures=excluded.consecutive_failures, consecutive_successes=excluded.consecutive_successes,
			weight_percent=excluded.weight_percent, cooldown_until=excluded.cooldown_until,
			observing_until=excluded.observing_until, last_probe_at=excluded.last_probe_at, updated_at=excluded.updated_at`,
		st.AccountID, st.AccountName, st.ProviderID, string(st.State), st.ConsecutiveFailures,
		st.ConsecutiveSuccesses, st.WeightPercent, fmtTimePtr(st.CooldownUntil), fmtTimePtr(st.ObservingUntil),
		fmtTimePtr(st.LastProbeAt), nowUTC())
	return err
}

// SetDisabled 人工启停（disabled ↔ healthy）。
func (r *HealthRepo) SetDisabled(ctx context.Context, accountID int64, disabled bool) error {
	state := string(health.StateHealthy)
	weight := 100
	if disabled {
		state = string(health.StateDisabled)
		weight = 0
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO health_states (account_id, state, weight_percent, updated_at) VALUES (?,?,?,?)
		ON CONFLICT(account_id) DO UPDATE SET state=excluded.state, weight_percent=excluded.weight_percent,
			consecutive_failures=0, consecutive_successes=0, cooldown_until=NULL, observing_until=NULL,
			updated_at=excluded.updated_at`,
		accountID, state, weight, nowUTC())
	return err
}

// InsertEvent 写入状态迁移事件。
func (r *HealthRepo) InsertEvent(ctx context.Context, e HealthEvent) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO health_events (account_id, from_state, to_state, reason, detail, created_at)
		VALUES (?,?,?,?,?,?)`,
		e.AccountID, e.FromState, e.ToState, e.Reason, e.Detail, nowUTC())
	return err
}

// Events 返回账号的迁移时间线（倒序）。
func (r *HealthRepo) Events(ctx context.Context, accountID int64, limit int) ([]HealthEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, account_id, from_state, to_state, reason, detail, created_at
		FROM health_events WHERE account_id = ? ORDER BY created_at DESC, id DESC LIMIT ?`, accountID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HealthEvent
	for rows.Next() {
		var e HealthEvent
		var detail sql.NullString
		var createdAt string
		if err := rows.Scan(&e.ID, &e.AccountID, &e.FromState, &e.ToState, &e.Reason, &detail, &createdAt); err != nil {
			return nil, err
		}
		if detail.Valid {
			d := detail.String
			e.Detail = &d
		}
		e.CreatedAt = parseTime(createdAt)
		out = append(out, e)
	}
	return out, rows.Err()
}

// TryConsumeProbeBudget 原子扣减当日探测预算；超限返回 false。
func (r *HealthRepo) TryConsumeProbeBudget(ctx context.Context, day string, limit int) (bool, error) {
	if limit <= 0 {
		return true, nil // 0 = 不限
	}
	// 先确保当日行存在，再条件递增（SQLite 单连接串行，无并发竞争）
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO probe_budget (day, used) VALUES (?, 0) ON CONFLICT(day) DO NOTHING`, day); err != nil {
		return false, err
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE probe_budget SET used = used + 1 WHERE day = ? AND used < ?`, day, limit)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// BudgetUsed 返回当日已用预算。
func (r *HealthRepo) BudgetUsed(ctx context.Context, day string) (int, error) {
	var used int
	err := r.db.QueryRowContext(ctx, `SELECT used FROM probe_budget WHERE day = ?`, day).Scan(&used)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return used, err
}

// CleanupBudget 清理 N 天前的预算行。
func (r *HealthRepo) CleanupBudget(ctx context.Context, beforeDay string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM probe_budget WHERE day < ?`, beforeDay)
	return err
}

// DeleteEventsOlderThan 清理历史事件。
func (r *HealthRepo) DeleteEventsOlderThan(ctx context.Context, before time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM health_events WHERE created_at < ?`, before.UTC().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func scanHealthState(row interface{ Scan(...any) error }) (HealthState, error) {
	var st HealthState
	var providerID sql.NullInt64
	var state string
	var cooldown, observing, lastProbe sql.NullString
	var updatedAt string
	if err := row.Scan(&st.AccountID, &st.AccountName, &providerID, &state, &st.ConsecutiveFailures,
		&st.ConsecutiveSuccesses, &st.WeightPercent, &cooldown, &observing, &lastProbe, &updatedAt); err != nil {
		return st, err
	}
	if providerID.Valid {
		v := providerID.Int64
		st.ProviderID = &v
	}
	st.State = health.State(state)
	st.CooldownUntil = parseTimePtr(cooldown)
	st.ObservingUntil = parseTimePtr(observing)
	st.LastProbeAt = parseTimePtr(lastProbe)
	st.UpdatedAt = parseTime(updatedAt)
	return st, nil
}

func fmtTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}
