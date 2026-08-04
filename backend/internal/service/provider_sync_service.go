package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"sub2api-account-monitor/internal/repository"
)

// taskSync collector_state 中统一同步任务的 task 名。
const taskSync = "sync"

// taskBackoff 按连续失败次数返回任务退避时长（封顶 2h）。
// 首败不退避（可能是瞬时网络抖动），持续失败逐级拉长，防反复撞上游 WAF。
func taskBackoff(failures int) time.Duration {
	switch {
	case failures <= 1:
		return 0
	case failures == 2:
		return 15 * time.Minute
	case failures == 3:
		return 30 * time.Minute
	default:
		return 2 * time.Hour
	}
}

// SyncOutcome 一次供应商同步的结果（AfterSync 钩子入参）。
type SyncOutcome struct {
	Provider    *repository.Provider
	Snapshot    *repository.BalanceSnapshot
	PrevBalance *float64 // 同步前的余额缓存（余额预警对比用）
	CostSynced  bool
	Failures    int   // 连续失败次数（0 = 本次成功）
	Err         error // 同步层错误（nil = 成功）
}

// syncAllConcurrency 全量刷新的并发上限。
//
// 定时调度刻意把各站点错峰铺开以免同刻齐发（见 SyncScheduler.rebuild），全量刷新
// 是用户主动要「现在全刷一遍」，无法错峰，只能靠并发上限压住瞬时压力：各站点是
// 不同上游域名，小并发不会集中打到同一站，同时把 N 个站点的总耗时从串行的 N×t
// 降到约 N/4×t。
const syncAllConcurrency = 4

// SyncAllResult 全量刷新的汇总结果。
type SyncAllResult struct {
	Total     int              `json:"total"`     // 参与刷新的站点数（不含跳过）
	Succeeded int              `json:"succeeded"` // 成功站点数
	Failed    int              `json:"failed"`    // 失败站点数
	Skipped   int              `json:"skipped"`   // 因登录冷却跳过的站点数
	Failures  []SyncAllFailure `json:"failures"`  // 失败明细（供前端展示具体是哪几个站挂了）
}

// SyncAllFailure 单个站点的失败信息。
type SyncAllFailure struct {
	ProviderID int64  `json:"provider_id"`
	Name       string `json:"name"`
	Error      string `json:"error"`
}

// SyncAll 刷新全部可采集供应商，返回逐站点成败汇总。
//
// 与手动刷新单站点的差异：不清登录冷却，且冷却中的站点直接跳过。冷却是上游拒绝
// 登录后的自我保护，全量刷新一次性绕过等于把所有坏凭据的站点同时怼向上游；要恢复
// 某个站请单独刷新它。
func (s *ProviderSyncService) SyncAll(ctx context.Context) (*SyncAllResult, error) {
	providers, err := s.providerRepo.ListCollectable(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询供应商失败: %w", err)
	}

	result := &SyncAllResult{}
	now := time.Now()
	pending := make([]*repository.Provider, 0, len(providers))
	for _, p := range providers {
		if p.LoginCooldownUntil != nil && now.Before(*p.LoginCooldownUntil) {
			result.Skipped++
			continue
		}
		pending = append(pending, p)
	}
	result.Total = len(pending)
	if result.Total == 0 {
		return result, nil
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, syncAllConcurrency)

	for _, p := range pending {
		wg.Add(1)
		go func(p *repository.Provider) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				mu.Lock()
				result.Failed++
				result.Failures = append(result.Failures, SyncAllFailure{
					ProviderID: p.ID, Name: p.Name, Error: ctx.Err().Error(),
				})
				mu.Unlock()
				return
			}

			// manual=false：沿用自动采集语义，不清冷却
			outcome, err := s.SyncOne(ctx, p.ID, false, false)
			if err == nil && outcome != nil {
				err = outcome.Err
			}

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				result.Failed++
				result.Failures = append(result.Failures, SyncAllFailure{
					ProviderID: p.ID, Name: p.Name, Error: truncate(err.Error(), 200),
				})
				return
			}
			result.Succeeded++
		}(p)
	}
	wg.Wait()

	return result, nil
}

