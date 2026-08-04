package service

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	"sub2api-account-monitor/internal/config"
	"sub2api-account-monitor/internal/repository"
)

// StatsService 收益/成本/利润统计。
//
// 成本口径：取上游供应商的 actual_cost（倍率折后实扣），即我们真正付出去的钱，
// 由 CostSyncService 定时同步到本地库，查询只读本地库（不实时打上游）。
type StatsService struct {
	pg           *repository.PG
	costRepo     *repository.UpstreamCostRepo
	opCostRepo   *repository.OperatingCostRepo
	linkRepo     *repository.ProviderAccountRepo
	providerRepo *repository.ProviderRepo
	cfg          *config.Config
}

// NewStatsService 创建 StatsService。
func NewStatsService(pg *repository.PG, costRepo *repository.UpstreamCostRepo,
	opCostRepo *repository.OperatingCostRepo,
	linkRepo *repository.ProviderAccountRepo, providerRepo *repository.ProviderRepo,
	cfg *config.Config) *StatsService {
	return &StatsService{
		pg: pg, costRepo: costRepo, opCostRepo: opCostRepo, linkRepo: linkRepo,
		providerRepo: providerRepo, cfg: cfg,
	}
}

// AccountStat 单账号统计。
type AccountStat struct {
	AccountID   int64   `json:"account_id"`
	AccountName string  `json:"account_name"`
	Requests    int64   `json:"requests"`
	Revenue     float64 `json:"revenue"`
	Cost        float64 `json:"cost"` // 上游实扣（真实成本）
	Profit      float64 `json:"profit"`
	CostMatched bool    `json:"cost_matched"` // false = 未匹配到上游 key，成本缺失
}

// statBucket 一个统计桶的公共累加字段，被 ProviderStat / GroupStat 匿名嵌入。
//
// 不带 json tag，让字段在 JSON 里被提升到外层，两个维度的响应形状因此完全一致，
// 前端可以共用一套渲染逻辑。
type statBucket struct {
	Requests int64   `json:"requests"`
	Revenue  float64 `json:"revenue"`
	Cost     float64 `json:"cost"`
	Profit   float64 `json:"profit"`
	// CostComplete 为 false 表示桶内存在有流量但没匹配到上游实扣的账号，
	// 此时 Cost 偏低、Profit 偏高，前端须提示「成本数据不完整」。
	CostComplete    bool          `json:"cost_complete"`
	AccountsMissing int           `json:"accounts_missing"`
	// OperatingCost 站点级运营成本（自营站手工录入的买号/订阅/服务器支出）。
	//
	// 它是固定成本，不随用量变化，也不属于任何分组，故只在「按供应商」维度有值，
	// 分组维度恒为 0。字段仍留在共用桶里：ProviderStat/GroupStat 形状一致
	// 让前端能共用一套渲染逻辑，这个收益大于一个恒零字段的成本。
	OperatingCost float64       `json:"operating_cost"`
	Accounts      []AccountStat `json:"accounts"`
}

// add 把一个账号明细并入桶。
//
// costExempt 为 true 时跳过完整性判定：自营站的上游实扣被有意计 0，且站点可能
// 根本没接上游采集（balance_type=none），其账号永远匹配不到成本行 ——
// 那是设计如此，不是数据缺失，计入 AccountsMissing 会让 ⚠ 变成永久噪音。
func (b *statBucket) add(acct AccountStat, costExempt bool) {
	b.Accounts = append(b.Accounts, acct)
	b.Requests += acct.Requests
	b.Revenue += acct.Revenue
	b.Cost += acct.Cost
	// 有流量却没有上游成本 → 该桶利润被高估，标记不完整
	if !costExempt && !acct.CostMatched && acct.Requests > 0 {
		b.CostComplete = false
		b.AccountsMissing++
	}
}

// finalize 结算桶级利润并把子账号按收益降序排列。
//
// 运营成本在此参与利润：Profit = Revenue − 上游实扣 − 运营成本。
// 它不摊到 Accounts 明细——买号/服务器不属于任何单个账号，
// 故子账号 Profit 之和会大于桶级 Profit，差额即运营成本。
func (b *statBucket) finalize() {
	b.Profit = b.Revenue - b.Cost - b.OperatingCost
	sort.Slice(b.Accounts, func(i, j int) bool { return b.Accounts[i].Revenue > b.Accounts[j].Revenue })
}

