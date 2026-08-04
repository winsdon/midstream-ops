package handler

import (
	"sub2api-account-monitor/internal/pkg/response"
	"sub2api-account-monitor/internal/repository"
	"sub2api-account-monitor/internal/service"

	"github.com/gin-gonic/gin"
)

// ProvisionHandler 自动建号处理器。
type ProvisionHandler struct {
	svc *service.ProvisionService
}

// NewProvisionHandler 创建 ProvisionHandler。
func NewProvisionHandler(svc *service.ProvisionService) *ProvisionHandler {
	return &ProvisionHandler{svc: svc}
}

// List GET /provision/connections?include_failed=
func (h *ProvisionHandler) List(c *gin.Context) {
	items, err := h.svc.Repo().List(c.Request.Context(), c.Query("include_failed") == "true")
	if err != nil {
		response.InternalError(c, "查询失败: "+err.Error())
		return
	}
	response.Success(c, gin.H{"items": items, "total": len(items)})
}

// Connected GET /provision/connected —— 已建号的上游分组集合（分组倍率页徽章用）。
func (h *ProvisionHandler) Connected(c *gin.Context) {
	m, err := h.svc.Repo().ConnectedUpstreams(c.Request.Context())
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

type connectRequest struct {
	ProviderID    int64   `json:"provider_id" binding:"required"`
	UpstreamGroup string  `json:"upstream_group" binding:"required"`
	LocalGroupIDs []int64 `json:"local_group_ids" binding:"required"`
	OperationID   string  `json:"operation_id"`
}

// Connect POST /provision/connect —— 自动建号（上游建 key + 本站建账号）。
func (h *ProvisionHandler) Connect(c *gin.Context) {
	var req connectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "provider_id / upstream_group / local_group_ids 必填")
		return
	}
	conn, err := h.svc.Connect(c.Request.Context(), service.ConnectRequest{
		ProviderID:    req.ProviderID,
		UpstreamGroup: req.UpstreamGroup,
		LocalGroupIDs: req.LocalGroupIDs,
		OperationID:   req.OperationID,
	})
	if err != nil {
		// 建号失败是业务结果而非系统错误：返回 200 + ok=false，前端展示明细
		response.Success(c, gin.H{"ok": false, "error": err.Error()})
		return
	}
	response.Success(c, gin.H{"ok": true, "connection": conn})
}

type bindRequest struct {
	ProviderID     int64   `json:"provider_id" binding:"required"`
	UpstreamGroup  string  `json:"upstream_group" binding:"required"`
	UpstreamKeyID  int64   `json:"upstream_key_id"`
	LocalAccountID int64   `json:"local_account_id" binding:"required"`
	LocalGroupIDs  []int64 `json:"local_group_ids"`
	OperationID    string  `json:"operation_id"`
}

// Bind POST /provision/bind —— 关联已有资源（不创建远端资源）。
func (h *ProvisionHandler) Bind(c *gin.Context) {
	var req bindRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "provider_id / upstream_group / local_account_id 必填")
		return
	}
	conn, err := h.svc.Bind(c.Request.Context(), service.BindRequest{
		ProviderID:     req.ProviderID,
		UpstreamGroup:  req.UpstreamGroup,
		UpstreamKeyID:  req.UpstreamKeyID,
		LocalAccountID: req.LocalAccountID,
		LocalGroupIDs:  req.LocalGroupIDs,
		OperationID:    req.OperationID,
	})
	if err != nil {
		response.Success(c, gin.H{"ok": false, "error": err.Error()})
		return
	}
	response.Success(c, gin.H{"ok": true, "connection": conn})
}

type disconnectRequest struct {
	DeleteRemote bool `json:"delete_remote"`
}

// Disconnect DELETE /provision/connections/:id?delete_remote=
func (h *ProvisionHandler) Disconnect(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var req disconnectRequest
	_ = c.ShouldBindJSON(&req) // body 可空
	deleteRemote := req.DeleteRemote || c.Query("delete_remote") == "true"

	if err := h.svc.Disconnect(c.Request.Context(), id, deleteRemote); err != nil {
		if err == repository.ErrNotFound {
			response.NotFound(c, "连接不存在")
			return
		}
		response.Success(c, gin.H{"ok": false, "error": err.Error()})
		return
	}
	response.Success(c, gin.H{"ok": true, "deleted": id})
}
