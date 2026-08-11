package handler

import (
	"context"
	"strconv"
	"time"

	"sub2api-account-monitor/internal/config"
	"sub2api-account-monitor/internal/pkg/response"
	"sub2api-account-monitor/internal/repository"
	"sub2api-account-monitor/internal/service"

	"github.com/gin-gonic/gin"
)

// StabilityHandler 稳定性监控处理器。
type StabilityHandler struct {
	probeSvc *service.ProbeService
	pg       *repository.PG
	cfg      *config.Config
}

// NewStabilityHandler 创建 StabilityHandler。
func NewStabilityHandler(probeSvc *service.ProbeService, pg *repository.PG, cfg *config.Config) *StabilityHandler {
	return &StabilityHandler{probeSvc: probeSvc, pg: pg, cfg: cfg}
}

// HealthStates GET /stability/health —— 全部账号健康状态 + 当日探测预算。
func (h *StabilityHandler) HealthStates(c *gin.Context) {
	states, err := h.probeSvc.HealthRepo().List(c.Request.Context())
	if err != nil {
		response.InternalError(c, "查询失败: "+err.Error())
		return
	}
	day := time.Now().In(h.cfg.Location).Format("2006-01-02")
	used, _ := h.probeSvc.HealthRepo().BudgetUsed(c.Request.Context(), day)
	_, nameByID := h.providerLookup(c.Request.Context())

	out := make([]gin.H, 0, len(states))
	for _, s := range states {
		item := gin.H{
			"account_id":            s.AccountID,
			"account_name":          s.AccountName,
			"provider_id":           s.ProviderID,
			"state":                 s.State,
			"consecutive_failures":  s.ConsecutiveFailures,
			"consecutive_successes": s.ConsecutiveSuccesses,
			"weight_percent":        s.WeightPercent,
		}
		// provider_id 存的是探测时的归属快照，可能已过期或为空；名字取现值表，
		// 查不到就留空字符串，前端按「未归属」显示。
		if s.ProviderID != nil {
			item["provider_name"] = nameByID[*s.ProviderID]
		} else {
			item["provider_name"] = ""
		}
		if s.CooldownUntil != nil {
			item["cooldown_until"] = s.CooldownUntil.Local().Format("2006-01-02 15:04:05")
		}
		if s.LastProbeAt != nil {
			item["last_probe_at"] = s.LastProbeAt.Local().Format("2006-01-02 15:04:05")
		}
		out = append(out, item)
	}
	response.Success(c, gin.H{"items": out, "total": len(out), "budget_used": used})
}

// HealthEvents GET /stability/health/events?account_id= —— 状态迁移时间线。
func (h *StabilityHandler) HealthEvents(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Query("account_id"), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "account_id 必填")
		return
	}
	limit := 50
	if l := c.Query("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}
	events, err := h.probeSvc.HealthRepo().Events(c.Request.Context(), accountID, limit)
	if err != nil {
		response.InternalError(c, "查询失败: "+err.Error())
		return
	}
	out := make([]gin.H, 0, len(events))
	for _, e := range events {
		out = append(out, gin.H{
			"id":         e.ID,
			"from_state": e.FromState,
			"to_state":   e.ToState,
			"reason":     e.Reason,
			"detail":     e.Detail,
			"created_at": e.CreatedAt.Local().Format("2006-01-02 15:04:05"),
		})
	}
	response.Success(c, gin.H{"items": out, "total": len(out)})
}

