package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
)

// 本文件是建号（资源层对接）所需的写端点封装。
//
// 两侧凭据不同：
//   - 上游站建/删 key 用「上游 provider 的用户 JWT」
//   - 本站建/删 account 用「self provider 的 admin JWT」
// 所有写操作的补偿删除都必须提供，否则跨站两步写失败会留下孤儿资源。

// CreateAPIKey 在上游站创建 API key（POST /api/v1/keys）。
// 返回 (keyID, 明文 key)。groupID 必须是上游分组的数字 id。
func (c *Sub2apiClient) CreateAPIKey(ctx context.Context, baseURL, accessToken, name string, groupID int64) (int64, string, error) {
	body, _ := json.Marshal(map[string]any{"name": name, "group_id": groupID})
	url := strings.TrimRight(baseURL, "/") + "/api/v1/keys"

	// 响应字段名各版本略有差异，逐个兜底
	var data struct {
		ID     int64  `json:"id"`
		Key    string `json:"key"`
		Token  string `json:"token"`
		APIKey string `json:"api_key"`
	}
	if err := c.requestJSON(ctx, "POST", url, accessToken, body, "创建 key", &data); err != nil {
		return 0, "", err
	}
	key := data.Key
	if key == "" {
		key = data.Token
	}
	if key == "" {
		key = data.APIKey
	}
	if key == "" {
		return 0, "", fmt.Errorf("创建 key 成功但响应未返回明文 key，无法继续建号")
	}
	return data.ID, key, nil
}

// DeleteAPIKey 删除上游 key（补偿用）。
func (c *Sub2apiClient) DeleteAPIKey(ctx context.Context, baseURL, accessToken string, keyID int64) error {
	url := fmt.Sprintf("%s/api/v1/keys/%d", strings.TrimRight(baseURL, "/"), keyID)
	var ignored json.RawMessage
	return c.requestJSON(ctx, "DELETE", url, accessToken, nil, "删除 key", &ignored)
}

// AdminAccount 本站管理端账号（建号后回读 / bind 模式选择用）。
type AdminAccount struct {
	ID       int64   `json:"id"`
	Name     string  `json:"name"`
	Type     string  `json:"type"`
	Platform string  `json:"platform"`
	Status   string  `json:"status"`
	GroupIDs []int64 `json:"group_ids"`
}

// CreateAdminAccount 在本站创建账号（POST /api/v1/admin/accounts）。
// payload 由 BuildAccountPayload 组装；返回新账号 id。
func (c *Sub2apiClient) CreateAdminAccount(ctx context.Context, baseURL, accessToken string, payload map[string]any) (int64, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	url := strings.TrimRight(baseURL, "/") + "/api/v1/admin/accounts"
	var data struct {
		ID int64 `json:"id"`
	}
	if err := c.requestJSON(ctx, "POST", url, accessToken, body, "创建账号", &data); err != nil {
		return 0, err
	}
	if data.ID == 0 {
		return 0, fmt.Errorf("创建账号成功但响应未返回 id")
	}
	return data.ID, nil
}

// DeleteAdminAccount 删除本站账号（补偿用）。
func (c *Sub2apiClient) DeleteAdminAccount(ctx context.Context, baseURL, accessToken string, accountID int64) error {
	url := fmt.Sprintf("%s/api/v1/admin/accounts/%d", strings.TrimRight(baseURL, "/"), accountID)
	var ignored json.RawMessage
	return c.requestJSON(ctx, "DELETE", url, accessToken, nil, "删除账号", &ignored)
}