// ProviderStat 按供应商归并的统计（含子账号明细）。
type ProviderStat struct {
	Provider string `json:"provider"` // 前缀名；未归属桶为 "(未归属)"
	// SelfOperated 自营站：成本取运营成本而非上游实扣，前端据此显示「自营」标签
	// 而不是「成本不完整」告警。
	SelfOperated bool `json:"self_operated"`
	statBucket
}

// unassignedBucket 未归属（无前缀）账号桶名。
const unassignedBucket = "(未归属)"

// ByProvider 按供应商归并收益/成本/利润（PG 按账号聚合 → Go 按关联表归并）。
// 成本来自本地库的上游实扣，按账号 join。
func (s *StatsService) ByProvider(ctx context.Context, start, end time.Time) ([]ProviderStat, error) {
	rows, err := s.pg.AggregateUsageByAccount(ctx, start, end)
	if err != nil {
		return nil, fmt.Errorf("按账号聚合用量失败: %w", err)
	}
	costs := s.accountCosts(ctx, start, end)

	// 归属取自 provider_accounts（唯一真相）；未关联的账号落「(未归属)」桶，
	// 这样漏关联的收益仍计入总计且看得见，而不是被静默吞掉。
	linkMap, err := s.linkRepo.AccountToProvider(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询账号归属失败: %w", err)
	}
	nameByID, err := s.providerRepo.NameByID(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询供应商名失败: %w", err)
	}
	// 自营站账号豁免成本完整性判定，见 statBucket.add
	selfRun, err := s.providerRepo.SelfOperatedIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询自营站失败: %w", err)
	}

	buckets := make(map[string]*ProviderStat)
	for _, r := range rows {
		name := unassignedBucket
		exempt := false
		if pid, ok := linkMap[r.AccountID]; ok {
			if n := nameByID[pid]; n != "" {
				name = n
			}
			exempt = selfRun[pid]
			// 关联指向已删供应商（CASCADE 理论上已清）：仍落未归属桶
		}
		b, exists := buckets[name]
		if !exists {
			b = &ProviderStat{Provider: name, SelfOperated: exempt, statBucket: statBucket{CostComplete: true}}
			buckets[name] = b
		}
		c, matched := costs[r.AccountID]
		b.add(AccountStat{
			AccountID:   r.AccountID,
			AccountName: r.AccountName,
			Requests:    r.Requests,
			Revenue:     r.Revenue,
			Cost:        c.ActualCost,
			Profit:      r.Revenue - c.ActualCost,
			// 自营站成本恒为 0 且这是有意为之，标为已匹配，前端才不会显示 "-"
			CostMatched: matched || exempt,
		}, exempt)
	}

	// 运营成本按站点名挂到桶上。必要时新建空桶：自营站可能当期零流量却有
	// 买号支出（PG 无 usage 行 → 上面不会为它建桶），不补桶这笔成本就凭空消失。
	s.attachOperatingCosts(ctx, buckets, nameByID, start, end)

	out := make([]ProviderStat, 0, len(buckets))
	for _, b := range buckets {
		b.finalize()
		out = append(out, *b)
	}
	// 供应商按收益降序，未归属桶排最后
	sort.Slice(out, func(i, j int) bool {
		if out[i].Provider == unassignedBucket {
			return false
		}
		if out[j].Provider == unassignedBucket {
			return true
		}
		return out[i].Revenue > out[j].Revenue
	})
	return out, nil
}

// costExemptAccounts 返回归属自营站的账号 id 集合。
//
// 这些账号的成本缺失是设计如此（自营站实扣被有意计 0，且站点可能没接上游采集），
// 不该计入 AccountsMissing 触发「⚠ 成本不完整」。
func (s *StatsService) costExemptAccounts(ctx context.Context) (map[int64]bool, error) {
	selfRun, err := s.providerRepo.SelfOperatedIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询自营站失败: %w", err)
	}
	out := make(map[int64]bool)
	if len(selfRun) == 0 {
		return out, nil
	}
	linkMap, err := s.linkRepo.AccountToProvider(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询账号归属失败: %w", err)
	}
	for acctID, pid := range linkMap {
		if selfRun[pid] {
			out[acctID] = true
		}
	}
	return out, nil
}

