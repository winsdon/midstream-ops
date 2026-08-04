package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// 本文件是 Sub2apiClient 的 admin 扩展：调价映射写自己站点用。
// 写操作一律 GET + PUT-merge：先取完整对象，只改目标字段回写，
// 绝不构造部分对象覆盖（transit-hub 同款纪律，防止抹掉其它配置）。

// AdminGroup 管理端分组（保留原始字段用于 merge 回写）。
type AdminGroup struct {
	ID   int64
	Name string
	Rate float64
	Raw  map[string]any // 完整原始对象
}

// GetAdminGroups 拉取管理端分组列表（GET /api/v1/admin/groups，分页取全量）。
func (c *Sub2apiClient) GetAdminGroups(ctx context.Context, baseURL, accessToken string) ([]AdminGroup, error) {
	base := strings.TrimRight(baseURL, "/")
	var out []AdminGroup
	const pageSize = 100
	for page := 1; page <= 20; page++ {
		url := fmt.Sprintf("%s/api/v1/admin/groups?page=%d&page_size=%d", base, page, pageSize)
		var raw json.RawMessage
		if err := c.getJSON(ctx, url, accessToken, "管理端分组", &raw); err != nil {
			return nil, err
		}
		items, total, err := decodeAdminGroupPage(raw)
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

// decodeAdminGroupPage 兼容 {items:[...],total} 与裸数组两种形态。
func decodeAdminGroupPage(raw json.RawMessage) ([]AdminGroup, int64, error) {
	var wrapper struct {
		Items []map[string]any `json:"items"`
		Total int64            `json:"total"`
	}
	var list []map[string]any
	if err := json.Unmarshal(raw, &wrapper); err == nil && wrapper.Items != nil {
		list = wrapper.Items
	} else if err := json.Unmarshal(raw, &list); err != nil {
		return nil, 0, fmt.Errorf("解析管理端分组响应失败: %w", err)
	}

	out := make([]AdminGroup, 0, len(list))
	for _, m := range list {
		g := AdminGroup{Raw: m}
		if v, ok := m["id"].(float64); ok {
			g.ID = int64(v)
		}
		if v, ok := m["name"].(string); ok {
			g.Name = v
		}
		if v, ok := m["rate_multiplier"].(float64); ok {
			g.Rate = v
		}
		out = append(out, g)
	}
	return out, wrapper.Total, nil
}

// UpdateAdminGroupRate 修改分组倍率（PUT /api/v1/admin/groups/:id，GET+PUT-merge）。
// group 须来自 GetAdminGroups（含完整 Raw），只覆盖 rate_multiplier 一个字段。
func (c *Sub2apiClient) UpdateAdminGroupRate(ctx context.Context, baseURL, accessToken string, group AdminGroup, newRate float64) error {
	if group.Raw == nil {
		return fmt.Errorf("缺少分组原始对象，无法安全合并")
	}
	// 复制原始对象，仅改目标字段（不修改入参）
	payload := make(map[string]any, len(group.Raw))
	for k, v := range group.Raw {
		payload[k] = v
	}
	payload["rate_multiplier"] = newRate

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/api/v1/admin/groups/%d", strings.TrimRight(baseURL, "/"), group.ID)
	var ignored json.RawMessage
	return c.requestJSON(ctx, "PUT", url, accessToken, body, "更新分组倍率", &ignored)
}