// ListAdminAccounts 拉取本站账号列表（可按分组过滤）。
// bind 模式选择已有账号、以及建号后的重名检查都用它。
func (c *Sub2apiClient) ListAdminAccounts(ctx context.Context, baseURL, accessToken string, groupID int64) ([]AdminAccount, error) {
	base := strings.TrimRight(baseURL, "/")
	var out []AdminAccount
	const pageSize = 100
	for page := 1; page <= 20; page++ {
		url := fmt.Sprintf("%s/api/v1/admin/accounts?page=%d&page_size=%d", base, page, pageSize)
		if groupID > 0 {
			url += fmt.Sprintf("&group=%d", groupID)
		}
		var raw json.RawMessage
		if err := c.getJSON(ctx, url, accessToken, "账号列表", &raw); err != nil {
			return nil, err
		}
		items, total, err := decodeAdminAccountPage(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
		if len(items) < pageSize || (total > 0 && int64(len(out)) >= total) {
			break
		}
	}
	return out, nil
}

// decodeAdminAccountPage 兼容 {items:[...],total} 与裸数组两种形态。
func decodeAdminAccountPage(raw json.RawMessage) ([]AdminAccount, int64, error) {
	var wrapper struct {
		Items []AdminAccount `json:"items"`
		Total int64          `json:"total"`
	}
	if err := json.Unmarshal(raw, &wrapper); err == nil && wrapper.Items != nil {
		return wrapper.Items, wrapper.Total, nil
	}
	var list []AdminAccount
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, 0, fmt.Errorf("解析账号列表失败: %w", err)
	}
	return list, 0, nil
}

// platformConcurrency 各平台的默认并发上限（沿用 transit-hub 的实测值）。
var platformConcurrency = map[string]int{
	"openai":      1000,
	"anthropic":   1000,
	"gemini":      1000,
	"antigravity": 10,
}

// SelfKeyBrand 上游 key 名称里的本站品牌前缀。self 站点在库里叫 __self__，不能当品牌。
const SelfKeyBrand = "kaola"

// FormatRate 整数原样，否则去尾零（与前端 formatRate 同构）。
func FormatRate(rate float64) string {
	if rate == 0 {
		return "0"
	}
	s := strconv.FormatFloat(rate, 'f', 4, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	if s == "" || s == "-0" {
		return "0"
	}
	return s
}

// UpstreamKeyName 上游 key：【kaola】{分组}-{倍率}。
func UpstreamKeyName(group string, rate float64) string {
	return "【" + SelfKeyBrand + "】" + group + "-" + FormatRate(rate)
}

// LocalAccountName 本站账号：【{上游名}】{分组}-{倍率}。
func LocalAccountName(provider, group string, rate float64) string {
	return "【" + provider + "】" + group + "-" + FormatRate(rate)
}

// PickAccountBaseURL 从子账号 URL 里随机取一条（去空白、去重）。空则返回空串。
func PickAccountBaseURL(urls []string) string {
	seen := map[string]struct{}{}
	uniq := make([]string, 0, len(urls))
	for _, raw := range urls {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		uniq = append(uniq, s)
	}
	if len(uniq) == 0 {
		return ""
	}
	if len(uniq) == 1 {
		return uniq[0]
	}
	return uniq[rand.Intn(len(uniq))]
}

// BuildAccountPayload 组装本站建账号的请求体。
//
// 按上游分组的 platform 分支填 credentials/extra/concurrency —— platform 由
// /api/v1/groups/available 直接返回，无需用户手选。
// group_ids 必须是数字数组：实测传字符串数组上游会返回 400。
func BuildAccountPayload(platform, upstreamBaseURL, apiKey, name string, localGroupIDs []int64) map[string]any {
	credentials := map[string]any{
		"base_url": strings.TrimRight(upstreamBaseURL, "/") + "/",
		"api_key":  apiKey,
	}
	extra := map[string]any{}

	switch platform {
	case "openai":
		credentials["pool_mode"] = true
		extra["openai_passthrough"] = true
	case "anthropic":
		credentials["pool_mode"] = true
		extra["anthropic_passthrough"] = true
	case "gemini":
		credentials["pool_mode"] = true
		credentials["tier_id"] = "aistudio_free"
	case "antigravity":
		// 无需 pool_mode 与 passthrough
	}

	concurrency, ok := platformConcurrency[platform]
	if !ok {
		concurrency = 100
	}

	if localGroupIDs == nil {
		localGroupIDs = []int64{}
	}
	payload := map[string]any{
		"name":        name,
		"type":        "apikey",
		"platform":    platform,
		"credentials": credentials,
		"priority":    1,
		"concurrency": concurrency,
		"group_ids":   localGroupIDs, // ★ 数字数组
	}
	if len(extra) > 0 {
		payload["extra"] = extra
	}
	return payload
}
