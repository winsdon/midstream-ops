package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"sub2api-account-monitor/internal/pkg/secretbox"
	"sub2api-account-monitor/internal/repository"
)

// fakeCreditNotifier 记录收到的告警事件（替代真实通知渠道）。
type fakeCreditNotifier struct {
	events []CreditAlertEvent
}

func (f *fakeCreditNotifier) HandleCreditAlert(ev CreditAlertEvent) {
	f.events = append(f.events, ev)
}

func (f *fakeCreditNotifier) bands() []int {
	out := make([]int, 0, len(f.events))
	for _, e := range f.events {
		out = append(out, e.Band)
	}
	return out
}

// newTestCreditService 建临时 monitor 测试库 + CreditService + 假通知器。
func newTestCreditService(t *testing.T) (*CreditService, *fakeCreditNotifier) {
	t.Helper()
	s := newTestStore(t)
	notifier := &fakeCreditNotifier{}
	return NewCreditService(repository.NewCreditRepo(s, &secretbox.Box{}), notifier), notifier
}

// newCustomer 建客户。
func newCustomer(t *testing.T, svc *CreditService, userID string, limit float64) *repository.Customer {
	t.Helper()
	c, err := svc.CreateCustomer(context.Background(), repository.CustomerParams{
		Sub2apiUserID: userID,
		DisplayName:   userID,
		CreditLimit:   limit,
	})
	if err != nil {
		t.Fatalf("建客户失败: %v", err)
	}
	return c
}

// advance 记一笔垫付。
func advance(t *testing.T, svc *CreditService, id int64, amount float64) *repository.Customer {
	t.Helper()
	c, err := svc.AppendEntry(context.Background(), AppendEntryInput{
		CustomerID: id, EntryType: EntryTypeAdvance, Amount: amount, Operator: "tester",
	})
	if err != nil {
		t.Fatalf("垫付 %v 失败: %v", amount, err)
	}
	return c
}

// repay 记一笔回款。
func repay(t *testing.T, svc *CreditService, id int64, amount float64) *repository.Customer {
	t.Helper()
	c, err := svc.AppendEntry(context.Background(), AppendEntryInput{
		CustomerID: id, EntryType: EntryTypeRepayment, Amount: amount, Operator: "tester",
	})
	if err != nil {
		t.Fatalf("回款 %v 失败: %v", amount, err)
	}
	return c
}

// TestCreditBand 档位计算：未授信恒为 0，边界值取闭区间。
func TestCreditBand(t *testing.T) {
	cases := []struct {
		name        string
		outstanding float64
		limit       float64
		want        int
	}{
		{"未授信-额度0", 500, 0, 0},
		{"未授信-额度负", 500, -1, 0},
		{"零敞口", 0, 100, 0},
		{"79%", 79, 100, 0},
		{"80%边界", 80, 100, creditBandWarning},
		{"99%", 99, 100, creditBandWarning},
		{"100%边界", 100, 100, creditBandOverflow},
		{"超额", 150, 100, creditBandOverflow},
		{"负敞口-预付", -50, 100, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := creditBand(c.outstanding, c.limit); got != c.want {
				t.Fatalf("creditBand(%v, %v) = %d，期望 %d", c.outstanding, c.limit, got, c.want)
			}
		})
	}
}