// SetHealthDisabled PUT /stability/health/:id/disabled —— 人工启停账号探测。
func (h *StabilityHandler) SetHealthDisabled(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "无效的 id")
		return
	}
	var req struct {
		Disabled bool `json:"disabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "disabled 必填")
		return
	}
	if err := h.probeSvc.HealthRepo().SetDisabled(c.Request.Context(), accountID, req.Disabled); err != nil {
		response.InternalError(c, "更新失败: "+err.Error())
		return
	}
	response.Success(c, gin.H{"account_id": accountID, "disabled": req.Disabled})
}

// Passive GET /stability/passive?minutes=
// 被动口径：真实流量的耗时/首字分位数（无成功率）。
func (h *StabilityHandler) Passive(c *gin.Context) {
	if !h.pg.Available() {
		response.ServiceUnavailable(c, "线上数据库暂不可用")
		return
	}
	window, minutes := parseWindow(c, 24*60)
	since := time.Now().Add(-window)
	rows, err := h.pg.PassiveStability(c.Request.Context(), since)
	if err != nil {
		response.InternalError(c, "查询失败: "+err.Error())
		return
	}
	linkMap, nameByID := h.providerLookup(c.Request.Context())
	out := make([]gin.H, 0, len(rows))
	for _, r := range rows {
		item := gin.H{
			"account_id":      r.AccountID,
			"account_name":    r.AccountName,
			"platform":        r.Platform,
			"requests":        r.Requests,
			"duration_p50":    r.DurationP50,
			"duration_p95":    r.DurationP95,
			"first_token_p50": r.FirstTokP50,
			"first_token_p95": r.FirstTokP95,
		}
		attachProvider(item, r.AccountID, linkMap, nameByID)
		out = append(out, item)
	}
	response.Success(c, gin.H{"minutes": minutes, "items": out, "note": "被动口径仅成功请求，无成功率"})
}

// Probes GET /stability/probes?account_id=&page=&page_size=
func (h *StabilityHandler) Probes(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)
	f := repository.ProbeFilter{Page: page, PageSize: pageSize}
	if aid := c.Query("account_id"); aid != "" {
		if v, err := strconv.ParseInt(aid, 10, 64); err == nil {
			f.AccountID = &v
		}
	}
	items, total, err := h.probeSvc.Repo().List(c.Request.Context(), f)
	if err != nil {
		response.InternalError(c, "查询失败: "+err.Error())
		return
	}
	out := make([]gin.H, 0, len(items))
	for _, pr := range items {
		out = append(out, probeDTO(pr))
	}
	response.Paginated(c, out, total, page, pageSize)
}

// ProbeSummary GET /stability/probes/summary?minutes=
func (h *StabilityHandler) ProbeSummary(c *gin.Context) {
	window, minutes := parseWindow(c, 24*60)
	since := time.Now().Add(-window)
	rows, err := h.probeSvc.Repo().Summary(c.Request.Context(), since)
	if err != nil {
		response.InternalError(c, "查询失败: "+err.Error())
		return
	}
	linkMap, nameByID := h.providerLookup(c.Request.Context())
	out := make([]gin.H, 0, len(rows))
	for _, s := range rows {
		var successRate float64
		if s.Total > 0 {
			successRate = float64(s.SuccessCnt) / float64(s.Total) * 100
		}
		var lastAt any
		if s.LastAt != nil {
			lastAt = s.LastAt.Format("2006-01-02 15:04:05")
		}
		item := gin.H{
			"account_id":    s.AccountID,
			"account_name":  s.AccountName,
			"platform":      s.Platform,
			"total":         s.Total,
			"success_count": s.SuccessCnt,
			"success_rate":  successRate,
			"avg_ttft_ms":   s.AvgTTFT,
			"avg_total_ms":  s.AvgTotal,
			"last_success":  s.LastSuccess,
			"last_at":       lastAt,
		}
		attachProvider(item, s.AccountID, linkMap, nameByID)
		out = append(out, item)
	}
	response.Success(c, gin.H{"minutes": minutes, "items": out})
}

// ProbeTrend GET /stability/probes/trend?account_id=&minutes=
func (h *StabilityHandler) ProbeTrend(c *gin.Context) {
	aid, err := strconv.ParseInt(c.Query("account_id"), 10, 64)
	if err != nil || aid <= 0 {
		response.BadRequest(c, "account_id 必填")
		return
	}
	window, minutes := parseWindow(c, 24*60)
	since := time.Now().Add(-window)
	items, err := h.probeSvc.Repo().Trend(c.Request.Context(), aid, since)
	if err != nil {
		response.InternalError(c, "查询失败: "+err.Error())
		return
	}
	out := make([]gin.H, 0, len(items))
	for _, pr := range items {
		out = append(out, probeDTO(pr))
	}
	response.Success(c, gin.H{"account_id": aid, "minutes": minutes, "items": out})
}

type runProbeRequest struct {
	AccountID  *int64 `json:"account_id"`
	ProviderID *int64 `json:"provider_id"`
}

// RunProbe POST /stability/probe/run  {account_id} 同步 / {provider_id} 异步
func (h *StabilityHandler) RunProbe(c *gin.Context) {
	if !h.pg.Available() {
		response.ServiceUnavailable(c, "线上数据库暂不可用")
		return
	}
	var req runProbeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "account_id 或 provider_id 必填")
		return
	}

	if req.AccountID != nil {
		// 同步探测单个账号（模型取所属供应商的 probe_model）
		acc, err := h.findAccount(c, *req.AccountID)
		if err != nil {
			response.NotFound(c, "账号不存在或不可探测")
			return
		}
		var modelOverride *string
		// 探测模型取自所属供应商（关联表为唯一真相）；未关联则用全局默认模型
		if pid, lerr := h.probeSvc.LinkRepo().ProviderIDOf(c.Request.Context(), acc.ID); lerr == nil && pid > 0 {
			if p, perr := h.probeSvc.ProviderRepo().GetByID(c.Request.Context(), pid); perr == nil {
				modelOverride = p.ProbeModel
			}
		}
		pr, err := h.probeSvc.RunProbeForAccount(c.Request.Context(), *acc, modelOverride, "manual")
		if err != nil {
			response.InternalError(c, "探测失败: "+err.Error())
			return
		}
		response.Success(c, probeDTO(pr))
		return
	}

	if req.ProviderID != nil {
		// 异步探测供应商下所有账号
		p, err := h.probeSvc.ProviderRepo().GetByID(c.Request.Context(), *req.ProviderID)
		if err != nil {
			response.NotFound(c, "供应商不存在")
			return
		}
		go h.probeSvc.RunProbesForProvider(context.Background(), p)
		response.Success(c, gin.H{"queued": true, "provider": p.Name})
		return
	}

	response.BadRequest(c, "account_id 或 provider_id 必填")
}

// findAccount 从 PG 探测候选中查找账号。
func (h *StabilityHandler) findAccount(c *gin.Context, accountID int64) (*repository.PGAccount, error) {
	accs, err := h.pg.ListProbeCandidates(c.Request.Context())
	if err != nil {
		return nil, err
	}
	for _, a := range accs {
		if a.ID == accountID {
			return &a, nil
		}
	}
	return nil, strconv.ErrSyntax
}

// providerLookup 取账号归属映射与供应商名表（两者都只读本地 monitor 库）。
//
// 失败返回 nil map —— 对 nil map 取值得零值，前端渲染成「未归属」，
// 比让整个接口 500 更合适：分位数本身仍然有价值，归属只是筛选维度。
func (h *StabilityHandler) providerLookup(ctx context.Context) (map[int64]int64, map[int64]string) {
	linkMap, _ := h.probeSvc.LinkRepo().AccountToProvider(ctx)
	nameByID, _ := h.probeSvc.ProviderRepo().NameByID(ctx)
	return linkMap, nameByID
}

// attachProvider 往 DTO 补归属字段。
//
// 归属一律取 provider_accounts 的现值，而不是 probe_results.provider_id 快照：
// 后者是探测发生时的归属，账号事后被别家「抢」走时历史行仍带旧 id，
// 按它分组会让同一账号在「每账号一行」的表里裂成两行。
//
// 未关联的账号两字段留空（0 / ""），前端按「未归属」渲染并可单独筛选，
// 与收益统计的 (未归属) 桶语义一致。
func attachProvider(item gin.H, accountID int64, linkMap map[int64]int64, nameByID map[int64]string) {
	pid := linkMap[accountID]
	item["provider_id"] = pid
	item["provider_name"] = nameByID[pid]
}

func probeDTO(pr *repository.ProbeResult) gin.H {
	return gin.H{
		"id":           pr.ID,
		"provider_id":  pr.ProviderID,
		"account_id":   pr.AccountID,
		"account_name": pr.AccountName,
		"platform":     pr.Platform,
		"model":        pr.Model,
		"base_url":     pr.BaseURL,
		"source":       pr.Source,
		"success":      pr.Success,
		"status_code":  pr.StatusCode,
		"ttft_ms":      pr.TTFTMs,
		"total_ms":     pr.TotalMs,
		"error":        pr.Error,
		"created_at":   pr.CreatedAt.Format("2006-01-02 15:04:05"),
	}
}

// maxWindowMinutes 与旧 hours 上限（720 小时 = 30 天）等价。
const maxWindowMinutes = 720 * 60

// parseWindow 解析统计窗口，返回时长与回显给前端的分钟数。
//
// 优先 minutes：稳定性页档位下探到 5 分钟，整数小时表达不了。
// 保留 hours 分支是为了兼容外部脚本；上限沿用原来的 30 天，
// 免得有人传天文数字把远端 PG 的 percentile_cont 拖死。
func parseWindow(c *gin.Context, defMinutes int) (time.Duration, int) {
	m := defMinutes
	if v := c.Query("minutes"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= maxWindowMinutes {
			m = n
		}
	} else if v := c.Query("hours"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 720 {
			m = n * 60
		}
	}
	return time.Duration(m) * time.Minute, m
}
