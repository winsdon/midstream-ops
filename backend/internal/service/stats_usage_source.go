package service

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	"sub2api-account-monitor/internal/repository"
)

// 收入侧读路径：已结算的日子读本地 usage_daily，未结算的（通常只有今天，
// 凌晨结算跑完前还包括昨天）实时查 sub2api usage_logs，两段按主键合并。
//
// 分界取自结算进度而不是墙上时钟：结算任务挂了，实时段会自动向前延伸，
// 统计不会凭空出现 0。

// usageSplit 一个 [start, end) 区间按结算进度切成的两段。
type usageSplit struct {
	settled            bool
	settledFrom        string // 闭区间日期，读 usage_daily
	settledTo          string
	live               bool
	liveStart, liveEnd time.Time // 左闭右开，查 usage_logs
}

// splitUsageRange 以 settledThrough 次日 0 点为界切分 [start, end)。
// settledThrough 为空（从未结算）时整段实时。
func splitUsageRange(start, end time.Time, settledThrough string, loc *time.Location) usageSplit {
	if !start.Before(end) {
		return usageSplit{}
	}
	boundary := start
	if settledThrough != "" {
		if t, err := time.ParseInLocation("2006-01-02", settledThrough, loc); err == nil {
			boundary = t.AddDate(0, 0, 1)
		}
	}
	var sp usageSplit
	if hEnd := minTime(end, boundary); start.Before(hEnd) {
		sp.settled = true
		sp.settledFrom = start.In(loc).Format("2006-01-02")
		sp.settledTo = hEnd.In(loc).Add(-time.Second).Format("2006-01-02")
	}
	if lStart := maxTime(start, boundary); lStart.Before(end) {
		sp.live = true
		sp.liveStart, sp.liveEnd = lStart, end
	}
	return sp
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// usageSplitFor 读结算进度并切分区间。汇总表不可用时降级为整段实时（日志还在的区间照样准）。
func (s *StatsService) usageSplitFor(ctx context.Context, start, end time.Time) usageSplit {
	through := ""
	if s.usageDaily != nil {
		t, err := s.usageDaily.SettledThrough(ctx)
		if err != nil {
			log.Printf("[stats] 读取日结进度失败，整段改查实时日志: %v", err)
		} else {
			through = t
		}
	}
	return splitUsageRange(start, end, through, s.cfg.Location)
}

// loadUsage 按切分结果分别取两段再合并。hist/live 只在对应段存在时调用。
func loadUsage[T any](ctx context.Context, s *StatsService, start, end time.Time,
	hist func(from, to string) ([]T, error),
	live func(start, end time.Time) ([]T, error),
	merge func(hist, live []T) []T,
) ([]T, error) {
	sp := s.usageSplitFor(ctx, start, end)
	var h, l []T
	var err error
	if sp.settled {
		if h, err = hist(sp.settledFrom, sp.settledTo); err != nil {
			return nil, fmt.Errorf("读取日结汇总失败: %w", err)
		}
	}
	if sp.live {
		if l, err = live(sp.liveStart, sp.liveEnd); err != nil {
			return nil, err
		}
	}
	return merge(h, l), nil
}

func (s *StatsService) usageByAccount(ctx context.Context, start, end time.Time) ([]repository.AccountUsageRow, error) {
	return loadUsage(ctx, s, start, end,
		func(from, to string) ([]repository.AccountUsageRow, error) { return s.usageDaily.ByAccount(ctx, from, to) },
		func(a, b time.Time) ([]repository.AccountUsageRow, error) { return s.pg.AggregateUsageByAccount(ctx, a, b) },
		mergeAccountRows)
}

func (s *StatsService) usageByGroupAccount(ctx context.Context, start, end time.Time) ([]repository.GroupAccountUsageRow, error) {
	return loadUsage(ctx, s, start, end,
		func(from, to string) ([]repository.GroupAccountUsageRow, error) {
			return s.usageDaily.ByGroupAccount(ctx, from, to)
		},
		func(a, b time.Time) ([]repository.GroupAccountUsageRow, error) {
			return s.pg.AggregateUsageByGroupAccount(ctx, a, b)
		},
		mergeGroupAccountRows)
}

func (s *StatsService) usageByUserGroupAccount(ctx context.Context, start, end time.Time) ([]repository.UserGroupAccountUsageRow, error) {
	return loadUsage(ctx, s, start, end,
		func(from, to string) ([]repository.UserGroupAccountUsageRow, error) {
			return s.usageDaily.ByUserGroupAccount(ctx, from, to)
		},
		func(a, b time.Time) ([]repository.UserGroupAccountUsageRow, error) {
			return s.pg.AggregateUsageByUserGroupAccount(ctx, a, b)
		},
		mergeUserGroupAccountRows)
}

func (s *StatsService) usageDailyTrend(ctx context.Context, start, end time.Time) ([]repository.DailyTrendRow, error) {
	return loadUsage(ctx, s, start, end,
		func(from, to string) ([]repository.DailyTrendRow, error) { return s.usageDaily.Daily(ctx, from, to) },
		func(a, b time.Time) ([]repository.DailyTrendRow, error) {
			return s.pg.AggregateUsageDaily(ctx, s.cfg.Timezone, a, b)
		},
		mergeDailyRows)
}

// ActiveUsers 区间内有请求的去重用户数（两段 id 取并集，跨段的同一用户只算一次）。
func (s *StatsService) ActiveUsers(ctx context.Context, start, end time.Time) (int, error) {
	ids, err := loadUsage(ctx, s, start, end,
		func(from, to string) ([]int64, error) { return s.usageDaily.ActiveUserIDs(ctx, from, to) },
		func(a, b time.Time) ([]int64, error) { return s.pg.ActiveUserIDs(ctx, a, b) },
		func(h, l []int64) []int64 { return append(h, l...) })
	if err != nil {
		return 0, err
	}
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		seen[id] = struct{}{}
	}
	return len(seen), nil
}

