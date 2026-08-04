package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// NewAPIClient new-api 平台客户端。
//
// 对接要点（移植 transit-hub 的兼容处理）：
//   - 信封为 {success,message,data}，且鉴权失败常以 HTTP 200 + success:false 返回，
//     必须显式当错误处理；
//   - 认证三种：会话 JWT（新版密码登录产物，≥v1.0.0-rc.22）、会话 Cookie
//     （旧版密码登录产物，须配 New-Api-User 头）、系统访问令牌（同样须配该头）；
//   - 余额以 quota 计，须除以 /api/status 的 quota_per_unit 折算美元。
type NewAPIClient struct {
	httpClient *http.Client
}

// NewNewAPIClient 创建客户端。
func NewNewAPIClient(timeout time.Duration) *NewAPIClient {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	transport := &http.Transport{
		TLSHandshakeTimeout:   20 * time.Second,
		ResponseHeaderTimeout: timeout,
		IdleConnTimeout:       30 * time.Second,
		MaxIdleConns:          20,
	}
	return &NewAPIClient{
		httpClient: &http.Client{Timeout: timeout + 10*time.Second, Transport: transport},
	}
}

// newapiEnvelope new-api 统一响应封装。
type newapiEnvelope struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// NewAPIAuth new-api 请求认证要素。
type NewAPIAuth struct {
	Cookie      string // 旧版 password 模式：登录 Set-Cookie 拼接
	AccessToken string // user_key 模式：系统访问令牌（PAT）
	JWT         string // 新版 password 模式（≥rc.22）：登录/续期得到的会话令牌
	UserID      string // New-Api-User 头；新版 JWT 自带身份，不再需要
}

// bearer 返回 Authorization 头要发的令牌。
// 两种令牌互斥（一个站点要么走 user_key 要么走密码登录），同时出现时以会话令牌为准。
func (a NewAPIAuth) bearer() string {
	if a.JWT != "" {
		return a.JWT
	}
	return a.AccessToken
}

// NewAPILoginResult 登录 / 会话续期结果。
//
// 新旧两版由 AccessToken 是否为空天然区分：new-api 自 v1.0.0-rc.22（PR #6329）
// 改用 JWT + refresh cookie，旧版只下发会话 Cookie。
type NewAPILoginResult struct {
	Cookie string // 旧版：会话 Cookie 拼接串；新版为空
	UserID string
	Quota  float64 // 原始 quota（未换算）

	AccessToken     string    // 新版：JWT 会话令牌
	RefreshToken    string    // 新版：new_api_refresh cookie 的裸 value
	AccessExpiresAt time.Time // 新版：JWT 到期时刻（默认 TTL 15min）
}

// newapiRefreshCookie 新版 new-api 下发 refresh 凭据的 cookie 名（Path=/api/user/auth）。
const newapiRefreshCookie = "new_api_refresh"

// Auth 把登录结果转成请求认证要素，新旧两版的分流只在这里做一次。
func (r *NewAPILoginResult) Auth() NewAPIAuth {
	if r.AccessToken != "" {
		return NewAPIAuth{JWT: r.AccessToken, UserID: r.UserID}
	}
	return NewAPIAuth{Cookie: r.Cookie, UserID: r.UserID}
}

// errRefreshUnsupported 上游没有新版 refresh 端点（HTTP 404）。
// 调用方据此降级到密码重登，而不是把它误判成凭据失效。
var errRefreshUnsupported = errors.New("refresh endpoint unsupported")

// newapiLoginData 登录 / 续期响应的 data 段。
// 新版把用户对象收进 user 子字段并加上 access_token；旧版直接把用户字段摊平在 data 上。
type newapiLoginData struct {
	AccessToken     string `json:"access_token"`
	AccessExpiresAt int64  `json:"access_expires_at"` // unix 秒

	User *struct {
		ID    int64   `json:"id"`
		Quota float64 `json:"quota"`
	} `json:"user"`

	// 旧版摊平形态
	ID    int64   `json:"id"`
	Quota float64 `json:"quota"`
}

