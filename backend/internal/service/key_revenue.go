package service

import (
	"context"
	"time"

	"sub2api-account-monitor/internal/repository"
)

// AttachKeyRevenue 给上游 key 成本明细挂上本站收益，返回新切片与收益合计。
//
// 收益按账号统计，而一个账号可能匹配到多个上游 key：账号收益按各 key 的上游成本
// 占比分摊（成本全为 0 时均分），使各行之和等于合计、不重复计算。
// 未匹配账号的 key 收益为 nil；合计只算明细里出现的账号。
func AttachKeyRevenue(items []repository.KeyCostRow, revenueByAccount map[int64]float64) ([]repository.KeyCostRow, float64) {
	costByAccount := make(map[int64]float64)
	keysByAccount := make(map[int64]int)
	for _, k := range items {
		if k.AccountID == nil {
			continue
		}
		costByAccount[*k.AccountID] += k.ActualCost
		keysByAccount[*k.AccountID]++
	}

	out := make([]repository.KeyCostRow, len(items))
	for i, k := range items {
		out[i] = k
		if k.AccountID == nil {
			continue
		}
		acc := *k.AccountID
		share := 1 / float64(keysByAccount[acc])
		if total := costByAccount[acc]; total > 0 {
			share = k.ActualCost / total
		}
		v := revenueByAccount[acc] * share
		out[i].Revenue = &v
	}

	var total float64
	for acc := range keysByAccount {
		total += revenueByAccount[acc]
	}
	return out, total
}

// RevenueByAccount 区间内各账号的收益，口径与统计页一致（日结汇总 + 未结算日实时）。
func (s *StatsService) RevenueByAccount(ctx context.Context, start, end time.Time) (map[int64]float64, error) {
	rows, err := s.usageByAccount(ctx, start, end)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]float64, len(rows))
	for _, r := range rows {
		out[r.AccountID] += r.Revenue
	}
	return out, nil
}
