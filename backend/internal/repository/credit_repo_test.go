package repository

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sub2api-account-monitor/internal/pkg/secretbox"
)

// newTestCreditRepo 建一个临时 SQLite（跑全部迁移）并返回 CreditRepo。
func newTestCreditRepo(t *testing.T) *CreditRepo {
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
	// &secretbox.Box{} 为明文直通实例，测试不依赖 MONITOR_CREDENTIALS_KEY
	return NewCreditRepo(s, &secretbox.Box{})
}

// mustCustomer 建一个带授信额度的客户。
func mustCustomer(t *testing.T, r *CreditRepo, userID string, limit float64) *Customer {
	t.Helper()
	c, err := r.CreateCustomer(context.Background(), CustomerParams{
		Sub2apiUserID: userID,
		DisplayName:   userID + "-name",
		CreditLimit:   limit,
	})
	if err != nil {
		t.Fatalf("建客户失败: %v", err)
	}
	return c
}

// mustEntry 记一笔台账。
func mustEntry(t *testing.T, r *CreditRepo, customerID int64, entryType string, amount float64) *Customer {
	t.Helper()
	c, err := r.AppendEntry(context.Background(), EntryParams{
		CustomerID: customerID,
		EntryType:  entryType,
		Amount:     amount,
		OccurredAt: time.Now(),
		Operator:   "tester",
	})
	if err != nil {
		t.Fatalf("记账失败(%s %v): %v", entryType, amount, err)
	}
	return c
}

// TestCustomerColsScanContract cols/scan 隐式契约：各字段值互不相同，读回逐字段断言。
//
// customerCols 的列顺序与 scanCustomer 的 Scan 参数顺序是手工维持的契约，无编译期保护。
// 若两处不同步，SELECT 会静默错位（例如 note 读到 admin_note 的值），本用例是唯一防线。
func TestCustomerColsScanContract(t *testing.T) {
	repo := newTestCreditRepo(t)
	ctx := context.Background()

	created, err := repo.CreateCustomer(ctx, CustomerParams{
		Sub2apiUserID: "uid-777",
		DisplayName:   "显示名",
		Email:         "a@example.com",
		Note:          "客户可见备注",
		AdminNote:     "内部备注",
		CreditLimit:   1234.56,
		Status:        "active",
	})
	if err != nil {
		t.Fatalf("建客户失败: %v", err)
	}
	// 让 alert_level / alert_at / last_entry_at / outstanding 都是非零值，
	// 否则零值列错位也看不出来
	fired := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	if err := repo.SetAlertLevel(ctx, created.ID, 80, &fired); err != nil {
		t.Fatalf("写闩锁失败: %v", err)
	}
	mustEntry(t, repo, created.ID, "advance", 999.99)

	got, err := repo.GetCustomer(ctx, created.ID)
	if err != nil {
		t.Fatalf("读客户失败: %v", err)
	}

	if got.Sub2apiUserID != "uid-777" {
		t.Errorf("sub2api_user_id 错位: %q", got.Sub2apiUserID)
	}
	if got.DisplayName != "显示名" {
		t.Errorf("display_name 错位: %q", got.DisplayName)
	}
	if got.Email != "a@example.com" {
		t.Errorf("email 错位: %q", got.Email)
	}
	if got.Note != "客户可见备注" {
		t.Errorf("note 错位: %q", got.Note)
	}
	if got.AdminNote != "内部备注" {
		t.Errorf("admin_note 错位: %q", got.AdminNote)
	}
	if got.CreditLimit != 1234.56 {
		t.Errorf("credit_limit 错位: %v", got.CreditLimit)
	}
	if got.Outstanding != 999.99 {
		t.Errorf("outstanding 错位: %v", got.Outstanding)
	}
	if got.Status != "active" {
		t.Errorf("status 错位: %q", got.Status)
	}
	if got.AlertLevel != 80 {
		t.Errorf("alert_level 错位: %v", got.AlertLevel)
	}
	if got.AlertAt == nil || !got.AlertAt.Equal(fired) {
		t.Errorf("alert_at 错位: %v", got.AlertAt)
	}
	if got.LastEntryAt == nil {
		t.Error("last_entry_at 应在记账后非空")
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Errorf("时间戳未解析: created=%v updated=%v", got.CreatedAt, got.UpdatedAt)
	}
}

