package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// CustomerKYC 客户身份资料（1:1 挂在 customers 上）。
//
// 结构体字段一律是**明文**：加解密发生在本文件的 scanKYC / UpsertKYC 边界内，
// 上层（service / handler）拿到的永远是可直接展示的值。
//
// 【禁止把任何 _enc 列写进 WHERE / ORDER BY / UNIQUE / INDEX】
// secretbox.Seal 每次用随机 nonce，同一明文两次加密结果不同，相等比较必然失败。
// 需要搜索的字段（email、sub2api_user_id）都在 customers 表且刻意保持明文。
type CustomerKYC struct {
	CustomerID    int64
	SubjectType   string // individual | company
	Status        string // draft | pending | approved | rejected
	CountryRegion string
	IDType        string

	// 个人主体
	LegalName string
	IDNumber  string
	BirthDate string
	Address   string

	// 公司主体
	CompanyName string
	RegNumber   string
	LegalRep    string
	RegAddress  string
	TaxNumber   string

	// 联系人（两种主体共用）
	ContactName  string
	ContactPhone string
	ContactEmail string

	// 收付款信息
	BankName    string
	BankAccount string
	BankHolder  string

	// 审核轨迹（明文）
	SubmittedAt *time.Time
	ReviewedAt  *time.Time
	ReviewedBy  string
	ReviewNote  string

	CreatedAt time.Time
	UpdatedAt time.Time
}

// kycCols 列清单，顺序与 012_credit_kyc.sql 的建表语句严格一致。
const kycCols = `customer_id, subject_type, status, country_region, id_type,
	legal_name_enc, id_number_enc, birth_date_enc, address_enc,
	company_name_enc, reg_number_enc, legal_rep_enc, reg_address_enc, tax_number_enc,
	contact_name_enc, contact_phone_enc, contact_email_enc,
	bank_name_enc, bank_account_enc, bank_holder_enc,
	submitted_at, reviewed_at, reviewed_by, review_note, created_at, updated_at`

// scanKYC 扫描一行 KYC 并就地解密全部 _enc 列。
//
// 注意：Scan 参数顺序与 kycCols 是手工维持的隐式契约，无编译期保护，
// 新增列必须在两处同序追加，否则所有 SELECT 会静默错位。
//
// 解密用 Open() 而非 MustOpen()：MustOpen 失败时静默返回空串，
// 对可重新配置的凭据尚可接受，对身份证号这类不可再生的 PII 不可接受 ——
// 密钥丢失必须让错误浮到调用方，而不是把 PII 悄悄显示成空白。
func (r *CreditRepo) scanKYC(row interface{ Scan(...any) error }) (*CustomerKYC, error) {
	var k CustomerKYC
	var submittedAt, reviewedAt sql.NullString
	var createdAt, updatedAt string

	// 先按密文扫进目标字段，再原地解密（避免声明 20 个中间变量）
	err := row.Scan(&k.CustomerID, &k.SubjectType, &k.Status, &k.CountryRegion, &k.IDType,
		&k.LegalName, &k.IDNumber, &k.BirthDate, &k.Address,
		&k.CompanyName, &k.RegNumber, &k.LegalRep, &k.RegAddress, &k.TaxNumber,
		&k.ContactName, &k.ContactPhone, &k.ContactEmail,
		&k.BankName, &k.BankAccount, &k.BankHolder,
		&submittedAt, &reviewedAt, &k.ReviewedBy, &k.ReviewNote, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}

	// 逐字段解密。任一失败即整体失败 —— 半解密的身份资料比没有更危险。
	for _, f := range []struct {
		name string
		ptr  *string
	}{
		{"legal_name", &k.LegalName}, {"id_number", &k.IDNumber},
		{"birth_date", &k.BirthDate}, {"address", &k.Address},
		{"company_name", &k.CompanyName}, {"reg_number", &k.RegNumber},
		{"legal_rep", &k.LegalRep}, {"reg_address", &k.RegAddress},
		{"tax_number", &k.TaxNumber},
		{"contact_name", &k.ContactName}, {"contact_phone", &k.ContactPhone},
		{"contact_email", &k.ContactEmail},
		{"bank_name", &k.BankName}, {"bank_account", &k.BankAccount},
		{"bank_holder", &k.BankHolder},
	} {
		plain, err := r.box.Open(*f.ptr)
		if err != nil {
			// 错误信息只带列名，绝不带值本身
			return nil, fmt.Errorf("解密 KYC 字段 %s 失败: %w", f.name, err)
		}
		*f.ptr = plain
	}

	k.SubmittedAt = parseTimePtr(submittedAt)
	k.ReviewedAt = parseTimePtr(reviewedAt)
	k.CreatedAt = parseTime(createdAt)
	k.UpdatedAt = parseTime(updatedAt)
	return &k, nil
}

