package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// 连接模式与状态。
const (
	ConnModeManaged  = "managed"  // 本系统创建的资源，可选删除远端
	ConnModeExisting = "existing" // 关联已有资源，绝不删远端

	ConnStatusPending = "pending"
	ConnStatusActive  = "active"
	ConnStatusFailed  = "failed"
)

// UpstreamConnection 上游 key ↔ 本站账号的对接记录。
type UpstreamConnection struct {
	ID               int64     `json:"id"`
	ProviderID       int64     `json:"provider_id"`
	UpstreamGroup    string    `json:"upstream_group"`
	UpstreamGroupID  int64     `json:"upstream_group_id"`
	UpstreamKeyID    int64     `json:"upstream_key_id"`
	UpstreamKeyName  string    `json:"upstream_key_name"`
	LocalAccountID   int64     `json:"local_account_id"`
	LocalAccountName string    `json:"local_account_name"`
	LocalGroupIDs    []int64   `json:"local_group_ids"`
	GroupPlatform    string    `json:"group_platform"`
	Mode             string    `json:"mode"`
	OperationID      string    `json:"operation_id"`
	Status           string    `json:"status"`
	Error            *string   `json:"error"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// CanDeleteRemote 是否允许删除远端资源（只有本系统建的才可以）。
func (c *UpstreamConnection) CanDeleteRemote() bool {
	return c.Mode == ConnModeManaged && c.UpstreamKeyID > 0
}

// ConnectionRepo 对接记录存储。
type ConnectionRepo struct {
	db *DB
}

// NewConnectionRepo 创建 ConnectionRepo。
func NewConnectionRepo(s *Store) *ConnectionRepo { return &ConnectionRepo{db: s.DB()} }

const connCols = `id, provider_id, upstream_group, upstream_group_id, upstream_key_id, upstream_key_name,
	local_account_id, local_account_name, local_group_ids, group_platform, mode, operation_id,
	status, error, created_at, updated_at`

func scanConnection(row interface{ Scan(...any) error }) (*UpstreamConnection, error) {
	var c UpstreamConnection
	var groupIDsJSON string
	var errMsg sql.NullString
	if err := row.Scan(&c.ID, &c.ProviderID, &c.UpstreamGroup, &c.UpstreamGroupID, &c.UpstreamKeyID, &c.UpstreamKeyName,
		&c.LocalAccountID, &c.LocalAccountName, &groupIDsJSON, &c.GroupPlatform, &c.Mode, &c.OperationID,
		&c.Status, &errMsg, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, err
	}
	c.LocalGroupIDs = []int64{}
	_ = json.Unmarshal([]byte(groupIDsJSON), &c.LocalGroupIDs)
	if errMsg.Valid {
		e := errMsg.String
		c.Error = &e
	}
	return &c, nil
}

// List 返回全部对接记录（默认只返回 active 与 pending，failed 供排障时单独查）。
func (r *ConnectionRepo) List(ctx context.Context, includeFailed bool) ([]*UpstreamConnection, error) {
	query := `SELECT ` + connCols + ` FROM upstream_connections`
	if !includeFailed {
		query += ` WHERE status <> 'failed'`
	}
	query += ` ORDER BY created_at DESC`
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*UpstreamConnection
	for rows.Next() {
		c, err := scanConnection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetByID 按 ID 查询。
func (r *ConnectionRepo) GetByID(ctx context.Context, id int64) (*UpstreamConnection, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+connCols+` FROM upstream_connections WHERE id = ?`, id)
	c, err := scanConnection(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

// GetByOperationID 幂等检查：同一 operation_id 已存在则直接返回既有记录。
func (r *ConnectionRepo) GetByOperationID(ctx context.Context, opID string) (*UpstreamConnection, error) {
	if opID == "" {
		return nil, ErrNotFound
	}
	row := r.db.QueryRowContext(ctx, `SELECT `+connCols+` FROM upstream_connections WHERE operation_id = ?`, opID)
	c, err := scanConnection(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

// GetActiveByUpstream 查某上游分组是否已有 active 连接（防重复建号）。
func (r *ConnectionRepo) GetActiveByUpstream(ctx context.Context, providerID int64, group string) (*UpstreamConnection, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+connCols+` FROM upstream_connections WHERE provider_id = ? AND upstream_group = ? AND status = 'active'`,
		providerID, group)
	c, err := scanConnection(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

// ConnectionParams 新建连接参数。
type ConnectionParams struct {
	ProviderID      int64
	UpstreamGroup   string
	UpstreamGroupID int64
	GroupPlatform   string
	LocalGroupIDs   []int64
	Mode            string
	OperationID     string
}

// CreatePending 先落 pending 记录再打 API —— 补偿失败后仍可对账。
func (r *ConnectionRepo) CreatePending(ctx context.Context, p ConnectionParams) (int64, error) {
	if p.LocalGroupIDs == nil {
		p.LocalGroupIDs = []int64{}
	}
	idsJSON, _ := json.Marshal(p.LocalGroupIDs)
	if p.Mode == "" {
		p.Mode = ConnModeManaged
	}
	var id int64
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO upstream_connections (provider_id, upstream_group, upstream_group_id, group_platform,
			local_group_ids, mode, operation_id, status, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,'pending',?,?) RETURNING id`,
		p.ProviderID, p.UpstreamGroup, p.UpstreamGroupID, p.GroupPlatform,
		string(idsJSON), p.Mode, p.OperationID, nowUTC(), nowUTC()).Scan(&id)
	return id, err
}

