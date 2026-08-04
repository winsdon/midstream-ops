package handler

import (
	"strconv"

	"sub2api-account-monitor/internal/pkg/response"
	"sub2api-account-monitor/internal/repository"
	"sub2api-account-monitor/internal/service"

	"github.com/gin-gonic/gin"
)

// RateHandler 倍率快照处理器（历史 + 当前列表）。
type RateHandler struct {
	rateSvc *service.RateService
}

// NewRateHandler 创建 RateHandler。
func NewRateHandler(rateSvc *service.RateService) *RateHandler {
	return &RateHandler{rateSvc: rateSvc}
}

// snapshotToH 快照行 → JSON（old_rate 由 prev_rate 提供，兼容旧前端字段名）。
func snapshotToH(it *repository.RateSnapshot) gin.H {
	h := gin.H{
		"id":            it.ID,
		"scope":         it.Scope,
		"provider_id":   it.ProviderID,
		"entity_type":   it.EntityType,
		"entity_id":     it.EntityID,
		"entity_name":   it.Name,
		"rate":          it.Rate,
		"new_rate":      it.Rate, // 兼容旧字段
		"platform":      it.Platform,
		"first_seen_at": it.FirstSeenAt.Format("2006-01-02 15:04:05"),
		"last_seen_at":  it.LastSeenAt.Format("2006-01-02 15:04:05"),
		"observed_at":   it.FirstSeenAt.Format("2006-01-02 15:04:05"), // 兼容旧字段
		"deleted":       it.Deleted,
	}
	if it.PrevRate != nil {
		h["old_rate"] = *it.PrevRate
		h["prev_rate"] = *it.PrevRate
	}
	return h
}

// History GET /rates/history?scope=&provider_id=&entity_type=&entity_id=&changes_only=&page=&page_size=
func (h *RateHandler) History(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)
	f := repository.SnapshotFilter{
		Scope:      c.Query("scope"),
		EntityType: c.Query("entity_type"),
		EntityID:   c.Query("entity_id"),
		Page:       page,
		PageSize:   pageSize,
	}
	if pid := c.Query("provider_id"); pid != "" {
		if v, err := strconv.ParseInt(pid, 10, 64); err == nil {
			f.ProviderID = &v
		}
	}
	items, total, err := h.rateSvc.History(c.Request.Context(), f)
	if err != nil {
		response.InternalError(c, "查询失败: "+err.Error())
		return
	}
	out := make([]gin.H, 0, len(items))
	for _, it := range items {
		// changes_only=true 时过滤掉首次建档行（无 prev）
		if c.Query("changes_only") == "true" && it.PrevRate == nil {
			continue
		}
		out = append(out, snapshotToH(it))
	}
	response.Paginated(c, out, total, page, pageSize)
}

// Current GET /rates/current?scope=local|upstream&provider_id=&include_deleted=
// 分组倍率页主列表：每实体当前生效倍率 + 上次倍率（涨跌持续展示到下一次变化）。
func (h *RateHandler) Current(c *gin.Context) {
	scope := c.Query("scope")
	if scope == "" {
		scope = "upstream"
	}
	if scope != "local" && scope != "upstream" {
		response.BadRequest(c, "scope 须为 local|upstream")
		return
	}
	var providerID *int64
	if pid := c.Query("provider_id"); pid != "" {
		if v, err := strconv.ParseInt(pid, 10, 64); err == nil {
			providerID = &v
		}
	}
	includeDeleted := c.Query("include_deleted") == "true"

	items, err := h.rateSvc.CurrentList(c.Request.Context(), scope, providerID, includeDeleted)
	if err != nil {
		response.InternalError(c, "查询失败: "+err.Error())
		return
	}
	out := make([]gin.H, 0, len(items))
	for _, it := range items {
		out = append(out, snapshotToH(it))
	}
	response.Success(c, gin.H{"items": out, "total": len(out)})
}