// GetKYC 读取某客户的 KYC 资料。不存在时返回 ErrNotFound。
func (r *CreditRepo) GetKYC(ctx context.Context, customerID int64) (*CustomerKYC, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+kycCols+` FROM customer_kyc WHERE customer_id = ?`, customerID)
	k, err := r.scanKYC(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return k, err
}

// KYCParams KYC 写入参数。
//
// 全字段整体覆盖：KYC 是一份完整的身份档案，部分更新会让「这次没填」与
// 「这次要清空」不可区分。调用方（service）负责先 Get 再合并。
//
// 不含 status —— 状态迁移只走 UpsertKYC 的 submit 开关与 ReviewKYC，
// 避免保存草稿时误改审核状态。
type KYCParams struct {
	SubjectType   string
	CountryRegion string
	IDType        string

	LegalName string
	IDNumber  string
	BirthDate string
	Address   string

	CompanyName string
	RegNumber   string
	LegalRep    string
	RegAddress  string
	TaxNumber   string

	ContactName  string
	ContactPhone string
	ContactEmail string

	BankName    string
	BankAccount string
	BankHolder  string
}

// UpsertKYC 写入或整体覆盖 KYC 资料，返回写入后的记录。
//
// submit 区分两个业务动作，避免调用方拼状态字符串：
//   - false（保存草稿）：只写资料字段，status 与 submitted_at 一律保留原值，
//     首次插入落 draft。这保证「客户改一个错字」不会把 rejected 悄悄变回 draft 绕过审核。
//   - true（提交送审）：同时置 pending 并打提交时间戳。
//
// 审核轨迹（reviewed_at / reviewed_by / review_note）不在 UPDATE 列表里 ——
// 重新保存资料不应抹掉上一轮的审核意见，那是 ReviewKYC 的职责。
func (r *CreditRepo) UpsertKYC(ctx context.Context, customerID int64, p KYCParams, submit bool) (*CustomerKYC, error) {
	var exists int
	if err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM customers WHERE id = ?`, customerID).Scan(&exists); err != nil {
		return nil, err
	}
	if exists == 0 {
		return nil, ErrNotFound
	}

	now := nowUTC()
	// 冲突分支用 COALESCE(?, 原值) 表达「nil = 不修改」
	status, submittedAt := "draft", any(nil)
	if submit {
		status, submittedAt = "pending", any(now)
	}
	statusOverride := any(nil)
	if submit {
		statusOverride = status
	}

	s := r.box.Seal // 局部别名，让参数列表能与列顺序逐行对齐
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO customer_kyc (`+kycCols+`)
		VALUES (?,?,?,?,?, ?,?,?,?, ?,?,?,?,?, ?,?,?, ?,?,?, ?,?,?,?,?,?)
		ON CONFLICT(customer_id) DO UPDATE SET
			subject_type=excluded.subject_type,
			status=COALESCE(?, customer_kyc.status),
			country_region=excluded.country_region,
			id_type=excluded.id_type,
			legal_name_enc=excluded.legal_name_enc,
			id_number_enc=excluded.id_number_enc,
			birth_date_enc=excluded.birth_date_enc,
			address_enc=excluded.address_enc,
			company_name_enc=excluded.company_name_enc,
			reg_number_enc=excluded.reg_number_enc,
			legal_rep_enc=excluded.legal_rep_enc,
			reg_address_enc=excluded.reg_address_enc,
			tax_number_enc=excluded.tax_number_enc,
			contact_name_enc=excluded.contact_name_enc,
			contact_phone_enc=excluded.contact_phone_enc,
			contact_email_enc=excluded.contact_email_enc,
			bank_name_enc=excluded.bank_name_enc,
			bank_account_enc=excluded.bank_account_enc,
			bank_holder_enc=excluded.bank_holder_enc,
			submitted_at=COALESCE(?, customer_kyc.submitted_at),
			updated_at=excluded.updated_at`,
		customerID, p.SubjectType, status, p.CountryRegion, p.IDType,
		s(p.LegalName), s(p.IDNumber), s(p.BirthDate), s(p.Address),
		s(p.CompanyName), s(p.RegNumber), s(p.LegalRep), s(p.RegAddress), s(p.TaxNumber),
		s(p.ContactName), s(p.ContactPhone), s(p.ContactEmail),
		s(p.BankName), s(p.BankAccount), s(p.BankHolder),
		submittedAt, nil, "", "", now, now,
		// ON CONFLICT 分支
		statusOverride, submittedAt)
	if err != nil {
		return nil, err
	}
	return r.GetKYC(ctx, customerID)
}

// ReviewKYC 审核：写入终态与审核轨迹。
func (r *CreditRepo) ReviewKYC(ctx context.Context, customerID int64, status, reviewer, note string) (*CustomerKYC, error) {
	now := nowUTC()
	res, err := r.db.ExecContext(ctx, `
		UPDATE customer_kyc
		SET status=?, reviewed_at=?, reviewed_by=?, review_note=?, updated_at=?
		WHERE customer_id=?`,
		status, now, reviewer, note, now, customerID)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return r.GetKYC(ctx, customerID)
}
