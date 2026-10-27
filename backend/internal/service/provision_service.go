package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"sub2api-account-monitor/internal/repository"
)

// ProvisionService 自动建号：在上游站建 key + 在本站建对应账号。
//
// 这是本系统唯一会「写远端资源」的功能，安全设计：
//  1. pending-intent：先落 pending 记录再打 API，补偿失败后仍可对账（比 transit-hub
//     原版只打日志更稳 —— 那样孤儿资源连线索都不留）；
//  2. 补偿：本站建号失败时删掉已建的上游 key，避免残留；
//  3. 幂等：operation_id 防手抖双击建两份；
//  4. sub2api 协议来自上游；new-api 协议由用户显式选择；
//  5. 本站分组 id 以 admin API 口径为准，不用 PG 读到的 id。
type ProvisionService struct {
	mu           sync.Mutex
	newapi       *NewAPIClient
	providerRepo *repository.ProviderRepo
	connRepo     *repository.ConnectionRepo
	linkRepo     *repository.ProviderAccountRepo
	pg           *repository.PG
	client       *Sub2apiClient
	tokens       *providerTokenManager
}

// NewProvisionService 创建 ProvisionService。
func NewProvisionService(
	providerRepo *repository.ProviderRepo,
	connRepo *repository.ConnectionRepo,
	linkRepo *repository.ProviderAccountRepo,
	pg *repository.PG,
	balanceSvc *BalanceService,
) *ProvisionService {
	return &ProvisionService{
		providerRepo: providerRepo,
		connRepo:     connRepo,
		linkRepo:     linkRepo,
		pg:           pg,
		client:       balanceSvc.Client(),
		newapi:       balanceSvc.NewAPIClient(),
		tokens:       balanceSvc.Tokens(),
	}
}

// Repo 暴露仓储。
func (s *ProvisionService) Repo() *repository.ConnectionRepo { return s.connRepo }

// ConnectRequest 建号请求。
type ConnectRequest struct {
	Platform      string
	ProviderID    int64   // 上游站
	UpstreamGroup string  // 上游分组名
	LocalGroupIDs []int64 // 本站分组 id（admin API 口径）
	OperationID   string  // 幂等键
	KeyName       string  // 空则 【kaola】{分组}-{倍率}
	AccountName   string  // 空则 【{上游}】{分组}-{倍率}
	BaseURL       string  // 空则随机取该站子账号 URL，再退站点地址
}

