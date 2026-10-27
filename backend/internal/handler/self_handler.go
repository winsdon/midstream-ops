package handler

import (
	"sub2api-account-monitor/internal/pkg/response"
	"sub2api-account-monitor/internal/repository"
	"sub2api-account-monitor/internal/service"

	"github.com/gin-gonic/gin"
)

// SelfHandler 本站连接处理器（凭据配置 + 本站分组下拉，供自动建号使用）。
type SelfHandler struct {
	svc *service.SelfService
	pg  *repository.PG
}

// NewSelfHandler 创建 SelfHandler。
func NewSelfHandler(svc *service.SelfService, pg *repository.PG) *SelfHandler {
	return &SelfHandler{svc: svc, pg: pg}
}

// GetSelf GET /self
func (h *SelfHandler) GetSelf(c *gin.Context) {
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

// SaveSelf PUT /self —— 验证失败以 ok=false 返回，前端就地提示，不当作请求错误。
func (h *SelfHandler) SaveSelf(c *gin.Context) {
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

// LocalGroups GET /self/local-groups —— 本站分组下拉选项（读 PG）。
func (h *SelfHandler) LocalGroups(c *gin.Context) {
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
