package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"sub2api-account-monitor/internal/repository"
)

// tokenLeeway token 提前失效余量：避免临界时刻用一个下一秒就过期的 token。
// 也是 token 模式的提前刷新窗口（transit-hub 用 60s skew，与此一致）。
const tokenLeeway = time.Minute

// defaultTokenTTL 上游未返回 expires_in 时的缺省有效期。
const defaultTokenTTL = 30 * time.Minute

// loginCooldown 按连续被拒次数返回冷却时长（阶梯退避，封顶 6h）。
// 供应商站点前置 WAF，登录被拒后反复撞接口易被拉黑 IP，故冷却比任务退避激进。
func loginCooldown(failures int) time.Duration {
	switch {
	case failures <= 1:
		return 30 * time.Minute
	case failures == 2:
		return 2 * time.Hour
	default:
		return 6 * time.Hour
	}
}

// ErrLoginCooldown 登录冷却中，未打上游。
var ErrLoginCooldown = errors.New("login cooldown")

// providerSession 一次可用的上游会话（平台中性）。
type providerSession struct {
	// sub2api：Bearer token
	AccessToken string
	// new-api：Cookie / token + user 头
	NewAPI NewAPIAuth
	// Balance 建立会话时顺带取得的余额（未发生登录时为 nil）
	Balance *float64
}

// providerTokenManager 供应商站点会话的获取与缓存（平台/认证模式分发）。
// 余额采集与成本同步都需要登录态，故收敛到一处，避免多份重登逻辑各自漂移。
type providerTokenManager struct {
	repo         *repository.ProviderRepo
	client       *Sub2apiClient
	newapiClient *NewAPIClient

	// refreshing 按 provider 隔离的续期锁，见 refreshNewAPISession。
	// 站点数量有限且长期存活，锁不回收。
	refreshMu  sync.Mutex
	refreshing map[int64]*sync.Mutex
}

// newTokenManager 创建 token 管理器。
func newTokenManager(repo *repository.ProviderRepo, client *Sub2apiClient, newapiClient *NewAPIClient) *providerTokenManager {
	return &providerTokenManager{
		repo:         repo,
		client:       client,
		newapiClient: newapiClient,
		refreshing:   make(map[int64]*sync.Mutex),
	}
}

// ensure 返回可用会话；缺失或即将过期时建立并写回缓存。
func (m *providerTokenManager) ensure(ctx context.Context, p *repository.Provider) (*providerSession, error) {
	switch p.Platform {
	case "new-api":
		return m.ensureNewAPI(ctx, p)
	default:
		return m.ensureSub2api(ctx, p)
	}
}

// refresh 丢弃当前会话并强制重建（收到 401 后调用）。
func (m *providerTokenManager) refresh(ctx context.Context, p *repository.Provider) (*providerSession, error) {
	if p.Platform == "new-api" {
		if p.AuthMode == "user_key" {
			// 静态令牌无从刷新：401 即令牌失效
			return nil, fmt.Errorf("%w: 系统访问令牌已失效，请更换", ErrLoginRejected)
		}
		// 新版：先试续期（内部失败会自动降级重登），避免 15 分钟撞一次登录接口
		if p.RefreshToken != "" {
			return m.refreshNewAPISession(ctx, p)
		}
		p.SessionCookie = ""
		_ = m.repo.UpdateSession(ctx, p.ID, "", p.UpstreamUserID, p.QuotaPerUnit)
		return m.ensureNewAPI(ctx, p)
	}

	if p.AuthMode == "token" {
		// token 模式：强制走 refresh_token 换新
		p.TokenExpiresAt = nil
		return m.ensureSub2api(ctx, p)
	}
	_ = m.repo.ClearToken(ctx, p.ID)
	p.AccessToken = ""
	p.TokenExpiresAt = nil
	return m.ensureSub2api(ctx, p)
}

// ---- sub2api ----

// ensureSub2api password 模式登录 / token 模式按需刷新。
func (m *providerTokenManager) ensureSub2api(ctx context.Context, p *repository.Provider) (*providerSession, error) {
	// 缓存有效直接用
	if p.AccessToken != "" && p.TokenExpiresAt != nil && time.Now().Before(p.TokenExpiresAt.Add(-tokenLeeway)) {
		return &providerSession{AccessToken: p.AccessToken}, nil
	}

	if p.AuthMode == "token" {
		return m.refreshSub2apiToken(ctx, p)
	}
	return m.loginSub2api(ctx, p)
}