// ProviderSyncService 供应商统一同步：一次登录态内完成余额 + 成本 + 上游分组倍率采集。
//
// 移植 transit-hub 的 sync 编排模式：三类数据共享 token（少两次登录），
// 结果统一写 collector_state（退避与 UI 健康展示），完成后触发 AfterSync
// 钩子（预警逻辑在装配层注入，本服务不感知通知渠道）。
type ProviderSyncService struct {
	providerRepo  *repository.ProviderRepo
	collectorRepo *repository.CollectorStateRepo
	balanceSvc    *BalanceService
	costSvc       *CostSyncService
	rateSvc       *RateService
	pg            *repository.PG

	// AfterSync 同步完成钩子（成功或失败都触发）；装配层注入。
	AfterSync func(SyncOutcome)
	// OnUpstreamRateChanged 上游倍率变化钩子（自动调价）；装配层注入。
	OnUpstreamRateChanged func(providerID int64, events []RateChangeEvent)
}

// NewProviderSyncService 创建 ProviderSyncService。
func NewProviderSyncService(
	providerRepo *repository.ProviderRepo,
	collectorRepo *repository.CollectorStateRepo,
	balanceSvc *BalanceService,
	costSvc *CostSyncService,
	rateSvc *RateService,
	pg *repository.PG,
) *ProviderSyncService {
	return &ProviderSyncService{
		providerRepo:  providerRepo,
		collectorRepo: collectorRepo,
		balanceSvc:    balanceSvc,
		costSvc:       costSvc,
		rateSvc:       rateSvc,
		pg:            pg,
	}
}

// SyncOne 同步单个供应商：余额快照 + （PG 可用时）成本同步。
// manual=true 时先清零登录冷却（管理员修好凭据后一键恢复）。
// backfill=true 强制回补历史成本；否则首次同步自动回补。
func (s *ProviderSyncService) SyncOne(ctx context.Context, providerID int64, manual, backfill bool) (*SyncOutcome, error) {
	p, err := s.providerRepo.GetByID(ctx, providerID)
	if err != nil {
		return nil, err
	}
	if p.BalanceType != "sub2api" {
		return nil, fmt.Errorf("供应商 %s 余额获取方式为 %s，不支持自动采集", p.Name, p.BalanceType)
	}
	if manual {
		// 手动触发绕过登录冷却并清零计数
		_ = s.providerRepo.ClearLoginCooldown(ctx, p.ID)
		p.LoginFailures = 0
		p.LoginCooldownUntil = nil
	}
	prevBalance := p.LastBalance

	// 余额采集（登录 → 仪表盘 → 快照落库；错误写入 snap.Error 不上抛）
	snap, err := s.balanceSvc.collect(ctx, p)
	if err != nil {
		// 本地库写入失败才会走到这里
		outcome := &SyncOutcome{Provider: p, PrevBalance: prevBalance, Err: err}
		outcome.Failures = s.recordFailure(ctx, p.ID, err.Error())
		return outcome, err
	}

	outcome := &SyncOutcome{Provider: p, Snapshot: snap, PrevBalance: prevBalance}
	if snap.Balance != nil {
		outcome.Provider.LastBalance = snap.Balance
	}

	var syncErr error
	if snap.Error != nil {
		syncErr = errors.New(*snap.Error)
	} else {
		// 上游分组倍率：复用余额侧刚建立的会话（余额成功说明会话可用）
		s.syncUpstreamRates(ctx, p)

		if s.pg.Available() {
			// 成本同步：复用同一登录态，按平台分发 per-key 实扣接口。
			syncErr = s.syncCost(ctx, p, backfill)
			outcome.CostSynced = syncErr == nil
		}
	}

	if syncErr != nil {
		outcome.Err = syncErr
		outcome.Failures = s.recordFailure(ctx, p.ID, syncErr.Error())
	} else {
		_ = s.collectorRepo.RecordSuccess(ctx, p.ID, taskSync)
	}

	if s.AfterSync != nil {
		s.AfterSync(*outcome)
	}
	return outcome, nil
}

