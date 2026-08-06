package handler

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"sub2api-account-monitor/internal/config"
	"sub2api-account-monitor/internal/pkg/response"
	"sub2api-account-monitor/internal/repository"
	"sub2api-account-monitor/internal/service"

	"github.com/gin-gonic/gin"
)

// ProviderHandler 供应商处理器。
type ProviderHandler struct {
	svc        *service.ProviderService
	balanceSvc *service.BalanceService
	costSvc    *service.CostSyncService
	syncSvc    *service.ProviderSyncService
	syncSched  *service.SyncScheduler
	rateRepo   *repository.RateRepo
	cfg        *config.Config
	pg         *repository.PG
}

// NewProviderHandler 创建 ProviderHandler。
func NewProviderHandler(svc *service.ProviderService, balanceSvc *service.BalanceService,
	costSvc *service.CostSyncService, syncSvc *service.ProviderSyncService,
	syncSched *service.SyncScheduler, rateRepo *repository.RateRepo,
	cfg *config.Config, pg *repository.PG) *ProviderHandler {
	return &ProviderHandler{svc: svc, balanceSvc: balanceSvc, costSvc: costSvc,
		syncSvc: syncSvc, syncSched: syncSched, rateRepo: rateRepo, cfg: cfg, pg: pg}
}

// providerDTO 列表/详情输出（脱敏：不回显密码/token）。
type providerDTO struct {
	ID                  int64    `json:"id"`
	Name                string   `json:"name"`
	Note                string   `json:"note"`
	BalanceType         string   `json:"balance_type"`
	Platform            string   `json:"platform"`
	AuthMode            string   `json:"auth_mode"`
	BaseURL             string   `json:"base_url"`
	LoginEmail          string   `json:"login_email"`
	HasPassword         bool     `json:"has_password"`
	HasAccessToken      bool     `json:"has_access_token"`
	HasRefreshToken     bool     `json:"has_refresh_token"`
	UpstreamUserID      string   `json:"upstream_user_id"`
	LowBalanceThreshold float64  `json:"low_balance_threshold"`
	RechargeRate        float64  `json:"recharge_rate"`
	// CredentialsReady 凭据是否齐备。false 时该站点不进采集队列（见 ListCollectable），
	// 前端据此把「待配置凭据」与「采集失败」画成两种健康态。
	CredentialsReady    bool     `json:"credentials_ready"`
	ProbeEnabled        bool     `json:"probe_enabled"`
	ProbeModel          *string  `json:"probe_model"`
	IgnoreBalanceAlert  bool     `json:"ignore_balance_alert"`
	SelfOperated        bool     `json:"self_operated"` // 自营站：上游实扣不计入成本，改记运营成本
	LastBalance         *float64 `json:"last_balance"`
	LastBalanceAt       *string  `json:"last_balance_at"`
	LastBalanceError    *string  `json:"last_balance_error"`
	LoginCooldownUntil  *string  `json:"login_cooldown_until"`
	AccountCount        int      `json:"account_count"`
	CreatedAt           string   `json:"created_at"`

	// 上游站点指标（来自最近一次余额快照的 metrics，USD 原值；前端按 recharge_rate 折 CNY）
	TodayCost  *float64 `json:"today_cost"`  // 今日实扣
	TotalCost  *float64 `json:"total_cost"`  // 历史累计实扣（≈历史充值参照）
	TodayReqs  *int64   `json:"today_reqs"`

	// 采集健康（collector_state；无记录时为 nil）
	SyncState *syncStateDTO `json:"sync_state,omitempty"`
}

// snapshotMetrics 余额快照 metrics JSON 中本页关心的字段。
type snapshotMetrics struct {
	TodayActualCost *float64 `json:"today_actual_cost"`
	TotalActualCost *float64 `json:"total_actual_cost"`
	TodayRequests   *int64   `json:"today_requests"`
}

// syncStateDTO 采集健康输出。
type syncStateDTO struct {
	LastRunAt           *string `json:"last_run_at"`
	LastSuccessAt       *string `json:"last_success_at"`
	LastError           *string `json:"last_error"`
	ConsecutiveFailures int     `json:"consecutive_failures"`
	NextEligibleAt      *string `json:"next_eligible_at"`
}