// attachOperatingCosts 把区间内的站点级运营成本挂到对应供应商桶上，零流量站点补空桶。
//
// 失败时降级为不挂载：运营成本是次要指标，不该让整个统计页 500。
func (s *StatsService) attachOperatingCosts(ctx context.Context, buckets map[string]*ProviderStat,
	nameByID map[int64]string, start, end time.Time) {
	if s.opCostRepo == nil {
		return
	}
	startDate, endDate := s.dateBounds(start, end)
	sums, err := s.opCostRepo.SumByProvider(ctx, startDate, endDate)
	if err != nil {
		log.Printf("[stats] 读取运营成本失败，按 0 处理: %v", err)
		return
	}
	for pid, amount := range sums {
		if amount == 0 {
			continue
		}
		name := nameByID[pid]
		if name == "" {
			continue // 站点已删（CASCADE 理论上已清），无处可挂
		}
		b, exists := buckets[name]
		if !exists {
			b = &ProviderStat{Provider: name, statBucket: statBucket{CostComplete: true}}
			buckets[name] = b
		}
		b.OperatingCost += amount
	}
}

// dateBounds 把左闭右开的时间区间转成成本表用的闭区间日期串（YYYY-MM-DD）。
func (s *StatsService) dateBounds(start, end time.Time) (string, string) {
	return start.In(s.cfg.Location).Format("2006-01-02"),
		end.In(s.cfg.Location).Add(-time.Second).Format("2006-01-02")
}

// GroupStat 按分组的统计（含子账号明细）。
//
// 上游按 key（≈本站账号）计一笔实扣，一个账号可服务多个分组，实扣本身拆不到分组。
// 故 Cost 为分摊值：按各分组在该账号内的原始 token 消耗（裸 total_cost，不含分组倍率）
// 占比，把账号实扣摊到分组。分摊不产生也不吞掉成本，分组的实扣合计 ≡ 按供应商口径的实扣合计。
//
// 但分组的**利润**合计会高于按供应商口径：自营站的运营成本是站点级固定成本，
// 不摊到分组（买号/服务器不属于任何分组，按用量强行分摊是虚假精度；且零用量站点
// 没有任何分组能承载它，成本会凭空消失）。OperatingCost 在本维度恒为 0。
type GroupStat struct {
	GroupID        int64   `json:"group_id"`
	GroupName      string  `json:"group_name"`
	RateMultiplier float64 `json:"rate_multiplier"`
	statBucket
}

// apportionShares 计算同一账号下各行（= 各分组）应分摊的成本比例，返回值与 rows 等长且和为 1。
//
// 权重优先级：裸用量 CostWeight → 请求数 → 均分。之所以有后两级兜底，是因为
// 免费模型 / 未计价请求的 total_cost 可能全为 0，此时按用量分摊会整个塌成 0，
// 导致账号实扣无处安放、分组合计对不上供应商合计。
func apportionShares(rows []repository.GroupAccountUsageRow) []float64 {
	shares := make([]float64, len(rows))
	if len(rows) == 0 {
		return shares
	}
	if len(rows) == 1 {
		shares[0] = 1
		return shares
	}

	var weightSum, reqSum float64
	for _, r := range rows {
		if r.CostWeight > 0 {
			weightSum += r.CostWeight
		}
		if r.Requests > 0 {
			reqSum += float64(r.Requests)
		}
	}

	switch {
	case weightSum > 0:
		for i, r := range rows {
			if r.CostWeight > 0 {
				shares[i] = r.CostWeight / weightSum
			}
		}
	case reqSum > 0:
		for i, r := range rows {
			if r.Requests > 0 {
				shares[i] = float64(r.Requests) / reqSum
			}
		}
	default:
		even := 1 / float64(len(rows))
		for i := range shares {
			shares[i] = even
		}
	}
	return shares
}

