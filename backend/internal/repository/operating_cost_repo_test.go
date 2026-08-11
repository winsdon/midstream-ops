package repository

import (
	"context"
	"testing"

	"sub2api-account-monitor/internal/pkg/secretbox"
)

// newCostTestDB 建临时 monitor 测试库并返回三个仓储。
func newCostTestDB(t *testing.T) (*UpstreamCostRepo, *OperatingCostRepo, *ProviderRepo) {
	t.Helper()
	s := newTestStore(t)
	return NewUpstreamCostRepo(s), NewOperatingCostRepo(s), NewProviderRepo(s, &secretbox.Box{})
}

// mustSite 建站点。
func mustSite(t *testing.T, r *ProviderRepo, name string, selfOperated bool) *Provider {
	t.Helper()
	p, err := r.Create(context.Background(), CreateParams{
		Name: name, BalanceType: "none", SelfOperated: selfOperated,
	})
	if err != nil {
		t.Fatalf("建站点 %s 失败: %v", name, err)
	}
	return p
}

// 自营站的上游实扣必须计 0，但账号仍要留在结果 map 里 ——
// 若被 WHERE 过滤掉，StatsService 会判成 CostMatched=false，
// 误触发前端「成本不完整、利润被高估 ⚠」告警。
func TestCostByAccountZeroesSelfOperated(t *testing.T) {
	costRepo, _, providerRepo := newCostTestDB(t)
	ctx := context.Background()

	selfRun := mustSite(t, providerRepo, "自营站", true)
	normal := mustSite(t, providerRepo, "普通站", false)

	selfAcct, normalAcct := int64(101), int64(202)
	if err := costRepo.UpsertCosts(ctx, []UpstreamKeyCost{
		{ProviderID: selfRun.ID, UpstreamKeyID: 1, AccountID: &selfAcct,
			UsageDate: "2026-07-15", ActualCost: 100, OfficialCost: 150, Requests: 10},
		{ProviderID: normal.ID, UpstreamKeyID: 2, AccountID: &normalAcct,
			UsageDate: "2026-07-15", ActualCost: 80, OfficialCost: 120, Requests: 20},
	}); err != nil {
		t.Fatalf("写入成本失败: %v", err)
	}

	costs, err := costRepo.CostByAccount(ctx, "2026-07-01", "2026-07-31")
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}

	// 自营站账号必须存在（matched）且实扣为 0
	got, ok := costs[selfAcct]
	if !ok {
		t.Fatal("自营站账号应留在结果里（置 0 而非过滤行），否则会误报成本不完整")
	}
	if got.ActualCost != 0 {
		t.Errorf("自营站 actual_cost = %v, want 0", got.ActualCost)
	}
	// 官价是对照口径，与「钱付给了谁」无关，不做剔除
	if got.OfficialCost != 150 {
		t.Errorf("自营站 official_cost = %v, want 150（官价不剔除）", got.OfficialCost)
	}

	// 普通站不受影响
	if costs[normalAcct].ActualCost != 80 {
		t.Errorf("普通站 actual_cost = %v, want 80", costs[normalAcct].ActualCost)
	}
}

func TestCostByDayZeroesSelfOperated(t *testing.T) {
	costRepo, _, providerRepo := newCostTestDB(t)
	ctx := context.Background()

	selfRun := mustSite(t, providerRepo, "自营站", true)
	normal := mustSite(t, providerRepo, "普通站", false)

	if err := costRepo.UpsertCosts(ctx, []UpstreamKeyCost{
		{ProviderID: selfRun.ID, UpstreamKeyID: 1, UsageDate: "2026-07-15", ActualCost: 100, OfficialCost: 150},
		{ProviderID: normal.ID, UpstreamKeyID: 2, UsageDate: "2026-07-15", ActualCost: 80, OfficialCost: 120},
		// 只有自营站有消耗的一天：实扣应整天归零，但该天仍要出现在结果里
		{ProviderID: selfRun.ID, UpstreamKeyID: 1, UsageDate: "2026-07-16", ActualCost: 60, OfficialCost: 90},
	}); err != nil {
		t.Fatalf("写入成本失败: %v", err)
	}

	byDay, err := costRepo.CostByDay(ctx, "2026-07-01", "2026-07-31")
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}

	// 混合日：只剩普通站的 80
	if got := byDay["2026-07-15"].ActualCost; got != 80 {
		t.Errorf("2026-07-15 actual_cost = %v, want 80（自营站的 100 应剔除）", got)
	}
	// 纯自营日：实扣 0，但该天必须存在，否则 CostComplete 会被判成 false
	day16, ok := byDay["2026-07-16"]
	if !ok {
		t.Fatal("纯自营站消耗的日子应仍出现在结果里")
	}
	if day16.ActualCost != 0 {
		t.Errorf("2026-07-16 actual_cost = %v, want 0", day16.ActualCost)
	}
	if day16.OfficialCost != 90 {
		t.Errorf("2026-07-16 official_cost = %v, want 90（官价不剔除）", day16.OfficialCost)
	}
}