// TestLedgerColsScanContract 台账 cols/scan 契约，同上。
func TestLedgerColsScanContract(t *testing.T) {
	repo := newTestCreditRepo(t)
	ctx := context.Background()
	c := mustCustomer(t, repo, "uid-ledger", 1000)

	occurred := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	orig := int64(4242)
	if _, err := repo.AppendEntry(ctx, EntryParams{
		CustomerID:  c.ID,
		EntryType:   "repayment",
		Amount:      88.25,
		Currency:    "CNY",
		OccurredAt:  occurred,
		Note:        "备注文本",
		ExternalRef: "REF-9",
		Operator:    "operator-x",
		ReversedOf:  &orig,
	}); err != nil {
		t.Fatalf("记账失败: %v", err)
	}

	items, total, err := repo.ListEntries(ctx, c.ID, 1, 10)
	if err != nil {
		t.Fatalf("查台账失败: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("应有 1 条分录，实际 total=%d len=%d", total, len(items))
	}
	e := items[0]
	if e.CustomerID != c.ID {
		t.Errorf("customer_id 错位: %v", e.CustomerID)
	}
	if e.EntryType != "repayment" {
		t.Errorf("entry_type 错位: %q", e.EntryType)
	}
	if e.Amount != 88.25 {
		t.Errorf("amount 错位: %v", e.Amount)
	}
	if e.Currency != "CNY" {
		t.Errorf("currency 错位: %q", e.Currency)
	}
	if !e.OccurredAt.Equal(occurred) {
		t.Errorf("occurred_at 错位: %v", e.OccurredAt)
	}
	if e.Note != "备注文本" {
		t.Errorf("note 错位: %q", e.Note)
	}
	if e.ExternalRef != "REF-9" {
		t.Errorf("external_ref 错位: %q", e.ExternalRef)
	}
	if e.Operator != "operator-x" {
		t.Errorf("operator 错位: %q", e.Operator)
	}
	if e.ReversedOf == nil || *e.ReversedOf != orig {
		t.Errorf("reversed_of 错位: %v", e.ReversedOf)
	}
	if e.CreatedAt.IsZero() {
		t.Error("created_at 未解析")
	}
}

// TestOutstandingEqualsLedgerSum 敞口 == Σ垫付 − Σ回款。
func TestOutstandingEqualsLedgerSum(t *testing.T) {
	repo := newTestCreditRepo(t)
	c := mustCustomer(t, repo, "uid-sum", 1000)

	mustEntry(t, repo, c.ID, "advance", 800)
	mustEntry(t, repo, c.ID, "advance", 100)
	got := mustEntry(t, repo, c.ID, "repayment", 250)

	const want = 800 + 100 - 250
	if got.Outstanding != want {
		t.Fatalf("敞口应为 %v，实际 %v", want, got.Outstanding)
	}
	if got.Available() != 1000-want {
		t.Fatalf("可用额度应为 %v，实际 %v", 1000-want, got.Available())
	}
}

// TestOutstandingCanGoNegative 回款超过垫付时敞口为负（预付），不夹紧到 0。
func TestOutstandingCanGoNegative(t *testing.T) {
	repo := newTestCreditRepo(t)
	c := mustCustomer(t, repo, "uid-neg", 1000)

	mustEntry(t, repo, c.ID, "advance", 100)
	got := mustEntry(t, repo, c.ID, "repayment", 300)
	if got.Outstanding != -200 {
		t.Fatalf("敞口应为 -200（预付），实际 %v", got.Outstanding)
	}
}

// TestAppendEntryRejectsUnknownCustomer SQLite 默认不强制外键，须显式校验。
func TestAppendEntryRejectsUnknownCustomer(t *testing.T) {
	repo := newTestCreditRepo(t)
	_, err := repo.AppendEntry(context.Background(), EntryParams{
		CustomerID: 99999,
		EntryType:  "advance",
		Amount:     10,
		OccurredAt: time.Now(),
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("对不存在的客户记账应返回 ErrNotFound，实际 %v", err)
	}
	// 且不得留下孤儿分录
	var n int
	if err := repo.db.QueryRow(`SELECT COUNT(1) FROM credit_ledger`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("事务应回滚，credit_ledger 不应有行，实际 %d 行", n)
	}
}

// TestRecalcIsIdempotent 重算幂等：跑两次结果相同，且不改变敞口。
func TestRecalcIsIdempotent(t *testing.T) {
	repo := newTestCreditRepo(t)
	ctx := context.Background()
	c := mustCustomer(t, repo, "uid-recalc", 500)
	mustEntry(t, repo, c.ID, "advance", 300)
	mustEntry(t, repo, c.ID, "repayment", 50)

	first, err := repo.RecalcCustomer(ctx, c.ID)
	if err != nil {
		t.Fatalf("重算失败: %v", err)
	}
	second, err := repo.RecalcCustomer(ctx, c.ID)
	if err != nil {
		t.Fatalf("二次重算失败: %v", err)
	}
	if first.Outstanding != 250 || second.Outstanding != 250 {
		t.Fatalf("重算应恒为 250，实际 %v / %v", first.Outstanding, second.Outstanding)
	}
}

// TestRecalcAllRepairsDrift 冗余列漂移时 RecalcAll 能修回台账真相。
func TestRecalcAllRepairsDrift(t *testing.T) {
	repo := newTestCreditRepo(t)
	ctx := context.Background()
	c := mustCustomer(t, repo, "uid-drift", 500)
	mustEntry(t, repo, c.ID, "advance", 400)

	// 人为制造漂移（模拟历史脏数据/漏改的代码路径）
	if _, err := repo.db.Exec(`UPDATE customers SET outstanding = 0 WHERE id = ?`, c.ID); err != nil {
		t.Fatal(err)
	}
	ids, err := repo.RecalcAll(ctx)
	if err != nil {
		t.Fatalf("全量重算失败: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("应重算 1 个客户，实际 %d", len(ids))
	}
	got, err := repo.GetCustomer(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Outstanding != 400 {
		t.Fatalf("重算应修回 400，实际 %v", got.Outstanding)
	}
}

// TestHasReversal 冲正标记可被检出（防重复冲正的依据）。
func TestHasReversal(t *testing.T) {
	repo := newTestCreditRepo(t)
	ctx := context.Background()
	c := mustCustomer(t, repo, "uid-rev", 1000)
	mustEntry(t, repo, c.ID, "advance", 200)

	items, _, err := repo.ListEntries(ctx, c.ID, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	origID := items[0].ID

	if done, err := repo.HasReversal(ctx, origID); err != nil || done {
		t.Fatalf("尚未冲正时应为 false，实际 %v err=%v", done, err)
	}
	if _, err := repo.AppendEntry(ctx, EntryParams{
		CustomerID: c.ID,
		EntryType:  "repayment",
		Amount:     200,
		OccurredAt: time.Now(),
		ReversedOf: &origID,
	}); err != nil {
		t.Fatal(err)
	}
	if done, err := repo.HasReversal(ctx, origID); err != nil || !done {
		t.Fatalf("冲正后应为 true，实际 %v err=%v", done, err)
	}
}

// TestSummaryCountsBands 总览的超额/预警计数按 80% 分档，未授信客户不计入。
func TestSummaryCountsBands(t *testing.T) {
	repo := newTestCreditRepo(t)
	ctx := context.Background()

	over := mustCustomer(t, repo, "uid-over", 100)
	mustEntry(t, repo, over.ID, "advance", 120) // 120%

	warn := mustCustomer(t, repo, "uid-warn", 100)
	mustEntry(t, repo, warn.ID, "advance", 85) // 85%

	safe := mustCustomer(t, repo, "uid-safe", 100)
	mustEntry(t, repo, safe.ID, "advance", 10) // 10%

	// 未授信：额度 0，敞口再大也不该进任何档
	none := mustCustomer(t, repo, "uid-none", 0)
	mustEntry(t, repo, none.ID, "advance", 999)

	s, err := repo.Summary(ctx)
	if err != nil {
		t.Fatalf("总览失败: %v", err)
	}
	if s.CustomerCount != 4 {
		t.Errorf("客户数应为 4，实际 %d", s.CustomerCount)
	}
	if s.GrantedCount != 3 {
		t.Errorf("已授信数应为 3，实际 %d", s.GrantedCount)
	}
	if s.TotalLimit != 300 {
		t.Errorf("授信总额应为 300，实际 %v", s.TotalLimit)
	}
	if s.TotalOutstanding != 120+85+10+999 {
		t.Errorf("敞口合计错误: %v", s.TotalOutstanding)
	}
	if s.OverLimitCount != 1 {
		t.Errorf("超额数应为 1，实际 %d", s.OverLimitCount)
	}
	if s.WarningCount != 1 {
		t.Errorf("预警数应为 1，实际 %d", s.WarningCount)
	}
}

// TestArchiveExcludedFromSummary 归档客户不计入总览。
func TestArchiveExcludedFromSummary(t *testing.T) {
	repo := newTestCreditRepo(t)
	ctx := context.Background()
	c := mustCustomer(t, repo, "uid-arch", 100)
	mustEntry(t, repo, c.ID, "advance", 50)

	if err := repo.ArchiveCustomer(ctx, c.ID); err != nil {
		t.Fatalf("归档失败: %v", err)
	}
	s, err := repo.Summary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s.CustomerCount != 0 || s.TotalOutstanding != 0 {
		t.Fatalf("归档客户不应计入总览，实际 count=%d outstanding=%v", s.CustomerCount, s.TotalOutstanding)
	}
	// 但台账必须保留（归档不是删除）
	_, total, err := repo.ListEntries(ctx, c.ID, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("归档后台账应保留，实际 %d 条", total)
	}
}

// TestGetCustomerNotFound 缺失记录返回 ErrNotFound 而非 sql.ErrNoRows。
func TestGetCustomerNotFound(t *testing.T) {
	repo := newTestCreditRepo(t)
	ctx := context.Background()
	if _, err := repo.GetCustomer(ctx, 12345); !errors.Is(err, ErrNotFound) {
		t.Fatalf("应返回 ErrNotFound，实际 %v", err)
	}
	if _, err := repo.GetCustomerBySub2apiID(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("应返回 ErrNotFound，实际 %v", err)
	}
	if _, err := repo.GetEntry(ctx, 12345); !errors.Is(err, ErrNotFound) {
		t.Fatalf("应返回 ErrNotFound，实际 %v", err)
	}
	if err := repo.ArchiveCustomer(ctx, 12345); !errors.Is(err, ErrNotFound) {
		t.Fatalf("应返回 ErrNotFound，实际 %v", err)
	}
}

// TestSub2apiUserIDIsUnique 客户口径唯一：同一 sub2api 用户不能建两条。
//
// 断言具体的 ErrDuplicate 而非泛泛的「有错」：handler 靠它区分 409 与 500，
// 只断言 err != nil 的话，错误类型退化成原始驱动错误也发现不了。
func TestSub2apiUserIDIsUnique(t *testing.T) {
	repo := newTestCreditRepo(t)
	ctx := context.Background()
	mustCustomer(t, repo, "uid-dup", 100)
	if _, err := repo.CreateCustomer(ctx, CustomerParams{Sub2apiUserID: "uid-dup"}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("重复建档应返回 ErrDuplicate，实际 %v", err)
	}
}

// TestUpdateCustomerDuplicate 改 user_id 撞已占用值同样是冲突而非 500。
//
// 前端虽禁用了该字段，但 API 是公开的。
func TestUpdateCustomerDuplicate(t *testing.T) {
	repo := newTestCreditRepo(t)
	ctx := context.Background()
	mustCustomer(t, repo, "uid-a", 100)
	b := mustCustomer(t, repo, "uid-b", 100)

	_, err := repo.UpdateCustomer(ctx, b.ID, CustomerParams{Sub2apiUserID: "uid-a"})
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("改到已占用的 user_id 应返回 ErrDuplicate，实际 %v", err)
	}
}

// TestListEnrolledUserIDs 已建档集合含归档客户。
//
// 归档只是停止跟进，档案还在，重复建档照样撞 UNIQUE —— 下拉必须照样禁选。
func TestListEnrolledUserIDs(t *testing.T) {
	repo := newTestCreditRepo(t)
	ctx := context.Background()
	mustCustomer(t, repo, "uid-active", 100)
	archived := mustCustomer(t, repo, "uid-archived", 100)
	if err := repo.ArchiveCustomer(ctx, archived.ID); err != nil {
		t.Fatal(err)
	}

	got, err := repo.ListEnrolledUserIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !got["uid-active"] || !got["uid-archived"] {
		t.Fatalf("已建档集合应含归档客户，实际 %v", got)
	}
	if got["uid-never"] {
		t.Error("未建档的 user_id 不该出现在集合里")
	}
}

// TestListCustomersSort 排序白名单：合法列生效，非法列静默回退，注入串不执行。
//
// 排序列名直接拼进 SQL（无法参数化），白名单是唯一防线，必须有测试守住。
func TestListCustomersSort(t *testing.T) {
	repo := newTestCreditRepo(t)
	ctx := context.Background()

	// 敞口：low=10（额度 500）、high=90（额度 100）
	// 故「按敞口降序」与「按可用额度升序」会得到不同顺序，能区分排序键是否真的生效
	low := mustCustomer(t, repo, "s-low", 500)
	high := mustCustomer(t, repo, "s-high", 100)
	mustEntry(t, repo, low.ID, "advance", 10)
	mustEntry(t, repo, high.ID, "advance", 90)

	ids := func(f CustomerFilter) []string {
		f.Page, f.PageSize = 1, 10
		got, _, err := repo.ListCustomers(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, 0, len(got))
		for _, c := range got {
			out = append(out, c.Sub2apiUserID)
		}
		return out
	}

	// 默认序：敞口降序
	if got := ids(CustomerFilter{}); got[0] != "s-high" {
		t.Errorf("默认应按敞口降序，实际 %v", got)
	}
	// 可用额度升序：high 剩 10、low 剩 490 → high 在前
	if got := ids(CustomerFilter{Sort: "available", Order: "asc"}); got[0] != "s-high" {
		t.Errorf("可用额度升序失效，实际 %v", got)
	}
	// 可用额度降序：low 在前，证明 order 参数真的被读了
	if got := ids(CustomerFilter{Sort: "available", Order: "desc"}); got[0] != "s-low" {
		t.Errorf("可用额度降序失效，实际 %v", got)
	}
	// 额度升序：high 的额度 100 < low 的 500
	if got := ids(CustomerFilter{Sort: "limit", Order: "asc"}); got[0] != "s-high" {
		t.Errorf("额度升序失效，实际 %v", got)
	}

	// 非法列静默回退默认序，不报错也不注入
	for _, bad := range []string{"", "nope", "password_hash", "outstanding; DROP TABLE customers"} {
		got, _, err := repo.ListCustomers(ctx, CustomerFilter{Sort: bad, Page: 1, PageSize: 10})
		if err != nil {
			t.Fatalf("非法排序列 %q 应静默回退而非报错: %v", bad, err)
		}
		if len(got) != 2 || got[0].Sub2apiUserID != "s-high" {
			t.Errorf("非法排序列 %q 未回退默认序，实际 %v", bad, got)
		}
	}
	// 表还在（注入串没被执行）
	if _, _, err := repo.ListCustomers(ctx, CustomerFilter{Page: 1, PageSize: 10}); err != nil {
		t.Fatalf("customers 表应完好: %v", err)
	}
}

// TestListCustomersFilters 关键词只匹配明文列，状态过滤生效。
func TestListCustomersFilters(t *testing.T) {
	repo := newTestCreditRepo(t)
	ctx := context.Background()

	a := mustCustomer(t, repo, "alpha", 100)
	mustCustomer(t, repo, "beta", 100)
	if err := repo.ArchiveCustomer(ctx, a.ID); err != nil {
		t.Fatal(err)
	}

	got, total, err := repo.ListCustomers(ctx, CustomerFilter{Status: "active", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(got) != 1 || got[0].Sub2apiUserID != "beta" {
		t.Fatalf("状态过滤失效: total=%d got=%v", total, got)
	}

	got, total, err = repo.ListCustomers(ctx, CustomerFilter{Keyword: "alph", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(got) != 1 || got[0].Sub2apiUserID != "alpha" {
		t.Fatalf("关键词过滤失效: total=%d got=%v", total, got)
	}
}

// TestListCustomersOrdersByOutstanding 欠得多的排前面（运营优先看应收）。
func TestListCustomersOrdersByOutstanding(t *testing.T) {
	repo := newTestCreditRepo(t)
	ctx := context.Background()

	small := mustCustomer(t, repo, "uid-small", 1000)
	big := mustCustomer(t, repo, "uid-big", 1000)
	mustEntry(t, repo, small.ID, "advance", 10)
	mustEntry(t, repo, big.ID, "advance", 900)

	got, _, err := repo.ListCustomers(ctx, CustomerFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Sub2apiUserID != "uid-big" {
		t.Fatalf("应按敞口降序，实际 %v", got)
	}
}

// TestSetAlertLevelKeepsAlertAtOnDowngrade 降档不覆盖 alert_at（保留最后一次告警时刻）。
func TestSetAlertLevelKeepsAlertAtOnDowngrade(t *testing.T) {
	repo := newTestCreditRepo(t)
	ctx := context.Background()
	c := mustCustomer(t, repo, "uid-latch", 100)

	fired := time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)
	if err := repo.SetAlertLevel(ctx, c.ID, 100, &fired); err != nil {
		t.Fatal(err)
	}
	// 降档：firedAt 传 nil，档位归零但时间戳保留
	if err := repo.SetAlertLevel(ctx, c.ID, 0, nil); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetCustomer(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AlertLevel != 0 {
		t.Fatalf("档位应归零，实际 %d", got.AlertLevel)
	}
	if got.AlertAt == nil || !got.AlertAt.Equal(fired) {
		t.Fatalf("alert_at 应保留上次告警时刻，实际 %v", got.AlertAt)
	}
}
