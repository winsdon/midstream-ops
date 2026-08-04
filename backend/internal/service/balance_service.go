package service

import (
	"context"
	"encoding/json"
	"time"

	"sub2api-account-monitor/internal/config"
	"sub2api-account-monitor/internal/repository"
)

// BalanceService 供应商余额采集编排（平台中性：sub2api / new-api 分发）。
type BalanceService struct {
	providerRepo *repository.ProviderRepo
	balanceRepo  *repository.BalanceRepo
	client       *Sub2apiClient
	newapiClient *NewAPIClient
	tokens       *providerTokenManager
	cfg          *config.Config
}

// NewBalanceService 创建 BalanceService。
func NewBalanceService(providerRepo *repository.ProviderRepo, balanceRepo *repository.BalanceRepo, cfg *config.Config) *BalanceService {
	timeout := time.Duration(cfg.Balance.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	client := NewSub2apiClient(timeout)
	newapiClient := NewNewAPIClient(timeout)
	return &BalanceService{
		providerRepo: providerRepo,
		balanceRepo:  balanceRepo,
		client:       client,
		newapiClient: newapiClient,
		tokens:       newTokenManager(providerRepo, client, newapiClient),
		cfg:          cfg,
	}
}

// Client 暴露 sub2api 客户端（供 test-connection）。
func (s *BalanceService) Client() *Sub2apiClient { return s.client }

// NewAPIClient 暴露 new-api 客户端（供 test-connection）。
func (s *BalanceService) NewAPIClient() *NewAPIClient { return s.newapiClient }

// Tokens 暴露 token 管理器（成本同步复用同一登录态）。
func (s *BalanceService) Tokens() *providerTokenManager { return s.tokens }

// collect 执行采集：取会话 → 拉指标 → 落快照 + 更新冗余列。
func (s *BalanceService) collect(ctx context.Context, p *repository.Provider) (*repository.BalanceSnapshot, error) {
	snap := &repository.BalanceSnapshot{
		ProviderID: p.ID,
		Currency:   "USD",
		Source:     "auto",
	}

	balance, metrics, err := s.fetchBalanceAndMetrics(ctx, p)
	if err != nil {
		errStr := truncate(err.Error(), 500)
		snap.Error = &errStr
		if dbErr := s.balanceRepo.InsertSnapshot(ctx, snap); dbErr != nil {
			return nil, dbErr
		}
		_ = s.providerRepo.UpdateBalanceCache(ctx, p.ID, p.LastBalance, &errStr)
		return snap, nil // 返回带 error 的快照，不视为致命
	}

	snap.Balance = balance
	if metrics != nil {
		if mj, mErr := json.Marshal(metrics); mErr == nil {
			ms := string(mj)
			snap.Metrics = &ms
		}
	}
	if err := s.balanceRepo.InsertSnapshot(ctx, snap); err != nil {
		return nil, err
	}
	_ = s.providerRepo.UpdateBalanceCache(ctx, p.ID, balance, nil)
	return snap, nil
}

// fetchBalanceAndMetrics 平台分发：取余额与仪表盘指标，含一次 401 重建会话。
func (s *BalanceService) fetchBalanceAndMetrics(ctx context.Context, p *repository.Provider) (*float64, *DashboardStats, error) {
	if p.Platform == "new-api" {
		return s.fetchNewAPI(ctx, p)
	}
	return s.fetchSub2api(ctx, p)
}

// fetchSub2api sub2api：登录余额 + /usage/dashboard/stats。
func (s *BalanceService) fetchSub2api(ctx context.Context, p *repository.Provider) (*float64, *DashboardStats, error) {
	sess, err := s.tokens.ensure(ctx, p)
	if err != nil {
		return nil, nil, err
	}
	balance := sess.Balance

	stats, err := s.client.GetDashboardStats(ctx, p.BaseURL, sess.AccessToken)
	if err != nil {
		if IsUnauthorized(err) {
			sess, err = s.tokens.refresh(ctx, p)
			if err != nil {
				return nil, nil, err
			}
			balance = sess.Balance
			stats, err = s.client.GetDashboardStats(ctx, p.BaseURL, sess.AccessToken)
		}
		if err != nil {
			// 仪表盘失败但已有登录余额：返回余额，metrics 为 nil
			if balance != nil {
				return balance, nil, nil
			}
			return nil, nil, err
		}
	}

	// 优先用登录响应的余额；缺失则回退 /auth/me
	if balance == nil {
		b, bErr := s.client.GetBalance(ctx, p.BaseURL, sess.AccessToken)
		if bErr == nil {
			balance = b
		}
	}
	return balance, stats, nil
}

// fetchNewAPI new-api：/user/self 的 quota 折美元 + /log/self/stat 今日消耗。
// 复用 DashboardStats 结构（只填 new-api 能给的字段），快照格式与 sub2api 一致。
func (s *BalanceService) fetchNewAPI(ctx context.Context, p *repository.Provider) (*float64, *DashboardStats, error) {
	sess, err := s.tokens.ensure(ctx, p)
	if err != nil {
		return nil, nil, err
	}

	self, err := s.newapiClient.GetSelf(ctx, p.BaseURL, sess.NewAPI)
	if err != nil {
		if IsUnauthorized(err) {
			sess, err = s.tokens.refresh(ctx, p)
			if err != nil {
				return nil, nil, err
			}
			self, err = s.newapiClient.GetSelf(ctx, p.BaseURL, sess.NewAPI)
		}
		if err != nil {
			return nil, nil, err
		}
	}

	qpu := p.QuotaPerUnit
	if qpu <= 0 {
		qpu = s.newapiClient.GetQuotaPerUnit(ctx, p.BaseURL)
		_ = s.providerRepo.UpdateSession(ctx, p.ID, p.SessionCookie, p.UpstreamUserID, qpu)
		p.QuotaPerUnit = qpu
	}
	balance := self.Quota / qpu

	// 今日消耗（失败不阻断余额落库）
	stats := &DashboardStats{}
	start, end := s.cfg.TodayRange()
	if quota, requests, err := s.newapiClient.GetTodayStat(ctx, p.BaseURL, sess.NewAPI, start, end); err == nil {
		cost := quota / qpu
		stats.TodayActualCost = &cost
		if requests > 0 {
			stats.TodayRequests = &requests
		}
	}
	total := self.UsedQuota / qpu
	stats.TotalActualCost = &total

	return &balance, stats, nil
}

// RecordManual 手动记账（source=manual）。
func (s *BalanceService) RecordManual(ctx context.Context, providerID int64, balance float64) (*repository.BalanceSnapshot, error) {
	if _, err := s.providerRepo.GetByID(ctx, providerID); err != nil {
		return nil, err
	}
	snap := &repository.BalanceSnapshot{
		ProviderID: providerID,
		Balance:    &balance,
		Currency:   "USD",
		Source:     "manual",
	}
	if err := s.balanceRepo.InsertSnapshot(ctx, snap); err != nil {
		return nil, err
	}
	_ = s.providerRepo.UpdateBalanceCache(ctx, providerID, &balance, nil)
	return snap, nil
}

// History 返回快照历史。
func (s *BalanceService) History(ctx context.Context, providerID int64, days int) ([]*repository.BalanceSnapshot, error) {
	return s.balanceRepo.History(ctx, providerID, days)
}

// LatestSnapshots 批量返回各供应商最新快照（列表页展示今日消费等指标）。
func (s *BalanceService) LatestSnapshots(ctx context.Context) (map[int64]*repository.BalanceSnapshot, error) {
	return s.balanceRepo.LatestSnapshots(ctx)
}
