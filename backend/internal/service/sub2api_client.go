package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// Sub2apiClient 供应商 sub2api 站点客户端（模拟登录取数）。
type Sub2apiClient struct {
	httpClient *http.Client
	timeout    time.Duration
}

// NewSub2apiClient 创建客户端。
func NewSub2apiClient(timeout time.Duration) *Sub2apiClient {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	transport := &http.Transport{
		TLSHandshakeTimeout:   20 * time.Second,
		ResponseHeaderTimeout: timeout,
		IdleConnTimeout:       30 * time.Second,
		MaxIdleConns:          20,
	}
	return &Sub2apiClient{
		httpClient: &http.Client{Timeout: timeout + 10*time.Second, Transport: transport},
		timeout:    timeout,
	}
}

// apiEnvelope sub2api 统一响应封装。
type apiEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// ProviderLoginResult 供应商站点登录结果。
type ProviderLoginResult struct {
	AccessToken string
	ExpiresIn   int64
	Balance     *float64
}

// loginResponse sub2api 登录响应 data 段。
type loginResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	TokenType    string `json:"token_type"`
	User         struct {
		Balance *float64 `json:"balance"`
	} `json:"user"`
}

// Login 模拟登录供应商 sub2api 站点。
// 站点开 Turnstile/2FA 时会返回 4xx，错误原样上抛。
func (c *Sub2apiClient) Login(ctx context.Context, baseURL, email, password string) (*ProviderLoginResult, error) {
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	url := strings.TrimRight(baseURL, "/") + "/api/v1/auth/login"

	resp, err := c.doWithRetry(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		setCommonHeaders(req)
		return req, nil
	})
	if err != nil {
		return nil, fmt.Errorf("登录请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("登录失败 HTTP %d: %s", resp.StatusCode, briefBody(raw))
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			// 4xx 视为被拒（密码错误 / Turnstile / 风控），调用方据此进入登录冷却
			return nil, fmt.Errorf("%w: %w", ErrLoginRejected, err)
		}
		return nil, err
	}

	var env apiEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("解析登录响应失败: %w", err)
	}
	if env.Code != 0 {
		// HTTP 200 + 业务 code 非 0：同样是被拒（密码错误等），进入冷却
		return nil, fmt.Errorf("%w: 登录失败: %s", ErrLoginRejected, env.Message)
	}

	var lr loginResponse
	if err := json.Unmarshal(env.Data, &lr); err != nil {
		return nil, fmt.Errorf("解析登录数据失败: %w", err)
	}
	if lr.AccessToken == "" {
		return nil, fmt.Errorf("登录响应缺少 access_token")
	}
	return &ProviderLoginResult{AccessToken: lr.AccessToken, ExpiresIn: lr.ExpiresIn, Balance: lr.User.Balance}, nil
}

// RefreshTokenResult 刷新令牌结果。
type RefreshTokenResult struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int64
}

// RefreshToken 用 refresh_token 换新 access_token（POST /api/v1/auth/refresh）。
// sub2api 的 refresh token 本身无过期；4xx 视为 refresh token 失效（ErrLoginRejected）。
func (c *Sub2apiClient) RefreshToken(ctx context.Context, baseURL, refreshToken string) (*RefreshTokenResult, error) {
	body, _ := json.Marshal(map[string]string{"refresh_token": refreshToken})
	url := strings.TrimRight(baseURL, "/") + "/api/v1/auth/refresh"

	resp, err := c.doWithRetry(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		setCommonHeaders(req)
		return req, nil
	})
	if err != nil {
		return nil, fmt.Errorf("刷新令牌请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("刷新令牌失败 HTTP %d: %s", resp.StatusCode, briefBody(raw))
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return nil, fmt.Errorf("%w: %w", ErrLoginRejected, err)
		}
		return nil, err
	}
	var env apiEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("解析刷新响应失败: %w", err)
	}
	if env.Code != 0 {
		return nil, fmt.Errorf("%w: 刷新令牌失败: %s", ErrLoginRejected, env.Message)
	}
	var lr loginResponse
	if err := json.Unmarshal(env.Data, &lr); err != nil {
		return nil, fmt.Errorf("解析刷新数据失败: %w", err)
	}
	if lr.AccessToken == "" {
		return nil, fmt.Errorf("刷新响应缺少 access_token")
	}
	out := &RefreshTokenResult{AccessToken: lr.AccessToken, RefreshToken: lr.RefreshToken, ExpiresIn: lr.ExpiresIn}
	if out.RefreshToken == "" {
		out.RefreshToken = refreshToken // 上游未轮换时沿用旧 refresh token
	}
	return out, nil
}

