package service

import (
	"context"
	"fmt"
	"sort"
	"time"

	"sub2api-account-monitor/internal/repository"
)

const unassignedUserBucket = "(无用户)"

// UserGroupStat 按用户展开时的分组明细。
type UserGroupStat struct {
	GroupID        int64   `json:"group_id"`
	GroupName      string  `json:"group_name"`
	RateMultiplier float64 `json:"rate_multiplier"`
	Requests       int64   `json:"requests"`
	Revenue        float64 `json:"revenue"`
	Cost           float64 `json:"cost"`
	Profit         float64 `json:"profit"`
	CostMatched    bool    `json:"cost_matched"`
}

// UserStat 按用户归并的统计（展开为分组明细）。
//
// 成本口径与按分组相同：账号实扣按该账号内各用户×分组的裸用量占比分摊。
// 运营成本是站点级固定成本，本维度恒为 0。
type UserStat struct {
	UserID          int64           `json:"user_id"`
	UserName        string          `json:"user_name"`
	Requests        int64           `json:"requests"`
	Revenue         float64         `json:"revenue"`
	Cost            float64         `json:"cost"`
	Profit          float64         `json:"profit"`
	OperatingCost   float64         `json:"operating_cost"`
	CostComplete    bool            `json:"cost_complete"`
	AccountsMissing int             `json:"accounts_missing"`
	Groups          []UserGroupStat `json:"groups"`
}

func assembleUserStats(
	rows []repository.UserGroupAccountUsageRow,
	costs map[int64]repository.AccountCost,
	exempt map[int64]bool,
) []UserStat {
	if len(rows) == 0 {
		return []UserStat{}
	}
	if costs == nil {
		costs = map[int64]repository.AccountCost{}
	}
	if exempt == nil {
		exempt = map[int64]bool{}
	}

	byAccount := make(map[int64][]repository.UserGroupAccountUsageRow)
	acctOrder := make([]int64, 0)
	for _, r := range rows {
		if _, seen := byAccount[r.AccountID]; !seen {
			acctOrder = append(acctOrder, r.AccountID)
		}
		byAccount[r.AccountID] = append(byAccount[r.AccountID], r)
	}

	type groupAcc struct {
		stat UserGroupStat
	}
	type userAcc struct {
		name            string
		requests        int64
		revenue         float64
		cost            float64
		groups          map[int64]*groupAcc
		missingAccounts map[int64]struct{}
	}
	users := make(map[int64]*userAcc)
	userOrder := make([]int64, 0)

	for _, accountID := range acctOrder {
		acctRows := byAccount[accountID]
		shares := apportionUserShares(acctRows)
		c, matched := costs[accountID]
		costMatched := matched || exempt[accountID]
		for i, r := range acctRows {
			u, ok := users[r.UserID]
			if !ok {
				u = &userAcc{
					name:            r.UserName,
					groups:          make(map[int64]*groupAcc),
					missingAccounts: make(map[int64]struct{}),
				}
				users[r.UserID] = u
				userOrder = append(userOrder, r.UserID)
			}
			cost := c.ActualCost * shares[i]
			g, ok := u.groups[r.GroupID]
			if !ok {
				g = &groupAcc{stat: UserGroupStat{
					GroupID:        r.GroupID,
					GroupName:      r.GroupName,
					RateMultiplier: r.RateMultiplier,
					CostMatched:    true,
				}}
				u.groups[r.GroupID] = g
			}
			g.stat.Requests += r.Requests
			g.stat.Revenue += r.Revenue
			g.stat.Cost += cost
			u.requests += r.Requests
			u.revenue += r.Revenue
			u.cost += cost
			if !costMatched && r.Requests > 0 {
				u.missingAccounts[accountID] = struct{}{}
				g.stat.CostMatched = false
			}
		}
	}

	out := make([]UserStat, 0, len(userOrder))
	for _, uid := range userOrder {
		u := users[uid]
		groups := make([]UserGroupStat, 0, len(u.groups))
		for _, g := range u.groups {
			g.stat.Profit = g.stat.Revenue - g.stat.Cost
			groups = append(groups, g.stat)
		}
		sort.Slice(groups, func(i, j int) bool { return groups[i].Revenue > groups[j].Revenue })
		out = append(out, UserStat{
			UserID:          uid,
			UserName:        u.name,
			Requests:        u.requests,
			Revenue:         u.revenue,
			Cost:            u.cost,
			Profit:          u.revenue - u.cost,
			CostComplete:    len(u.missingAccounts) == 0,
			AccountsMissing: len(u.missingAccounts),
			Groups:          groups,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UserID == 0 {
			return false
		}
		if out[j].UserID == 0 {
			return true
		}
		return out[i].Revenue > out[j].Revenue
	})
	return out
}

// ByUser 按用户统计收益/成本/利润，展开为分组明细。
//
// 成本由账号实扣按该账号内各用户×分组的裸用量占比分摊，合计与按供应商口径的实扣一致。
// 运营成本不摊到用户。
func (s *StatsService) ByUser(ctx context.Context, start, end time.Time) ([]UserStat, error) {
	rows, err := s.pg.AggregateUsageByUserGroupAccount(ctx, start, end)
	if err != nil {
		return nil, fmt.Errorf("按用户×分组×账号聚合用量失败: %w", err)
	}
	costs := s.accountCosts(ctx, start, end)
	exemptAccts, err := s.costExemptAccounts(ctx)
	if err != nil {
		return nil, err
	}
	return assembleUserStats(rows, costs, exemptAccts), nil
}
