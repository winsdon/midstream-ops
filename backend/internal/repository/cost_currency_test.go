package repository

import (
	"context"
	"testing"
)

// mustRatedSite 建带充值倍率的站点。
func mustRatedSite(t *testing.T, r *ProviderRepo, name string, rate float64, selfOperated bool) *Provider {
	t.Helper()
	p, err := r.Create(context.Background(), CreateParams{
		Name: name, BalanceType: "none", RechargeRate: rate, SelfOperated: selfOperated,
	})
	if err != nil {
		t.Fatalf("建站点 %s 失败: %v", name, err)
	}
	if p.RechargeRate != rate {
		t.Fatalf("站点 %s recharge_rate = %v, want %v", name, p.RechargeRate, rate)
	}
	return p
}

// 上游实扣以各站自己的计价单位入库，收益（usage_logs）恒为 CNY。
// 三个聚合必须按 recharge_rate 折算成 CNY，否则 Profit = Revenue − Cost 在混算币种。
//
// 用 0.1 这个真实场景值（上游 1R:10U）：不折算会让成本虚高 10 倍，利润符号翻转。
func TestCostAggregatesConvertToCNY(t *testing.T) {
	costRepo, _, providerRepo := newCostTestDB(t)
	ctx := context.Background()

	uPriced := mustRatedSite(t, providerRepo, "U计价站", 0.1, false) // 1 U = 0.1 元
	cnyPriced := mustRatedSite(t, providerRepo, "CNY站", 1, false) // 本就是人民币
	selfRun := mustRatedSite(t, providerRepo, "自营站", 0.1, true)   // 实扣恒 0，倍率不该救活它

	uAcct, cnyAcct, selfAcct := int64(101), int64(202), int64(303)
	if err := costRepo.UpsertCosts(ctx, []UpstreamKeyCost{
		{ProviderID: uPriced.ID, UpstreamKeyID: 1, AccountID: &uAcct,
			UsageDate: "2026-08-21", ActualCost: 21226.35, OfficialCost: 30000, Requests: 10},
		{ProviderID: cnyPriced.ID, UpstreamKeyID: 2, AccountID: &cnyAcct,
			UsageDate: "2026-08-21", ActualCost: 3272.17, OfficialCost: 4000, Requests: 20},
		{ProviderID: selfRun.ID, UpstreamKeyID: 3, AccountID: &selfAcct,
			UsageDate: "2026-08-21", ActualCost: 500, OfficialCost: 700, Requests: 5},
	}); err != nil {
		t.Fatalf("写入成本失败: %v", err)
	}

	const eps = 0.005
	near := func(got, want float64) bool { return got-want < eps && want-got < eps }

	t.Run("CostByAccount", func(t *testing.T) {
		costs, err := costRepo.CostByAccount(ctx, "2026-08-01", "2026-08-31")
		if err != nil {
			t.Fatalf("查询失败: %v", err)
		}
		// 21226.35 U × 0.1 = 2122.635 元
		if got := costs[uAcct].ActualCost; !near(got, 2122.635) {
			t.Errorf("U计价站账号实扣 = %v, want 2122.635（21226.35 × 0.1）", got)
		}
		if got := costs[cnyAcct].ActualCost; !near(got, 3272.17) {
			t.Errorf("CNY站账号实扣 = %v, want 3272.17（rate=1 不变）", got)
		}
		if got := costs[selfAcct].ActualCost; got != 0 {
			t.Errorf("自营站账号实扣 = %v, want 0（先置零，倍率不参与）", got)
		}
		// 官价是对照口径，同样以上游单位入库，须一并折算才能与实扣同轴比较
		if got := costs[uAcct].OfficialCost; !near(got, 3000) {
			t.Errorf("U计价站官价 = %v, want 3000（30000 × 0.1）", got)
		}
	})

	t.Run("CostByProvider", func(t *testing.T) {
		byProv, err := costRepo.CostByProvider(ctx, "2026-08-01", "2026-08-31")
		if err != nil {
			t.Fatalf("查询失败: %v", err)
		}
		if got := byProv[uPriced.ID].ActualCost; !near(got, 2122.635) {
			t.Errorf("U计价站实扣 = %v, want 2122.635", got)
		}
		if got := byProv[selfRun.ID].ActualCost; got != 0 {
			t.Errorf("自营站实扣 = %v, want 0", got)
		}
	})

	t.Run("CostByDay", func(t *testing.T) {
		byDay, err := costRepo.CostByDay(ctx, "2026-08-01", "2026-08-31")
		if err != nil {
			t.Fatalf("查询失败: %v", err)
		}
		// 跨站点合并后仍须各按自己倍率折算：2122.635 + 3272.17 + 0（自营）
		if got := byDay["2026-08-21"].ActualCost; !near(got, 5394.805) {
			t.Errorf("当日实扣 = %v, want 5394.805（各站按自己倍率折算后相加）", got)
		}
	})
}

// 倍率为 0 / 负数时不能把成本抹成 0 —— 那会让利润凭空变好看。
// Create/Update 已把非正值兜底为 1，这里守住「即使库里被手工改坏也按 1 处理」。
func TestCostAggregatesGuardNonPositiveRate(t *testing.T) {
	costRepo, _, providerRepo := newCostTestDB(t)
	ctx := context.Background()

	site := mustRatedSite(t, providerRepo, "站点", 1, false)
	// 绕过 Create 的兜底，直接把倍率写坏
	if _, err := providerRepo.db.ExecContext(ctx,
		`UPDATE providers SET recharge_rate = 0 WHERE id = ?`, site.ID); err != nil {
		t.Fatalf("改坏倍率失败: %v", err)
	}

	acct := int64(101)
	if err := costRepo.UpsertCosts(ctx, []UpstreamKeyCost{
		{ProviderID: site.ID, UpstreamKeyID: 1, AccountID: &acct,
			UsageDate: "2026-08-21", ActualCost: 100, OfficialCost: 150},
	}); err != nil {
		t.Fatalf("写入成本失败: %v", err)
	}

	costs, err := costRepo.CostByAccount(ctx, "2026-08-01", "2026-08-31")
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if got := costs[acct].ActualCost; got != 100 {
		t.Errorf("倍率为 0 时实扣 = %v, want 100（按 1 兜底，不能抹成 0）", got)
	}
}
