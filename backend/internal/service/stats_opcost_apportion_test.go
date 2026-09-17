package service

import (
	"math"
	"testing"

	"sub2api-account-monitor/internal/repository"
)

func TestSplitOperatingCostByAccountByWeight(t *testing.T) {
	accts := []opAccountShare{
		{AccountID: 1, ProviderID: 10, Weight: 80, Requests: 8},
		{AccountID: 2, ProviderID: 10, Weight: 20, Requests: 2},
		{AccountID: 3, ProviderID: 20, Weight: 50, Requests: 5},
	}
	op := map[int64]float64{10: 100, 20: 40}

	byAcct, leftover := splitOperatingCostByAccount(accts, op)
	if leftover != 0 {
		t.Errorf("leftover = %v, want 0", leftover)
	}
	if math.Abs(byAcct[1]-80) > 1e-9 {
		t.Errorf("account 1 = %v, want 80", byAcct[1])
	}
	if math.Abs(byAcct[2]-20) > 1e-9 {
		t.Errorf("account 2 = %v, want 20", byAcct[2])
	}
	if math.Abs(byAcct[3]-40) > 1e-9 {
		t.Errorf("account 3 = %v, want 40", byAcct[3])
	}
}

func TestSplitOperatingCostByAccountLeftoverWhenNoUsage(t *testing.T) {
	accts := []opAccountShare{
		{AccountID: 1, ProviderID: 10, Weight: 10, Requests: 1},
	}
	op := map[int64]float64{10: 50, 99: 30}

	byAcct, leftover := splitOperatingCostByAccount(accts, op)
	if math.Abs(byAcct[1]-50) > 1e-9 {
		t.Errorf("account 1 = %v, want 50", byAcct[1])
	}
	if math.Abs(leftover-30) > 1e-9 {
		t.Errorf("leftover = %v, want 30（站点 99 当期无流量）", leftover)
	}
}

func TestSplitOperatingCostByAccountIgnoresUnassigned(t *testing.T) {
	accts := []opAccountShare{
		{AccountID: 1, ProviderID: 0, Weight: 100, Requests: 10},
	}
	op := map[int64]float64{7: 12}

	_, leftover := splitOperatingCostByAccount(accts, op)
	if leftover != 12 {
		t.Errorf("未归属账号不能吃掉运营成本, leftover = %v, want 12", leftover)
	}
}

func TestAssembleGroupStatsDeductsOperatingCost(t *testing.T) {
	// 账号 1 实扣 50、运营 40；两组用量 3:1 → 成本 37.5/12.5，运营 30/10
	rows := []repository.GroupAccountUsageRow{
		{GroupID: 1, GroupName: "fast", RateMultiplier: 1, AccountID: 1, AccountName: "a", Requests: 30, Revenue: 90, CostWeight: 30},
		{GroupID: 2, GroupName: "slow", RateMultiplier: 1, AccountID: 1, AccountName: "a", Requests: 10, Revenue: 30, CostWeight: 10},
	}
	costs := map[int64]repository.AccountCost{1: {AccountID: 1, ActualCost: 50}}
	opByAccount := map[int64]float64{1: 40}

	got := assembleGroupStats(rows, costs, nil, opByAccount, 0)
	if len(got) != 2 {
		t.Fatalf("分组数 = %d, want 2", len(got))
	}
	byName := map[string]GroupStat{}
	for _, g := range got {
		byName[g.GroupName] = g
	}
	fast, slow := byName["fast"], byName["slow"]
	if math.Abs(fast.OperatingCost-30) > 1e-9 {
		t.Errorf("fast 运营成本 = %v, want 30", fast.OperatingCost)
	}
	if math.Abs(slow.OperatingCost-10) > 1e-9 {
		t.Errorf("slow 运营成本 = %v, want 10", slow.OperatingCost)
	}
	if math.Abs(fast.Profit-(90-37.5-30)) > 1e-9 {
		t.Errorf("fast 利润 = %v, want 22.5", fast.Profit)
	}
	if math.Abs(slow.Profit-(30-12.5-10)) > 1e-9 {
		t.Errorf("slow 利润 = %v, want 7.5", slow.Profit)
	}
	if math.Abs(fast.OperatingCost+slow.OperatingCost-40) > 1e-9 {
		t.Errorf("分组运营成本合计 = %v, want 40", fast.OperatingCost+slow.OperatingCost)
	}
}

func TestAssembleGroupStatsIdleBucketHoldsLeftover(t *testing.T) {
	rows := []repository.GroupAccountUsageRow{
		{GroupID: 1, GroupName: "g", AccountID: 1, Requests: 1, Revenue: 10, CostWeight: 1},
	}
	got := assembleGroupStats(rows, nil, nil, nil, 25)
	if len(got) != 2 {
		t.Fatalf("应含流量分组 + 无流量桶, got %d", len(got))
	}
	idle := got[len(got)-1]
	if idle.GroupName != idleOpCostBucket || idle.GroupID != idleOpCostID {
		t.Errorf("末位应为无流量桶, got id=%d name=%q", idle.GroupID, idle.GroupName)
	}
	if idle.OperatingCost != 25 || idle.Profit != -25 {
		t.Errorf("无流量桶 operating=%v profit=%v, want 25 / -25", idle.OperatingCost, idle.Profit)
	}
}

func TestAssembleUserStatsDeductsOperatingCost(t *testing.T) {
	rows := []repository.UserGroupAccountUsageRow{
		ugRow(1, 10, 99, 30, 80, 30, "alice", "A"),
		ugRow(2, 10, 99, 10, 20, 10, "bob", "A"),
	}
	costs := map[int64]repository.AccountCost{99: {AccountID: 99, ActualCost: 100}}
	opByAccount := map[int64]float64{99: 40}

	got := assembleUserStats(rows, costs, nil, opByAccount, 0)
	if len(got) != 2 {
		t.Fatalf("用户数 = %d, want 2", len(got))
	}
	if math.Abs(got[0].OperatingCost-30) > 1e-9 {
		t.Errorf("alice 运营成本 = %v, want 30", got[0].OperatingCost)
	}
	if math.Abs(got[1].OperatingCost-10) > 1e-9 {
		t.Errorf("bob 运营成本 = %v, want 10", got[1].OperatingCost)
	}
	if math.Abs(got[0].Profit-(80-75-30)) > 1e-9 {
		t.Errorf("alice 利润 = %v, want -25", got[0].Profit)
	}
	if math.Abs(got[1].Profit-(20-25-10)) > 1e-9 {
		t.Errorf("bob 利润 = %v, want -15", got[1].Profit)
	}
	if math.Abs(got[0].OperatingCost+got[1].OperatingCost-40) > 1e-9 {
		t.Errorf("用户运营成本合计 = %v, want 40", got[0].OperatingCost+got[1].OperatingCost)
	}
}

func TestAssembleUserStatsIdleBucketLast(t *testing.T) {
	rows := []repository.UserGroupAccountUsageRow{
		ugRow(0, 1, 1, 1, 999, 1, unassignedUserBucket, "g"),
		ugRow(8, 1, 1, 1, 1, 1, "small", "g"),
	}
	got := assembleUserStats(rows, nil, nil, nil, 9)
	if got[len(got)-1].UserID != idleOpCostID {
		t.Errorf("无流量桶应排最后, 末位 user_id = %d", got[len(got)-1].UserID)
	}
	if got[len(got)-1].OperatingCost != 9 {
		t.Errorf("无流量桶运营成本 = %v, want 9", got[len(got)-1].OperatingCost)
	}
}