func formatProviderTime(t time.Time, loc *time.Location) string {
	if loc == nil {
		loc = time.Local
	}
	return t.In(loc).Format("2006-01-02 15:04:05")
}

func toSyncStateDTO(st repository.CollectorState, loc *time.Location) *syncStateDTO {
	d := &syncStateDTO{ConsecutiveFailures: st.ConsecutiveFailures, LastError: st.LastError}
	if st.LastRunAt != nil {
		s := formatProviderTime(*st.LastRunAt, loc)
		d.LastRunAt = &s
	}
	if st.LastSuccessAt != nil {
		s := formatProviderTime(*st.LastSuccessAt, loc)
		d.LastSuccessAt = &s
	}
	if st.NextEligibleAt != nil {
		s := formatProviderTime(*st.NextEligibleAt, loc)
		d.NextEligibleAt = &s
	}
	return d
}

func toDTO(p *repository.Provider, accountCount int, loc *time.Location) providerDTO {
	d := providerDTO{
		ID:                  p.ID,
		Name:                p.Name,
		Note:                p.Note,
		BalanceType:         p.BalanceType,
		Platform:            p.Platform,
		AuthMode:            p.AuthMode,
		BaseURL:             p.BaseURL,
		LoginEmail:          p.LoginEmail,
		HasPassword:         p.LoginPassword != "",
		HasAccessToken:      p.AccessToken != "",
		HasRefreshToken:     p.RefreshToken != "",
		UpstreamUserID:      p.UpstreamUserID,
		LowBalanceThreshold: p.LowBalanceThreshold,
		RechargeRate:        p.RechargeRate,
		CredentialsReady:    p.CredentialsReady(),
		ProbeEnabled:        p.ProbeEnabled,
		ProbeModel:          p.ProbeModel,
		IgnoreBalanceAlert:  p.IgnoreBalanceAlert,
		SelfOperated:        p.SelfOperated,
		LastBalance:         p.LastBalance,
		LastBalanceError:    p.LastBalanceError,
		AccountCount:        accountCount,
		CreatedAt:           formatProviderTime(p.CreatedAt, loc),
	}
	if p.LastBalanceAt != nil {
		s := formatProviderTime(*p.LastBalanceAt, loc)
		d.LastBalanceAt = &s
	}
	if p.LoginCooldownUntil != nil && p.LoginCooldownUntil.After(timeNow()) {
		s := formatProviderTime(*p.LoginCooldownUntil, loc)
		d.LoginCooldownUntil = &s
	}
	return d
}

// List GET /providers
func (h *ProviderHandler) List(c *gin.Context) {
	providers, err := h.svc.Repo().List(c.Request.Context())
	if err != nil {
		response.InternalError(c, "查询失败: "+err.Error())
		return
	}
	countMap, _ := h.svc.AccountCountMap(c.Request.Context())
	states, _ := h.syncSvc.States(c.Request.Context())
	// 批量取最新快照，解析出今日消费/历史累计（次要数据，失败不影响列表）
	snaps, _ := h.balanceSvc.LatestSnapshots(c.Request.Context())

	out := make([]providerDTO, 0, len(providers))
	for _, p := range providers {
		d := toDTO(p, countMap[p.ID], h.cfg.Location)
		if st, ok := states[p.ID]; ok {
			d.SyncState = toSyncStateDTO(st, h.cfg.Location)
		}
		if snap, ok := snaps[p.ID]; ok && snap.Metrics != nil {
			var m snapshotMetrics
			if json.Unmarshal([]byte(*snap.Metrics), &m) == nil {
				d.TodayCost = m.TodayActualCost
				d.TotalCost = m.TotalActualCost
				d.TodayReqs = m.TodayRequests
			}
		}
		out = append(out, d)
	}
	response.Success(c, gin.H{"items": out, "total": len(out)})
}