// fillNewAPILoginResult 从 data 段与响应 Set-Cookie 组装统一结果，新旧两版共用。
// refresh 凭据单独摘出（它的 Path 限定在 /api/user/auth，不能混进普通请求的 Cookie 头），
// 其余 cookie 仍拼成旧版会话串。
func fillNewAPILoginResult(data json.RawMessage, resp *http.Response) *NewAPILoginResult {
	var d newapiLoginData
	_ = json.Unmarshal(data, &d)

	out := &NewAPILoginResult{AccessToken: d.AccessToken}
	if d.User != nil {
		out.UserID = fmt.Sprintf("%d", d.User.ID)
		out.Quota = d.User.Quota
	} else {
		out.UserID = fmt.Sprintf("%d", d.ID)
		out.Quota = d.Quota
	}
	if d.AccessExpiresAt > 0 {
		out.AccessExpiresAt = time.Unix(d.AccessExpiresAt, 0)
	}

	var parts []string
	for _, ck := range resp.Cookies() {
		if ck.Name == newapiRefreshCookie {
			out.RefreshToken = ck.Value
			continue
		}
		parts = append(parts, ck.Name+"="+ck.Value)
	}
	out.Cookie = strings.Join(parts, "; ")
	return out
}

// Login 密码登录（POST /api/user/login）。
// 新版返回 JWT + refresh cookie，旧版只返回会话 Cookie，由 fillNewAPILoginResult 分流。
func (c *NewAPIClient) Login(ctx context.Context, baseURL, username, password string) (*NewAPILoginResult, error) {
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	url := strings.TrimRight(baseURL, "/") + "/api/user/login"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	setCommonHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("登录请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("登录失败 HTTP %d: %s", resp.StatusCode, briefBody(raw))
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return nil, fmt.Errorf("%w: %w", ErrLoginRejected, err)
		}
		return nil, err
	}

	var env newapiEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("解析登录响应失败: %w", err)
	}
	if !env.Success {
		// HTTP 200 + success:false：凭据错误，进入冷却
		return nil, fmt.Errorf("%w: 登录失败: %s", ErrLoginRejected, env.Message)
	}

	out := fillNewAPILoginResult(env.Data, resp)
	// 新版可能只下发 refresh cookie，故不能再要求必须有会话 Cookie
	if out.AccessToken == "" && out.Cookie == "" {
		return nil, fmt.Errorf("登录响应既无 access_token 也无 Set-Cookie")
	}
	return out, nil
}

// RefreshSession 新版 new-api 会话续期（POST /api/user/auth/refresh，≥v1.0.0-rc.22）。
// 无请求体，凭据走 new_api_refresh cookie；返回新 JWT 与轮换后的 refresh token。
//
// 三个上游约束：
//   - refresh cookie 的 Path=/api/user/auth，故只在此端点发送，不能混进 getJSON；
//   - refresh token 每次调用都会轮换，旧值重放会触发上游的重放检测并吊销整个会话，
//     故同一站点的调用必须串行（见 providerTokenManager.refreshNewAPISession）；
//   - 上游开启 SessionCookieSecure 时校验 Origin/Referer，缺失即 403。
//
// 错误语义刻意与 Login 不同：这里绝不返回 ErrLoginRejected，否则续期失败会被
// recordRejected 误计为登录被拒，把站点锁进 6 小时冷却。
func (c *NewAPIClient) RefreshSession(ctx context.Context, baseURL, refreshToken string) (*NewAPILoginResult, error) {
	base := strings.TrimRight(baseURL, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/user/auth/refresh", nil)
	if err != nil {
		return nil, err
	}
	setCommonHeaders(req)
	req.Header.Set("Cookie", newapiRefreshCookie+"="+refreshToken)
	// 同源请求的自然取值
	origin := originOf(base)
	req.Header.Set("Origin", origin)
	req.Header.Set("Referer", origin+"/")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("会话续期请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusNotFound:
		// 上游没有该端点（旧版或已降级）：让调用方回落到密码重登
		return nil, errRefreshUnsupported
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("%w: 会话续期被拒 HTTP %d: %s", errUnauthorized, resp.StatusCode, briefBody(raw))
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("会话续期失败 HTTP %d: %s", resp.StatusCode, briefBody(raw))
	}

	var env newapiEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("解析续期响应失败: %w", err)
	}
	if !env.Success {
		return nil, fmt.Errorf("%w: 会话续期失败: %s", errUnauthorized, env.Message)
	}

	out := fillNewAPILoginResult(env.Data, resp)
	if out.AccessToken == "" {
		return nil, fmt.Errorf("%w: 续期响应缺少 access_token", errUnauthorized)
	}
	// 上游未轮换时不下发新 cookie，沿用旧值，调用方可无条件写库
	if out.RefreshToken == "" {
		out.RefreshToken = refreshToken
	}
	return out, nil
}