// DashboardStats 供应商站点仪表盘数据（匹配 UserDashboardStats，字段缺省为 nil）。
type DashboardStats struct {
	TotalAPIKeys    *int64   `json:"total_api_keys"`
	ActiveAPIKeys   *int64   `json:"active_api_keys"`
	TotalRequests   *int64   `json:"total_requests"`
	TotalTokens     *int64   `json:"total_tokens"`
	TotalCost       *float64 `json:"total_cost"`
	TotalActualCost *float64 `json:"total_actual_cost"`

	TodayRequests   *int64   `json:"today_requests"`
	TodayTokens     *int64   `json:"today_tokens"`
	TodayCost       *float64 `json:"today_cost"`
	TodayActualCost *float64 `json:"today_actual_cost"`

	AverageDurationMs *float64 `json:"average_duration_ms"`
	RPM               *int64   `json:"rpm"`
	TPM               *int64   `json:"tpm"`
}

// GetDashboardStats 拉取供应商站点仪表盘数据。
func (c *Sub2apiClient) GetDashboardStats(ctx context.Context, baseURL, accessToken string) (*DashboardStats, error) {
	url := strings.TrimRight(baseURL, "/") + "/api/v1/usage/dashboard/stats"
	var stats DashboardStats
	if err := c.getJSON(ctx, url, accessToken, "仪表盘", &stats); err != nil {
		return nil, err
	}
	return &stats, nil
}

// GetBalance 通过 /auth/me 取余额（备用）。
func (c *Sub2apiClient) GetBalance(ctx context.Context, baseURL, accessToken string) (*float64, error) {
	url := strings.TrimRight(baseURL, "/") + "/api/v1/auth/me"
	var user struct {
		Balance *float64 `json:"balance"`
	}
	if err := c.getJSON(ctx, url, accessToken, "余额", &user); err != nil {
		return nil, err
	}
	return user.Balance, nil
}

var errUnauthorized = errors.New("unauthorized")

// ErrLoginRejected 登录被上游明确拒绝（4xx / 业务错误码）。
// 与网络错误区分：被拒说明凭据或风控问题，重试无意义且可能触发 WAF 拉黑，须进入冷却。
var ErrLoginRejected = errors.New("login rejected")

// IsLoginRejected 判断是否为登录被拒。
func IsLoginRejected(err error) bool { return errors.Is(err, ErrLoginRejected) }

// ProviderAPIKey 供应商站点的一个 API key（对应本站一个账号）。
// Key 为明文，仅用于与本站 accounts.credentials->>'api_key' 做指纹匹配，绝不出后端。
type ProviderAPIKey struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Key    string `json:"key"`
	Status string `json:"status"`
	Group  *struct {
		Name           string  `json:"name"`
		RateMultiplier float64 `json:"rate_multiplier"`
	} `json:"group"`
	CurrentConcurrency *int64     `json:"current_concurrency"`
	ExpiresAt          *time.Time `json:"expires_at"`
}

// GetAPIKeys 拉取供应商站点的 API key 列表（GET /api/v1/keys，分页取全量）。
func (c *Sub2apiClient) GetAPIKeys(ctx context.Context, baseURL, accessToken string) ([]ProviderAPIKey, error) {
	const pageSize = 100
	var out []ProviderAPIKey
	for page := 1; page <= 20; page++ { // 上限 2000 个 key，防御性分页
		url := fmt.Sprintf("%s/api/v1/keys?page=%d&page_size=%d", strings.TrimRight(baseURL, "/"), page, pageSize)
		var data struct {
			Items []ProviderAPIKey `json:"items"`
			Total int64            `json:"total"`
		}
		if err := c.getJSON(ctx, url, accessToken, "key 列表", &data); err != nil {
			return nil, err
		}
		out = append(out, data.Items...)
		if len(data.Items) < pageSize || int64(len(out)) >= data.Total {
			break
		}
	}
	return out, nil
}