// syncUpstreamRates 拉取上游站点分组倍率并三路分拣落快照（失败仅记日志，不影响主流程）。
func (s *ProviderSyncService) syncUpstreamRates(ctx context.Context, p *repository.Provider) {
	sess, err := s.balanceSvc.Tokens().ensure(ctx, p)
	if err != nil {
		return // 会话失效由余额侧兜底，这里静默跳过
	}

	var entities []RateEntity
	if p.Platform == "new-api" {
		rates, err := s.balanceSvc.NewAPIClient().GetGroupRates(ctx, p.BaseURL, sess.NewAPI)
		if err != nil {
			log.Printf("[sync] 供应商 %s 拉取分组倍率失败: %v", p.Name, err)
			return
		}
		for _, r := range rates {
			// new-api 的分组接口不返回平台归属，Platform 留空 → 前端归「未分类」
			entities = append(entities, RateEntity{ID: r.Name, Name: r.Name, Rate: r.Ratio})
		}
	} else {
		rates, err := s.balanceSvc.Client().GetGroupRates(ctx, p.BaseURL, sess.AccessToken)
		if err != nil {
			log.Printf("[sync] 供应商 %s 拉取分组倍率失败: %v", p.Name, err)
			return
		}
		for _, r := range rates {
			entities = append(entities, RateEntity{ID: r.Name, Name: r.Name, Rate: r.Rate, Platform: r.Platform})
		}
	}
	if len(entities) == 0 {
		return
	}

	// upstream scope 的变化事件：自动调价 + 倍率预警
	events := s.rateSvc.Reconcile(ctx, "upstream", p.ID, "group", entities)
	if len(events) == 0 {
		return
	}
	if s.OnUpstreamRateChanged != nil {
		s.OnUpstreamRateChanged(p.ID, events)
	}
	if s.rateSvc.OnRateChanged != nil {
		// 预警事件带上供应商名前缀便于识别来源
		alertEvents := make([]RateChangeEvent, len(events))
		copy(alertEvents, events)
		for i := range alertEvents {
			alertEvents[i].EntityName = p.Name + " / " + alertEvents[i].EntityName
		}
		s.rateSvc.OnRateChanged(alertEvents)
	}
}

// syncCost 执行成本侧同步（key 映射 + 今日实扣 + 按需回补）。
func (s *ProviderSyncService) syncCost(ctx context.Context, p *repository.Provider, backfill bool) error {
	fingerprints, err := s.costSvc.accountFingerprints(ctx)
	if err != nil {
		return fmt.Errorf("查询账号指纹失败: %w", err)
	}
	doBackfill := backfill || s.costSvc.NeedsBackfillFor(ctx, p.ID)
	return s.costSvc.SyncOne(ctx, p, fingerprints, doBackfill)
}

// recordFailure 写失败状态并按最新次数计算退避解禁时刻，返回连续失败次数。
func (s *ProviderSyncService) recordFailure(ctx context.Context, providerID int64, msg string) int {
	n, err := s.collectorRepo.RecordFailure(ctx, providerID, taskSync, truncate(msg, 500), nil)
	if err != nil {
		return 0
	}
	if b := taskBackoff(n); b > 0 {
		next := time.Now().Add(b)
		_ = s.collectorRepo.UpdateBackoff(ctx, providerID, taskSync, &next)
	}
	return n
}

// States 返回全部供应商的同步健康状态（供应商列表 UI 用）。
func (s *ProviderSyncService) States(ctx context.Context) (map[int64]repository.CollectorState, error) {
	return s.collectorRepo.ListByTask(ctx, taskSync)
}