// refreshSub2apiToken token 模式：用 refresh_token 换新（无 refresh_token 时直接用现有 access_token）。
func (m *providerTokenManager) refreshSub2apiToken(ctx context.Context, p *repository.Provider) (*providerSession, error) {
	if p.RefreshToken == "" {
		if p.AccessToken == "" {
			return nil, errors.New("token 模式下缺少 access_token / refresh_token")
		}
		// 只有 access_token：直接使用，过期由 401 → refresh 上抛
		return &providerSession{AccessToken: p.AccessToken}, nil
	}
	if err := m.checkCooldown(p); err != nil {
		return nil, err
	}

	rr, err := m.client.RefreshToken(ctx, p.BaseURL, p.RefreshToken)
	if err != nil {
		m.recordRejected(ctx, p, err)
		return nil, err
	}
	m.clearRejected(ctx, p)

	ttl := defaultTokenTTL
	if rr.ExpiresIn > 0 {
		ttl = time.Duration(rr.ExpiresIn) * time.Second
	}
	expiresAt := time.Now().Add(ttl)
	_ = m.repo.UpdateTokenPair(ctx, p.ID, rr.AccessToken, rr.RefreshToken, &expiresAt)
	p.AccessToken = rr.AccessToken
	p.RefreshToken = rr.RefreshToken
	p.TokenExpiresAt = &expiresAt
	return &providerSession{AccessToken: rr.AccessToken}, nil
}

// loginSub2api password 模式：邮箱密码登录。
func (m *providerTokenManager) loginSub2api(ctx context.Context, p *repository.Provider) (*providerSession, error) {
	if p.BaseURL == "" || p.LoginEmail == "" || p.LoginPassword == "" {
		return nil, errors.New("供应商缺少 base_url / login_email / login_password")
	}
	if err := m.checkCooldown(p); err != nil {
		return nil, err
	}

	lr, err := m.client.Login(ctx, p.BaseURL, p.LoginEmail, p.LoginPassword)
	if err != nil {
		m.recordRejected(ctx, p, err)
		return nil, err
	}
	m.clearRejected(ctx, p)

	ttl := defaultTokenTTL
	if lr.ExpiresIn > 0 {
		ttl = time.Duration(lr.ExpiresIn) * time.Second
	}
	expiresAt := time.Now().Add(ttl)
	_ = m.repo.UpdateToken(ctx, p.ID, lr.AccessToken, &expiresAt)
	p.AccessToken = lr.AccessToken
	p.TokenExpiresAt = &expiresAt
	return &providerSession{AccessToken: lr.AccessToken, Balance: lr.Balance}, nil
}

// ---- new-api ----

// ensureNewAPI 三种会话形态按优先级取用：
//
//	user_key  → 静态 PAT，无会话概念；
//	新版      → JWT（15min TTL）未过期直接用，过期则凭 refresh token 续期；
//	旧版      → 会话 Cookie 无过期字段，有就先用，401 时由 refresh 重登。
//
// refresh_token 非空即「已确认是新版」——它只可能由新版登录成功写入，
// 故旧版站点永远不会白打一次 404 续期请求。
func (m *providerTokenManager) ensureNewAPI(ctx context.Context, p *repository.Provider) (*providerSession, error) {
	if p.AuthMode == "user_key" {
		if p.AccessToken == "" || p.UpstreamUserID == "" {
			return nil, errors.New("user_key 模式下缺少系统访问令牌 / 用户 ID")
		}
		return &providerSession{NewAPI: NewAPIAuth{AccessToken: p.AccessToken, UserID: p.UpstreamUserID}}, nil
	}

	if newapiJWTValid(p) {
		return &providerSession{NewAPI: newapiJWTAuth(p)}, nil
	}
	if p.RefreshToken != "" {
		return m.refreshNewAPISession(ctx, p)
	}
	if p.SessionCookie != "" && p.UpstreamUserID != "" {
		return &providerSession{NewAPI: NewAPIAuth{Cookie: p.SessionCookie, UserID: p.UpstreamUserID}}, nil
	}
	return m.loginNewAPI(ctx, p)
}

// newapiJWTValid 新版会话令牌是否仍在有效期内（含 tokenLeeway 提前量）。
func newapiJWTValid(p *repository.Provider) bool {
	return p.AccessToken != "" && p.TokenExpiresAt != nil &&
		time.Now().Before(p.TokenExpiresAt.Add(-tokenLeeway))
}

// newapiJWTAuth 用新版会话令牌构造认证要素。
func newapiJWTAuth(p *repository.Provider) NewAPIAuth {
	return NewAPIAuth{JWT: p.AccessToken, UserID: p.UpstreamUserID}
}