// APIKeyUsage 单个 key 的用量（上游口径）。
// ActualCost 为倍率折后实扣，即我们真正付给供应商的金额。
type APIKeyUsage struct {
	APIKeyID        int64   `json:"api_key_id"`
	TodayActualCost float64 `json:"today_actual_cost"`
	TotalActualCost float64 `json:"total_actual_cost"`
	Requests        int64   `json:"requests"`
}

// GetAPIKeysUsage 批量拉取 per-key 今日/近 30 天实扣（POST /api/v1/usage/dashboard/api-keys-usage）。
// 上游单次限 100 个 id，超出自动分批。
func (c *Sub2apiClient) GetAPIKeysUsage(ctx context.Context, baseURL, accessToken string, keyIDs []int64) (map[int64]APIKeyUsage, error) {
	out := make(map[int64]APIKeyUsage, len(keyIDs))
	if len(keyIDs) == 0 {
		return out, nil
	}
	const batch = 100
	url := strings.TrimRight(baseURL, "/") + "/api/v1/usage/dashboard/api-keys-usage"
	for start := 0; start < len(keyIDs); start += batch {
		end := start + batch
		if end > len(keyIDs) {
			end = len(keyIDs)
		}
		body, _ := json.Marshal(map[string]any{"api_key_ids": keyIDs[start:end]})
		var data struct {
			Stats map[string]APIKeyUsage `json:"stats"`
		}
		if err := c.postJSON(ctx, url, accessToken, body, "key 用量", &data); err != nil {
			return nil, err
		}
		for _, s := range data.Stats {
			out[s.APIKeyID] = s
		}
	}
	return out, nil
}

// APIKeyDailyUsage 单个 key 某一天的用量。
type APIKeyDailyUsage struct {
	Date       string  `json:"date"` // YYYY-MM-DD（上游按其用户时区）
	Requests   int64   `json:"requests"`
	Cost       float64 `json:"cost"`        // 原始官价
	ActualCost float64 `json:"actual_cost"` // 倍率折后实扣
}

// GetAPIKeyDailyUsage 拉取单个 key 的逐日用量（GET /api/v1/user/api-keys/:id/usage/daily）。
// days 范围 1-90，用于回补历史成本。
func (c *Sub2apiClient) GetAPIKeyDailyUsage(ctx context.Context, baseURL, accessToken string, keyID int64, days int) ([]APIKeyDailyUsage, error) {
	if days < 1 {
		days = 1
	}
	if days > 90 {
		days = 90
	}
	url := fmt.Sprintf("%s/api/v1/user/api-keys/%d/usage/daily?days=%d", strings.TrimRight(baseURL, "/"), keyID, days)
	var data struct {
		Items []APIKeyDailyUsage `json:"items"`
	}
	if err := c.getJSON(ctx, url, accessToken, "key 逐日用量", &data); err != nil {
		return nil, err
	}
	return data.Items, nil
}

// Sub2apiGroupRate 上游分组倍率。
// Sub2apiGroupRate 上游分组倍率。
// ID 与 Platform 是建号必需的：建 key 要传数字 group_id，建本站账号要按 platform 分支组装 payload。
type Sub2apiGroupRate struct {
	ID       int64
	Name     string
	Rate     float64
	Platform string // anthropic | openai | gemini | antigravity | ...
}

// GetGroupRates 拉取上游站点可用分组及倍率。
// 主：GET /api/v1/groups/available 的 rate_multiplier；
// 辅：GET /api/v1/groups/rates 的专属倍率覆盖合并（拉取失败只回退默认倍率，不阻断）。
func (c *Sub2apiClient) GetGroupRates(ctx context.Context, baseURL, accessToken string) ([]Sub2apiGroupRate, error) {
	base := strings.TrimRight(baseURL, "/")

	var groups []struct {
		ID             int64   `json:"id"`
		Name           string  `json:"name"`
		Platform       string  `json:"platform"`
		RateMultiplier float64 `json:"rate_multiplier"`
	}
	if err := c.getJSON(ctx, base+"/api/v1/groups/available", accessToken, "分组", &groups); err != nil {
		return nil, err
	}

	// 专属倍率覆盖（可选接口，失败忽略）
	overrides := map[string]float64{}
	var rates []struct {
		GroupID   int64   `json:"group_id"`
		GroupName string  `json:"group_name"`
		Rate      float64 `json:"rate_multiplier"`
	}
	if err := c.getJSON(ctx, base+"/api/v1/groups/rates", accessToken, "专属倍率", &rates); err == nil {
		for _, r := range rates {
			if r.GroupName != "" && r.Rate > 0 {
				overrides[r.GroupName] = r.Rate
			}
		}
	}

	out := make([]Sub2apiGroupRate, 0, len(groups))
	for _, g := range groups {
		rate := g.RateMultiplier
		if v, ok := overrides[g.Name]; ok {
			rate = v
		}
		out = append(out, Sub2apiGroupRate{ID: g.ID, Name: g.Name, Rate: rate, Platform: g.Platform})
	}
	return out, nil
}