// SetKeyCreated 记录上游 key 建成（第一步成功）。
func (r *ConnectionRepo) SetKeyCreated(ctx context.Context, id, keyID int64, keyName string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE upstream_connections SET upstream_key_id=?, upstream_key_name=?, updated_at=? WHERE id=?`,
		keyID, keyName, nowUTC(), id)
	return err
}

// SetActive 两步都成功：转 active。
func (r *ConnectionRepo) SetActive(ctx context.Context, id, accountID int64, accountName string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE upstream_connections SET local_account_id=?, local_account_name=?, status='active', error=NULL, updated_at=? WHERE id=?`,
		accountID, accountName, nowUTC(), id)
	return err
}

// SetFailed 标记失败并记录原因（补偿是否成功另记日志）。
func (r *ConnectionRepo) SetFailed(ctx context.Context, id int64, errMsg string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE upstream_connections SET status='failed', error=?, updated_at=? WHERE id=?`,
		errMsg, nowUTC(), id)
	return err
}

// BindExisting 直接落一条 existing 模式的 active 记录（不创建任何远端资源）。
func (r *ConnectionRepo) BindExisting(ctx context.Context, p ConnectionParams,
	keyID int64, keyName string, accountID int64, accountName string) (int64, error) {
	if p.LocalGroupIDs == nil {
		p.LocalGroupIDs = []int64{}
	}
	idsJSON, _ := json.Marshal(p.LocalGroupIDs)
	var id int64
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO upstream_connections (provider_id, upstream_group, upstream_group_id, upstream_key_id, upstream_key_name,
			local_account_id, local_account_name, local_group_ids, group_platform, mode, operation_id, status, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,'existing',?,'active',?,?) RETURNING id`,
		p.ProviderID, p.UpstreamGroup, p.UpstreamGroupID, keyID, keyName,
		accountID, accountName, string(idsJSON), p.GroupPlatform, p.OperationID, nowUTC(), nowUTC()).Scan(&id)
	return id, err
}

// Delete 删除连接记录（远端资源是否删除由服务层决定）。
func (r *ConnectionRepo) Delete(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM upstream_connections WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteByProvider 供应商删除时清理其连接记录。
func (r *ConnectionRepo) DeleteByProvider(ctx context.Context, providerID int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM upstream_connections WHERE provider_id = ?`, providerID)
	return err
}

// ConnectedUpstreams 返回已建号的上游分组集合（key = "providerID:group"）。
func (r *ConnectionRepo) ConnectedUpstreams(ctx context.Context) (map[string]bool, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT provider_id, upstream_group FROM upstream_connections WHERE status = 'active'`)
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