type providerRequest struct {
	Name                string   `json:"name" binding:"required"`
	Note                string   `json:"note"`
	BalanceType         string   `json:"balance_type"`
	Platform            string   `json:"platform"`  // sub2api | new-api
	AuthMode            string   `json:"auth_mode"` // password | token | user_key
	BaseURL             string   `json:"base_url"`
	LoginEmail          string   `json:"login_email"`
	LoginPassword       *string  `json:"login_password"`
	AccessToken         *string  `json:"access_token"`  // token / user_key 模式
	RefreshToken        *string  `json:"refresh_token"` // sub2api token 模式
	UpstreamUserID      string   `json:"upstream_user_id"`
	LowBalanceThreshold float64  `json:"low_balance_threshold"`
	RechargeRate        float64  `json:"recharge_rate"`
	ProbeEnabled        bool     `json:"probe_enabled"`
	ProbeModel          *string  `json:"probe_model"`
	IgnoreBalanceAlert  bool     `json:"ignore_balance_alert"`
	SelfOperated        bool     `json:"self_operated"`
}

var validBalanceTypes = map[string]bool{"sub2api": true, "manual": true, "none": true}
var validPlatforms = map[string]bool{"": true, "sub2api": true, "new-api": true}
var validAuthModes = map[string]bool{"": true, "password": true, "token": true, "user_key": true}

// validatePlatformAuth 校验平台与认证模式组合。
func validatePlatformAuth(platform, authMode string) string {
	if !validPlatforms[platform] {
		return "platform 须为 sub2api|new-api"
	}
	if !validAuthModes[authMode] {
		return "auth_mode 须为 password|token|user_key"
	}
	if platform != "new-api" && authMode == "user_key" {
		return "user_key 模式仅支持 new-api 平台"
	}
	if platform == "new-api" && authMode == "token" {
		return "token 模式仅支持 sub2api 平台（new-api 请用 user_key）"
	}
	return ""
}

// Create POST /providers
func (h *ProviderHandler) Create(c *gin.Context) {
	var req providerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "name 必填")
		return
	}
	if req.BalanceType == "" {
		req.BalanceType = "none"
	}
	if !validBalanceTypes[req.BalanceType] {
		response.BadRequest(c, "balance_type 须为 sub2api|manual|none")
		return
	}
	if msg := validatePlatformAuth(req.Platform, req.AuthMode); msg != "" {
		response.BadRequest(c, msg)
		return
	}
	name := strings.TrimSpace(req.Name)
	if existing, _ := h.svc.Repo().GetByName(c.Request.Context(), name); existing != nil {
		response.BadRequest(c, "供应商已存在: "+name)
		return
	}
	password := ""
	if req.LoginPassword != nil {
		password = *req.LoginPassword
	}
	accessToken := ""
	if req.AccessToken != nil {
		accessToken = *req.AccessToken
	}
	refreshToken := ""
	if req.RefreshToken != nil {
		refreshToken = *req.RefreshToken
	}
	p, err := h.svc.Repo().Create(c.Request.Context(), repository.CreateParams{
		Name:                name,
		Note:                req.Note,
		BalanceType:         req.BalanceType,
		Platform:            req.Platform,
		AuthMode:            req.AuthMode,
		BaseURL:             strings.TrimRight(req.BaseURL, "/"),
		LoginEmail:          req.LoginEmail,
		LoginPassword:       password,
		AccessToken:         accessToken,
		RefreshToken:        refreshToken,
		UpstreamUserID:      strings.TrimSpace(req.UpstreamUserID),
		LowBalanceThreshold: req.LowBalanceThreshold,
		RechargeRate:        req.RechargeRate,
		ProbeEnabled:        req.ProbeEnabled,
		ProbeModel:          req.ProbeModel,
		IgnoreBalanceAlert:  req.IgnoreBalanceAlert,
		SelfOperated:        req.SelfOperated,
	})
	if err != nil {
		response.InternalError(c, "创建失败: "+err.Error())
		return
	}
	h.syncSched.OnProviderChanged(p.ID)
	response.Success(c, toDTO(p, 0, h.cfg.Location))
}

