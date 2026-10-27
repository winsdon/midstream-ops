package repository

import (
	"context"
	"reflect"
	"sort"
	"testing"
)

func newUsageDailyTestRepo(t *testing.T) *UsageDailyRepo {
	t.Helper()
	s, cleanup, err := NewTestStore()
	if err == ErrNoTestDSN {
		t.Skip("未设置 MONITOR_TEST_PG_DSN，跳过需要 PG 的测试")
	}
	if err != nil {
		t.Fatalf("初始化测试 Store 失败: %v", err)
	}
	t.Cleanup(cleanup)
	return NewUsageDailyRepo(s)
}

// sub2api 清日志后重跑某天，请求数变少：整行保留旧值；增长照常覆盖。
func TestUsageDailyUpsertNeverShrinks(t *testing.T) {
	repo := newUsageDailyTestRepo(t)
	ctx := context.Background()
	row := func(req int64, rev float64, name string) UsageDailyRow {
		return UsageDailyRow{UsageDate: "2026-10-01", UserID: 1, UserName: name, GroupID: 2, GroupName: "g",
			GroupRate: 1, AccountID: 3, AccountName: "a", Requests: req, Revenue: rev, CostWeight: rev, OfficialCost: rev}
	}
	read := func() AccountUsageRow {
		t.Helper()
		rows, err := repo.ByAccount(ctx, "2026-10-01", "2026-10-01")
		if err != nil || len(rows) != 1 {
			t.Fatalf("ByAccount rows=%v err=%v", rows, err)
		}
		return rows[0]
	}

	if err := repo.Upsert(ctx, []UsageDailyRow{row(10, 5, "u")}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := repo.Upsert(ctx, []UsageDailyRow{row(4, 2, "u")}); err != nil {
		t.Fatalf("Upsert shrink: %v", err)
	}
	if got := read(); got.Requests != 10 || got.Revenue != 5 {
		t.Fatalf("清日志后 = %+v, want 保持 10/5", got)
	}
	if err := repo.Upsert(ctx, []UsageDailyRow{row(12, 6, "u")}); err != nil {
		t.Fatalf("Upsert grow: %v", err)
	}
	if got := read(); got.Requests != 12 || got.Revenue != 6 {
		t.Fatalf("增长后 = %+v, want 12/6", got)
	}
}

func TestUsageDailyRollups(t *testing.T) {
	repo := newUsageDailyTestRepo(t)
	ctx := context.Background()
	if err := repo.Upsert(ctx, []UsageDailyRow{
		{UsageDate: "2026-10-01", UserID: 1, UserName: "alice", GroupID: 1, GroupName: "旧组名", GroupRate: 1,
			AccountID: 9, AccountName: "acc", Requests: 2, Revenue: 2, CostWeight: 1, OfficialCost: 3},
		{UsageDate: "2026-10-02", UserID: 1, UserName: "alice", GroupID: 1, GroupName: "新组名", GroupRate: 2,
			AccountID: 9, AccountName: "acc", Requests: 3, Revenue: 4, CostWeight: 2, OfficialCost: 5},
		{UsageDate: "2026-10-02", UserID: 2, UserName: "bob", GroupID: 0, GroupName: "(无分组)", GroupRate: 1,
			AccountID: 9, AccountName: "acc", Requests: 1, Revenue: 1, CostWeight: 1, OfficialCost: 1},
		{UsageDate: "2026-10-02", UserID: 0, UserName: "(无用户)", GroupID: 0, GroupName: "(无分组)", GroupRate: 1,
			AccountID: 8, AccountName: "other", Requests: 1, Revenue: 0.5, CostWeight: 0.5, OfficialCost: 0.5},
		{UsageDate: "2026-10-03", UserID: 3, UserName: "carol", GroupID: 1, GroupName: "新组名", GroupRate: 2,
			AccountID: 9, AccountName: "acc", Requests: 7, Revenue: 7, CostWeight: 7, OfficialCost: 7},
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	from, to := "2026-10-01", "2026-10-02" // 10-03 在区间外

	t.Run("ByAccount", func(t *testing.T) {
		rows, err := repo.ByAccount(ctx, from, to)
		if err != nil {
			t.Fatal(err)
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].AccountID < rows[j].AccountID })
		want := []AccountUsageRow{
			{AccountID: 8, AccountName: "other", Requests: 1, Revenue: 0.5},
			{AccountID: 9, AccountName: "acc", Requests: 6, Revenue: 7},
		}
		if !reflect.DeepEqual(rows, want) {
			t.Fatalf("got %+v\nwant %+v", rows, want)
		}
	})
	t.Run("ByGroupAccount 取最新组名与倍率", func(t *testing.T) {
		rows, err := repo.ByGroupAccount(ctx, from, to)
		if err != nil {
			t.Fatal(err)
		}
		var g1 *GroupAccountUsageRow
		for i := range rows {
			if rows[i].GroupID == 1 {
				g1 = &rows[i]
			}
		}
		if len(rows) != 3 || g1 == nil || g1.GroupName != "新组名" || g1.RateMultiplier != 2 ||
			g1.Requests != 5 || g1.Revenue != 6 || g1.CostWeight != 3 {
			t.Fatalf("rows=%+v", rows)
		}
	})
	t.Run("ByUserGroupAccount", func(t *testing.T) {
		rows, err := repo.ByUserGroupAccount(ctx, from, to)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 3 {
			t.Fatalf("rows=%+v", rows)
		}
	})
	t.Run("Daily", func(t *testing.T) {
		rows, err := repo.Daily(ctx, from, to)
		if err != nil {
			t.Fatal(err)
		}
		want := []DailyTrendRow{
			{Day: "2026-10-01", Requests: 2, Revenue: 2, OfficialCost: 3},
			{Day: "2026-10-02", Requests: 5, Revenue: 5.5, OfficialCost: 6.5},
		}
		if !reflect.DeepEqual(rows, want) {
			t.Fatalf("got %+v\nwant %+v", rows, want)
		}
	})
	t.Run("ActiveUserIDs 不含无用户", func(t *testing.T) {
		ids, err := repo.ActiveUserIDs(ctx, from, to)
		if err != nil {
			t.Fatal(err)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		if !reflect.DeepEqual(ids, []int64{1, 2}) {
			t.Fatalf("ids=%v", ids)
		}
	})
}

func TestUsageRollupStateOnlyAdvances(t *testing.T) {
	repo := newUsageDailyTestRepo(t)
	ctx := context.Background()
	if got, err := repo.SettledThrough(ctx); err != nil || got != "" {
		t.Fatalf("初始进度 = %q, %v; want 空", got, err)
	}
	for _, d := range []string{"2026-10-05", "2026-10-03"} {
		if err := repo.SetSettledThrough(ctx, d); err != nil {
			t.Fatalf("SetSettledThrough(%s): %v", d, err)
		}
	}
	if got, err := repo.SettledThrough(ctx); err != nil || got != "2026-10-05" {
		t.Fatalf("进度 = %q, %v; want 2026-10-05", got, err)
	}
}
