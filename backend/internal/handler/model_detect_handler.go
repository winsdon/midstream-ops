package handler

import (
	"encoding/json"
	"errors"
	"strconv"

	"sub2api-account-monitor/internal/pkg/response"
	"sub2api-account-monitor/internal/repository"
	"sub2api-account-monitor/internal/service"
	"sub2api-account-monitor/internal/service/modeldetect"

	"github.com/gin-gonic/gin"
)

// ModelDetectHandler 上游 Claude 渠道检测处理器。
type ModelDetectHandler struct {
	svc *service.ModelDetectService
}

// NewModelDetectHandler 创建 ModelDetectHandler。
func NewModelDetectHandler(svc *service.ModelDetectService) *ModelDetectHandler {
	return &ModelDetectHandler{svc: svc}
}

// Checks GET /detect/checks —— 检测项清单与推荐勾选。
func (h *ModelDetectHandler) Checks(c *gin.Context) {
	response.Success(c, gin.H{
		"items":    modeldetect.Checks,
		"defaults": modeldetect.DefaultCheckIDs(),
		"presets":  modeldetect.Presets,
	})
}

// Accounts GET /detect/accounts —— 可检测的 anthropic 账号（不含密钥）。
func (h *ModelDetectHandler) Accounts(c *gin.Context) {
	items, err := h.svc.ListAccounts(c.Request.Context())
	if err != nil {
		if errors.Is(err, service.ErrDetectPGUnavailable) {
			response.ServiceUnavailable(c, "线上数据库暂不可用，可改用手动输入目标")
			return
		}
		response.InternalError(c, "查询失败: "+err.Error())
		return
	}
	response.Success(c, gin.H{"items": items, "total": len(items)})
}

// Run POST /detect/run —— 发起检测作业。
func (h *ModelDetectHandler) Run(c *gin.Context) {
	var req service.DetectRunRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求体格式错误: "+err.Error())
		return
	}
	jobID, err := h.svc.Start(c.Request.Context(), req)
	if err != nil {
		if errors.Is(err, service.ErrDetectPGUnavailable) {
			response.ServiceUnavailable(c, err.Error())
			return
		}
		response.BadRequest(c, err.Error())
		return
	}
	checks := modeldetect.ResolveCheckIDs(req.Checks)
	response.Success(c, gin.H{
		"job_id":            jobID,
		"checks":            checks,
		"requests_estimate": modeldetect.TotalRequests(checks) * len(req.Targets),
	})
}

// Job GET /detect/jobs/:id —— 作业进度与已完成结果。
func (h *ModelDetectHandler) Job(c *gin.Context) {
	job, ok := h.svc.Get(c.Param("id"))
	if !ok {
		response.NotFound(c, "作业不存在或已过期")
		return
	}
	response.Success(c, job)
}

// Cancel POST /detect/jobs/:id/cancel —— 取消作业。
func (h *ModelDetectHandler) Cancel(c *gin.Context) {
	if !h.svc.Cancel(c.Param("id")) {
		response.NotFound(c, "作业不存在或已过期")
		return
	}
	response.Success(c, gin.H{"cancelled": true})
}

// Retry POST /detect/jobs/:id/retry —— 重试请求失败的检测项。
func (h *ModelDetectHandler) Retry(c *gin.Context) {
	var req service.DetectRetryRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			response.BadRequest(c, "请求体格式错误: "+err.Error())
			return
		}
	}
	n, err := h.svc.Retry(c.Param("id"), req)
	if err != nil {
		if errors.Is(err, service.ErrDetectJobNotFound) {
			response.NotFound(c, err.Error())
			return
		}
		if errors.Is(err, service.ErrDetectJobBusy) {
			response.Conflict(c, err.Error())
			return
		}
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, gin.H{"retried": n})
}

// History GET /detect/history —— 历史判定分页列表。
func (h *ModelDetectHandler) History(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)
	f := repository.DetectionFilter{Page: page, PageSize: pageSize,
		TargetFP: c.Query("target_fp"), Label: c.Query("label")}
	if v := c.Query("account_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			f.AccountID = &id
		}
	}
	items, total, err := h.svc.Repo().List(c.Request.Context(), f)
	if err != nil {
		response.InternalError(c, "查询失败: "+err.Error())
		return
	}
	out := make([]gin.H, 0, len(items))
	for _, d := range items {
		out = append(out, detectionDTO(d))
	}
	response.Paginated(c, out, total, page, pageSize)
}

// HistoryDetail GET /detect/history/:id —— 单条完整报告。
func (h *ModelDetectHandler) HistoryDetail(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "无效的 id")
		return
	}
	d, err := h.svc.Repo().Get(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrDetectionNotFound) {
			response.NotFound(c, "检测记录不存在")
			return
		}
		response.InternalError(c, "查询失败: "+err.Error())
		return
	}
	item := detectionDTO(d)
	item["report"] = json.RawMessage(d.Report)
	response.Success(c, item)
}

// detectionDTO 历史行的展示形态。report 体积大，只在详情接口带上。
func detectionDTO(d *repository.ModelDetection) gin.H {
	return gin.H{
		"id":                 d.ID,
		"account_id":         d.AccountID,
		"account_name":       d.AccountName,
		"provider_id":        d.ProviderID,
		"target_fp":          d.TargetFP,
		"target_name":        d.TargetName,
		"base_url":           d.BaseURL,
		"model":              d.Model,
		"label":              d.Label,
		"confidence":         d.Confidence,
		"authenticity_score": d.AuthenticityScore,
		"authenticity_grade": d.AuthenticityGrade,
		"scores":             json.RawMessage(d.Scores),
		"created_at":         d.CreatedAt.Local().Format("2006-01-02 15:04:05"),
	}
}