// refreshNewAPISession 用 refresh token 续期新版会话；失败则清凭据降级密码重登。
//
// 必须串行：上游的 refresh token 每次调用都会轮换，旧值在重放窗口内被再次使用
// 会触发重放检测并吊销整个会话——并发刷新等于自己把自己踢下线。故同一站点加锁，
// 并在拿到锁后重读库做双重检查（等锁期间别的 goroutine 可能已经刷过；入参 p
// 是各调用方的独立副本，看不到彼此的更新）。
//
// 双重检查要求「库里的令牌与手上这个不同」，而不只是「库里的令牌未过期」：
// 401 触发的续期里，手上的令牌尚未到期却已被上游拒绝，只看有效期会把同一个
// 坏令牌原样返回。加这一条后同一份逻辑对 ensure（令牌过期）与 refresh
// （令牌被拒）两条路径都成立。
func (m *providerTokenManager) refreshNewAPISession(ctx context.Context, p *repository.Provider) (*providerSession, error) {
	mu := m.perProviderMu(p.ID)
	mu.Lock()
	defer mu.Unlock()

	if fresh, err := m.repo.GetByID(ctx, p.ID); err == nil &&
		newapiJWTValid(fresh) && fresh.AccessToken != p.AccessToken {
		p.AccessToken, p.RefreshToken, p.TokenExpiresAt = fresh.AccessToken, fresh.RefreshToken, fresh.TokenExpiresAt
		return &providerSession{NewAPI: newapiJWTAuth(p)}, nil
	}

	rr, err := m.newapiClient.RefreshSession(ctx, p.BaseURL, p.RefreshToken)
	if err != nil {
		// 续期失败（凭据过期/被吊销/上游降级回旧版）→ 清新版凭据，降级密码重登。
		// 刻意不调 recordRejected：续期失败不是登录被拒，不该触发 6h 冷却阶梯。
		_ = m.repo.UpdateTokenPair(ctx, p.ID, "", "", nil)
		p.AccessToken, p.RefreshToken, p.TokenExpiresAt = "", "", nil
		return m.loginNewAPI(ctx, p) // 内部自带 checkCooldown / recordRejected
	}

	_ = m.repo.UpdateTokenPair(ctx, p.ID, rr.AccessToken, rr.RefreshToken, &rr.AccessExpiresAt)
	p.AccessToken, p.RefreshToken, p.TokenExpiresAt = rr.AccessToken, rr.RefreshToken, &rr.AccessExpiresAt
	return &providerSession{NewAPI: newapiJWTAuth(p)}, nil
}

// perProviderMu 取（或懒建）某站点的续期锁。
func (m *providerTokenManager) perProviderMu(id int64) *sync.Mutex {
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()
	mu, ok := m.refreshing[id]
	if !ok {
		mu = &sync.Mutex{}
		m.refreshing[id] = mu
	}
	return mu
}

// loginNewAPI new-api 密码登录：新版存 JWT+refresh，旧版存 Cookie，顺带拉 quota_per_unit。
func (m *providerTokenManager) loginNewAPI(ctx context.Context, p *repository.Provider) (*providerSession, error) {
	if p.BaseURL == "" || p.LoginEmail == "" || p.LoginPassword == "" {
		return nil, errors.New("供应商缺少 base_url / 登录账号 / 登录密码")
	}
	if err := m.checkCooldown(p); err != nil {
		return nil, err
	}

	lr, err := m.newapiClient.Login(ctx, p.BaseURL, p.LoginEmail, p.LoginPassword)
	if err != nil {
		m.recordRejected(ctx, p, err)
		return nil, err
	}
	m.clearRejected(ctx, p)

	qpu := m.newapiClient.GetQuotaPerUnit(ctx, p.BaseURL)
	_ = m.repo.UpdateSession(ctx, p.ID, lr.Cookie, lr.UserID, qpu)
	p.SessionCookie = lr.Cookie
	p.UpstreamUserID = lr.UserID
	p.QuotaPerUnit = qpu

	// 新版：会话凭据落到 token 三列，后续走 refresh 续期而非反复密码登录。
	// 上游未给到期时刻时留空，令牌立即被判过期从而下次即续期——保守但不会错用失效令牌。
	if lr.AccessToken != "" {
		var expiresAt *time.Time
		if !lr.AccessExpiresAt.IsZero() {
			expiresAt = &lr.AccessExpiresAt
		}
		_ = m.repo.UpdateTokenPair(ctx, p.ID, lr.AccessToken, lr.RefreshToken, expiresAt)
		p.AccessToken = lr.AccessToken
		p.RefreshToken = lr.RefreshToken
		p.TokenExpiresAt = expiresAt
	}

	balance := lr.Quota / qpu
	return &providerSession{NewAPI: lr.Auth(), Balance: &balance}, nil
}

// ---- 冷却共用 ----

// checkCooldown 冷却期内拒绝打上游。
func (m *providerTokenManager) checkCooldown(p *repository.Provider) error {
	if p.LoginCooldownUntil != nil && time.Now().Before(*p.LoginCooldownUntil) {
		return fmt.Errorf("%w: 登录冷却至 %s（连续 %d 次被拒）",
			ErrLoginCooldown, p.LoginCooldownUntil.Local().Format("15:04"), p.LoginFailures)
	}
	return nil
}

// recordRejected 登录被拒时递增计数并按阶梯写入冷却。
func (m *providerTokenManager) recordRejected(ctx context.Context, p *repository.Provider, err error) {
	if !IsLoginRejected(err) {
		return
	}
	n, dbErr := m.repo.RecordLoginRejected(ctx, p.ID, time.Now().Add(loginCooldown(1)))
	if dbErr != nil {
		return
	}
	if n > 1 {
		until := time.Now().Add(loginCooldown(n))
		_ = m.repo.SetLoginCooldown(ctx, p.ID, until)
		p.LoginCooldownUntil = &until
	}
	p.LoginFailures = n
}

// clearRejected 登录成功后清零冷却计数。
func (m *providerTokenManager) clearRejected(ctx context.Context, p *repository.Provider) {
	if p.LoginFailures > 0 {
		_ = m.repo.ClearLoginCooldown(ctx, p.ID)
		p.LoginFailures = 0
		p.LoginCooldownUntil = nil
	}
}
