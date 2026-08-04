package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"time"

	"sub2api-account-monitor/internal/repository"
)

// pricingEpsilon 倍率比较容差（冲突检测与「无变化」判定）。
const pricingEpsilon = 1e-6

// PricingService 调价：上游倍率变化 → 联动调整自己站点分组倍率。
//
// 安全纪律（移植 transit-hub）：
//  1. 写自己站点一律走 admin API 的 GET + PUT-merge，绝不写 DB、绝不部分覆盖；
//  2. 应用前做冲突检测：当前值 ≠ last_applied_rate 说明有人工改动，
//     置 conflict 并永久停止自动覆盖，直到人工确认；
//  3. 每次应用先落 pending 审计再调 API（崩溃后可对账）；
//  4. 跟随阈值：上游变化幅度 ≤ 阈值才自动跟随，超过则不动留给人工。
type PricingService struct {
	providerRepo *repository.ProviderRepo
	pricingRepo  *repository.PricingRepo
	rateRepo     *repository.RateRepo
	client       *Sub2apiClient
	tokens       *providerTokenManager
	pg           *repository.PG
}

// NewPricingService 创建 PricingService。
func NewPricingService(
	providerRepo *repository.ProviderRepo,
	pricingRepo *repository.PricingRepo,
	rateRepo *repository.RateRepo,
	balanceSvc *BalanceService,
	pg *repository.PG,
) *PricingService {
	// 复用 balance 侧的客户端与 token 管理器（self 站点也是一条 provider 记录）
	return &PricingService{
		providerRepo: providerRepo,
		pricingRepo:  pricingRepo,
		rateRepo:     rateRepo,
		client:       balanceSvc.Client(),
		tokens:       balanceSvc.Tokens(),
		pg:           pg,
	}
}

// Repo 暴露仓储（handler 直读列表）。
func (s *PricingService) Repo() *repository.PricingRepo { return s.pricingRepo }

// SelfInfo 自己站点连接状态。
type SelfInfo struct {
	Configured bool   `json:"configured"`
	BaseURL    string `json:"base_url"`
	LoginEmail string `json:"login_email"`
}

// GetSelf 返回自己站点连接状态（脱敏）。
func (s *PricingService) GetSelf(ctx context.Context) (SelfInfo, error) {
	p, err := s.providerRepo.GetSelf(ctx)
	if errors.Is(err, repository.ErrNotFound) {
		return SelfInfo{}, nil
	}
	if err != nil {
		return SelfInfo{}, err
	}
	return SelfInfo{Configured: true, BaseURL: p.BaseURL, LoginEmail: p.LoginEmail}, nil
}

// SaveSelf 保存自己站点连接并验证登录 + admin 权限。
func (s *PricingService) SaveSelf(ctx context.Context, baseURL, email string, password *string) error {
	p, err := s.providerRepo.UpsertSelf(ctx, baseURL, email, password)
	if err != nil {
		return err
	}
	sess, err := s.tokens.ensure(ctx, p)
	if err != nil {
		return fmt.Errorf("登录验证失败: %w", err)
	}
	if _, err := s.client.GetAdminGroups(ctx, p.BaseURL, sess.AccessToken); err != nil {
		return fmt.Errorf("管理端权限验证失败（账号须为 admin）: %w", err)
	}
	return nil
}

// selfSession 取自己站点的可用会话。
func (s *PricingService) selfSession(ctx context.Context) (*repository.Provider, string, error) {
	p, err := s.providerRepo.GetSelf(ctx)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, "", errors.New("尚未配置本站连接，请先在调价页保存本站管理员凭据")
	}
	if err != nil {
		return nil, "", err
	}
	sess, err := s.tokens.ensure(ctx, p)
	if err != nil {
		return nil, "", err
	}
	return p, sess.AccessToken, nil
}

// upstreamRates 返回全部上游分组当前倍率（key = "providerID:group"）。
func (s *PricingService) upstreamRates(ctx context.Context) (map[string]float64, error) {
	list, err := s.rateRepo.CurrentList(ctx, "upstream", nil, false)
	if err != nil {
		return nil, err
	}
	out := make(map[string]float64, len(list))
	for _, u := range list {
		out[repository.SourceKey(u.ProviderID, u.EntityID)] = u.Rate
	}
	return out, nil
}