// Update PUT /providers/:id
func (h *ProviderHandler) Update(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	if _, err := h.svc.Repo().GetByID(c.Request.Context(), id); err != nil {
		response.NotFound(c, "供应商不存在")
		return
	}
	var req providerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "name 必填")
		return
	}
	if req.BalanceType == "" {
		req.BalanceType = "none"
	}
	if !validBalanceTypes[req.BalanceType] {
		response.BadRequest(c, "balance_type 须为 sub2api|manual|none")
		return
	}
	if msg := validatePlatformAuth(req.Platform, req.AuthMode); msg != "" {
		response.BadRequest(c, msg)
		return
	}
	p, err := h.svc.Repo().Update(c.Request.Context(), id, repository.UpdateParams{
		Name:                strings.TrimSpace(req.Name),
		Note:                req.Note,
		BalanceType:         req.BalanceType,
		Platform:            req.Platform,
		AuthMode:            req.AuthMode,
		BaseURL:             strings.TrimRight(req.BaseURL, "/"),
		LoginEmail:          req.LoginEmail,
		LoginPassword:       req.LoginPassword, // nil=不修改；空串=清除
		AccessToken:         req.AccessToken,
		RefreshToken:        req.RefreshToken,
		UpstreamUserID:      strings.TrimSpace(req.UpstreamUserID),
		LowBalanceThreshold: req.LowBalanceThreshold,
		RechargeRate:        req.RechargeRate,
		ProbeEnabled:        req.ProbeEnabled,
		ProbeModel:          req.ProbeModel,
		IgnoreBalanceAlert:  req.IgnoreBalanceAlert,
		SelfOperated:        req.SelfOperated,
	})
	if err != nil {
		response.InternalError(c, "更新失败: "+err.Error())
		return
	}
	h.syncSched.OnProviderChanged(p.ID)
	countMap, _ := h.svc.AccountCountMap(c.Request.Context())
	response.Success(c, toDTO(p, countMap[p.ID], h.cfg.Location))
}

// Delete DELETE /providers/:id
func (h *ProviderHandler) Delete(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	if err := h.svc.Repo().Delete(c.Request.Context(), id); err != nil {
		if err == repository.ErrNotFound {
			response.NotFound(c, "供应商不存在")
			return
		}
		response.InternalError(c, "删除失败: "+err.Error())
		return
	}
	// 上游倍率快照随供应商删除清理（transit-hub 同款语义：用户主动删站点时清历史）
	_ = h.rateRepo.DeleteByProvider(c.Request.Context(), id)
	h.syncSched.OnProviderDeleted(id)
	response.Success(c, gin.H{"deleted": id})
}

// Scan GET /providers/scan
func (h *ProviderHandler) Scan(c *gin.Context) {
	if !h.pg.Available() {
		response.ServiceUnavailable(c, "线上数据库暂不可用")
		return
	}
	prefixes, err := h.svc.ScanPrefixes(c.Request.Context())
	if err != nil {
		response.InternalError(c, "扫描失败: "+err.Error())
		return
	}
	response.Success(c, gin.H{"items": prefixes})
}

type importRequest struct {
	Items []service.ImportItem `json:"items" binding:"required"`
}

// Import POST /providers/import —— 批量建站并顺带写入账号关联。
func (h *ProviderHandler) Import(c *gin.Context) {
	var req importRequest
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Items) == 0 {
		response.BadRequest(c, "items 必填且非空")
		return
	}
	for _, it := range req.Items {
		if it.BalanceType != "" && !validBalanceTypes[it.BalanceType] {
			response.BadRequest(c, "balance_type 须为 sub2api|manual|none")
			return
		}
	}
	result, err := h.svc.Import(c.Request.Context(), req.Items)
	if err != nil {
		response.InternalError(c, "导入失败: "+err.Error())
		return
	}
	// 新建站点纳入调度（缺凭据的由 ListCollectable 过滤，排了也不会真去采）
	for _, id := range result.CreatedIDs {
		h.syncSched.OnProviderChanged(id)
	}
	response.Success(c, result)
}