// mergeRows 按 key 合并两段结果：同 key 累加，名称等属性以实时段为准（反映当前值）。
// 顺序：先汇总段，再实时段新出现的 key。
func mergeRows[T any, K comparable](hist, live []T, key func(T) K, add func(h, l T) T) []T {
	if len(hist) == 0 {
		return live
	}
	if len(live) == 0 {
		return hist
	}
	idx := make(map[K]int, len(hist))
	out := make([]T, 0, len(hist)+len(live))
	for _, h := range hist {
		idx[key(h)] = len(out)
		out = append(out, h)
	}
	for _, l := range live {
		if i, ok := idx[key(l)]; ok {
			out[i] = add(out[i], l)
			continue
		}
		idx[key(l)] = len(out)
		out = append(out, l)
	}
	return out
}

func mergeAccountRows(hist, live []repository.AccountUsageRow) []repository.AccountUsageRow {
	return mergeRows(hist, live,
		func(r repository.AccountUsageRow) int64 { return r.AccountID },
		func(h, l repository.AccountUsageRow) repository.AccountUsageRow {
			l.Requests += h.Requests
			l.Revenue += h.Revenue
			return l
		})
}

type groupAccountKey struct{ group, account int64 }

func mergeGroupAccountRows(hist, live []repository.GroupAccountUsageRow) []repository.GroupAccountUsageRow {
	return mergeRows(hist, live,
		func(r repository.GroupAccountUsageRow) groupAccountKey {
			return groupAccountKey{r.GroupID, r.AccountID}
		},
		func(h, l repository.GroupAccountUsageRow) repository.GroupAccountUsageRow {
			l.Requests += h.Requests
			l.Revenue += h.Revenue
			l.CostWeight += h.CostWeight
			return l
		})
}

type userGroupAccountKey struct{ user, group, account int64 }

func mergeUserGroupAccountRows(hist, live []repository.UserGroupAccountUsageRow) []repository.UserGroupAccountUsageRow {
	return mergeRows(hist, live,
		func(r repository.UserGroupAccountUsageRow) userGroupAccountKey {
			return userGroupAccountKey{r.UserID, r.GroupID, r.AccountID}
		},
		func(h, l repository.UserGroupAccountUsageRow) repository.UserGroupAccountUsageRow {
			l.Requests += h.Requests
			l.Revenue += h.Revenue
			l.CostWeight += h.CostWeight
			return l
		})
}

// mergeDailyRows 两段日期本不相交，仍按日合并兜底，并保持按日升序的契约。
func mergeDailyRows(hist, live []repository.DailyTrendRow) []repository.DailyTrendRow {
	out := mergeRows(hist, live,
		func(r repository.DailyTrendRow) string { return r.Day },
		func(h, l repository.DailyTrendRow) repository.DailyTrendRow {
			l.Requests += h.Requests
			l.Revenue += h.Revenue
			l.OfficialCost += h.OfficialCost
			return l
		})
	sort.Slice(out, func(i, j int) bool { return out[i].Day < out[j].Day })
	return out
}
