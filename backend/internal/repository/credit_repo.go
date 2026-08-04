package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"sub2api-account-monitor/internal/pkg/secretbox"

	sqlite3 "modernc.org/sqlite"
)

// ErrDuplicate 唯一约束冲突（调用方应转 409）。
//
// 与 ErrNotFound（provider_repo.go）并列的仓储层哨兵错误，放在本文件是因为
// 目前只有 customers.sub2api_user_id 这一处唯一键需要向上层区分。
var ErrDuplicate = errors.New("记录已存在")

// sqliteConstraintUnique 是 SQLITE_CONSTRAINT_UNIQUE 的扩展错误码。
//
// 用错误码而非匹配错误文案：文案随驱动版本变化，错误码是 SQLite 的稳定契约。
const sqliteConstraintUnique = 2067

// isUniqueViolation 判定是否为唯一约束冲突。
func isUniqueViolation(err error) bool {
	var se *sqlite3.Error
	return errors.As(err, &se) && se.Code() == sqliteConstraintUnique
}

// Customer 授信客户（一个客户 = 一个 sub2api user_id）。
//
// Outstanding 是冗余缓存列，credit_ledger 才是唯一真相；每次写台账都在同一事务里
// 用全量 SUM 重算（见 AppendEntry），不做增量累加。
type Customer struct {
	ID            int64
	Sub2apiUserID string
	DisplayName   string
	Email         string
	Note          string
	AdminNote     string // 仅管理端，永不进客户侧 DTO
	CreditLimit   float64
	Outstanding   float64
	Status        string // active | archived
	AlertLevel    int    // 告警闩锁：0 | 80 | 100
	AlertAt       *time.Time
	LastEntryAt   *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Available 可用额度。未授信（CreditLimit ≤ 0）时恒为 0。
func (c *Customer) Available() float64 {
	if c.CreditLimit <= 0 {
		return 0
	}
	return c.CreditLimit - c.Outstanding
}

// UsageRatio 额度使用率。未授信时返回 0（不参与告警与进度条）。
func (c *Customer) UsageRatio() float64 {
	if c.CreditLimit <= 0 {
		return 0
	}
	return c.Outstanding / c.CreditLimit
}

// LedgerEntry 一条台账分录。Amount 恒为正，方向由 EntryType 决定。
type LedgerEntry struct {
	ID          int64
	CustomerID  int64
	EntryType   string // advance（垫付）| repayment（回款）
	Amount      float64
	Currency    string
	OccurredAt  time.Time
	Note        string
	ExternalRef string
	Operator    string
	ReversedOf  *int64 // 冲正指向原分录
	CreatedAt   time.Time
}

// CreditRepo 授信台账存储。
//
// box 用于 KYC 字段加解密（见 credit_kyc.go）；客户与台账本身不含需加密字段。
type CreditRepo struct {
	db  *sql.DB
	box *secretbox.Box
}

// NewCreditRepo 创建 CreditRepo（box 为 KYC 的 PII 加解密器）。
func NewCreditRepo(s *SQLite, box *secretbox.Box) *CreditRepo {
	return &CreditRepo{db: s.DB(), box: box}
}

const customerCols = `id, sub2api_user_id, display_name, email, note, admin_note,
	credit_limit, outstanding, status, alert_level, alert_at, last_entry_at, created_at, updated_at`

// scanCustomer 扫描一行客户。
// 注意：Scan 参数顺序与 customerCols 是手工维持的隐式契约，无编译期保护，
// 新增列必须在两处同序追加，否则所有 SELECT 会静默错位。
func scanCustomer(row interface{ Scan(...any) error }) (*Customer, error) {
	var c Customer
	var alertAt, lastEntryAt sql.NullString
	var createdAt, updatedAt string
	err := row.Scan(&c.ID, &c.Sub2apiUserID, &c.DisplayName, &c.Email, &c.Note, &c.AdminNote,
		&c.CreditLimit, &c.Outstanding, &c.Status, &c.AlertLevel, &alertAt, &lastEntryAt,
		&createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	c.AlertAt = parseTimePtr(alertAt)
	c.LastEntryAt = parseTimePtr(lastEntryAt)
	c.CreatedAt = parseTime(createdAt)
	c.UpdatedAt = parseTime(updatedAt)
	return &c, nil
}

const ledgerCols = `id, customer_id, entry_type, amount, currency, occurred_at,
	note, external_ref, operator, reversed_of, created_at`

// scanLedgerEntry 扫描一行台账分录。
// 注意：Scan 参数顺序与 ledgerCols 是手工维持的隐式契约（同 scanCustomer）。
func scanLedgerEntry(row interface{ Scan(...any) error }) (*LedgerEntry, error) {
	var e LedgerEntry
	var reversedOf sql.NullInt64
	var occurredAt, createdAt string
	err := row.Scan(&e.ID, &e.CustomerID, &e.EntryType, &e.Amount, &e.Currency, &occurredAt,
		&e.Note, &e.ExternalRef, &e.Operator, &reversedOf, &createdAt)
	if err != nil {
		return nil, err
	}
	if reversedOf.Valid {
		v := reversedOf.Int64
		e.ReversedOf = &v
	}
	e.OccurredAt = parseTime(occurredAt)
	e.CreatedAt = parseTime(createdAt)
	return &e, nil
}

// ---------- 客户 ----------

// CustomerFilter 客户列表筛选。
type CustomerFilter struct {
	Keyword  string // 模糊匹配 sub2api_user_id / display_name / email（均为明文列）
	Status   string // 空 = 全部
	Sort     string // 排序列（customerSortCols 的键），空或非法 = 默认按敞口降序
	Order    string // asc | desc，默认 asc
	Page     int
	PageSize int
}

// customerSortCols 排序白名单：列名要直接拼进 SQL，必须白名单化防注入。
//
// 键是对外的稳定标识（前端 SortableTh 的 sort-key），值是真实列表达式 ——
// 两者解耦，改列名不影响前端。
// 刻意不含 KYC 的 _enc 列：随机 nonce 让密文排序毫无意义（见 012 迁移注释）。
var customerSortCols = map[string]string{
	"user_id":     "sub2api_user_id",
	"name":        "display_name",
	"limit":       "credit_limit",
	"outstanding": "outstanding",
	"available":   "(credit_limit - outstanding)",
	"last_entry":  "last_entry_at",
}

// orderClause 把过滤条件里的排序意图翻译成 ORDER BY 子句。
//
// 非法列静默回退默认序而不报 400：排序是查看动作不是业务操作，前端传了
// 未知列（版本不同步）时给出结果比给出错误有用。
// id DESC 恒作末位 tie-break —— 同值行若无稳定次序，翻页时会在页间反复跳动。
func orderClause(sort, order string) string {
	col, ok := customerSortCols[sort]
	if !ok {
		return `outstanding DESC, id DESC`
	}
	dir := "ASC"
	if strings.EqualFold(order, "desc") {
		dir = "DESC"
	}
	return col + " " + dir + ", id DESC"
}

// ListCustomers 分页查询客户。默认按敞口降序（欠得多的排前面），可按白名单列改排序。
func (r *CreditRepo) ListCustomers(ctx context.Context, f CustomerFilter) ([]*Customer, int64, error) {
	where := ` WHERE 1=1`
	var args []any
	if f.Status != "" {
		where += ` AND status = ?`
		args = append(args, f.Status)
	}
	if f.Keyword != "" {
		where += ` AND (sub2api_user_id LIKE ? OR display_name LIKE ? OR email LIKE ?)`
		kw := "%" + f.Keyword + "%"
		args = append(args, kw, kw, kw)
	}

	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM customers`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	limit, offset := f.PageSize, (f.Page-1)*f.PageSize
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+customerCols+` FROM customers`+where+
			` ORDER BY `+orderClause(f.Sort, f.Order)+` LIMIT ? OFFSET ?`,
		append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*Customer
	for rows.Next() {
		c, err := scanCustomer(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, c)
	}
	return out, total, rows.Err()
}

// GetCustomer 按 ID 查询。
func (r *CreditRepo) GetCustomer(ctx context.Context, id int64) (*Customer, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+customerCols+` FROM customers WHERE id = ?`, id)
	c, err := scanCustomer(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

// GetCustomerBySub2apiID 按 sub2api 用户 ID 查询（嵌入页身份入口）。
func (r *CreditRepo) GetCustomerBySub2apiID(ctx context.Context, userID string) (*Customer, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+customerCols+` FROM customers WHERE sub2api_user_id = ?`, userID)
	c, err := scanCustomer(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

// ListEnrolledUserIDs 返回所有已建档的 sub2api_user_id 集合（建档下拉去重用）。
//
// 含 archived：归档只是停止跟进，档案与台账都还在，重复建档照样撞 UNIQUE。
func (r *CreditRepo) ListEnrolledUserIDs(ctx context.Context) (map[string]bool, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT sub2api_user_id FROM customers`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// CustomerParams 新建/编辑客户参数。
type CustomerParams struct {
	Sub2apiUserID string
	DisplayName   string
	Email         string
	Note          string
	AdminNote     string
	CreditLimit   float64
	Status        string
}

// CreateCustomer 新建客户。sub2api_user_id 唯一，重复时返回 ErrDuplicate。
func (r *CreditRepo) CreateCustomer(ctx context.Context, p CustomerParams) (*Customer, error) {
	if p.Status == "" {
		p.Status = "active"
	}
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO customers (sub2api_user_id, display_name, email, note, admin_note,
			credit_limit, status, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		p.Sub2apiUserID, p.DisplayName, p.Email, p.Note, p.AdminNote,
		p.CreditLimit, p.Status, nowUTC(), nowUTC())
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrDuplicate
		}
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return r.GetCustomer(ctx, id)
}

// UpdateCustomer 编辑客户。
//
// 不改 outstanding / alert_level：前者由 AppendEntry 事务内重算，后者由告警评估写入。
// credit_limit 变化会改变告警分母，调用方须在此之后重新评估告警档位。
// sub2api_user_id 改到已被占用的值时返回 ErrDuplicate（前端虽禁用该字段，但 API 是公开的）。
func (r *CreditRepo) UpdateCustomer(ctx context.Context, id int64, p CustomerParams) (*Customer, error) {
	if p.Status == "" {
		p.Status = "active"
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE customers SET sub2api_user_id=?, display_name=?, email=?, note=?, admin_note=?,
			credit_limit=?, status=?, updated_at=?
		WHERE id=?`,
		p.Sub2apiUserID, p.DisplayName, p.Email, p.Note, p.AdminNote,
		p.CreditLimit, p.Status, nowUTC(), id)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrDuplicate
		}
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return r.GetCustomer(ctx, id)
}

// ArchiveCustomer 归档客户（不物理删除，台账须保留）。
func (r *CreditRepo) ArchiveCustomer(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE customers SET status='archived', updated_at=? WHERE id=?`, nowUTC(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------- 台账 ----------

// EntryParams 记一笔台账。
type EntryParams struct {
	CustomerID  int64
	EntryType   string  // advance | repayment
	Amount      float64 // 恒为正
	Currency    string
	OccurredAt  time.Time
	Note        string
	ExternalRef string
	Operator    string
	ReversedOf  *int64
}

// AppendEntry 追加一条分录并在同一事务内重算敞口，返回更新后的客户。
//
// 为何全量 SUM 重算而非增量 ±amount：SetMaxOpenConns(1) 只串行化写，不保护读-改-写；
// 且 float 增量会累积误差，任何一条漏改的代码路径都会造成永久漂移。
// 全量重算在单客户数百条分录的量级下开销可忽略，且天然幂等。
//
// 调用方须在本函数返回后评估告警档位（见 credit_service.go）。
func (r *CreditRepo) AppendEntry(ctx context.Context, p EntryParams) (*Customer, error) {
	if p.Currency == "" {
		p.Currency = "USD"
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	// 客户必须存在（外键在 SQLite 默认不强制，显式校验）
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM customers WHERE id = ?`, p.CustomerID).Scan(&exists); err != nil {
		return nil, err
	}
	if exists == 0 {
		return nil, ErrNotFound
	}

	occurred := p.OccurredAt.UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO credit_ledger (customer_id, entry_type, amount, currency, occurred_at,
			note, external_ref, operator, reversed_of, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		p.CustomerID, p.EntryType, p.Amount, p.Currency, occurred,
		p.Note, p.ExternalRef, p.Operator, p.ReversedOf, nowUTC()); err != nil {
		return nil, err
	}

	if err := recalcOutstandingTx(ctx, tx, p.CustomerID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE customers SET last_entry_at=?, updated_at=? WHERE id=?`,
		nowUTC(), nowUTC(), p.CustomerID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r.GetCustomer(ctx, p.CustomerID)
}

// recalcOutstandingTx 事务内全量重算某客户敞口并写回冗余列。
func recalcOutstandingTx(ctx context.Context, tx *sql.Tx, customerID int64) error {
	var outstanding float64
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(CASE WHEN entry_type='advance' THEN amount ELSE -amount END), 0)
		FROM credit_ledger WHERE customer_id = ?`, customerID).Scan(&outstanding); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx,
		`UPDATE customers SET outstanding=?, updated_at=? WHERE id=?`,
		outstanding, nowUTC(), customerID)
	return err
}

// GetEntry 按 ID 查询单条分录（冲正时校验用）。
func (r *CreditRepo) GetEntry(ctx context.Context, id int64) (*LedgerEntry, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+ledgerCols+` FROM credit_ledger WHERE id = ?`, id)
	e, err := scanLedgerEntry(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return e, err
}

// ListEntries 分页查询某客户的台账，按业务时间倒序。
func (r *CreditRepo) ListEntries(ctx context.Context, customerID int64, page, pageSize int) ([]*LedgerEntry, int64, error) {
	var total int64
	if err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM credit_ledger WHERE customer_id = ?`, customerID).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit, offset := pageSize, (page-1)*pageSize
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+ledgerCols+` FROM credit_ledger WHERE customer_id = ?
		 ORDER BY occurred_at DESC, id DESC LIMIT ? OFFSET ?`,
		customerID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*LedgerEntry
	for rows.Next() {
		e, err := scanLedgerEntry(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, e)
	}
	return out, total, rows.Err()
}

// HasReversal 该分录是否已被冲正过（防重复冲正）。
func (r *CreditRepo) HasReversal(ctx context.Context, entryID int64) (bool, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM credit_ledger WHERE reversed_of = ?`, entryID).Scan(&n)
	return n > 0, err
}

// RecalcCustomer 重算单个客户敞口（幂等兜底）。
func (r *CreditRepo) RecalcCustomer(ctx context.Context, id int64) (*Customer, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := recalcOutstandingTx(ctx, tx, id); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r.GetCustomer(ctx, id)
}

// RecalcAll 重算全部客户敞口（幂等兜底，管理端「重算」按钮）。
// 返回被修正过的客户 ID 列表，调用方据此重新评估告警。
func (r *CreditRepo) RecalcAll(ctx context.Context) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id FROM customers`)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	for _, id := range ids {
		if err := recalcOutstandingTx(ctx, tx, id); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return ids, nil
}

// ---------- 告警闩锁 ----------

// SetAlertLevel 写入告警闩锁档位（0 | 80 | 100）。
//
// 为何持久化而非像 AlertService.lastAlert 那样存内存：余额告警有周期采集，
// 重启后下一轮自然重建；授信告警纯由写操作驱动，没有周期任务自愈，
// 内存闩锁重启即丢会导致重复告警。
func (r *CreditRepo) SetAlertLevel(ctx context.Context, id int64, level int, firedAt *time.Time) error {
	var at *string
	if firedAt != nil {
		s := firedAt.UTC().Format(time.RFC3339)
		at = &s
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE customers SET alert_level=?, alert_at=COALESCE(?, alert_at), updated_at=? WHERE id=?`,
		level, at, nowUTC(), id)
	return err
}

// ---------- 汇总 ----------

// CreditSummary 授信总览。
type CreditSummary struct {
	CustomerCount    int64   `json:"customer_count"`
	GrantedCount     int64   `json:"granted_count"`     // 已授信（credit_limit > 0）
	TotalLimit       float64 `json:"total_limit"`       // 授信总额
	TotalOutstanding float64 `json:"total_outstanding"` // 敞口合计（应收账）
	OverLimitCount   int64   `json:"over_limit_count"`  // 超额客户数
	WarningCount     int64   `json:"warning_count"`     // 已达 80% 但未超额
}

// Summary 授信总览（仅统计 active 客户）。
func (r *CreditRepo) Summary(ctx context.Context) (*CreditSummary, error) {
	var s CreditSummary
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(1),
		       COALESCE(SUM(CASE WHEN credit_limit > 0 THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(credit_limit), 0),
		       COALESCE(SUM(outstanding), 0),
		       COALESCE(SUM(CASE WHEN credit_limit > 0 AND outstanding >= credit_limit THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN credit_limit > 0 AND outstanding >= credit_limit * 0.8
		                          AND outstanding < credit_limit THEN 1 ELSE 0 END), 0)
		FROM customers WHERE status = 'active'`).Scan(
		&s.CustomerCount, &s.GrantedCount, &s.TotalLimit, &s.TotalOutstanding,
		&s.OverLimitCount, &s.WarningCount)
	if err != nil {
		return nil, err
	}
	return &s, nil
}