// ByGroup 按分组统计收益/成本/利润，成本由账号实扣按用量占比分摊而来。
func (s *StatsService) ByGroup(ctx context.Context, start, end time.Time) ([]GroupStat, error) {
	rows, err := s.pg.AggregateUsageByGroupAccount(ctx, start, end)
	if err != nil {
		return nil, fmt.Errorf("按分组×账号聚合用量失败: %w", err)
	}
	costs := s.accountCosts(ctx, start, end)
	// 自营站账号豁免成本完整性判定（与 ByProvider 同口径），见 statBucket.add
	exemptAccts, err := s.costExemptAccounts(ctx)
	if err != nil {
		return nil, err
	}

	// 先按账号归拢：分摊比例只在同一账号内部才有意义
	byAccount := make(map[int64][]repository.GroupAccountUsageRow)
	order := make([]int64, 0, len(rows))
	for _, r := range rows {
		if _, seen := byAccount[r.AccountID]; !seen {
			order = append(order, r.AccountID)
		}
		byAccount[r.AccountID] = append(byAccount[r.AccountID], r)
	}

	buckets := make(map[int64]*GroupStat)
	for _, accountID := range order {
		acctRows := byAccount[accountID]
		shares := apportionShares(acctRows)
		c, matched := costs[accountID]
		exempt := exemptAccts[accountID]
		for i, r := range acctRows {
			b, exists := buckets[r.GroupID]
			if !exists {
				b = &GroupStat{
					GroupID:        r.GroupID,
					GroupName:      r.GroupName,
					RateMultiplier: r.RateMultiplier,
					statBucket:     statBucket{CostComplete: true},
				}
				buckets[r.GroupID] = b
			}
			cost := c.ActualCost * shares[i]
			b.add(AccountStat{
				AccountID:   r.AccountID,
				AccountName: r.AccountName,
				Requests:    r.Requests,
				Revenue:     r.Revenue,
				Cost:        cost,
				Profit:      r.Revenue - cost,
				CostMatched: matched || exempt,
			}, exempt)
		}
	}

	out := make([]GroupStat, 0, len(buckets))
	for _, b := range buckets {
		b.finalize()
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Revenue > out[j].Revenue })
	return out, nil
}

// TrendPoint 趋势数据点。
type TrendPoint struct {
	Day          string  `json:"day"`
	Requests     int64   `json:"requests"`
	Revenue      float64 `json:"revenue"`
	Cost         float64 `json:"cost"`
	OfficialCost float64 `json:"official_cost"`
	// OperatingCost 当天发生的站点级运营成本（自营站买号/订阅/服务器）。
	// 全额计入发生日，不做跨期摊销，故单日可能出现尖刺。
	OperatingCost float64 `json:"operating_cost"`
	Profit        float64 `json:"profit"`
	// CostComplete 为 false 表示当天没有任何上游成本记录（同步覆盖不到该日），
	// Profit 不可信，前端应弱化显示。
	CostComplete bool `json:"cost_complete"`
}

// Trend 返回近 N 天的日趋势。成本按天取上游实扣（含未匹配账号的 key）。
func (s *StatsService) Trend(ctx context.Context, days int) ([]TrendPoint, error) {
	if days <= 0 {
		days = 7
	}
	// 起点 = 当前时区今天 0 点往前 days-1 天；终点 = 明天 0 点
	_, todayEnd := s.cfg.TodayRange()
	start := todayEnd.AddDate(0, 0, -days)
	return s.TrendRange(ctx, start, todayEnd)
}

