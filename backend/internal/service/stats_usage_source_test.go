package service

import (
	"reflect"
	"testing"
	"time"

	"sub2api-account-monitor/internal/repository"
)

func TestSplitUsageRange(t *testing.T) {
	loc := time.FixedZone("CST", 8*60*60)
	day := func(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, loc) }
	cases := []struct {
		name       string
		start, end time.Time
		through    string
		want       usageSplit
	}{
		{
			name: "从未结算：整段实时", start: day(20), end: day(27), through: "",
			want: usageSplit{live: true, liveStart: day(20), liveEnd: day(27)},
		},
		{
			name: "分界在区间中：历史读汇总、今天实时", start: day(20), end: day(28), through: "2026-10-26",
			want: usageSplit{settled: true, settledFrom: "2026-10-20", settledTo: "2026-10-26",
				live: true, liveStart: day(27), liveEnd: day(28)},
		},
		{
			name: "结算没跑：昨天也实时", start: day(20), end: day(28), through: "2026-10-25",
			want: usageSplit{settled: true, settledFrom: "2026-10-20", settledTo: "2026-10-25",
				live: true, liveStart: day(26), liveEnd: day(28)},
		},
		{
			name: "整段已结算", start: day(1), end: day(8), through: "2026-10-26",
			want: usageSplit{settled: true, settledFrom: "2026-10-01", settledTo: "2026-10-07"},
		},
		{
			name: "整段在分界之后", start: day(27), end: day(28), through: "2026-10-26",
			want: usageSplit{live: true, liveStart: day(27), liveEnd: day(28)},
		},
		{
			name: "跨月分界", start: time.Date(2026, 9, 28, 0, 0, 0, 0, loc), end: day(3), through: "2026-09-30",
			want: usageSplit{settled: true, settledFrom: "2026-09-28", settledTo: "2026-09-30",
				live: true, liveStart: day(1), liveEnd: day(3)},
		},
		{name: "空区间", start: day(5), end: day(5), through: "2026-10-26", want: usageSplit{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// 入参按 handler 习惯传 UTC
			got := splitUsageRange(c.start.UTC(), c.end.UTC(), c.through, loc)
			if got.settled != c.want.settled || got.settledFrom != c.want.settledFrom || got.settledTo != c.want.settledTo ||
				got.live != c.want.live || !got.liveStart.Equal(c.want.liveStart) || !got.liveEnd.Equal(c.want.liveEnd) {
				t.Fatalf("split = %+v\nwant  %+v", got, c.want)
			}
		})
	}
}

func TestPendingDays(t *testing.T) {
	loc := time.FixedZone("CST", 8*60*60)
	from := time.Date(2026, 2, 27, 0, 0, 0, 0, loc)
	today := time.Date(2026, 3, 2, 0, 0, 0, 0, loc)
	var got []string
	for _, d := range pendingDays(from, today) {
		got = append(got, d.Format("2006-01-02"))
	}
	want := []string{"2026-02-27", "2026-02-28", "2026-03-01"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pendingDays = %v, want %v", got, want)
	}
	if n := len(pendingDays(today, today)); n != 0 {
		t.Fatalf("已结算到昨天时应无待结算日，got %d", n)
	}
}

// 合并后的结果必须与「整段实时查」逐字段相等：同 key 累加，名称以实时段为准。
func TestMergeGroupAccountRows(t *testing.T) {
	hist := []repository.GroupAccountUsageRow{
		{GroupID: 1, GroupName: "旧名", RateMultiplier: 1, AccountID: 10, AccountName: "a", Requests: 5, Revenue: 1.5, CostWeight: 2},
		{GroupID: 2, GroupName: "g2", RateMultiplier: 2, AccountID: 10, AccountName: "a", Requests: 1, Revenue: 1, CostWeight: 1},
	}
	live := []repository.GroupAccountUsageRow{
		{GroupID: 1, GroupName: "新名", RateMultiplier: 1.5, AccountID: 10, AccountName: "a", Requests: 3, Revenue: 0.5, CostWeight: 1},
		{GroupID: 3, GroupName: "g3", RateMultiplier: 1, AccountID: 11, AccountName: "b", Requests: 2, Revenue: 4, CostWeight: 4},
	}
	got := mergeGroupAccountRows(hist, live)
	want := []repository.GroupAccountUsageRow{
		{GroupID: 1, GroupName: "新名", RateMultiplier: 1.5, AccountID: 10, AccountName: "a", Requests: 8, Revenue: 2, CostWeight: 3},
		{GroupID: 2, GroupName: "g2", RateMultiplier: 2, AccountID: 10, AccountName: "a", Requests: 1, Revenue: 1, CostWeight: 1},
		{GroupID: 3, GroupName: "g3", RateMultiplier: 1, AccountID: 11, AccountName: "b", Requests: 2, Revenue: 4, CostWeight: 4},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("merge = %+v\nwant  %+v", got, want)
	}
}

func TestMergeAccountAndUserRows(t *testing.T) {
	acc := mergeAccountRows(
		[]repository.AccountUsageRow{{AccountID: 1, AccountName: "x", Requests: 2, Revenue: 3}},
		[]repository.AccountUsageRow{{AccountID: 1, AccountName: "y", Requests: 1, Revenue: 1}})
	if len(acc) != 1 || acc[0].Requests != 3 || acc[0].Revenue != 4 || acc[0].AccountName != "y" {
		t.Fatalf("mergeAccountRows = %+v", acc)
	}

	users := mergeUserGroupAccountRows(
		[]repository.UserGroupAccountUsageRow{{UserID: 1, GroupID: 1, AccountID: 1, Requests: 1, Revenue: 1, CostWeight: 1}},
		[]repository.UserGroupAccountUsageRow{
			{UserID: 1, GroupID: 1, AccountID: 1, Requests: 2, Revenue: 2, CostWeight: 2},
			{UserID: 2, GroupID: 1, AccountID: 1, Requests: 1, Revenue: 1, CostWeight: 1},
		})
	if len(users) != 2 || users[0].Requests != 3 || users[0].CostWeight != 3 || users[1].UserID != 2 {
		t.Fatalf("mergeUserGroupAccountRows = %+v", users)
	}
	if got := mergeAccountRows(nil, nil); len(got) != 0 {
		t.Fatalf("两段皆空应为空，got %+v", got)
	}
}

func TestMergeDailyRowsKeepsAscending(t *testing.T) {
	got := mergeDailyRows(
		[]repository.DailyTrendRow{{Day: "2026-10-25", Requests: 1}, {Day: "2026-10-26", Requests: 2}},
		[]repository.DailyTrendRow{{Day: "2026-10-27", Requests: 3}})
	var days []string
	for _, d := range got {
		days = append(days, d.Day)
	}
	if !reflect.DeepEqual(days, []string{"2026-10-25", "2026-10-26", "2026-10-27"}) {
		t.Fatalf("days = %v", days)
	}
}