// PreviewRow 规则预览：参考价 → 目标倍率 vs 本站当前值。
type PreviewRow struct {
	Pricing     *repository.LocalGroupPricing `json:"pricing"`
	Reference   *float64                      `json:"reference_rate"` // nil = 数据源缺失
	CurrentRate *float64                      `json:"current_rate"`   // nil = 本站分组不存在
	TargetRate  *float64                      `json:"target_rate"`
	NeedsApply  bool                          `json:"needs_apply"`
	// SourceRates 各数据源当前倍率明细（UI 展示聚合依据）
	SourceRates map[string]float64 `json:"source_rates"`
}

// Preview 计算全部规则的应用预览（只读，不写任何东西）。
func (s *PricingService) Preview(ctx context.Context) ([]PreviewRow, error) {
	rules, err := s.pricingRepo.List(ctx)
	if err != nil {
		return nil, err
	}
	if len(rules) == 0 {
		return []PreviewRow{}, nil
	}
	rates, err := s.upstreamRates(ctx)
	if err != nil {
		return nil, err
	}

	// 本站当前倍率（PG groups）
	localRate := map[int64]float64{}
	if s.pg.Available() {
		if groups, gErr := s.pg.ListGroupRates(ctx); gErr == nil {
			for _, g := range groups {
				localRate[g.ID] = g.Rate
			}
		}
	}

	out := make([]PreviewRow, 0, len(rules))
	for _, r := range rules {
		row := PreviewRow{Pricing: r, SourceRates: map[string]float64{}}
		for _, src := range r.Sources {
			k := repository.SourceKey(src.ProviderID, src.UpstreamGroup)
			if v, ok := rates[k]; ok {
				row.SourceRates[k] = v
			}
		}
		if ref, ok := r.Reference(rates); ok {
			row.Reference = &ref
			t := r.Target(ref)
			row.TargetRate = &t
		}
		if v, ok := localRate[r.LocalGroupID]; ok {
			cur := v
			row.CurrentRate = &cur
		}
		if row.TargetRate != nil && row.CurrentRate != nil {
			row.NeedsApply = math.Abs(*row.TargetRate-*row.CurrentRate) > pricingEpsilon
		}
		out = append(out, row)
	}
	return out, nil
}

// thresholdExceeded 判断上游变化幅度是否超过跟随阈值。
// 语义：≤ 阈值才自动跟随；超过则不动（防上游异常暴涨暴跌把自己带偏）。
func thresholdExceeded(oldRef, newRef, thresholdPercent float64) bool {
	if oldRef == 0 {
		return false
	}
	changePercent := math.Abs(newRef-oldRef) / oldRef * 100
	// epsilon 消除浮点误判：等于阈值不算超限
	return changePercent-thresholdPercent > 1e-9
}

