package handler

import (
	"strconv"

	"sub2api-account-monitor/internal/pkg/response"
	"sub2api-account-monitor/internal/repository"
	"sub2api-account-monitor/internal/service"

	"github.com/gin-gonic/gin"
)

// PricingHandler 调价规则处理器。
type PricingHandler struct {
	svc      *service.PricingService
	rateRepo *repository.RateRepo
	pg       *repository.PG
}

// NewPricingHandler 创建 PricingHandler。
func NewPricingHandler(svc *service.PricingService, rateRepo *repository.RateRepo, pg *repository.PG) *PricingHandler {
	return &PricingHandler{svc: svc, rateRepo: rateRepo, pg: pg}
}

// GetSelf GET /pricing/self
func (h *PricingHandler) GetSelf(c *gin.Context) {
	info, err := h.svc.GetSelf(c.Request.Context())
	if err != nil {
		response.InternalError(c, "查询失败: "+err.Error())
		return
	}
	response.Success(c, info)
}

type selfRequest struct {
	BaseURL  string  `json:"base_url" binding:"required"`
	Email    string  `json:"email" binding:"required"`
	Password *string `json:"password"` // nil = 不修改
}

// SaveSelf PUT /pricing/self
func (h *PricingHandler) SaveSelf(c *gin.Context) {
	var req selfRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "base_url / email 必填")
		return
	}
	if err := h.svc.SaveSelf(c.Request.Context(), req.BaseURL, req.Email, req.Password); err != nil {
		response.Success(c, gin.H{"ok": false, "error": err.Error()})
		return
	}
	response.Success(c, gin.H{"ok": true})
}

// LocalGroups GET /pricing/local-groups —— 本站分组下拉选项（读 PG）。
func (h *PricingHandler) LocalGroups(c *gin.Context) {
	if !h.pg.Available() {
		response.ServiceUnavailable(c, "线上数据库暂不可用")
		return
	}
	groups, err := h.pg.ListGroups(c.Request.Context())
	if err != nil {
		response.InternalError(c, "查询失败: "+err.Error())
		return
	}
	out := make([]gin.H, 0, len(groups))
	for _, g := range groups {
		out = append(out, gin.H{"id": g.ID, "name": g.Name, "rate": g.RateMultiplier, "platform": g.Platform})
	}
	response.Success(c, gin.H{"items": out})
}

// Preview GET /pricing/rules —— 全部规则 + 应用预览。
func (h *PricingHandler) Preview(c *gin.Context) {
	rows, err := h.svc.Preview(c.Request.Context())
	if err != nil {
		response.InternalError(c, "查询失败: "+err.Error())
		return
	}
	response.Success(c, gin.H{"items": rows, "total": len(rows)})
}

// pricingSourceReq 一个上游数据源。
type pricingSourceReq struct {
	ProviderID    int64  `json:"provider_id"`
	UpstreamGroup string `json:"upstream_group"`
}

type pricingRequest struct {
	LocalGroupID    int64              `json:"local_group_id" binding:"required"`
	LocalGroupName  string             `json:"local_group_name"`
	AutoEnabled     bool               `json:"auto_enabled"`
	PriceSource     string             `json:"price_source"`
	PrimaryProvider *int64             `json:"primary_provider_id"`
	PrimaryGroup    *string            `json:"primary_group"`
	MarkupMode      string             `json:"markup_mode"`
	MarkupValue     float64            `json:"markup_value"`
	FollowThreshold float64            `json:"follow_threshold"`
	MinRate         *float64           `json:"min_rate"`
	MaxRate         *float64           `json:"max_rate"`
	Sources         []pricingSourceReq `json:"sources"`
}

var validPriceSources = map[string]bool{
	"": true, repository.PriceSourcePrimary: true, repository.PriceSourceLowest: true,
	repository.PriceSourceHighest: true, repository.PriceSourceAverage: true,
}
var validMarkupModes = map[string]bool{
	"": true, repository.MarkupFixed: true, repository.MarkupPercentage: true,
}

// validate 校验规则参数，返回错误消息（空 = 通过）。
func (r *pricingRequest) validate() string {
	if !validPriceSources[r.PriceSource] {
		return "price_source 须为 primary|lowest|highest|average"
	}
	if !validMarkupModes[r.MarkupMode] {
		return "markup_mode 须为 fixed|percentage"
	}
	if len(r.Sources) == 0 {
		return "至少需要一个上游数据源"
	}
	if r.FollowThreshold < 0 {
		return "跟随阈值不能为负"
	}
	if r.MinRate != nil && *r.MinRate < 0 {
		return "下限不能为负"
	}
	if r.MaxRate != nil && *r.MaxRate < 0 {
		return "上限不能为负"
	}
	if r.MinRate != nil && r.MaxRate != nil && *r.MinRate > *r.MaxRate {
		return "下限不能大于上限"
	}
	// primary 模式：主上游必填且必须在数据源列表里
	if r.PriceSource == repository.PriceSourcePrimary || r.PriceSource == "" {
		if r.PrimaryProvider == nil || r.PrimaryGroup == nil || *r.PrimaryGroup == "" {
			return "指定主上游模式下必须选择主上游"
		}
		found := false
		for _, s := range r.Sources {
			if s.ProviderID == *r.PrimaryProvider && s.UpstreamGroup == *r.PrimaryGroup {
				found = true
				break
			}
		}
		if !found {
			return "主上游必须是已选数据源之一"
		}
	}
	return ""
}