// ScanURLs GET /providers/scan-urls —— 按账号 base_url 归组（关联账号用）。
func (h *ProviderHandler) ScanURLs(c *gin.Context) {
	if !h.pg.Available() {
		response.ServiceUnavailable(c, "线上数据库暂不可用")
		return
	}
	groups, err := h.svc.ScanURLGroups(c.Request.Context())
	if err != nil {
		response.InternalError(c, "扫描失败: "+err.Error())
		return
	}
	response.Success(c, gin.H{"items": groups})
}

// ListLinks GET /providers/:id/links —— 该供应商已关联的账号（只读本地库）。
//
// 不做 pg.Available 检查：关联本身存在本地，线上库挂了也该能看见关联了哪些账号。
func (h *ProviderHandler) ListLinks(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	links, err := h.svc.LinkRepo().ListByProvider(c.Request.Context(), id)
	if err != nil {
		response.InternalError(c, "查询失败: "+err.Error())
		return
	}
	out := make([]linkDTO, 0, len(links))
	for _, l := range links {
		out = append(out, linkDTO{AccountID: l.AccountID, AccountName: l.AccountName, Note: l.Note})
	}
	response.Success(c, gin.H{"items": out, "total": len(out)})
}

// linkDTO 关联账号输出。
type linkDTO struct {
	AccountID   int64  `json:"account_id"`
	AccountName string `json:"account_name"`
	Note        string `json:"note"`
}

type saveLinksRequest struct {
	AccountIDs []int64 `json:"account_ids"`
}

// SaveLinks PUT /providers/:id/links —— 全量替换该供应商的关联集合。
//
// 全量替换而非增量：弹窗里用户看到的是完整勾选态，提交的就是完整意图。
// 勾选别的供应商已关联的账号会把它抢过来（UNIQUE 只允许一个归属），前端已明示。
func (h *ProviderHandler) SaveLinks(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	if _, err := h.svc.Repo().GetByID(c.Request.Context(), id); err != nil {
		response.NotFound(c, "供应商不存在")
		return
	}
	var req saveLinksRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求体格式错误")
		return
	}

	// 账号名冗余落库：PG 不可用时留空，不阻断关联本身
	nameByID := map[int64]string{}
	if accs, err := h.pg.ListActiveAccounts(c.Request.Context()); err == nil {
		for _, a := range accs {
			nameByID[a.ID] = a.Name
		}
	}
	items := make([]repository.ProviderAccount, 0, len(req.AccountIDs))
	for _, aid := range req.AccountIDs {
		items = append(items, repository.ProviderAccount{
			ProviderID: id, AccountID: aid, AccountName: nameByID[aid],
		})
	}
	if err := h.svc.LinkRepo().Replace(c.Request.Context(), id, items); err != nil {
		response.InternalError(c, "保存失败: "+err.Error())
		return
	}
	response.Success(c, gin.H{"linked": len(items)})
}

// Accounts GET /providers/:id/accounts
func (h *ProviderHandler) Accounts(c *gin.Context) {
	if !h.pg.Available() {
		response.ServiceUnavailable(c, "线上数据库暂不可用")
		return
	}
	id, ok := parseID(c)
	if !ok {
		return
	}
	p, err := h.svc.Repo().GetByID(c.Request.Context(), id)
	if err != nil {
		response.NotFound(c, "供应商不存在")
		return
	}
	accounts, err := h.svc.AccountsOf(c.Request.Context(), p.ID)
	if err != nil {
		response.InternalError(c, "查询失败: "+err.Error())
		return
	}
	response.Success(c, gin.H{"provider": p.Name, "items": accounts, "total": len(accounts)})
}

type testConnectionRequest struct {
	Platform    string `json:"platform"`  // sub2api（默认）| new-api
	AuthMode    string `json:"auth_mode"` // password（默认）| token | user_key
	BaseURL     string `json:"base_url" binding:"required"`
	Email       string `json:"email"`
	Password    string `json:"password"`
	AccessToken string `json:"access_token"`
	UserID      string `json:"user_id"`
}