// selfSession 取本站 admin 会话。
func (s *ProvisionService) selfSession(ctx context.Context) (*repository.Provider, string, error) {
	p, err := s.providerRepo.GetSelf(ctx)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, "", errors.New("尚未配置本站连接，请先在设置页「本站连接」保存本站管理员凭据")
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
// 已知失败补偿并标 failed；创建结果不明时保留 pending，绝不重发创建。
func (s *ProvisionService) Connect(ctx context.Context, req ConnectRequest) (*repository.UpstreamConnection, error) {
	// Serialize local connect/disconnect; the unique operation_id index also protects multiple instances.
	s.mu.Lock()
	defer s.mu.Unlock()
	if req.OperationID != "" {
		existing, err := s.connRepo.GetByOperationID(ctx, req.OperationID)
		if err == nil {
			if existing.ProviderID != req.ProviderID || existing.UpstreamGroup != req.UpstreamGroup || existing.Mode != repository.ConnModeManaged || (req.Platform != "" && existing.GroupPlatform != req.Platform) {
				return nil, errors.New("operation_id 已用于其他请求")
			}
			if existing.Status != repository.ConnStatusActive {
				return nil, fmt.Errorf("对接记录 #%d 状态为 %s，请核查记录后处理，未重复创建", existing.ID, existing.Status)
			}
			return existing, nil
		}
		if !errors.Is(err, repository.ErrNotFound) {
			return nil, err
		}
	}
	if len(req.LocalGroupIDs) == 0 {
		return nil, errors.New("至少选择一个本站分组")
	}
	up, err := s.providerRepo.GetByID(ctx, req.ProviderID)
	if err != nil {
		return nil, err
	}
	isNewAPI := up.Platform == "new-api"
	if isNewAPI && req.Platform != "openai" && req.Platform != "anthropic" && req.Platform != "gemini" {
		return nil, errors.New("new-api 必须选择 openai、anthropic 或 gemini 协议")
	}
	upSess, err := s.tokens.ensure(ctx, up)
	if err != nil {
		return nil, fmt.Errorf("上游登录失败: %w", err)
	}
	var group *Sub2apiGroupRate
	if isNewAPI {
		group, err = s.newapi.ProvisionGroup(ctx, up.BaseURL, upSess.NewAPI, req.UpstreamGroup)
		if err != nil {
			return nil, err
		}
		group.Platform = req.Platform
	} else {
		groups, err := s.client.GetGroupRates(ctx, up.BaseURL, upSess.AccessToken)
		if err != nil {
			return nil, fmt.Errorf("拉取上游分组失败: %w", err)
		}
		for i := range groups {
			if groups[i].Name == req.UpstreamGroup {
				group = &groups[i]
				break
			}
		}
		if group == nil || group.ID == 0 {
			return nil, errors.New("上游分组不存在或缺少数字 id")
		}
	}
	self, selfToken, err := s.selfSession(ctx)
	if err != nil {
		return nil, err
	}
	locals, err := s.client.GetAdminGroups(ctx, self.BaseURL, selfToken)
	if err != nil {
		return nil, errors.New("拉取本站分组失败")
	}
	for _, id := range req.LocalGroupIDs {
		valid := false
		for _, local := range locals {
			if local.ID == id && id > 0 && local.Raw["platform"] == group.Platform && group.Platform != "" {
				valid = true
				break
			}
		}
		if !valid {
			return nil, fmt.Errorf("本站分组 #%d 不存在或协议不一致", id)
		}
	}
	keyName := strings.TrimSpace(req.KeyName)
	if keyName == "" {
		keyName = UpstreamKeyName(group.Name, group.Rate)
	}
	if isNewAPI {
		keyName, err = NewAPITokenName(keyName)
		if err != nil {
			return nil, err
		}
	}
	accountName := strings.TrimSpace(req.AccountName)
	if accountName == "" {
		accountName = LocalAccountName(up.Name, group.Name, group.Rate)
	}
	baseURL := strings.TrimSpace(req.BaseURL)
	if baseURL == "" {
		baseURL = s.pickProviderAccountURL(ctx, req.ProviderID)
	}
	if baseURL == "" {
		baseURL = up.BaseURL
	}
	connID, err := s.connRepo.CreatePending(ctx, repository.ConnectionParams{
		ProviderID: req.ProviderID, UpstreamGroup: req.UpstreamGroup, UpstreamGroupID: group.ID,
		GroupPlatform: group.Platform, LocalGroupIDs: req.LocalGroupIDs, Mode: repository.ConnModeManaged,
		OperationID: req.OperationID, UpstreamKeyName: keyName,
	})
	if err != nil {
		return nil, fmt.Errorf("创建对接记录失败: %w", err)
	}

	// Once intent is durable, finish reconciliation/compensation even if the browser disconnects.
	work, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	var keyID int64
	var apiKey string
	fail := func(message string, compensate bool) (*repository.UpstreamConnection, error) {
		cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer done()
		if compensate && keyID > 0 {
			if err := s.deleteUpstreamKey(cleanup, up, upSess, keyID); err != nil {
				message += fmt.Sprintf("；补偿删除上游令牌 #%d 失败，需人工清理或重试取消对接", keyID)
				// Retry persisting the known ID if the initial state write failed.
				if err := s.connRepo.SetKeyCreated(cleanup, connID, keyID, keyName); err != nil {
					message += "；令牌 ID 写入仍失败，可按已保存的唯一名称核查"
				}
			} else if err := s.connRepo.SetKeyCreated(cleanup, connID, 0, keyName); err != nil {
				message += "；令牌已删除但清理状态写入失败，需人工核查"
			}
		}
		if err := s.connRepo.SetFailed(cleanup, connID, message); err != nil {
			return nil, fmt.Errorf("%s；失败状态写入失败，记录 #%d 需人工核查", message, connID)
		}
		return nil, errors.New(message)
	}
	if isNewAPI {
		// Preflight prevents adopting any pre-existing token, even on a name collision.
		tokens, err := s.newapi.ListTokens(work, up.BaseURL, upSess.NewAPI)
		if err != nil {
			return fail("创建前读取令牌列表失败（未创建）", false)
		}
		for _, token := range tokens {
			if token.Name == keyName {
				return fail("唯一令牌名称已存在（未创建）", false)
			}
		}
		createErr := s.newapi.CreateToken(work, up.BaseURL, upSess.NewAPI, keyName, group.Name)
		// A timeout does not mean failure: query once, never repeat POST.
		lookup, done := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		token, findErr := s.newapi.FindToken(lookup, up.BaseURL, upSess.NewAPI, keyName, group.Name)
		done()
		if findErr != nil {
			message := "创建后未能唯一定位令牌，保留待核查记录；请勿重复创建"
			if createErr != nil {
				message = "创建请求结果不确定，未能唯一定位令牌；请勿重复创建"
			}
			if err := s.connRepo.SetPendingError(work, connID, message); err != nil {
				return nil, fmt.Errorf("%s；状态写入失败，记录 #%d", message, connID)
			}
			return nil, fmt.Errorf("%s（记录 #%d）", message, connID)
		}
		keyID = token.ID
		if err := s.connRepo.SetKeyCreated(work, connID, keyID, keyName); err != nil {
			return fail("令牌 ID 写入失败，已停止建号", true)
		}
		apiKey, err = s.newapi.ProvisionTokenKey(work, up.BaseURL, upSess.NewAPI, token)
		if err != nil {
			return fail("读取完整令牌密钥失败", true)
		}
	} else {
		keyID, apiKey, err = s.client.CreateAPIKey(work, up.BaseURL, upSess.AccessToken, keyName, group.ID)
		if err != nil {
			return fail("上游建 key 失败", keyID > 0)
		}
		if err := s.connRepo.SetKeyCreated(work, connID, keyID, keyName); err != nil {
			return fail("上游 key ID 写入失败，已停止建号", true)
		}
	}
	payload := BuildAccountPayload(group.Platform, baseURL, apiKey, accountName, req.LocalGroupIDs)
	accountID, err := s.client.CreateAdminAccount(work, self.BaseURL, selfToken, payload)
	if err != nil {
		return fail("本站建账号失败（若请求超时，请核查本站是否已创建账号）", true)
	}
	if err := s.connRepo.SetLocalAccount(work, connID, accountID, accountName); err != nil {
		return nil, fmt.Errorf("本站账号 #%d 已创建，但记录 #%d 写入失败，需人工核查；请勿重复创建", accountID, connID)
	}
	if s.linkRepo != nil {
		if err := s.linkRepo.LinkMany(work, []repository.ProviderAccount{{ProviderID: req.ProviderID, AccountID: accountID, AccountName: accountName}}); err != nil {
			return fail("账号已创建但供应商关联失败，需人工处理；请勿重复创建", false)
		}
	}
	if err := s.connRepo.SetActive(work, connID, accountID, accountName); err != nil {
		return nil, fmt.Errorf("对接已完成但状态写入失败，记录 #%d 需人工核查", connID)
	}
	return s.connRepo.GetByID(work, connID)
}

func (s *ProvisionService) deleteUpstreamKey(ctx context.Context, up *repository.Provider, sess *providerSession, id int64) error {
	if up.Platform == "new-api" {
		return s.newapi.DeleteToken(ctx, up.BaseURL, sess.NewAPI, id)
	}
	return s.client.DeleteAPIKey(ctx, up.BaseURL, sess.AccessToken, id)
}

// pickProviderAccountURL 从该站已关联子账号的转发地址里随机取一条。
func (s *ProvisionService) pickProviderAccountURL(ctx context.Context, providerID int64) string {
	if s.pg == nil || !s.pg.Available() || s.linkRepo == nil {
		return ""
	}
	links, err := s.linkRepo.ListByProvider(ctx, providerID)
	if err != nil || len(links) == 0 {
		return ""
	}
	accs, err := s.pg.ListActiveAccounts(ctx)
	if err != nil {
		return ""
	}
	byID := make(map[int64]repository.PGAccount, len(accs))
	for _, a := range accs {
		byID[a.ID] = a
	}
	urls := make([]string, 0, len(links))
	for _, l := range links {
		if a, ok := byID[l.AccountID]; ok {
			urls = append(urls, a.BaseURL)
		}
	}
	return PickAccountBaseURL(urls)
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
			if existing.Status != repository.ConnStatusActive || existing.Mode != repository.ConnModeExisting || existing.ProviderID != req.ProviderID || existing.UpstreamGroup != req.UpstreamGroup || existing.LocalAccountID != req.LocalAccountID {
				return nil, errors.New("operation_id 已用于其他请求或原操作尚未成功")
			}
			return existing, nil
		} else if !errors.Is(err, repository.ErrNotFound) {
			return nil, err
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
	s.mu.Lock()
	defer s.mu.Unlock()
	conn, err := s.connRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if !deleteRemote {
		return s.connRepo.Delete(ctx, id)
	}
	if conn.Mode != repository.ConnModeManaged {
		return errors.New("关联已有资源模式不能删除远端资源")
	}
	up, err := s.providerRepo.GetByID(ctx, conn.ProviderID)
	if err != nil {
		return err
	}
	sess, err := s.tokens.ensure(ctx, up)
	if err != nil {
		return errors.New("上游会话不可用，保留记录以便重试")
	}
	// Unknown creation outcomes must be reconciled before removing the only tracking record.
	if up.Platform == "new-api" && conn.UpstreamKeyID == 0 && (conn.Status == repository.ConnStatusPending || (conn.Error != nil && strings.Contains(*conn.Error, "令牌 ID 写入仍失败"))) {
		token, err := s.newapi.FindToken(ctx, up.BaseURL, sess.NewAPI, conn.UpstreamKeyName, conn.UpstreamGroup)
		if err != nil {
			return errors.New("尚未确认令牌创建结果，保留待核查记录")
		}
		conn.UpstreamKeyID = token.ID
		if err := s.connRepo.SetKeyCreated(ctx, id, token.ID, conn.UpstreamKeyName); err != nil {
			return err
		}
	}
	if conn.LocalAccountID > 0 {
		self, token, err := s.selfSession(ctx)
		if err != nil {
			return err
		}
		if err := s.client.DeleteAdminAccount(ctx, self.BaseURL, token, conn.LocalAccountID); err != nil {
			return errors.New("删除本站账号失败，保留记录以便重试")
		}
		if err := s.connRepo.SetLocalAccount(ctx, id, 0, conn.LocalAccountName); err != nil {
			return err
		}
	}
	if conn.UpstreamKeyID > 0 {
		if err := s.deleteUpstreamKey(ctx, up, sess, conn.UpstreamKeyID); err != nil {
			message := "删除上游令牌失败，保留记录以便重试"
			if saveErr := s.connRepo.SetFailed(ctx, id, message); saveErr != nil {
				return fmt.Errorf("%s；状态写入失败", message)
			}
			return errors.New(message)
		}
		if err := s.connRepo.SetKeyCreated(ctx, id, 0, conn.UpstreamKeyName); err != nil {
			return err
		}
	}
	return s.connRepo.Delete(ctx, id)
}