// originOf 取 URL 的 scheme://host —— Origin 头的合法形态，不含路径。
func originOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return strings.TrimRight(rawURL, "/")
	}
	return u.Scheme + "://" + u.Host
}

// GetQuotaPerUnit 拉取 quota→USD 换算系数（GET /api/status，免鉴权）。
// 失败时返回默认 500000。
func (c *NewAPIClient) GetQuotaPerUnit(ctx context.Context, baseURL string) float64 {
	const fallback = 500000
	var data struct {
		QuotaPerUnit float64 `json:"quota_per_unit"`
	}
	if err := c.getJSON(ctx, strings.TrimRight(baseURL, "/")+"/api/status", NewAPIAuth{}, "状态", &data); err != nil {
		return fallback
	}
	if data.QuotaPerUnit <= 0 {
		return fallback
	}
	return data.QuotaPerUnit
}

// NewAPISelf 用户信息（quota 未换算）。
type NewAPISelf struct {
	ID        int64   `json:"id"`
	Username  string  `json:"username"`
	Quota     float64 `json:"quota"`
	UsedQuota float64 `json:"used_quota"`
}

// GetSelf 拉取当前用户（GET /api/user/self）。
func (c *NewAPIClient) GetSelf(ctx context.Context, baseURL string, auth NewAPIAuth) (*NewAPISelf, error) {
	var self NewAPISelf
	if err := c.getJSON(ctx, strings.TrimRight(baseURL, "/")+"/api/user/self", auth, "用户信息", &self); err != nil {
		return nil, err
	}
	return &self, nil
}

// GetTodayStat 拉取时间窗内用量聚合（GET /api/log/self/stat?type=0）。
// 返回 quota（未换算）与请求数（rpm 字段实为区间请求计数，缺省 0）。
func (c *NewAPIClient) GetTodayStat(ctx context.Context, baseURL string, auth NewAPIAuth, start, end time.Time) (quota float64, requests int64, err error) {
	url := fmt.Sprintf("%s/api/log/self/stat?type=0&start_timestamp=%d&end_timestamp=%d",
		strings.TrimRight(baseURL, "/"), start.Unix(), end.Unix())
	var data struct {
		Quota float64 `json:"quota"`
		Rpm   int64   `json:"rpm"`
		Tpm   int64   `json:"tpm"`
	}
	if err := c.getJSON(ctx, url, auth, "用量统计", &data); err != nil {
		return 0, 0, err
	}
	return data.Quota, data.Rpm, nil
}

// NewAPIToken 是 new-api 的一个 token。Key 仅在成本同步期间短暂持有，绝不持久化或记录日志。
type NewAPIToken struct {
	ID     int64             `json:"id"`
	Name   string            `json:"name"`
	Group  string            `json:"group"`
	Status NewAPITokenStatus `json:"status"`
	Key    string            `json:"-"`
}

// NewAPITokenStatus 兼容 new-api 不同版本返回的数字或字符串状态。
type NewAPITokenStatus string

func (s *NewAPITokenStatus) UnmarshalJSON(raw []byte) error {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	switch v := value.(type) {
	case string:
		*s = NewAPITokenStatus(v)
	case float64:
		*s = NewAPITokenStatus(strconv.FormatFloat(v, 'f', -1, 64))
	case bool:
		*s = NewAPITokenStatus(strconv.FormatBool(v))
	default:
		*s = ""
	}
	return nil
}

type newAPITokenPage struct {
	Items []NewAPIToken `json:"items"`
	Total int64         `json:"total"`
}

