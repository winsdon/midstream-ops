package service

import (
	"context"
	"errors"
	"fmt"

	"sub2api-account-monitor/internal/repository"
)

// SelfService 本站（自己的 sub2api）连接：保存并验证管理员凭据。
//
// 凭据落在 providers 表 role='self' 的那条记录里，自动建号经由它调本站 admin API。
type SelfService struct {
	providerRepo *repository.ProviderRepo
	client       *Sub2apiClient
	tokens       *providerTokenManager
}

// NewSelfService 创建 SelfService。复用 balance 侧的客户端与 token 管理器（本站也是一条 provider 记录）。
func NewSelfService(providerRepo *repository.ProviderRepo, balanceSvc *BalanceService) *SelfService {
	return &SelfService{
		providerRepo: providerRepo,
		client:       balanceSvc.Client(),
		tokens:       balanceSvc.Tokens(),
	}
}

// SelfInfo 本站连接状态（脱敏，不含密码）。
type SelfInfo struct {
	Configured bool   `json:"configured"`
	BaseURL    string `json:"base_url"`
	LoginEmail string `json:"login_email"`
}

// GetSelf 返回本站连接状态。
func (s *SelfService) GetSelf(ctx context.Context) (SelfInfo, error) {
	p, err := s.providerRepo.GetSelf(ctx)
	if errors.Is(err, repository.ErrNotFound) {
		return SelfInfo{}, nil
	}
	if err != nil {
		return SelfInfo{}, err
	}
	return SelfInfo{Configured: true, BaseURL: p.BaseURL, LoginEmail: p.LoginEmail}, nil
}

// SaveSelf 保存本站连接并验证登录 + admin 权限。password 为 nil 表示不修改。
func (s *SelfService) SaveSelf(ctx context.Context, baseURL, email string, password *string) error {
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
