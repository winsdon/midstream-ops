package service

import (
	"context"
	"errors"
	"fmt"
	"log"

	"sub2api-account-monitor/internal/repository"
)

// ProvisionService 自动建号：在上游站建 key + 在本站建对应账号。
//
// 这是本系统唯一会「写远端资源」的功能，安全设计：
//  1. pending-intent：先落 pending 记录再打 API，补偿失败后仍可对账（比 transit-hub
//     原版只打日志更稳 —— 那样孤儿资源连线索都不留）；
//  2. 补偿：本站建号失败时删掉已建的上游 key，避免残留；
//  3. 幂等：operation_id 防手抖双击建两份；
//  4. 分组 platform 由上游接口直接返回，无需用户手选，避免建出配置错误的账号；
//  5. 本站分组 id 以 admin API 口径为准，不用 PG 读到的 id。
type ProvisionService struct {
	providerRepo *repository.ProviderRepo
	connRepo     *repository.ConnectionRepo
	client       *Sub2apiClient
	tokens       *providerTokenManager
}

// NewProvisionService 创建 ProvisionService。
func NewProvisionService(
	providerRepo *repository.ProviderRepo,
	connRepo *repository.ConnectionRepo,
	balanceSvc *BalanceService,
) *ProvisionService {
	return &ProvisionService{
		providerRepo: providerRepo,
		connRepo:     connRepo,
		client:       balanceSvc.Client(),
		tokens:       balanceSvc.Tokens(),
	}
}

// Repo 暴露仓储。
func (s *ProvisionService) Repo() *repository.ConnectionRepo { return s.connRepo }

// ConnectRequest 建号请求。
type ConnectRequest struct {
	ProviderID    int64   // 上游站
	UpstreamGroup string  // 上游分组名
	LocalGroupIDs []int64 // 本站分组 id（admin API 口径）
	OperationID   string  // 幂等键
}