func (r *pricingRequest) toParams() repository.PricingParams {
	sources := make([]repository.PricingSource, 0, len(r.Sources))
	for _, s := range r.Sources {
		sources = append(sources, repository.PricingSource{ProviderID: s.ProviderID, UpstreamGroup: s.UpstreamGroup})
	}
	return repository.PricingParams{
		LocalGroupID:    r.LocalGroupID,
		LocalGroupName:  r.LocalGroupName,
		AutoEnabled:     r.AutoEnabled,
		PriceSource:     r.PriceSource,
		PrimaryProvider: r.PrimaryProvider,
		PrimaryGroup:    r.PrimaryGroup,
		MarkupMode:      r.MarkupMode,
		MarkupValue:     r.MarkupValue,
		FollowThreshold: r.FollowThreshold,
		MinRate:         r.MinRate,
		MaxRate:         r.MaxRate,
		Sources:         sources,
	}
}

// SaveRule POST /pricing/rules —— 按本站分组新建或更新规则（upsert）。
func (h *PricingHandler) SaveRule(c *gin.Context) {
	var req pricingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "local_group_id 必填")
		return
	}
	if msg := req.validate(); msg != "" {
		response.BadRequest(c, msg)
		return
	}
	r, err := h.svc.Repo().Upsert(c.Request.Context(), req.toParams())
	if err != nil {
		response.InternalError(c, "保存失败: "+err.Error())
		return
	}
	response.Success(c, r)
}

// DeleteRule DELETE /pricing/rules/:id
func (h *PricingHandler) DeleteRule(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	if err := h.svc.Repo().Delete(c.Request.Context(), id); err != nil {
		if err == repository.ErrNotFound {
			response.NotFound(c, "规则不存在")
			return
		}
		response.InternalError(c, "删除失败: "+err.Error())
		return
	}
	response.Success(c, gin.H{"deleted": id})
}

type applyRequest struct {
	Force bool `json:"force"` // 冲突确认后强制应用
}

// ApplyRule POST /pricing/rules/:id/apply —— 手动应用一条规则。
func (h *PricingHandler) ApplyRule(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var req applyRequest
	_ = c.ShouldBindJSON(&req) // body 可空
	if err := h.svc.Apply(c.Request.Context(), id, "manual", req.Force); err != nil {
		response.Success(c, gin.H{"ok": false, "error": err.Error()})
		return
	}
	response.Success(c, gin.H{"ok": true})
}

// ResolveConflict POST /pricing/rules/:id/resolve-conflict
func (h *PricingHandler) ResolveConflict(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	if err := h.svc.ResolveConflict(c.Request.Context(), id); err != nil {
		response.InternalError(c, "处理失败: "+err.Error())
		return
	}
	response.Success(c, gin.H{"ok": true})
}

// Actions GET /pricing/rules/:id/actions —— 调价审计历史。
func (h *PricingHandler) Actions(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	limit := 50
	if l := c.Query("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}
	actions, err := h.svc.Repo().Actions(c.Request.Context(), id, limit)
	if err != nil {
		response.InternalError(c, "查询失败: "+err.Error())
		return
	}
	out := make([]gin.H, 0, len(actions))
	for _, a := range actions {
		out = append(out, gin.H{
			"id":         a.ID,
			"trigger_by": a.TriggerBy,
			"old_rate":   a.OldRate,
			"new_rate":   a.NewRate,
			"status":     a.Status,
			"error":      a.Error,
			"created_at": a.CreatedAt.Format("2006-01-02 15:04:05"),
		})
	}
	response.Success(c, gin.H{"items": out, "total": len(out)})
}

// MappedUpstreams GET /pricing/mapped —— 已对接的上游分组集合（分组倍率页徽章用）。
func (h *PricingHandler) MappedUpstreams(c *gin.Context) {
	m, err := h.svc.Repo().MappedUpstreams(c.Request.Context())
	if err != nil {
		response.InternalError(c, "查询失败: "+err.Error())
		return
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	response.Success(c, gin.H{"keys": keys})
}