// TrendRange 返回指定区间（左闭右开）的日趋势，供仪表盘时间范围选择器使用。
func (s *StatsService) TrendRange(ctx context.Context, start, end time.Time) ([]TrendPoint, error) {
	rows, err := s.pg.AggregateUsageDaily(ctx, s.cfg.Timezone, start, end)
	if err != nil {
		return nil, err
	}

	startDate, endDate := s.dateBounds(start, end)
	dailyCosts, err := s.costRepo.CostByDay(ctx, startDate, endDate)
	if err != nil {
		log.Printf("[stats] 读取每日上游成本失败，趋势成本按 0 处理: %v", err)
		dailyCosts = map[string]repository.DailyCost{}
	}
	// 运营成本按天挂载；失败降级为 0，不让次要指标拖垮趋势图
	opCosts := map[string]float64{}
	if s.opCostRepo != nil {
		if m, opErr := s.opCostRepo.SumByDay(ctx, startDate, endDate); opErr == nil {
			opCosts = m
		} else {
			log.Printf("[stats] 读取每日运营成本失败，按 0 处理: %v", opErr)
		}
	}

	out := make([]TrendPoint, 0, len(rows))
	for _, r := range rows {
		dc, hasCost := dailyCosts[r.Day]
		op := opCosts[r.Day]
		delete(opCosts, r.Day) // 标记已消费，剩下的是零流量日
		out = append(out, TrendPoint{
			Day:           r.Day,
			Requests:      r.Requests,
			Revenue:       r.Revenue,
			Cost:          dc.ActualCost,
			OfficialCost:  r.OfficialCost,
			OperatingCost: op,
			Profit:        r.Revenue - dc.ActualCost - op,
			CostComplete:  hasCost,
		})
	}

	// 补「零流量但有运营成本」的日子：PG 无 usage 行，上面的循环碰不到它们，
	// 不补则当天买号支出在趋势图上凭空消失，与按供应商口径的合计也对不上。
	for day, op := range opCosts {
		if op == 0 {
			continue
		}
		dc, hasCost := dailyCosts[day]
		out = append(out, TrendPoint{
			Day:           day,
			Cost:          dc.ActualCost,
			OperatingCost: op,
			Profit:        -dc.ActualCost - op,
			CostComplete:  hasCost,
		})
	}
	// AggregateUsageDaily 按日升序返回，补点后须重排以维持该契约
	sort.Slice(out, func(i, j int) bool { return out[i].Day < out[j].Day })
	return out, nil
}

// SyncStatus 成本数据的同步状态（前端展示「同步时间」）。
type SyncStatus struct {
	LastSyncedAt   *time.Time `json:"last_synced_at"`   // 所有供应商中最早的一次同步时间（最保守）
	ProvidersTotal int        `json:"providers_total"`  // 参与成本同步的供应商数
	ProvidersOK    int        `json:"providers_ok"`     // 本轮同步成功的供应商数
	KeysTotal      int64      `json:"keys_total"`       // 上游 key 总数
	KeysMatched    int64      `json:"keys_matched"`     // 成功匹配到本站账号的 key 数
	IntervalMin    int        `json:"interval_minutes"` // 同步间隔
	LastError      string     `json:"last_error"`       // 任一供应商的最近错误（截断）
}

// CostSyncStatus 汇总各供应商的成本同步状态。
func (s *StatsService) CostSyncStatus(ctx context.Context) (*SyncStatus, error) {
	states, err := s.costRepo.SyncStates(ctx)
	if err != nil {
		return nil, err
	}
	st := &SyncStatus{
		ProvidersTotal: len(states),
		IntervalMin:    s.cfg.Cost.IntervalMinutes,
	}
	for _, v := range states {
		st.KeysTotal += v.KeysTotal
		st.KeysMatched += v.KeysMatched
		if v.LastError != nil && st.LastError == "" {
			st.LastError = *v.LastError
		}
		if v.LastSyncedAt == nil {
			continue
		}
		st.ProvidersOK++
		// 取最早的同步时间：数据新鲜度以最落后的供应商为准
		if st.LastSyncedAt == nil || v.LastSyncedAt.Before(*st.LastSyncedAt) {
			st.LastSyncedAt = v.LastSyncedAt
		}
	}
	return st, nil
}

// accountCosts 读取区间内按账号的上游实扣；失败时降级为空（成本记 0 并由 CostMatched 标记）。
func (s *StatsService) accountCosts(ctx context.Context, start, end time.Time) map[int64]repository.AccountCost {
	// end 是右开边界（次日 0 点），成本表按天闭区间查，故 dateBounds 回退一秒
	startDate, endDate := s.dateBounds(start, end)
	costs, err := s.costRepo.CostByAccount(ctx, startDate, endDate)
	if err != nil {
		log.Printf("[stats] 读取账号上游成本失败，成本按 0 处理: %v", err)
		return map[int64]repository.AccountCost{}
	}
	return costs
}
