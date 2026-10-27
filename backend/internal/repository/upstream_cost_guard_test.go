package repository

import (
	"context"
	"testing"
)

// 上游随时可能清日志：清掉后今日实扣会变小、回补已清日期会得到 0。
// 成本列必须只增不减，否则已经采到的真实成本会被冲掉，利润虚高。
func TestUpsertCostsNeverDecreases(t *testing.T) {
	costRepo, _, providerRepo := newCostTestDB(t)
	ctx := context.Background()
	site := mustSite(t, providerRepo, "清日志站", false)
	acct := int64(7)

	write := func(actual, official float64, requests int64) {
		t.Helper()
		if err := costRepo.UpsertCosts(ctx, []UpstreamKeyCost{{
			ProviderID: site.ID, UpstreamKeyID: 1, AccountID: &acct, UsageDate: "2026-10-01",
			ActualCost: actual, OfficialCost: official, Requests: requests,
		}}); err != nil {
			t.Fatalf("UpsertCosts: %v", err)
		}
	}
	read := func() float64 {
		t.Helper()
		costs, err := costRepo.CostByAccount(ctx, "2026-10-01", "2026-10-01")
		if err != nil {
			t.Fatalf("CostByAccount: %v", err)
		}
		return costs[acct].ActualCost
	}

	write(10, 12, 100)
	write(3, 4, 30) // 上游当天清了日志
	if got := read(); got != 10 {
		t.Fatalf("清日志后实扣 = %v, want 保持 10", got)
	}
	write(0, 0, 0) // 回补一个已清掉的日期
	if got := read(); got != 10 {
		t.Fatalf("回补 0 后实扣 = %v, want 保持 10", got)
	}
	write(12, 15, 120) // 正常增长照常覆盖
	if got := read(); got != 12 {
		t.Fatalf("增长后实扣 = %v, want 12", got)
	}
}

// finalized_through 只进不退：一次失败/旧值写入不能把终结进度拉回去。
func TestSyncStateFinalizedThroughOnlyAdvances(t *testing.T) {
	costRepo, _, providerRepo := newCostTestDB(t)
	ctx := context.Background()
	site := mustSite(t, providerRepo, "终结站", false)
	str := func(s string) *string { return &s }

	for _, v := range []*string{str("2026-10-05"), str("2026-10-03"), nil} {
		if err := costRepo.SaveSyncState(ctx, CostSyncState{ProviderID: site.ID, FinalizedThrough: v}); err != nil {
			t.Fatalf("SaveSyncState: %v", err)
		}
	}
	states, err := costRepo.SyncStates(ctx)
	if err != nil {
		t.Fatalf("SyncStates: %v", err)
	}
	got := states[site.ID].FinalizedThrough
	if got == nil || *got != "2026-10-05" {
		t.Fatalf("finalized_through = %v, want 2026-10-05", got)
	}
}
