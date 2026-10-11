package service

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// ErrRefreshInvalid refresh token 已失效。密码模式可以降级重登一次。
// 限流、验证码、网页和 5xx 不是这类失败，调用方必须留着 refresh token，下轮再换。
var ErrRefreshInvalid = errors.New("refresh token invalid")

// ErrUpstreamHTML 上游返回了网页，而不是 JSON API。
var ErrUpstreamHTML = errors.New("upstream returned html")

// bodyIsHTML 站点用 HTML 挡请求（WAF、验证页、网关 403）时正文以 < 开头。
func bodyIsHTML(raw []byte) bool {
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return false
	}
	return s[0] == '<' || strings.HasPrefix(strings.ToLower(s), "<!doctype")
}

// transientLoginText 限流或人机验证。重试密码没有意义，也不能记成凭据错误。
func transientLoginText(s string) bool {
	low := strings.ToLower(s)
	for _, k := range []string{
		"turnstile", "captcha", "rate limit", "too many", "retry later", "slow down",
		"限流", "验证码", "人机",
	} {
		if strings.Contains(low, k) {
			return true
		}
	}
	return false
}

// refreshTokenDeadText 刷新接口明确表示 refresh token 不能再用。
func refreshTokenDeadText(s string) bool {
	low := strings.ToLower(s)
	for _, k := range []string{
		"invalid refresh", "refresh token invalid", "refresh_token_invalid",
		"invalid token", "token expired", "session revoked",
	} {
		if strings.Contains(low, k) {
			return true
		}
	}
	return false
}

// classifyLoginStatus 给非 2xx 登录响应一个哨兵。
// 返回 nil 表示普通失败（5xx、网络），调用方不要包 ErrLoginRejected。
func classifyLoginStatus(status int, raw []byte) error {
	if status == http.StatusTooManyRequests || transientLoginText(string(raw)) {
		return ErrRateLimited
	}
	if bodyIsHTML(raw) {
		return ErrUpstreamHTML
	}
	if status >= 400 && status < 500 {
		return ErrLoginRejected
	}
	return nil
}

// classifyLoginMessage 给 HTTP 200 但业务失败的登录文案一个哨兵。
// 验证码返回 nil：失败，但不进冷却阶梯。
func classifyLoginMessage(message string) error {
	if !transientLoginText(message) {
		return ErrLoginRejected
	}
	low := strings.ToLower(message)
	if strings.Contains(low, "rate") || strings.Contains(low, "too many") || strings.Contains(message, "限流") {
		return ErrRateLimited
	}
	return nil
}

// classifyRefreshStatus 给刷新接口的非 2xx 响应一个哨兵。
// ErrRefreshInvalid 才能降级密码登录；其余必须留着原来的 refresh token。
func classifyRefreshStatus(status int, raw []byte) error {
	if status == http.StatusTooManyRequests || transientLoginText(string(raw)) {
		return ErrRateLimited
	}
	if bodyIsHTML(raw) {
		return ErrUpstreamHTML
	}
	if status == http.StatusUnauthorized || refreshTokenDeadText(string(raw)) {
		return ErrRefreshInvalid
	}
	return nil
}

// loginHardBackoff 这种失败再打密码登录也不会好，调度器拉长到 6 小时。
func loginHardBackoff(msg string) bool {
	low := strings.ToLower(msg)
	return strings.Contains(msg, ErrUpstreamHTML.Error()) ||
		strings.Contains(low, "turnstile") ||
		strings.Contains(low, "captcha") ||
		strings.Contains(msg, "验证码") ||
		strings.Contains(msg, "网页")
}

// upstreamHost 限流按出口 IP 打到同一主机上，密码登录按主机串起来。
func upstreamHost(baseURL string) string {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return strings.ToLower(strings.TrimRight(baseURL, "/"))
	}
	return strings.ToLower(u.Host)
}