// UnmarshalJSON 兼容 data.items 与部分旧版直接返回 data 数组的形态。
func (p *newAPITokenPage) UnmarshalJSON(raw []byte) error {
	type pageAlias newAPITokenPage
	var page pageAlias
	if err := json.Unmarshal(raw, &page); err == nil && page.Items != nil {
		*p = newAPITokenPage(page)
		return nil
	}
	var object struct {
		Items   []NewAPIToken `json:"items"`
		List    []NewAPIToken `json:"list"`
		Records []NewAPIToken `json:"records"`
		Data    []NewAPIToken `json:"data"`
		Total   int64         `json:"total"`
	}
	if err := json.Unmarshal(raw, &object); err == nil {
		items := object.Items
		if items == nil {
			items = object.List
		}
		if items == nil {
			items = object.Records
		}
		if items == nil {
			items = object.Data
		}
		if items != nil {
			*p = newAPITokenPage{Items: items, Total: object.Total}
			return nil
		}
	}
	var items []NewAPIToken
	if err := json.Unmarshal(raw, &items); err != nil {
		return err
	}
	p.Items = items
	p.Total = int64(len(items))
	return nil
}

// ListTokens 分页拉取 new-api 当前用户的全部 token。
func (c *NewAPIClient) ListTokens(ctx context.Context, baseURL string, auth NewAPIAuth) ([]NewAPIToken, error) {
	const pageSize = 100
	const maxPages = 100
	base := strings.TrimRight(baseURL, "/")
	var out []NewAPIToken
	for page := 1; page <= maxPages; page++ {
		endpoint := fmt.Sprintf("%s/api/token/?p=%d&page_size=%d", base, page, pageSize)
		var data newAPITokenPage
		if err := c.getJSON(ctx, endpoint, auth, "token 列表", &data); err != nil {
			return nil, err
		}
		out = append(out, data.Items...)
		if len(data.Items) < pageSize || (data.Total > 0 && int64(len(out)) >= data.Total) {
			break
		}
	}
	return out, nil
}

// GetTokenKey 获取指定 token 的完整 key。调用方只能用于内存指纹匹配。
func (c *NewAPIClient) GetTokenKey(ctx context.Context, baseURL string, auth NewAPIAuth, tokenID int64) (string, error) {
	endpoint := fmt.Sprintf("%s/api/token/%d/key", strings.TrimRight(baseURL, "/"), tokenID)
	var data struct {
		Key string `json:"key"`
	}
	if err := c.requestJSON(ctx, http.MethodPost, endpoint, auth, "token key", &data); err != nil {
		return "", err
	}
	if strings.TrimSpace(data.Key) == "" {
		return "", errors.New("token key 响应缺少 key")
	}
	return data.Key, nil
}

// NewAPITokenUsage 是一个 token 在指定时间窗内的美元成本与请求数。
type NewAPITokenUsage struct {
	ActualCost float64
	Requests   int64
}

