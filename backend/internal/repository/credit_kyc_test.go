package repository

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"sub2api-account-monitor/internal/pkg/secretbox"
)

// newEncryptedRepo 建一个**启用了加密**的 CreditRepo（区别于 newTestCreditRepo 的明文直通）。
func newEncryptedRepo(t *testing.T) *CreditRepo {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := NewSQLite(path)
	if err != nil {
		t.Fatalf("初始化 SQLite 失败: %v", err)
	}
	t.Cleanup(func() {
		_ = s.Close()
		_ = os.Remove(path)
	})
	// 32 字节全零密钥：测试只关心「加密确实发生」，不关心密钥强度
	t.Setenv("MONITOR_CREDENTIALS_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	box := secretbox.FromEnv()
	if !box.Enabled() {
		t.Fatal("测试前置条件失败：加密未启用")
	}
	return NewCreditRepo(s, box)
}

// fullKYCParams 各字段值互不相同，用于验证 kycCols/scanKYC 的列序契约。
func fullKYCParams() KYCParams {
	return KYCParams{
		SubjectType:   "company",
		CountryRegion: "country-CN",
		IDType:        "idtype-passport",

		LegalName: "name-张三",
		IDNumber:  "idnum-110101199001011234",
		BirthDate: "birth-1990-01-01",
		Address:   "addr-北京市朝阳区",

		CompanyName: "company-某某科技",
		RegNumber:   "reg-91110108MA01",
		LegalRep:    "rep-李四",
		RegAddress:  "regaddr-上海市浦东新区",
		TaxNumber:   "tax-91110108MA01XXXX",

		ContactName:  "contact-王五",
		ContactPhone: "phone-13800138000",
		ContactEmail: "email-a@example.com",

		BankName:    "bank-某某银行",
		BankAccount: "acct-6222020000000000",
		BankHolder:  "holder-某某科技有限公司",
	}
}

// TestKYCColsScanContract 全字段互异的一行读回后逐字段断言，锁死 kycCols 与 scanKYC 的列序。
//
// 这是风险清单里的第 5 条：26 列的隐式契约没有编译期保护，
// 任一处漏改都会让所有 SELECT 静默错位（比如身份证号显示成银行账号）。
func TestKYCColsScanContract(t *testing.T) {
	r := newTestCreditRepo(t)
	ctx := context.Background()
	cust := mustCustomer(t, r, "uid-kyc-cols", 1000)

	p := fullKYCParams()
	if _, err := r.UpsertKYC(ctx, cust.ID, p, false); err != nil {
		t.Fatalf("UpsertKYC 失败: %v", err)
	}
	got, err := r.GetKYC(ctx, cust.ID)
	if err != nil {
		t.Fatalf("GetKYC 失败: %v", err)
	}

	// 用反射逐字段比对：新增字段时本断言自动覆盖，无需手工补
	pv, gv := reflect.ValueOf(p), reflect.ValueOf(*got)
	for i := 0; i < pv.NumField(); i++ {
		name := pv.Type().Field(i).Name
		want := pv.Field(i).String()
		gf := gv.FieldByName(name)
		if !gf.IsValid() {
			t.Fatalf("CustomerKYC 缺少字段 %s", name)
		}
		if gf.String() != want {
			t.Errorf("字段 %s 错位：want %q, got %q", name, want, gf.String())
		}
	}

	if got.CustomerID != cust.ID {
		t.Errorf("CustomerID: want %d, got %d", cust.ID, got.CustomerID)
	}
	if got.Status != "draft" {
		t.Errorf("首次保存草稿应为 draft，got %q", got.Status)
	}
	if got.SubmittedAt != nil {
		t.Error("存草稿不应打提交时间戳")
	}
}

// TestKYCEncryptionRoundTrip 启用加密后：库里是密文，读回是明文。
func TestKYCEncryptionRoundTrip(t *testing.T) {
	r := newEncryptedRepo(t)
	ctx := context.Background()
	cust := mustCustomer(t, r, "uid-kyc-enc", 1000)

	p := fullKYCParams()
	if _, err := r.UpsertKYC(ctx, cust.ID, p, false); err != nil {
		t.Fatalf("UpsertKYC 失败: %v", err)
	}

	// 绕开 repo 直接查库，确认 _enc 列确实是密文
	var idNumber, bankAccount, countryRegion string
	err := r.db.QueryRowContext(ctx,
		`SELECT id_number_enc, bank_account_enc, country_region FROM customer_kyc WHERE customer_id = ?`,
		cust.ID).Scan(&idNumber, &bankAccount, &countryRegion)
	if err != nil {
		t.Fatalf("直查失败: %v", err)
	}
	for _, tc := range []struct{ name, val string }{
		{"id_number_enc", idNumber},
		{"bank_account_enc", bankAccount},
	} {
		if !strings.HasPrefix(tc.val, "enc:v1:") {
			t.Errorf("%s 未加密: %q", tc.name, tc.val)
		}
	}
	if strings.Contains(idNumber, "110101199001011234") {
		t.Error("身份证号以明文出现在库中")
	}
	// 明文列不应被加密（它们要参与搜索）
	if countryRegion != p.CountryRegion {
		t.Errorf("country_region 应保持明文: want %q, got %q", p.CountryRegion, countryRegion)
	}

	got, err := r.GetKYC(ctx, cust.ID)
	if err != nil {
		t.Fatalf("GetKYC 失败: %v", err)
	}
	if got.IDNumber != p.IDNumber {
		t.Errorf("解密后不等于原文: want %q, got %q", p.IDNumber, got.IDNumber)
	}
	if got.BankAccount != p.BankAccount {
		t.Errorf("解密后不等于原文: want %q, got %q", p.BankAccount, got.BankAccount)
	}
}

// TestKYCWrongKeyFailsLoudly 密钥不匹配时必须报错，绝不能静默返回空串。
//
// 这是选用 Open() 而非 MustOpen() 的理由：身份证号不可再生，
// 悄悄显示成空白会让人以为「客户没填」，进而覆盖掉尚可挽救的密文。
func TestKYCWrongKeyFailsLoudly(t *testing.T) {
	r := newEncryptedRepo(t)
	ctx := context.Background()
	cust := mustCustomer(t, r, "uid-kyc-badkey", 1000)
	if _, err := r.UpsertKYC(ctx, cust.ID, fullKYCParams(), false); err != nil {
		t.Fatalf("UpsertKYC 失败: %v", err)
	}

	// 换一把不同的密钥
	key := make([]byte, 32)
	key[0] = 1
	t.Setenv("MONITOR_CREDENTIALS_KEY", base64.StdEncoding.EncodeToString(key))
	r.box = secretbox.FromEnv()

	if _, err := r.GetKYC(ctx, cust.ID); err == nil {
		t.Fatal("密钥不匹配时 GetKYC 应返回错误，实际返回 nil")
	} else if !strings.Contains(err.Error(), "解密") {
		t.Errorf("错误信息应指明解密失败，got %v", err)
	}
}

// TestUpsertKYCPreservesStatus 重复保存草稿不改动审核状态。
func TestUpsertKYCPreservesStatus(t *testing.T) {
	r := newTestCreditRepo(t)
	ctx := context.Background()
	cust := mustCustomer(t, r, "uid-kyc-status", 1000)

	p := fullKYCParams()
	if _, err := r.UpsertKYC(ctx, cust.ID, p, true); err != nil { // 提交送审
		t.Fatalf("提交失败: %v", err)
	}
	k, err := r.GetKYC(ctx, cust.ID)
	if err != nil {
		t.Fatalf("GetKYC 失败: %v", err)
	}
	if k.Status != "pending" {
		t.Fatalf("提交后应为 pending，got %q", k.Status)
	}
	if k.SubmittedAt == nil {
		t.Fatal("提交后应有 submitted_at")
	}
	submittedAt := *k.SubmittedAt

	// 审核驳回
	if _, err := r.ReviewKYC(ctx, cust.ID, "rejected", "admin", "证件照不清晰"); err != nil {
		t.Fatalf("审核失败: %v", err)
	}

	// 再次保存草稿：不得把 rejected 悄悄改回 draft（那会绕过审核）
	p.Address = "addr-改了地址"
	if _, err := r.UpsertKYC(ctx, cust.ID, p, false); err != nil {
		t.Fatalf("再次保存失败: %v", err)
	}
	k, err = r.GetKYC(ctx, cust.ID)
	if err != nil {
		t.Fatalf("GetKYC 失败: %v", err)
	}
	if k.Status != "rejected" {
		t.Errorf("保存草稿不应改动审核状态：want rejected, got %q", k.Status)
	}
	if k.Address != "addr-改了地址" {
		t.Errorf("资料未更新: got %q", k.Address)
	}
	if k.ReviewNote != "证件照不清晰" {
		t.Errorf("保存草稿不应抹掉审核意见: got %q", k.ReviewNote)
	}
	if k.SubmittedAt == nil || !k.SubmittedAt.Equal(submittedAt) {
		t.Error("保存草稿不应改动 submitted_at")
	}
}

// TestKYCNotFound 未录入与客户不存在都应返回 ErrNotFound。
func TestKYCNotFound(t *testing.T) {
	r := newTestCreditRepo(t)
	ctx := context.Background()

	if _, err := r.GetKYC(ctx, 9999); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在的客户应返回 ErrNotFound，got %v", err)
	}
	cust := mustCustomer(t, r, "uid-kyc-none", 0)
	if _, err := r.GetKYC(ctx, cust.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("未录入 KYC 应返回 ErrNotFound，got %v", err)
	}
	if _, err := r.UpsertKYC(ctx, 9999, fullKYCParams(), false); !errors.Is(err, ErrNotFound) {
		t.Errorf("向不存在的客户写 KYC 应返回 ErrNotFound，got %v", err)
	}
}