// TestConnection POST /providers/test-connection
// 即时验证供应商站点凭据（用于「测试连接」按钮，不落库）。
func (h *ProviderHandler) TestConnection(c *gin.Context) {
	var req testConnectionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "base_url 必填")
		return
	}
	baseURL := strings.TrimRight(req.BaseURL, "/")
	ctx := c.Request.Context()

	if req.Platform == "new-api" {
		client := h.balanceSvc.NewAPIClient()
		var auth service.NewAPIAuth
		switch req.AuthMode {
		case "user_key":
			if req.AccessToken == "" || req.UserID == "" {
				response.BadRequest(c, "user_key 模式需提供 access_token / user_id")
				return
			}
			auth = service.NewAPIAuth{AccessToken: req.AccessToken, UserID: req.UserID}
		default:
			if req.Email == "" || req.Password == "" {
				response.BadRequest(c, "password 模式需提供 email / password")
				return
			}
			lr, err := client.Login(ctx, baseURL, req.Email, req.Password)
			if err != nil {
				response.Success(c, gin.H{"ok": false, "error": err.Error()})
				return
			}
			auth = lr.Auth()
		}
		self, err := client.GetSelf(ctx, baseURL, auth)
		if err != nil {
			response.Success(c, gin.H{"ok": false, "error": err.Error()})
			return
		}
		qpu := client.GetQuotaPerUnit(ctx, baseURL)
		balance := self.Quota / qpu
		response.Success(c, gin.H{"ok": true, "balance": balance, "username": self.Username})
		return
	}

	// sub2api
	var token string
	var balance *float64
	switch req.AuthMode {
	case "token":
		if req.AccessToken == "" {
			response.BadRequest(c, "token 模式需提供 access_token")
			return
		}
		token = req.AccessToken
	default:
		if req.Email == "" || req.Password == "" {
			response.BadRequest(c, "password 模式需提供 email / password")
			return
		}
		lr, err := h.balanceSvc.Client().Login(ctx, baseURL, req.Email, req.Password)
		if err != nil {
			response.Success(c, gin.H{"ok": false, "error": err.Error()})
			return
		}
		token = lr.AccessToken
		balance = lr.Balance
	}

	stats, err := h.balanceSvc.Client().GetDashboardStats(ctx, baseURL, token)
	if err != nil && balance == nil {
		// token 模式下仪表盘是唯一验证手段，失败即凭据无效
		response.Success(c, gin.H{"ok": false, "error": err.Error()})
		return
	}
	if balance == nil {
		if b, bErr := h.balanceSvc.Client().GetBalance(ctx, baseURL, token); bErr == nil {
			balance = b
		}
	}
	out := gin.H{"ok": true, "balance": balance}
	if stats != nil {
		out["today_actual_cost"] = stats.TodayActualCost
		out["today_requests"] = stats.TodayRequests
	}
	response.Success(c, out)
}

// RefreshBalance POST /providers/:id/balance/refresh
// 手动触发统一同步（余额+成本）：绕过登录冷却，完成后重置该供应商的自动计时。
func (h *ProviderHandler) RefreshBalance(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	outcome, err := h.syncSvc.SyncOne(c.Request.Context(), id, true, false)
	if err != nil {
		response.InternalError(c, "采集失败: "+err.Error())
		return
	}
	h.syncSched.OnManualSync(id)
	response.Success(c, snapshotDTO(outcome.Snapshot, h.cfg.Location))
}

// refreshAllTimeout 全量刷新的总时长上限，与前端 refreshAll 的请求超时对齐。
// 单站点自身还有 15min 上限（syncTaskTimeout），这里兜住「站点多 × 每个都慢」的累积。
const refreshAllTimeout = 15 * time.Minute

// RefreshAllBalance POST /providers/balance/refresh-all
// 一键刷新全部上游站点：并发受限，登录冷却中的站点跳过（不绕过冷却，避免同时撞上游）。
func (h *ProviderHandler) RefreshAllBalance(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), refreshAllTimeout)
	defer cancel()

	result, err := h.syncSvc.SyncAll(ctx)
	if err != nil {
		response.InternalError(c, "刷新失败: "+err.Error())
		return
	}
	h.syncSched.OnManualSyncAll()
	response.Success(c, result)
}

