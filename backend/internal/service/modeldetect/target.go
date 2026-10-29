// Package modeldetect 对上游 Claude 兼容端点做渠道指纹检测。
//
// 它回答两个正交的问题：
//  1. 这条渠道的后面是什么（官方 Max 号池 / 官方 API Key / Bedrock / Kiro / Vertex / 包装伪装）；
//  2. 后端到底是不是真 Claude（协议严格性与签名完整性构成的真实性评分）。
//
// 两问分开是刻意的：Max 号池必然注入 Claude Code 人设，那是分类证据而不是造假证据；
// 反过来，一条干净的官方 API 直连也可能被人在中间换成国产模型。
package modeldetect

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// 认证方式。中转站实现不一，默认两者都发（与参考脚本一致）。
const (
	AuthAPIKey = "x-api-key"
	AuthBearer = "bearer"
	AuthBoth   = "both"
)

// DefaultModel 未指定模型时的兜底。
const DefaultModel = "claude-opus-5"

// DefaultTimeout 单次请求超时。
const DefaultTimeout = 90 * time.Second

// Target 一个待检测的上游端点。APIKey 只在进程内用于发请求，绝不入库、不进报告。
type Target struct {
	// Name 展示名（账号名或用户填的别名）。
	Name string
	// BaseURL 已归一化的站点根地址（不含 /v1）。
	BaseURL string
	// APIKey 渠道密钥。
	APIKey string
	// Model 请求的模型 id。
	Model string
	// AuthMode 取 AuthAPIKey / AuthBearer / AuthBoth。
	AuthMode string
	// ExtraHeaders 附加请求头（如 anthropic-beta）。
	ExtraHeaders map[string]string
	// Timeout 单请求超时。
	Timeout time.Duration
	// AccountID 关联的本站账号 id（手填目标为 nil）。
	AccountID *int64
	// ProviderID 账号归属的供应商 id（未关联为 nil）。
	ProviderID *int64
}

// Normalize 校验并补齐缺省值，返回新值（不改原对象）。
func (t Target) Normalize() (Target, error) {
	out := t
	base, err := NormalizeBaseURL(t.BaseURL)
	if err != nil {
		return out, err
	}
	out.BaseURL = base
	out.Name = strings.TrimSpace(t.Name)
	if out.Name == "" {
		out.Name = base
	}
	out.APIKey = strings.TrimSpace(t.APIKey)
	if out.APIKey == "" {
		return out, errors.New("API Key 不能为空")
	}
	out.Model = strings.TrimSpace(t.Model)
	if out.Model == "" {
		out.Model = DefaultModel
	}
	switch t.AuthMode {
	case AuthAPIKey, AuthBearer, AuthBoth:
	default:
		out.AuthMode = AuthBoth
	}
	if out.Timeout <= 0 {
		out.Timeout = DefaultTimeout
	}
	if out.Timeout > 5*time.Minute {
		out.Timeout = 5 * time.Minute
	}
	return out, nil
}

// NormalizeBaseURL 归一化站点地址：去尾斜杠、剥掉 /v1/messages 后缀，禁止内嵌凭据。
// 与 probe_service 的拼接约定一致——保留 /v1 后缀的判断留给 endpoint()。
func NormalizeBaseURL(input string) (string, error) {
	v := strings.TrimSpace(input)
	v = strings.TrimRight(v, "/")
	if v == "" {
		return "", errors.New("Base URL 不能为空")
	}
	if !strings.HasPrefix(strings.ToLower(v), "http://") && !strings.HasPrefix(strings.ToLower(v), "https://") {
		return "", errors.New("Base URL 必须以 http:// 或 https:// 开头")
	}
	u, err := url.Parse(v)
	if err != nil {
		return "", fmt.Errorf("Base URL 无法解析: %w", err)
	}
	if u.User != nil {
		return "", errors.New("Base URL 不能内嵌用户名或密码，请改用认证字段")
	}
	for _, suffix := range []string{"/v1/messages/count_tokens", "/v1/messages"} {
		if strings.HasSuffix(strings.ToLower(v), suffix) {
			v = v[:len(v)-len(suffix)]
			break
		}
	}
	return strings.TrimRight(v, "/"), nil
}

// endpoint 拼出目标端点。base 可能已带 /v1，避免重复拼接。
func endpoint(base, kind string) string {
	b := strings.TrimRight(base, "/")
	prefix := b + "/v1"
	if strings.HasSuffix(b, "/v1") {
		prefix = b
	}
	switch kind {
	case KindCountTokens:
		return prefix + "/messages/count_tokens"
	case KindModels:
		return prefix + "/models"
	default:
		return prefix + "/messages"
	}
}

// 端点类型。
const (
	KindMessages    = "messages"
	KindCountTokens = "count_tokens"
	KindModels      = "models"
)
