package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// 本文件是 Sub2apiClient 的 admin 扩展：读本站管理端分组（保存本站连接时校验 admin 权限）。

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