type manualBalanceRequest struct {
	Balance float64 `json:"balance" binding:"required"`
}

// ManualBalance PUT /providers/:id/balance
func (h *ProviderHandler) ManualBalance(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var req manualBalanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "balance 必填")
		return
	}
	snap, err := h.balanceSvc.RecordManual(c.Request.Context(), id, req.Balance)
	if err != nil {
		response.InternalError(c, "记录失败: "+err.Error())
		return
	}
	response.Success(c, snapshotDTO(snap, h.cfg.Location))
}

// BalanceHistory GET /providers/:id/balance/history?days=
func (h *ProviderHandler) BalanceHistory(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	days := 7
	if d := c.Query("days"); d != "" {
		if v, err := strconv.Atoi(d); err == nil && v > 0 && v <= 90 {
			days = v
		}
	}
	snaps, err := h.balanceSvc.History(c.Request.Context(), id, days)
	if err != nil {
		response.InternalError(c, "查询失败: "+err.Error())
		return
	}
	out := make([]gin.H, 0, len(snaps))
	for _, s := range snaps {
		out = append(out, snapshotDTO(s, h.cfg.Location))
	}
	response.Success(c, gin.H{"items": out, "total": len(out)})
}

// KeyCosts GET /providers/:id/costs?start=&end=
// 返回该供应商 per-key 的上游实扣明细（只读本地库）。
func (h *ProviderHandler) KeyCosts(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	p, err := h.svc.Repo().GetByID(c.Request.Context(), id)
	if err != nil {
		response.NotFound(c, "供应商不存在")
		return
	}
	start, end := resolveDayRange(c, h.cfg)
	startDate, endDate := dateBounds(h.cfg, start, end)

	items, err := h.costSvc.KeyCosts(c.Request.Context(), id, startDate, endDate)
	if err != nil {
		response.InternalError(c, "查询失败: "+err.Error())
		return
	}
	state, err := h.costSvc.SyncState(c.Request.Context(), id)
	if err != nil {
		response.InternalError(c, "查询同步状态失败: "+err.Error())
		return
	}

	var actual, official float64
	for _, k := range items {
		actual += k.ActualCost
		official += k.OfficialCost
	}
	response.Success(c, gin.H{
		"provider":      p.Name,
		"start":         startDate,
		"end":           endDate,
		"items":         items,
		"total":         len(items),
		"actual_cost":   actual,
		"official_cost": official,
		"sync_state":    state,
	})
}

// SyncCost POST /providers/:id/costs/sync?backfill=true
// 手动触发一次上游成本同步（backfill=true 时同时回补历史，耗时较长）。
func (h *ProviderHandler) SyncCost(c *gin.Context) {
	if !h.pg.Available() {
		response.ServiceUnavailable(c, "线上数据库暂不可用")
		return
	}
	id, ok := parseID(c)
	if !ok {
		return
	}
	backfill := c.Query("backfill") == "true"
	if err := h.costSvc.SyncOneByID(c.Request.Context(), id, backfill); err != nil {
		response.InternalError(c, "同步失败: "+err.Error())
		return
	}
	h.syncSched.OnManualSync(id)
	state, err := h.costSvc.SyncState(c.Request.Context(), id)
	if err != nil {
		response.InternalError(c, "查询同步状态失败: "+err.Error())
		return
	}
	response.Success(c, gin.H{"synced": true, "backfill": backfill, "sync_state": state})
}

func snapshotDTO(s *repository.BalanceSnapshot, loc *time.Location) gin.H {
	if s == nil {
		return gin.H{}
	}
	d := gin.H{
		"id":         s.ID,
		"balance":    s.Balance,
		"currency":   s.Currency,
		"source":     s.Source,
		"error":      s.Error,
		"created_at": formatProviderTime(s.CreatedAt, loc),
	}
	if s.Metrics != nil {
		d["metrics"] = s.Metrics // 前端自行 JSON.parse
	}
	return d
}

// timeNow 便于测试替换的当前时间。
var timeNow = time.Now

func parseID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "无效的 id")
		return 0, false
	}
	return id, true
}
