package service

import (
	"context"
	"log"
	"time"

	"sub2api-account-monitor/internal/repository"
)

// 当期零流量但有运营成本的站点：没有任何分组/用户能承载它。
// 单独成桶，避免这笔支出在分组/用户维度凭空消失，合计才能对上按供应商口径。
const idleOpCostBucket = "(无流量)"
const idleOpCostID int64 = -1

// opAccountShare 一个账号在当期的分摊权重，用于把站点运营成本摊到账号。
type opAccountShare struct {
	AccountID  int64
	ProviderID int64
	Weight     float64
	Requests   int64
}

// splitOperatingCostByAccount 把各站点运营成本按账号用量摊到账号。
//
// 权重优先级与实扣分摊相同：裸用量 → 请求数 → 均分。
// 返回 leftover：当期完全没有流量的站点，其运营成本无处可摊。
func splitOperatingCostByAccount(accts []opAccountShare, opByProvider map[int64]float64) (map[int64]float64, float64) {
	byAccount := map[int64]float64{}
	if len(opByProvider) == 0 {
		return byAccount, 0
	}

	byProvider := map[int64][]opAccountShare{}
	for _, a := range accts {
		if a.ProviderID == 0 {
			continue
		}
		byProvider[a.ProviderID] = append(byProvider[a.ProviderID], a)
	}

	var leftover float64
	for pid, amount := range opByProvider {
		if amount == 0 {
			continue
		}
		members := byProvider[pid]
		if len(members) == 0 {
			leftover += amount
			continue
		}
		shares := apportionSharesN(len(members),
			func(i int) float64 { return members[i].Weight },
			func(i int) int64 { return members[i].Requests },
		)
		for i, a := range members {
			byAccount[a.AccountID] += amount * shares[i]
		}
	}
	return byAccount, leftover
}

func groupAccountOpShares(rows []repository.GroupAccountUsageRow) []opAccountShare {
	return collectAccountOpShares(len(rows),
		func(i int) int64 { return rows[i].AccountID },
		func(i int) float64 { return rows[i].CostWeight },
		func(i int) int64 { return rows[i].Requests },
	)
}

func userAccountOpShares(rows []repository.UserGroupAccountUsageRow) []opAccountShare {
	return collectAccountOpShares(len(rows),
		func(i int) int64 { return rows[i].AccountID },
		func(i int) float64 { return rows[i].CostWeight },
		func(i int) int64 { return rows[i].Requests },
	)
}

func collectAccountOpShares(n int, accountID func(int) int64, weight func(int) float64, requests func(int) int64) []opAccountShare {
	type acc struct {
		id       int64
		weight   float64
		requests int64
	}
	seen := map[int64]*acc{}
	order := make([]int64, 0)
	for i := 0; i < n; i++ {
		id := accountID(i)
		a, ok := seen[id]
		if !ok {
			a = &acc{id: id}
			seen[id] = a
			order = append(order, id)
		}
		a.weight += weight(i)
		a.requests += requests(i)
	}
	out := make([]opAccountShare, 0, len(order))
	for _, id := range order {
		a := seen[id]
		out = append(out, opAccountShare{AccountID: a.id, Weight: a.weight, Requests: a.requests})
	}
	return out
}

// periodOpCostByAccount 读取区间内站点运营成本并摊到账号。失败降级为全 0，不拖垮统计页。
func (s *StatsService) periodOpCostByAccount(ctx context.Context, start, end time.Time, accts []opAccountShare) (map[int64]float64, float64) {
	if s.opCostRepo == nil || s.linkRepo == nil {
		return nil, 0
	}
	startDate, endDate := s.dateBounds(start, end)
	sums, err := s.opCostRepo.SumByProvider(ctx, startDate, endDate)
	if err != nil {
		log.Printf("[stats] 读取运营成本失败，分组/用户维度按 0 处理: %v", err)
		return nil, 0
	}
	linkMap, err := s.linkRepo.AccountToProvider(ctx)
	if err != nil {
		log.Printf("[stats] 查询账号归属失败，运营成本不摊到分组/用户: %v", err)
		return nil, 0
	}
	for i := range accts {
		accts[i].ProviderID = linkMap[accts[i].AccountID]
	}
	return splitOperatingCostByAccount(accts, sums)
}