// Apply 应用单条规则（trigger: auto | manual）。
// force=true 跳过冲突检测（人工确认后强制应用）。
func (s *PricingService) Apply(ctx context.Context, pricingID int64, trigger string, force bool) error {
	r, err := s.pricingRepo.GetByID(ctx, pricingID)
	if err != nil {
		return err
	}
	if r.Conflict && !force {
		return errors.New("该规则存在人工修改冲突，请先在页面上确认处理")
	}

	rates, err := s.upstreamRates(ctx)
	if err != nil {
		return err
	}
	ref, ok := r.Reference(rates)
	if !ok {
		return errors.New("上游数据源暂无倍率数据，无法计算参考价")
	}
	newRate := r.Target(ref)

	p, token, err := s.selfSession(ctx)
	if err != nil {
		return err
	}

	// GET：取本站分组当前完整对象（401 自动重建会话重试一次）
	groups, err := s.client.GetAdminGroups(ctx, p.BaseURL, token)
	if err != nil {
		if IsUnauthorized(err) {
			if _, err = s.tokens.refresh(ctx, p); err != nil {
				return err
			}
			if _, token, err = s.selfSession(ctx); err != nil {
				return err
			}
			groups, err = s.client.GetAdminGroups(ctx, p.BaseURL, token)
		}
		if err != nil {
			return fmt.Errorf("拉取本站分组失败: %w", err)
		}
	}
	var target *AdminGroup
	for i := range groups {
		if groups[i].ID == r.LocalGroupID {
			target = &groups[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("本站分组 #%d 不存在（可能已删除）", r.LocalGroupID)
	}

	// 冲突检测：当前值 ≠ 系统最后写入值 → 有人工改动
	if !force && r.LastAppliedRate != nil && math.Abs(target.Rate-*r.LastAppliedRate) > pricingEpsilon {
		_ = s.pricingRepo.SetConflict(ctx, r.ID)
		old := target.Rate
		msg := fmt.Sprintf("检测到人工修改：当前 %.4f ≠ 系统上次写入 %.4f", target.Rate, *r.LastAppliedRate)
		_, _ = s.pricingRepo.InsertAction(ctx, repository.RateAction{
			PricingID: r.ID, TriggerBy: trigger, OldRate: &old,
			NewRate: newRate, Status: "skipped_conflict", Error: &msg,
		})
		return errors.New(msg + "，已停止自动覆盖")
	}

	if math.Abs(newRate-target.Rate) <= pricingEpsilon {
		// 无变化：只更新基准，不打上游
		_ = s.pricingRepo.SetApplied(ctx, r.ID, target.Rate)
		return nil
	}

	// pending-intent：先落审计再写上游
	old := target.Rate
	actionID, err := s.pricingRepo.InsertAction(ctx, repository.RateAction{
		PricingID: r.ID, TriggerBy: trigger, OldRate: &old, NewRate: newRate, Status: "pending",
	})
	if err != nil {
		return err
	}

	// PUT-merge：完整对象仅改 rate_multiplier
	if err := s.client.UpdateAdminGroupRate(ctx, p.BaseURL, token, *target, newRate); err != nil {
		msg := truncate(err.Error(), 500)
		_ = s.pricingRepo.UpdateActionStatus(ctx, actionID, "failed", &msg)
		return fmt.Errorf("写入本站分组倍率失败: %w", err)
	}
	_ = s.pricingRepo.UpdateActionStatus(ctx, actionID, "applied", nil)
	_ = s.pricingRepo.SetApplied(ctx, r.ID, newRate)
	log.Printf("[pricing] 规则 #%d %s: 参考 %.4f → 目标 %.4f（原 %.4f，%s）",
		r.ID, r.LocalGroupName, ref, newRate, old, trigger)
	return nil
}

// ResolveConflict 人工确认冲突：以本站当前值为新基准，恢复自动覆盖资格。
func (s *PricingService) ResolveConflict(ctx context.Context, pricingID int64) error {
	r, err := s.pricingRepo.GetByID(ctx, pricingID)
	if err != nil {
		return err
	}
	p, token, err := s.selfSession(ctx)
	if err != nil {
		return err
	}
	groups, err := s.client.GetAdminGroups(ctx, p.BaseURL, token)
	if err != nil {
		return fmt.Errorf("拉取本站分组失败: %w", err)
	}
	for _, g := range groups {
		if g.ID == r.LocalGroupID {
			return s.pricingRepo.ResolveConflict(ctx, pricingID, g.Rate)
		}
	}
	return fmt.Errorf("本站分组 #%d 不存在", r.LocalGroupID)
}

// HandleUpstreamRateChanges 自动调价钩子：上游倍率变化时应用相关规则。
// 挂在装配层的倍率变更事件管道上；仅处理 auto_enabled 且无冲突的规则。
func (s *PricingService) HandleUpstreamRateChanges(providerID int64, events []RateChangeEvent) {
	if len(events) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// 同一规则可能被同轮多个上游变化命中，去重后每条规则最多应用一次
	seen := make(map[int64]bool)
	for _, e := range events {
		rules, err := s.pricingRepo.ListByUpstream(ctx, providerID, e.UpstreamGroup)
		if err != nil {
			log.Printf("[pricing] 查询规则失败: %v", err)
			continue
		}
		for _, r := range rules {
			if seen[r.ID] || !r.AutoEnabled || r.Conflict {
				continue
			}
			seen[r.ID] = true

			// 跟随阈值：变化幅度超限则不动，留给人工确认
			if thresholdExceeded(e.OldRate, e.NewRate, r.FollowThreshold) {
				log.Printf("[pricing] 规则 #%d 跳过：上游 %s 变化 %.4f→%.4f 超过跟随阈值 %.1f%%",
					r.ID, e.UpstreamGroup, e.OldRate, e.NewRate, r.FollowThreshold)
				msg := fmt.Sprintf("上游变化超过跟随阈值 %.1f%%，需人工确认", r.FollowThreshold)
				_, _ = s.pricingRepo.InsertAction(ctx, repository.RateAction{
					PricingID: r.ID, TriggerBy: "auto", NewRate: e.NewRate,
					Status: "skipped_threshold", Error: &msg,
				})
				continue
			}
			if err := s.Apply(ctx, r.ID, "auto", false); err != nil {
				log.Printf("[pricing] 自动调价失败 规则#%d: %v", r.ID, err)
			}
		}
	}
}
