package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// ProviderAccount 供应商与本站账号的显式关联。
type ProviderAccount struct {
	ProviderID  int64
	AccountID   int64
	AccountName string // 关联时的快照；展示时以 PG 现值优先
	Note        string
	CreatedAt   string
}

// ProviderAccountRepo 供应商账号关联存储。
type ProviderAccountRepo struct {
	db *DB
}

// NewProviderAccountRepo 创建 ProviderAccountRepo。
func NewProviderAccountRepo(s *Store) *ProviderAccountRepo {
	return &ProviderAccountRepo{db: s.DB()}
}

const providerAccountCols = `provider_id, account_id, account_name, note, created_at`

func scanProviderAccounts(rows *sql.Rows) ([]ProviderAccount, error) {
	var out []ProviderAccount
	for rows.Next() {
		var pa ProviderAccount
		if err := rows.Scan(&pa.ProviderID, &pa.AccountID, &pa.AccountName, &pa.Note, &pa.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, pa)
	}
	return out, rows.Err()
}

// ListByProvider 返回某供应商的关联账号（按 account_id 升序）。
func (r *ProviderAccountRepo) ListByProvider(ctx context.Context, providerID int64) ([]ProviderAccount, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+providerAccountCols+` FROM provider_accounts WHERE provider_id = ? ORDER BY account_id`,
		providerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanProviderAccounts(rows)
}

// ListAll 返回全部关联。
func (r *ProviderAccountRepo) ListAll(ctx context.Context) ([]ProviderAccount, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+providerAccountCols+` FROM provider_accounts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanProviderAccounts(rows)
}

// AccountToProvider 返回 account_id → provider_id 映射（统计归并与反查的公共原料）。
func (r *ProviderAccountRepo) AccountToProvider(ctx context.Context) (map[int64]int64, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT account_id, provider_id FROM provider_accounts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]int64)
	for rows.Next() {
		var aid, pid int64
		if err := rows.Scan(&aid, &pid); err != nil {
			return nil, err
		}
		out[aid] = pid
	}
	return out, rows.Err()
}

// ProviderIDOf 反查某账号所属供应商（0 = 未关联）。
//
// 探测热路径使用（每次探测查一次），走 idx_provider_accounts_account 唯一索引。
func (r *ProviderAccountRepo) ProviderIDOf(ctx context.Context, accountID int64) (int64, error) {
	var pid int64
	err := r.db.QueryRowContext(ctx,
		`SELECT provider_id FROM provider_accounts WHERE account_id = ?`, accountID).Scan(&pid)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return pid, err
}

// CountByProvider 返回 provider_id → 关联账号数。
//
// 取代原先每次列表请求都全表拉一遍远端 PG accounts 的实现：计数现在只读本地库，
// 远端查询由此降为 0 次。代价是计数含「远端已删但关联未清」的悬垂 id，
// 属可接受的少量高估。
func (r *ProviderAccountRepo) CountByProvider(ctx context.Context) (map[int64]int, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT provider_id, COUNT(1) FROM provider_accounts GROUP BY provider_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]int)
	for rows.Next() {
		var pid int64
		var n int
		if err := rows.Scan(&pid, &n); err != nil {
			return nil, err
		}
		out[pid] = n
	}
	return out, rows.Err()
}

// Replace 全量替换某供应商的关联集合（关联弹窗「保存」语义）。
//
// 用替换而非增量 diff：弹窗里用户看到的是完整勾选态，提交的就是完整意图。
// 同一事务内先删后插 —— SetMaxOpenConns(1) 只串行化写、不保护读-改-写，
// 事务是这里唯一能保证不出现「删了还没插」中间态的手段。
//
// 别的供应商已关联的账号会被「抢过来」：UNIQUE(account_id) 不允许一个账号
// 属于两个供应商，故插入前先解除其旧关联。UI 必须明示这一点。
func (r *ProviderAccountRepo) Replace(ctx context.Context, providerID int64, items []ProviderAccount) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM provider_accounts WHERE provider_id = ?`, providerID); err != nil {
		return err
	}
	if err := insertLinks(ctx, tx, providerID, items); err != nil {
		return err
	}
	return tx.Commit()
}

// LinkMany 批量关联（扫描导入用，不清除目标供应商的既有关联）。
func (r *ProviderAccountRepo) LinkMany(ctx context.Context, items []ProviderAccount) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, it := range items {
		if it.ProviderID <= 0 {
			continue
		}
		if err := insertLinks(ctx, tx, it.ProviderID, []ProviderAccount{it}); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// insertLinks 在事务内写入关联，逐条先解除该账号的旧归属（抢占语义）。
func insertLinks(ctx context.Context, tx *Tx, providerID int64, items []ProviderAccount) error {
	for _, it := range items {
		if it.AccountID <= 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM provider_accounts WHERE account_id = ?`, it.AccountID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO provider_accounts (provider_id, account_id, account_name, note)
			VALUES (?,?,?,?)`,
			providerID, it.AccountID, strings.TrimSpace(it.AccountName), it.Note); err != nil {
			return err
		}
	}
	return nil
}
