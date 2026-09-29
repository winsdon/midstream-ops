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
	svc    *service.ModelDetectService
	models *service.DetectModelStore
}

// NewModelDetectHandler 创建 ModelDetectHandler。
func NewModelDetectHandler(svc *service.ModelDetectService) *ModelDetectHandler {
	return &ModelDetectHandler{svc: svc}
}

func (h *ModelDetectHandler) SetModelStore(store *service.DetectModelStore) { h.models = store }

func (h *ModelDetectHandler) Models(c *gin.Context) {
	if h.models == nil {
		response.InternalError(c, "模型清单服务未初始化")
		return
	}
	models, err := h.models.List(c.Request.Context())
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, gin.H{"items": models})
}

func (h *ModelDetectHandler) AddModel(c *gin.Context) {
	if h.models == nil {
		response.InternalError(c, "模型清单服务未初始化")
		return
	}
	var req struct {
		Model string `json:"model"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求体格式错误: "+err.Error())
		return
	}
	models, err := h.models.Add(c.Request.Context(), req.Model)
	if err != nil {
		if errors.Is(err, service.ErrInvalidDetectModel) {
			response.BadRequest(c, err.Error())
		} else {
			response.InternalError(c, err.Error())
		}
		return
	}
	response.Success(c, gin.H{"items": models})
}

func (h *ModelDetectHandler) RemoveModel(c *gin.Context) {
	if h.models == nil {
		response.InternalError(c, "模型清单服务未初始化")
		return
	}
	model := c.Query("model")
	if model == "" {
		response.BadRequest(c, "模型名不能为空")
		return
	}
	models, err := h.models.Remove(c.Request.Context(), model)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, gin.H{"items": models})
}

// Checks GET /detect/checks —— 检测项清单（含两个套件，按 suite 区分）与真伪检测的推荐勾选。
func (h *ModelDetectHandler) Checks(c *gin.Context) {
	response.Success(c, gin.H{
		"items":    modeldetect.Checks,
		"defaults": modeldetect.DefaultCheckIDs(modeldetect.SuiteAuthenticity),
		"presets":  modeldetect.Presets,
	})
}

// Baseline GET /detect/baseline —— 当前用户已保存的 CCMax 基准（含脱敏报文）。
func (h *ModelDetectHandler) Baseline(c *gin.Context) {
	owner := c.GetString("username")
	b, err := h.svc.GetBaseline(c.Request.Context(), owner)
	if err != nil {
		if errors.Is(err, repository.ErrBaselineNotFound) {
			response.Success(c, gin.H{"baseline": nil})
			return
		}
		response.InternalError(c, "查询基准失败: "+err.Error())
		return
	}
	response.Success(c, gin.H{"baseline": baselineDTO(b)})
}

// CreateBaseline POST /detect/baseline —— 按账号生成并覆盖基准。
func (h *ModelDetectHandler) CreateBaseline(c *gin.Context) {
	var req service.DetectBaselineRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求体格式错误: "+err.Error())
		return
	}
	req.Owner = c.GetString("username")
	b, err := h.svc.CreateBaseline(c.Request.Context(), req.Owner, req)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, baselineDTO(b))
}

func baselineDTO(b *repository.ModelDetectionBaseline) gin.H {
	dto := gin.H{"id": b.ID, "account_id": b.AccountID, "target_fp": b.TargetFP, "target_name": b.TargetName, "base_url": b.BaseURL, "model": b.Model, "template_version": b.TemplateVersion, "status": b.Status, "quality_ok": b.QualityOK, "input_tokens": b.InputTokens, "output_tokens": b.OutputTokens, "thinking_tokens": b.ThinkingTokens, "thinking_chars": b.ThinkingChars, "ttft_ms": b.TTFTMs, "duration_ms": b.DurationMs, "response_summary": b.ResponseSummary, "error": b.Error, "created_at": b.CreatedAt.Local().Format("2006-01-02 15:04:05")}
	var report map[string]json.RawMessage
	if json.Unmarshal(b.Report, &report) == nil {
		if ex, ok := report["exchange"]; ok {
			dto["exchange"] = json.RawMessage(ex)
		}
	}
	return dto
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
	req.Owner = c.GetString("username")
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
	checks := modeldetect.ResolveSuiteCheckIDs(modeldetect.NormalizeSuite(req.Suite), req.Checks)
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
	h.svc.TrimDetectionHistory(c.Request.Context())
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

// DeleteHistory DELETE /detect/history/:id —— 删除一条真伪检测历史。
func (h *ModelDetectHandler) DeleteHistory(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	repo := h.svc.Repo()
	if repo == nil {
		response.NotFound(c, "检测记录不存在")
		return
	}
	if err := repo.Delete(c.Request.Context(), id); err != nil {
		if errors.Is(err, repository.ErrDetectionNotFound) {
			response.NotFound(c, "检测记录不存在")
			return
		}
		response.InternalError(c, "删除失败: "+err.Error())
		return
	}
	response.Success(c, gin.H{"deleted": id})
}

// IQHistory GET /detect/iq/history —— 智商测试历史分页列表。
func (h *ModelDetectHandler) IQHistory(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)
	items, total, err := h.svc.IQHistory(c.Request.Context(), page, pageSize)
	if err != nil {
		response.InternalError(c, "查询失败: "+err.Error())
		return
	}
	out := make([]gin.H, 0, len(items))
	for _, run := range items {
		out = append(out, iqRunDTO(run))
	}
	response.Paginated(c, out, total, page, pageSize)
}

// IQHistoryDetail GET /detect/iq/history/:id —— 单条智商测试报告（含作品与回复全文）。
func (h *ModelDetectHandler) IQHistoryDetail(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "无效的 id")
		return
	}
	run, err := h.svc.IQHistoryDetail(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrIQRunNotFound) {
			response.NotFound(c, "智商测试记录不存在")
			return
		}
		response.InternalError(c, "查询失败: "+err.Error())
		return
	}
	item := iqRunDTO(run)
	item["report"] = json.RawMessage(run.Report)
	response.Success(c, item)
}

// DeleteIQHistory DELETE /detect/iq/history/:id —— 删除一条智商测试历史。
func (h *ModelDetectHandler) DeleteIQHistory(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteIQHistory(c.Request.Context(), id); err != nil {
		if errors.Is(err, repository.ErrIQRunNotFound) {
			response.NotFound(c, "智商测试记录不存在")
			return
		}
		response.InternalError(c, "删除失败: "+err.Error())
		return
	}
	response.Success(c, gin.H{"deleted": id})
}

// iqRunDTO 智商测试历史行的展示形态。report 体积大，只在详情接口带上。
func iqRunDTO(run *repository.ModelIQRun) gin.H {
	return gin.H{
		"id":           run.ID,
		"account_id":   run.AccountID,
		"account_name": run.AccountName,
		"provider_id":  run.ProviderID,
		"target_fp":    run.TargetFP,
		"target_name":  run.TargetName,
		"base_url":     run.BaseURL,
		"model":        run.Model,
		"passed":       run.Passed,
		"total":        run.Total,
		"results":      json.RawMessage(run.Results),
		"created_at":   run.CreatedAt.Local().Format("2006-01-02 15:04:05"),
	}
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
