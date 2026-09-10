package service

import (
	"math"
	"testing"

	"sub2api-account-monitor/internal/repository"
)

func ugRow(userID, groupID, accountID, reqs int64, revenue, weight float64, user, group string) repository.UserGroupAccountUsageRow {
	return repository.UserGroupAccountUsageRow{
		UserID: userID, UserName: user,
		GroupID: groupID, GroupName: group, RateMultiplier: 1,
		AccountID: accountID, Requests: reqs, Revenue: revenue, CostWeight: weight,
	}
}

func TestAssembleUserStatsSplitsAccountCostAcrossUsers(t *testing.T) {
	// 同一账号被两个用户用：用量 3:1，实扣 100 → 75 / 25
	rows := []repository.UserGroupAccountUsageRow{
		ugRow(1, 10, 99, 30, 80, 30, "alice", "A"),
		ugRow(2, 10, 99, 10, 20, 10, "bob", "A"),
	}
	costs := map[int64]repository.AccountCost{99: {AccountID: 99, ActualCost: 100}}

	got := assembleUserStats(rows, costs, nil)
	if len(got) != 2 {
		t.Fatalf("用户数 = %d, 期望 2", len(got))
	}
	if got[0].UserName != "alice" || got[1].UserName != "bob" {
		t.Fatalf("顺序应为收益降序 alice, bob，实际 %q, %q", got[0].UserName, got[1].UserName)
	}
	if math.Abs(got[0].Cost-75) > 1e-9 {
		t.Errorf("alice 成本 = %v, 期望 75", got[0].Cost)
	}
	if math.Abs(got[1].Cost-25) > 1e-9 {
		t.Errorf("bob 成本 = %v, 期望 25", got[1].Cost)
	}
	if math.Abs(got[0].Cost+got[1].Cost-100) > 1e-9 {
		t.Errorf("用户成本合计 = %v, 期望 100（不吞不造）", got[0].Cost+got[1].Cost)
	}
}

func TestAssembleUserStatsExpandsByGroup(t *testing.T) {
	// 同一用户两个分组；同一分组两笔账号用量应合成一行
	rows := []repository.UserGroupAccountUsageRow{
		ugRow(1, 10, 1, 5, 40, 4, "alice", "default"),
		ugRow(1, 10, 2, 5, 10, 1, "alice", "default"),
		ugRow(1, 20, 1, 5, 50, 5, "alice", "vip"),
	}
	costs := map[int64]repository.AccountCost{
		1: {AccountID: 1, ActualCost: 90}, // default 4/9 + vip 5/9
		2: {AccountID: 2, ActualCost: 10},
	}

	got := assembleUserStats(rows, costs, nil)
	if len(got) != 1 {
		t.Fatalf("用户数 = %d, 期望 1", len(got))
	}
	u := got[0]
	if len(u.Groups) != 2 {
		t.Fatalf("分组明细数 = %d, 期望 2", len(u.Groups))
	}
	var defaultRev, vipRev float64
	for _, g := range u.Groups {
		switch g.GroupName {
		case "default":
			defaultRev = g.Revenue
		case "vip":
			vipRev = g.Revenue
		}
	}
	if defaultRev != 50 || vipRev != 50 {
		t.Errorf("default 收益 = %v vip 收益 = %v, 期望各 50（同一分组两笔账号应合成一行）", defaultRev, vipRev)
	}
	if math.Abs(u.Revenue-100) > 1e-9 {
		t.Errorf("用户收益 = %v, 期望 100", u.Revenue)
	}
}

func TestAssembleUserStatsGroupsSortedByRevenue(t *testing.T) {
	rows := []repository.UserGroupAccountUsageRow{
		ugRow(1, 10, 1, 1, 10, 1, "alice", "small"),
		ugRow(1, 20, 1, 1, 90, 1, "alice", "big"),
	}
	got := assembleUserStats(rows, nil, nil)
	if got[0].Groups[0].GroupName != "big" {
		t.Errorf("分组首位 = %q, 期望 big", got[0].Groups[0].GroupName)
	}
}

func TestAssembleUserStatsUnassignedLast(t *testing.T) {
	rows := []repository.UserGroupAccountUsageRow{
		ugRow(0, 1, 1, 1, 999, 1, unassignedUserBucket, "g"),
		ugRow(8, 1, 1, 1, 1, 1, "small", "g"),
	}
	got := assembleUserStats(rows, nil, nil)
	if len(got) != 2 {
		t.Fatalf("用户数 = %d, 期望 2", len(got))
	}
	if got[len(got)-1].UserID != 0 {
		t.Errorf("无用户桶应排最后，末位 user_id = %d", got[len(got)-1].UserID)
	}
}

func TestAssembleUserStatsMissingAccountCountedOnce(t *testing.T) {
	// 同一未匹配账号贡献两个分组，缺失账号只计 1
	rows := []repository.UserGroupAccountUsageRow{
		ugRow(1, 10, 7, 10, 10, 1, "alice", "A"),
		ugRow(1, 20, 7, 10, 10, 1, "alice", "B"),
	}
	got := assembleUserStats(rows, nil, nil)
	if len(got) != 1 {
		t.Fatalf("用户数 = %d, 期望 1", len(got))
	}
	if got[0].AccountsMissing != 1 {
		t.Errorf("AccountsMissing = %d, 期望 1", got[0].AccountsMissing)
	}
	if got[0].CostComplete {
		t.Error("CostComplete 应为 false")
	}
	for _, g := range got[0].Groups {
		if g.CostMatched {
			t.Errorf("分组 %s CostMatched 应为 false", g.GroupName)
		}
	}
}

func TestAssembleUserStatsExemptAccountNotMissing(t *testing.T) {
	rows := []repository.UserGroupAccountUsageRow{
		ugRow(1, 10, 7, 10, 10, 1, "alice", "A"),
	}
	got := assembleUserStats(rows, nil, map[int64]bool{7: true})
	if !got[0].CostComplete || got[0].AccountsMissing != 0 {
		t.Errorf("自营豁免后应完整，complete=%v missing=%d", got[0].CostComplete, got[0].AccountsMissing)
	}
	if !got[0].Groups[0].CostMatched {
		t.Error("豁免账号的分组应视为已匹配")
	}
}

func TestAssembleUserStatsEmpty(t *testing.T) {
	if got := assembleUserStats(nil, nil, nil); len(got) != 0 {
		t.Errorf("空输入应返回空切片，实际 %#v", got)
	}
}