// selfSession 取本站 admin 会话。
func (s *ProvisionService) selfSession(ctx context.Context) (*repository.Provider, string, error) {
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

// Connect 自动建号：上游建 key → 本站建账号 → 记录转 active。
// 任一步失败都会补偿已建资源并把记录标 failed。
func (s *ProvisionService) Connect(ctx context.Context, req ConnectRequest) (*repository.UpstreamConnection, error) {
	// 幂等：同一 operation_id 已处理过直接返回
	if req.OperationID != "" {
		if existing, err := s.connRepo.GetByOperationID(ctx, req.OperationID); err == nil {
			return existing, nil
		}
	}
	// 防重：同一上游分组已有 active 连接
	if existing, err := s.connRepo.GetActiveByUpstream(ctx, req.ProviderID, req.UpstreamGroup); err == nil {
		return nil, fmt.Errorf("该上游分组已对接（账号 %s），如需重建请先取消对接", existing.LocalAccountName)
	}
	if len(req.LocalGroupIDs) == 0 {
		return nil, errors.New("至少选择一个本站分组")
	}

	up, err := s.providerRepo.GetByID(ctx, req.ProviderID)
	if err != nil {
		return nil, err
	}
	if up.Platform == "new-api" {
		return nil, errors.New("new-api 平台暂不支持自动建号，请手动建号后使用「关联已有资源」")
	}

	// 上游会话 + 定位分组（拿数字 id 与 platform）
	upSess, err := s.tokens.ensure(ctx, up)
	if err != nil {
		return nil, fmt.Errorf("上游登录失败: %w", err)
	}
	groups, err := s.client.GetGroupRates(ctx, up.BaseURL, upSess.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("拉取上游分组失败: %w", err)
	}
	var group *Sub2apiGroupRate
	for i := range groups {
		if groups[i].Name == req.UpstreamGroup {
			group = &groups[i]
			break
		}
	}
	if group == nil {
		return nil, fmt.Errorf("上游分组 %q 不存在", req.UpstreamGroup)
	}
	if group.ID == 0 {
		return nil, fmt.Errorf("上游分组 %q 缺少数字 id，无法建 key", req.UpstreamGroup)
	}

	// 本站会话
	self, selfToken, err := s.selfSession(ctx)
	if err != nil {
		return nil, err
	}

	// ① 落 pending 记录
	connID, err := s.connRepo.CreatePending(ctx, repository.ConnectionParams{
		ProviderID:      req.ProviderID,
		UpstreamGroup:   req.UpstreamGroup,
		UpstreamGroupID: group.ID,
		GroupPlatform:   group.Platform,
		LocalGroupIDs:   req.LocalGroupIDs,
		Mode:            repository.ConnModeManaged,
		OperationID:     req.OperationID,
	})
	if err != nil {
		return nil, fmt.Errorf("创建对接记录失败: %w", err)
	}

	accountName := AccountName(group.Platform, up.Name, group.Rate)

	// ② 上游建 key
	keyID, apiKey, err := s.client.CreateAPIKey(ctx, up.BaseURL, upSess.AccessToken, accountName, group.ID)
	if err != nil {
		msg := truncate(fmt.Sprintf("上游建 key 失败: %v", err), 500)
		_ = s.connRepo.SetFailed(ctx, connID, msg)
		return nil, errors.New(msg)
	}
	_ = s.connRepo.SetKeyCreated(ctx, connID, keyID, accountName)

	// ③ 本站建账号（失败则补偿删除上游 key）
	payload := BuildAccountPayload(group.Platform, up.BaseURL, apiKey, accountName, req.LocalGroupIDs)
	accountID, err := s.client.CreateAdminAccount(ctx, self.BaseURL, selfToken, payload)
	if err != nil {
		msg := truncate(fmt.Sprintf("本站建账号失败: %v", err), 500)
		// 补偿：删掉刚建的上游 key，避免孤儿资源
		if delErr := s.client.DeleteAPIKey(ctx, up.BaseURL, upSess.AccessToken, keyID); delErr != nil {
			msg += fmt.Sprintf("；补偿删除上游 key #%d 也失败: %v（需人工清理）", keyID, delErr)
			log.Printf("[provision] 补偿失败 conn=%d upstream_key=%d: %v", connID, keyID, delErr)
		} else {
			log.Printf("[provision] 已补偿删除上游 key #%d", keyID)
		}
		_ = s.connRepo.SetFailed(ctx, connID, truncate(msg, 500))
		return nil, errors.New(msg)
	}

	// ④ 转 active
	if err := s.connRepo.SetActive(ctx, connID, accountID, accountName); err != nil {
		return nil, fmt.Errorf("对接已完成但状态写入失败: %w", err)
	}
	log.Printf("[provision] 建号成功 上游=%s/%s key=#%d → 本站账号 %s(#%d)",
		up.Name, req.UpstreamGroup, keyID, accountName, accountID)
	return s.connRepo.GetByID(ctx, connID)
}

// BindRequest 关联已有资源（不创建任何远端资源）。
type BindRequest struct {
	ProviderID     int64
	UpstreamGroup  string
	UpstreamKeyID  int64
	LocalAccountID int64
	LocalGroupIDs  []int64
	OperationID    string
}

// Bind 关联已有的上游 key 与本站账号。
func (s *ProvisionService) Bind(ctx context.Context, req BindRequest) (*repository.UpstreamConnection, error) {
	if req.OperationID != "" {
		if existing, err := s.connRepo.GetByOperationID(ctx, req.OperationID); err == nil {
			return existing, nil
		}
	}
	if existing, err := s.connRepo.GetActiveByUpstream(ctx, req.ProviderID, req.UpstreamGroup); err == nil {
		return nil, fmt.Errorf("该上游分组已对接（账号 %s）", existing.LocalAccountName)
	}

	up, err := s.providerRepo.GetByID(ctx, req.ProviderID)
	if err != nil {
		return nil, err
	}
	self, selfToken, err := s.selfSession(ctx)
	if err != nil {
		return nil, err
	}

	// 校验本站账号确实存在（避免绑一个不存在的 id）
	accounts, err := s.client.ListAdminAccounts(ctx, self.BaseURL, selfToken, 0)
	if err != nil {
		return nil, fmt.Errorf("拉取本站账号失败: %w", err)
	}
	var account *AdminAccount
	for i := range accounts {
		if accounts[i].ID == req.LocalAccountID {
			account = &accounts[i]
			break
		}
	}
	if account == nil {
		return nil, fmt.Errorf("本站账号 #%d 不存在", req.LocalAccountID)
	}

	// 上游分组元数据（拿 platform 与数字 id，失败不阻断绑定）
	var groupID int64
	platform := account.Platform
	if upSess, sErr := s.tokens.ensure(ctx, up); sErr == nil {
		if groups, gErr := s.client.GetGroupRates(ctx, up.BaseURL, upSess.AccessToken); gErr == nil {
			for _, g := range groups {
				if g.Name == req.UpstreamGroup {
					groupID = g.ID
					if g.Platform != "" {
						platform = g.Platform
					}
					break
				}
			}
		}
	}

	id, err := s.connRepo.BindExisting(ctx, repository.ConnectionParams{
		ProviderID:      req.ProviderID,
		UpstreamGroup:   req.UpstreamGroup,
		UpstreamGroupID: groupID,
		GroupPlatform:   platform,
		LocalGroupIDs:   req.LocalGroupIDs,
		OperationID:     req.OperationID,
	}, req.UpstreamKeyID, "", req.LocalAccountID, account.Name)
	if err != nil {
		return nil, fmt.Errorf("保存对接记录失败: %w", err)
	}
	log.Printf("[provision] 关联已有资源 上游=%s/%s → 本站账号 %s(#%d)",
		up.Name, req.UpstreamGroup, account.Name, req.LocalAccountID)
	return s.connRepo.GetByID(ctx, id)
}

// Disconnect 取消对接。
// deleteRemote=true 时同时删除远端资源（仅 managed 模式允许）。
func (s *ProvisionService) Disconnect(ctx context.Context, id int64, deleteRemote bool) error {
	conn, err := s.connRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}

	if deleteRemote {
		if !conn.CanDeleteRemote() {
			return errors.New("该连接为「关联已有资源」模式，不能删除远端资源（那些资源不是本系统创建的）")
		}
		// 先删本站账号再删上游 key：本站账号还引用着 key，反序会短暂产生失效账号
		if conn.LocalAccountID > 0 {
			if self, selfToken, sErr := s.selfSession(ctx); sErr == nil {
				if dErr := s.client.DeleteAdminAccount(ctx, self.BaseURL, selfToken, conn.LocalAccountID); dErr != nil {
					return fmt.Errorf("删除本站账号失败: %w", dErr)
				}
			} else {
				return fmt.Errorf("本站会话不可用，无法删除账号: %w", sErr)
			}
		}
		if conn.UpstreamKeyID > 0 {
			up, uErr := s.providerRepo.GetByID(ctx, conn.ProviderID)
			if uErr == nil {
				if upSess, sErr := s.tokens.ensure(ctx, up); sErr == nil {
					if dErr := s.client.DeleteAPIKey(ctx, up.BaseURL, upSess.AccessToken, conn.UpstreamKeyID); dErr != nil {
						// 本站账号已删，上游 key 删失败只记日志：记录仍会被移除，
						// 残留 key 由日志提示人工清理，避免卡住整个取消流程
						log.Printf("[provision] 删除上游 key #%d 失败（需人工清理）: %v", conn.UpstreamKeyID, dErr)
					}
				}
			}
		}
	}
	return s.connRepo.Delete(ctx, id)
}