// 切换自营标记后，历史成本的口径应立即随之改变（查询时判定，不回刷数据）。
func TestSelfOperatedToggleAffectsHistoricalCosts(t *testing.T) {
	costRepo, _, providerRepo := newCostTestDB(t)
	ctx := context.Background()

	site := mustSite(t, providerRepo, "站点", false)
	acct := int64(101)
	if err := costRepo.UpsertCosts(ctx, []UpstreamKeyCost{
		{ProviderID: site.ID, UpstreamKeyID: 1, AccountID: &acct,
			UsageDate: "2026-07-15", ActualCost: 100},
	}); err != nil {
		t.Fatalf("写入成本失败: %v", err)
	}

	costs, err := costRepo.CostByAccount(ctx, "2026-07-01", "2026-07-31")
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if costs[acct].ActualCost != 100 {
		t.Fatalf("标记前 actual_cost = %v, want 100", costs[acct].ActualCost)
	}

	// 打开自营标记
	if _, err := providerRepo.Update(ctx, site.ID, UpdateParams{
		Name: "站点", BalanceType: "none", SelfOperated: true,
	}); err != nil {
		t.Fatalf("更新站点失败: %v", err)
	}

	costs, err = costRepo.CostByAccount(ctx, "2026-07-01", "2026-07-31")
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if costs[acct].ActualCost != 0 {
		t.Errorf("标记后 actual_cost = %v, want 0（历史数据未动，口径由查询决定）", costs[acct].ActualCost)
	}
}

// 删除站点应级联清掉其运营成本（PG 外键恒强制）。
func TestOperatingCostCascadeOnProviderDelete(t *testing.T) {
	_, opRepo, providerRepo := newCostTestDB(t)
	ctx := context.Background()

	site := mustSite(t, providerRepo, "自营站", true)
	if _, err := opRepo.Create(ctx, OperatingCostParams{
		ProviderID: site.ID, Category: "account", Amount: 200, OccurredOn: "2026-07-15",
	}); err != nil {
		t.Fatalf("写入运营成本失败: %v", err)
	}

	if err := providerRepo.Delete(ctx, site.ID); err != nil {
		t.Fatalf("删除站点失败: %v", err)
	}

	items, err := opRepo.ListByProvider(ctx, site.ID, "", "")
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("len(items) = %d, want 0（CASCADE 应已清理）", len(items))
	}
}

// SumByProvider / SumByDay 是统计页的聚合原料，须按闭区间正确分桶。
func TestOperatingCostSums(t *testing.T) {
	_, opRepo, providerRepo := newCostTestDB(t)
	ctx := context.Background()

	a := mustSite(t, providerRepo, "自营站A", true)
	b := mustSite(t, providerRepo, "自营站B", true)

	for _, it := range []OperatingCostParams{
		{ProviderID: a.ID, Category: "account", Amount: 200, OccurredOn: "2026-07-15"},
		{ProviderID: a.ID, Category: "server", Amount: 50, OccurredOn: "2026-07-15"},
		{ProviderID: a.ID, Category: "other", Amount: 30, OccurredOn: "2026-07-16"},
		{ProviderID: b.ID, Category: "subscription", Amount: 20, OccurredOn: "2026-07-16"},
		{ProviderID: a.ID, Category: "other", Amount: 999, OccurredOn: "2026-08-01"}, // 区间外
	} {
		if _, err := opRepo.Create(ctx, it); err != nil {
			t.Fatalf("写入失败: %v", err)
		}
	}

	byProvider, err := opRepo.SumByProvider(ctx, "2026-07-01", "2026-07-31")
	if err != nil {
		t.Fatalf("SumByProvider 失败: %v", err)
	}
	if got := byProvider[a.ID]; got != 280 {
		t.Errorf("站点A 合计 = %v, want 280（200+50+30，8月那笔在区间外）", got)
	}
	if got := byProvider[b.ID]; got != 20 {
		t.Errorf("站点B 合计 = %v, want 20", got)
	}

	byDay, err := opRepo.SumByDay(ctx, "2026-07-01", "2026-07-31")
	if err != nil {
		t.Fatalf("SumByDay 失败: %v", err)
	}
	// 按天聚合跨站点合并：趋势图是全局视角
	if got := byDay["2026-07-15"]; got != 250 {
		t.Errorf("07-15 合计 = %v, want 250", got)
	}
	if got := byDay["2026-07-16"]; got != 50 {
		t.Errorf("07-16 合计 = %v, want 50（跨站点合并）", got)
	}
	if _, ok := byDay["2026-08-01"]; ok {
		t.Error("区间外的日期不应出现")
	}
}