// GetTokenUsage 按 token_name 和可选 group 查询统计，并把 quota 换算成美元。
func (c *NewAPIClient) GetTokenUsage(
	ctx context.Context,
	baseURL string,
	auth NewAPIAuth,
	tokenName, group string,
	start, end time.Time,
	quotaPerUnit float64,
) (NewAPITokenUsage, error) {
	if quotaPerUnit <= 0 {
		quotaPerUnit = 500000
	}
	query := url.Values{}
	query.Set("type", "2")
	query.Set("start_timestamp", strconv.FormatInt(start.Unix(), 10))
	query.Set("end_timestamp", strconv.FormatInt(end.Unix(), 10))
	query.Set("token_name", tokenName)
	if strings.TrimSpace(group) != "" {
		query.Set("group", group)
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/api/log/self/stat?" + query.Encode()
	var data struct {
		Quota float64 `json:"quota"`
		Rpm   int64   `json:"rpm"`
	}
	if err := c.getJSON(ctx, endpoint, auth, "token 用量", &data); err != nil {
		return NewAPITokenUsage{}, err
	}
	return NewAPITokenUsage{ActualCost: data.Quota / quotaPerUnit, Requests: data.Rpm}, nil
}

// GetTokensUsage 并发拉取多个 token 的区间用量，并发上限固定为 4。
func (c *NewAPIClient) GetTokensUsage(
	ctx context.Context,
	baseURL string,
	auth NewAPIAuth,
	tokens []NewAPIToken,
	start, end time.Time,
	quotaPerUnit float64,
) (map[int64]APIKeyUsage, error) {
	out := make(map[int64]APIKeyUsage, len(tokens))
	if len(tokens) == 0 {
		return out, nil
	}
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for _, token := range tokens {
		token := token
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				mu.Lock()
				if firstErr == nil {
					firstErr = ctx.Err()
				}
				mu.Unlock()
				return
			}
			usage, err := c.GetTokenUsage(ctx, baseURL, auth, token.Name, token.Group, start, end, quotaPerUnit)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			out[token.ID] = APIKeyUsage{
				APIKeyID:        token.ID,
				TodayActualCost: usage.ActualCost,
				TotalActualCost: usage.ActualCost,
				Requests:        usage.Requests,
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

// NewAPIGroupRate 分组倍率（P3 上游倍率追踪用）。
type NewAPIGroupRate struct {
	Name  string
	Ratio float64
}

// GetGroupRates 拉取当前用户可用分组及倍率。
// 主：GET /api/user/self/groups（{name: {ratio,desc}} 或 {name: ratio}）；
// 备：GET /api/pricing 的 group_ratio。
func (c *NewAPIClient) GetGroupRates(ctx context.Context, baseURL string, auth NewAPIAuth) ([]NewAPIGroupRate, error) {
	base := strings.TrimRight(baseURL, "/")

	var rawGroups map[string]json.RawMessage
	if err := c.getJSON(ctx, base+"/api/user/self/groups", auth, "分组", &rawGroups); err == nil && len(rawGroups) > 0 {
		out := make([]NewAPIGroupRate, 0, len(rawGroups))
		for name, raw := range rawGroups {
			// 兼容 {"ratio":1.5,...} 对象与裸数字两种形态
			var obj struct {
				Ratio float64 `json:"ratio"`
			}
			var num float64
			if json.Unmarshal(raw, &obj) == nil && obj.Ratio > 0 {
				out = append(out, NewAPIGroupRate{Name: name, Ratio: obj.Ratio})
			} else if json.Unmarshal(raw, &num) == nil && num > 0 {
				out = append(out, NewAPIGroupRate{Name: name, Ratio: num})
			}
		}
		if len(out) > 0 {
			return out, nil
		}
	}

	// fallback：/api/pricing
	var pricing struct {
		GroupRatio map[string]float64 `json:"group_ratio"`
	}
	if err := c.getJSON(ctx, base+"/api/pricing", auth, "定价", &pricing); err != nil {
		return nil, err
	}
	out := make([]NewAPIGroupRate, 0, len(pricing.GroupRatio))
	for name, ratio := range pricing.GroupRatio {
		out = append(out, NewAPIGroupRate{Name: name, Ratio: ratio})
	}
	return out, nil
}

// getJSON 发起带认证的 GET 并把 envelope.data 解到 dst。
func (c *NewAPIClient) getJSON(ctx context.Context, url string, auth NewAPIAuth, what string, dst any) error {
	return c.requestJSON(ctx, http.MethodGet, url, auth, what, dst)
}

// requestJSON 发起带认证的请求并把 envelope.data 解到 dst。
func (c *NewAPIClient) requestJSON(ctx context.Context, method, url string, auth NewAPIAuth, what string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return err
	}
	setCommonHeaders(req)
	if auth.Cookie != "" {
		req.Header.Set("Cookie", auth.Cookie)
	}
	if tok := auth.bearer(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	// New-Api-User 在新版（PR #6329）已移除，JWT 自带身份；
	// 旧版 Cookie 模式与 user_key 模式仍依赖它，故只在无 JWT 时发送。
	if auth.JWT == "" && auth.UserID != "" {
		req.Header.Set("New-Api-User", auth.UserID)
	}

	resp, err := c.httpClient.Do(req)
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
	var env newapiEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("解析%s响应失败: %w", what, err)
	}
	if !env.Success {
		// HTTP 200 + success:false 一律按鉴权/业务失败处理（new-api 惯例）
		if isNewAPIAuthMessage(env.Message) {
			return errUnauthorized
		}
		return fmt.Errorf("%s请求失败: %s", what, env.Message)
	}
	if dst != nil && len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, dst); err != nil {
			return fmt.Errorf("解析%s数据失败: %w", what, err)
		}
	}
	return nil
}

// isNewAPIAuthMessage 判断 success:false 的 message 是否为鉴权失败语义。
func isNewAPIAuthMessage(msg string) bool {
	m := strings.ToLower(msg)
	return strings.Contains(m, "未登录") || strings.Contains(m, "登录") ||
		strings.Contains(m, "unauthorized") || strings.Contains(m, "token") ||
		strings.Contains(m, "无权")
}