// TestAlertLatchEdgeTriggered 闩锁的完整边沿序列。
//
// 这是本模块最容易回归的行为：同档内静默、升档才发、降档静默改写闩锁、
// 降档后重新冲高必须再发。任何一条断言失败都意味着运营方会收到噪声或漏掉告警。
func TestAlertLatchEdgeTriggered(t *testing.T) {
	svc, notifier := newTestCreditService(t)
	c := newCustomer(t, svc, "uid-latch", 1000)

	// 79% —— 未达档，不发
	advance(t, svc, c.ID, 790)
	if len(notifier.events) != 0 {
		t.Fatalf("79%% 不应告警，实际发了 %v", notifier.bands())
	}

	// 81% —— 升到 80 档，发一条
	advance(t, svc, c.ID, 20)
	if got := notifier.bands(); len(got) != 1 || got[0] != 80 {
		t.Fatalf("81%% 应发一条 80 档告警，实际 %v", got)
	}

	// 85% —— 同档内，静默
	advance(t, svc, c.ID, 40)
	if got := notifier.bands(); len(got) != 1 {
		t.Fatalf("同档内不应重复告警，实际 %v", got)
	}

	// 105% —— 升到 100 档，再发一条
	advance(t, svc, c.ID, 200)
	if got := notifier.bands(); len(got) != 2 || got[1] != 100 {
		t.Fatalf("105%% 应发 100 档告警，实际 %v", got)
	}

	// 回款到 50% —— 降档静默
	repay(t, svc, c.ID, 550)
	if got := notifier.bands(); len(got) != 2 {
		t.Fatalf("降档不应告警，实际 %v", got)
	}
	got, err := svc.GetCustomer(context.Background(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AlertLevel != 0 {
		t.Fatalf("降档后闩锁应归零（否则再次冲高不会告警），实际 %d", got.AlertLevel)
	}

	// 再冲到 81% —— 闩锁已归零，必须重新告警
	advance(t, svc, c.ID, 310)
	if bands := notifier.bands(); len(bands) != 3 || bands[2] != 80 {
		t.Fatalf("降档后重新冲高应再次告警，实际 %v", bands)
	}
}

// TestNoAlertWithoutCreditLimit 未授信客户任何敞口都不告警。
func TestNoAlertWithoutCreditLimit(t *testing.T) {
	svc, notifier := newTestCreditService(t)
	c := newCustomer(t, svc, "uid-nolimit", 0)

	advance(t, svc, c.ID, 99999)
	if len(notifier.events) != 0 {
		t.Fatalf("未授信客户不应告警，实际 %v", notifier.bands())
	}
}

// TestChangingCreditLimitReevaluatesAlert 改额度是第五个写入点：分母变了要重新评估。
//
// 场景：敞口 500 在额度 1000 下是 50%（不告警），把额度调到 600 后变成 83%，必须告警。
func TestChangingCreditLimitReevaluatesAlert(t *testing.T) {
	svc, notifier := newTestCreditService(t)
	ctx := context.Background()
	c := newCustomer(t, svc, "uid-limit", 1000)

	advance(t, svc, c.ID, 500)
	if len(notifier.events) != 0 {
		t.Fatalf("50%% 不应告警，实际 %v", notifier.bands())
	}

	if _, err := svc.UpdateCustomer(ctx, c.ID, repository.CustomerParams{
		Sub2apiUserID: "uid-limit",
		DisplayName:   "uid-limit",
		CreditLimit:   600, // 500/600 ≈ 83%
	}); err != nil {
		t.Fatalf("改额度失败: %v", err)
	}
	if bands := notifier.bands(); len(bands) != 1 || bands[0] != 80 {
		t.Fatalf("调低额度导致越档，应告警，实际 %v", bands)
	}
}

// TestRecalcDoesNotDuplicateAlert 重算是幂等的，不应因重复评估而重复告警。
func TestRecalcDoesNotDuplicateAlert(t *testing.T) {
	svc, notifier := newTestCreditService(t)
	ctx := context.Background()
	c := newCustomer(t, svc, "uid-recalc", 100)

	advance(t, svc, c.ID, 90)
	if len(notifier.events) != 1 {
		t.Fatalf("应有一条告警，实际 %v", notifier.bands())
	}
	if _, err := svc.RecalcCustomer(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := svc.RecalcAll(ctx); err != nil || n != 1 {
		t.Fatalf("全量重算应处理 1 个客户，实际 %d err=%v", n, err)
	}
	if len(notifier.events) != 1 {
		t.Fatalf("重算不应重复告警，实际 %v", notifier.bands())
	}
}

// TestAlertEventPayload 告警事件携带的字段正确（模板渲染依赖这些值）。
func TestAlertEventPayload(t *testing.T) {
	svc, notifier := newTestCreditService(t)
	c := newCustomer(t, svc, "uid-payload", 1000)
	advance(t, svc, c.ID, 850)

	if len(notifier.events) != 1 {
		t.Fatalf("应有一条告警，实际 %d 条", len(notifier.events))
	}
	ev := notifier.events[0]
	if ev.CustomerID != c.ID {
		t.Errorf("CustomerID 错误: %v", ev.CustomerID)
	}
	if ev.CustomerName != "uid-payload" {
		t.Errorf("CustomerName 错误: %q", ev.CustomerName)
	}
	if ev.CreditLimit != 1000 || ev.Outstanding != 850 || ev.Available != 150 {
		t.Errorf("金额字段错误: limit=%v outstanding=%v available=%v",
			ev.CreditLimit, ev.Outstanding, ev.Available)
	}
	if ev.Band != 80 {
		t.Errorf("Band 错误: %d", ev.Band)
	}
}

// TestAlertNameFallsBackToUserID display_name 为空时回落 sub2api_user_id（通知里不能是空白）。
func TestAlertNameFallsBackToUserID(t *testing.T) {
	svc, notifier := newTestCreditService(t)
	c, err := svc.CreateCustomer(context.Background(), repository.CustomerParams{
		Sub2apiUserID: "uid-anon",
		CreditLimit:   100,
	})
	if err != nil {
		t.Fatal(err)
	}
	advance(t, svc, c.ID, 90)
	if len(notifier.events) != 1 || notifier.events[0].CustomerName != "uid-anon" {
		t.Fatalf("无显示名时应回落 user_id，实际 %v", notifier.events)
	}
}

// TestNilNotifierIsSafe 未配置通知器时不 panic（装配层允许传 nil）。
func TestNilNotifierIsSafe(t *testing.T) {
	s := newTestStore(t)

	svc := NewCreditService(repository.NewCreditRepo(s, &secretbox.Box{}), nil)
	c := newCustomer(t, svc, "uid-nil", 100)
	got := advance(t, svc, c.ID, 200) // 越档，会走到告警分支
	if got.Outstanding != 200 {
		t.Fatalf("敞口应为 200，实际 %v", got.Outstanding)
	}
	// 闩锁仍应落库
	if got, err := svc.GetCustomer(context.Background(), c.ID); err != nil || got.AlertLevel != 100 {
		t.Fatalf("闩锁应为 100，实际 %v err=%v", got.AlertLevel, err)
	}
}

// TestReverseEntryRestoresOutstanding 冲正后敞口回到原值。
func TestReverseEntryRestoresOutstanding(t *testing.T) {
	svc, _ := newTestCreditService(t)
	ctx := context.Background()
	c := newCustomer(t, svc, "uid-reverse", 1000)

	advance(t, svc, c.ID, 300)
	after := advance(t, svc, c.ID, 500)
	if after.Outstanding != 800 {
		t.Fatalf("敞口应为 800，实际 %v", after.Outstanding)
	}

	items, _, err := svc.ListEntries(ctx, c.ID, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	// 找到金额 500 的那笔
	var target int64
	for _, e := range items {
		if e.Amount == 500 {
			target = e.ID
		}
	}
	if target == 0 {
		t.Fatal("未找到待冲正分录")
	}

	got, err := svc.ReverseEntry(ctx, target, "tester")
	if err != nil {
		t.Fatalf("冲正失败: %v", err)
	}
	if got.Outstanding != 300 {
		t.Fatalf("冲正后敞口应回到 300，实际 %v", got.Outstanding)
	}
	// 冲正是追加而非删除，审计轨迹须完整
	_, total, err := svc.ListEntries(ctx, c.ID, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Fatalf("冲正应追加分录（2 原始 + 1 冲正），实际 %d 条", total)
	}
}

// TestReverseEntryGuards 防重复冲正、防冲正冲正分录。
func TestReverseEntryGuards(t *testing.T) {
	svc, _ := newTestCreditService(t)
	ctx := context.Background()
	c := newCustomer(t, svc, "uid-guard", 1000)
	advance(t, svc, c.ID, 100)

	items, _, err := svc.ListEntries(ctx, c.ID, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	origID := items[0].ID

	if _, err := svc.ReverseEntry(ctx, origID, "tester"); err != nil {
		t.Fatalf("首次冲正应成功: %v", err)
	}
	if _, err := svc.ReverseEntry(ctx, origID, "tester"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("重复冲正应被拒绝，实际 %v", err)
	}

	// 冲正分录本身不能再被冲正
	items, _, err = svc.ListEntries(ctx, c.ID, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	var reversalID int64
	for _, e := range items {
		if e.ReversedOf != nil {
			reversalID = e.ID
		}
	}
	if reversalID == 0 {
		t.Fatal("未找到冲正分录")
	}
	if _, err := svc.ReverseEntry(ctx, reversalID, "tester"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("冲正分录不应可再冲正，实际 %v", err)
	}
}

// TestReverseEntryNotFound 冲正不存在的分录返回 ErrNotFound。
func TestReverseEntryNotFound(t *testing.T) {
	svc, _ := newTestCreditService(t)
	if _, err := svc.ReverseEntry(context.Background(), 9999, "tester"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("应返回 ErrNotFound，实际 %v", err)
	}
}

// TestAppendEntryValidation 金额与方向校验（唯一收口点）。
func TestAppendEntryValidation(t *testing.T) {
	svc, _ := newTestCreditService(t)
	ctx := context.Background()
	c := newCustomer(t, svc, "uid-valid", 1000)

	cases := []struct {
		name      string
		entryType string
		amount    float64
	}{
		{"未知方向", "withdraw", 100},
		{"空方向", "", 100},
		{"零金额", EntryTypeAdvance, 0},
		{"负金额", EntryTypeAdvance, -100},
		{"归一后为零", EntryTypeAdvance, 0.004}, // 四舍五入到分 = 0
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.AppendEntry(ctx, AppendEntryInput{
				CustomerID: c.ID, EntryType: tc.entryType, Amount: tc.amount,
			})
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("应返回 ErrInvalidInput，实际 %v", err)
			}
		})
	}
}

// TestAmountRoundedToCents 金额归一到分，消除前端浮点噪声。
func TestAmountRoundedToCents(t *testing.T) {
	svc, _ := newTestCreditService(t)
	c := newCustomer(t, svc, "uid-round", 1000)

	got := advance(t, svc, c.ID, 10.005)
	if got.Outstanding != 10.01 {
		t.Fatalf("金额应归一到 10.01，实际 %v", got.Outstanding)
	}
}

// TestCustomerValidation 客户参数校验。
func TestCustomerValidation(t *testing.T) {
	svc, _ := newTestCreditService(t)
	ctx := context.Background()

	if _, err := svc.CreateCustomer(ctx, repository.CustomerParams{}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("空 sub2api_user_id 应被拒绝，实际 %v", err)
	}
	if _, err := svc.CreateCustomer(ctx, repository.CustomerParams{
		Sub2apiUserID: "u", CreditLimit: -1,
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("负额度应被拒绝，实际 %v", err)
	}
	if _, err := svc.CreateCustomer(ctx, repository.CustomerParams{
		Sub2apiUserID: "u", Status: "deleted",
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("非法状态应被拒绝，实际 %v", err)
	}
	// 空状态回落 active
	c, err := svc.CreateCustomer(ctx, repository.CustomerParams{Sub2apiUserID: "u-ok"})
	if err != nil {
		t.Fatalf("合法参数应通过: %v", err)
	}
	if c.Status != CustomerStatusActive {
		t.Fatalf("状态应回落 active，实际 %q", c.Status)
	}
}

// TestEnsureCustomerIsIdempotent 嵌入页惰性建客户：已存在时复用，不建重复记录。
func TestEnsureCustomerIsIdempotent(t *testing.T) {
	svc, _ := newTestCreditService(t)
	ctx := context.Background()

	first, err := svc.EnsureCustomer(ctx, "uid-embed", "a@example.com")
	if err != nil {
		t.Fatalf("首次惰性建客户失败: %v", err)
	}
	second, err := svc.EnsureCustomer(ctx, "uid-embed", "changed@example.com")
	if err != nil {
		t.Fatalf("二次调用失败: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("应复用同一客户，实际 %d vs %d", first.ID, second.ID)
	}
	// 空 userID 必须拒绝（防会话缺失时建出空身份记录）
	if _, err := svc.EnsureCustomer(ctx, "", "x@example.com"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("空 userID 应被拒绝，实际 %v", err)
	}
}

// TestAppendEntryUsesProvidedOccurredAt 补录历史时用传入的业务时间，不用当前时间。
func TestAppendEntryUsesProvidedOccurredAt(t *testing.T) {
	svc, _ := newTestCreditService(t)
	ctx := context.Background()
	c := newCustomer(t, svc, "uid-backfill", 1000)

	past := time.Date(2025, 12, 1, 10, 0, 0, 0, time.UTC)
	if _, err := svc.AppendEntry(ctx, AppendEntryInput{
		CustomerID: c.ID, EntryType: EntryTypeAdvance, Amount: 100, OccurredAt: &past,
	}); err != nil {
		t.Fatal(err)
	}
	items, _, err := svc.ListEntries(ctx, c.ID, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || !items[0].OccurredAt.Equal(past) {
		t.Fatalf("应保留补录的业务时间，实际 %v", items)
	}
}

// TestRenderCreditTemplate 模板渲染：变量替换与空模板回落。
func TestRenderCreditTemplate(t *testing.T) {
	got := renderCreditTemplate("{customerName}/{band}/{outstanding}/{limit}/{available}",
		"张三", 80, 850, 1000, 150)
	const want = "张三/80/850.00/1000.00/150.00"
	if got != want {
		t.Fatalf("渲染结果错误:\n实际 %q\n期望 %q", got, want)
	}

	// 空模板回落默认，且不残留未替换的占位符
	fallback := renderCreditTemplate("   ", "李四", 100, 1200, 1000, -200)
	if fallback == "" {
		t.Fatal("空模板应回落默认文案")
	}
	for _, ph := range []string{"{customerName}", "{band}", "{outstanding}", "{limit}", "{available}"} {
		if strings.Contains(fallback, ph) {
			t.Fatalf("默认模板渲染后残留占位符 %s: %q", ph, fallback)
		}
	}
	if !strings.Contains(fallback, "李四") {
		t.Fatalf("默认模板未替换客户名: %q", fallback)
	}
}