// getJSON 发起 GET 并把 envelope.data 解到 dst。
func (c *Sub2apiClient) getJSON(ctx context.Context, url, accessToken, what string, dst any) error {
	return c.requestJSON(ctx, http.MethodGet, url, accessToken, nil, what, dst)
}

// postJSON 发起 POST（JSON body）并把 envelope.data 解到 dst。
func (c *Sub2apiClient) postJSON(ctx context.Context, url, accessToken string, body []byte, what string, dst any) error {
	return c.requestJSON(ctx, http.MethodPost, url, accessToken, body, what, dst)
}

// requestJSON 统一处理带认证的 JSON 请求：重试、401 识别、envelope 解封。
func (c *Sub2apiClient) requestJSON(ctx context.Context, method, url, accessToken string, body []byte, what string, dst any) error {
	resp, err := c.doWithRetry(ctx, func() (*http.Request, error) {
		var rdr io.Reader
		if body != nil {
			rdr = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, url, rdr)
		if err != nil {
			return nil, err
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)
		setCommonHeaders(req)
		return req, nil
	})
	if err != nil {
		return fmt.Errorf("%s请求失败: %w", what, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode == http.StatusUnauthorized {
		return errUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s请求失败 HTTP %d: %s", what, resp.StatusCode, briefBody(raw))
	}
	var env apiEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("解析%s响应失败: %w", what, err)
	}
	if env.Code != 0 {
		return fmt.Errorf("%s请求失败: %s", what, env.Message)
	}
	if dst != nil && len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, dst); err != nil {
			return fmt.Errorf("解析%s数据失败: %w", what, err)
		}
	}
	return nil
}

// IsUnauthorized 判断是否为 401 错误。
func IsUnauthorized(err error) bool { return errors.Is(err, errUnauthorized) }

// setCommonHeaders 设置浏览器风格的通用请求头（部分供应商站点有 WAF，拦截非浏览器 UA）。
func setCommonHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36")
	req.Header.Set("Accept", "application/json, text/plain, */*")
}

// requestFactory 构造一次 HTTP 请求（重试时重新创建，因 body 只能读一次）。
type requestFactory func() (*http.Request, error)

// doWithRetry 执行请求，网络层错误（超时/TLS 等）最多重试 2 次，指数退避。
// HTTP 状态错误（4xx/5xx）不重试，直接返回响应由调用方判断。
func (c *Sub2apiClient) doWithRetry(ctx context.Context, factory requestFactory) (*http.Response, error) {
	const maxAttempts = 3
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * 800 * time.Millisecond):
			}
		}
		req, err := factory()
		if err != nil {
			return nil, err
		}
		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue // 网络错误，重试
		}
		return resp, nil
	}
	return nil, lastErr
}

// truncate 按 rune 边界截断，避免把多字节字符切成非法 UTF-8。
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// 从 n 处向前退到 rune 起始字节
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "..."
}

// briefBody 把响应体压成一句可读的诊断信息。
//
// 供应商站点的 nginx 在路径不存在时返回整页 HTML，原样塞进错误信息会污染
// 数据库与前端展示，故 HTML 一律折叠为固定短语，只保留 JSON/纯文本正文。
func briefBody(raw []byte) string {
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return "(空响应)"
	}
	if s[0] == '<' {
		return "(站点返回 HTML 页面，接口路径可能不存在)"
	}
	return truncate(strings.Join(strings.Fields(s), " "), 200)
}
