package handler

import (
	"strconv"
	"time"

	"sub2api-account-monitor/internal/config"
	"sub2api-account-monitor/internal/pkg/response"
	"sub2api-account-monitor/internal/repository"
	"sub2api-account-monitor/internal/service"

	"github.com/gin-gonic/gin"
)

// DashboardHandler 仪表盘处理器。
type DashboardHandler struct {
	statsSvc    *service.StatsService
	providerSvc *service.ProviderService
	cfg         *config.Config
	pg          *repository.PG
}

// NewDashboardHandler 创建 DashboardHandler。
func NewDashboardHandler(statsSvc *service.StatsService, providerSvc *service.ProviderService, cfg *config.Config, pg *repository.PG) *DashboardHandler {
	return &DashboardHandler{statsSvc: statsSvc, providerSvc: providerSvc, cfg: cfg, pg: pg}
}

// Summary GET /dashboard/summary?start=&end=
// 返回区间内收益/成本/利润/请求数 + 供应商/账号计数。缺省为今日。
func (h *DashboardHandler) Summary(c *gin.Context) {
	if !h.pg.Available() {
		response.ServiceUnavailable(c, "线上数据库暂不可用")
		return
	}
	start, end := h.resolveRange(c)

	stats, err := h.statsSvc.ByProvider(c.Request.Context(), start, end)
	if err != nil {
		response.InternalError(c, "查询失败: "+err.Error())
		return
	}

	var revenue, cost, operatingCost float64
	var requests int64
	// 成本完整性：任一供应商缺失上游实扣，则 profit 偏高，须让前端提示
	costComplete := true
	accountsMissing := 0
	for _, s := range stats {
		revenue += s.Revenue
		cost += s.Cost
		operatingCost += s.OperatingCost
		requests += s.Requests
		if !s.CostComplete {
			costComplete = false
			accountsMissing += s.AccountsMissing
		}
	}

	// 供应商与账号计数
	providerCount := 0
	accountCount := 0
	if h.providerSvc != nil {
		providerCount = h.providerSvc.Count(c.Request.Context())
	}
	if accs, err := h.pg.ListActiveAccounts(c.Request.Context()); err == nil {
		accountCount = len(accs)
	}

	// 成本同步状态：让前端能显示数据新鲜度，避免误读缺失成本
	syncStatus, err := h.statsSvc.CostSyncStatus(c.Request.Context())
	if err != nil {
		syncStatus = nil // 状态读取失败不影响主数据
	}

	// 分组贡献：Top N 收益分组 + Top3 集中度（次要数据，失败不影响主指标）
	groups := []gin.H{}
	groupTotal := 0.0
	concentration := 0.0
	if gs, gErr := h.statsSvc.ByGroup(c.Request.Context(), start, end); gErr == nil {
		const topN = 6
		var top3 float64
		for i, g := range gs {
			groupTotal += g.Revenue
			if i < 3 {
				top3 += g.Revenue
			}
			if i < topN {
				groups = append(groups, gin.H{
					"group_id":   g.GroupID,
					"group_name": g.GroupName,
					"revenue":    g.Revenue,
					"requests":   g.Requests,
				})
			}
		}
		if groupTotal > 0 {
			concentration = top3 / groupTotal * 100
		}
	}

	loc := h.cfg.Location
	response.Success(c, gin.H{
		"date":    start.In(loc).Format("2006-01-02"),
		"start":   start.In(loc).Format("2006-01-02"),
		"end":     end.In(loc).Add(-time.Second).Format("2006-01-02"),
		"revenue": revenue,
		"cost":    cost,
		// 自营站的买号/订阅/服务器等站外支出，与上游实扣同为真实成本，一并从利润中扣除
		"operating_cost":      operatingCost,
		"profit":              revenue - cost - operatingCost,
		"requests":            requests,
		"provider_count":      providerCount,
		"account_count":       accountCount,
		"cost_complete":       costComplete,
		"accounts_missing":    accountsMissing,
		"cost_sync":           syncStatus,
		"groups":              groups,
		"group_total":         groupTotal,
		"group_concentration": concentration,
	})
}

// Trend GET /dashboard/trend?days=7|30 或 ?start=&end=
// 区间参数优先；两者都缺省时取近 7 天。
func (h *DashboardHandler) Trend(c *gin.Context) {
	if !h.pg.Available() {
		response.ServiceUnavailable(c, "线上数据库暂不可用")
		return
	}

	// 区间模式：与 summary 共用同一套解析，保证指标卡与趋势图口径一致
	if s, e, ok := h.parseRangeParams(c); ok {
		points, err := h.statsSvc.TrendRange(c.Request.Context(), s, e)
		if err != nil {
			response.InternalError(c, "查询失败: "+err.Error())
			return
		}
		loc := h.cfg.Location
		response.Success(c, gin.H{
			"start":  s.In(loc).Format("2006-01-02"),
			"end":    e.In(loc).Add(-time.Second).Format("2006-01-02"),
			"points": points,
		})
		return
	}

	days := 7
	if d := c.Query("days"); d != "" {
		if v, err := strconv.Atoi(d); err == nil && v > 0 && v <= 90 {
			days = v
		}
	}
	points, err := h.statsSvc.Trend(c.Request.Context(), days)
	if err != nil {
		response.InternalError(c, "查询失败: "+err.Error())
		return
	}
	response.Success(c, gin.H{"days": days, "points": points})
}

// parseRangeParams 解析 start/end 查询参数（YYYY-MM-DD，闭区间）。
// 返回的 end 是右开边界（次日 0 点）。两个参数都缺省时 ok=false。
func (h *DashboardHandler) parseRangeParams(c *gin.Context) (time.Time, time.Time, bool) {
	ss, es := c.Query("start"), c.Query("end")
	if ss == "" && es == "" {
		return time.Time{}, time.Time{}, false
	}
	loc := h.cfg.Location
	startDay, err := time.ParseInLocation("2006-01-02", ss, loc)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	endDay := startDay
	if es != "" {
		if v, err := time.ParseInLocation("2006-01-02", es, loc); err == nil {
			endDay = v
		}
	}
	if endDay.Before(startDay) {
		startDay, endDay = endDay, startDay
	}
	start, _ := h.cfg.DayRange(startDay)
	_, end := h.cfg.DayRange(endDay)
	return start, end, true
}

// resolveRange 解析时间范围：start/end 区间优先，其次单个 date，缺省为今日。
func (h *DashboardHandler) resolveRange(c *gin.Context) (time.Time, time.Time) {
	if s, e, ok := h.parseRangeParams(c); ok {
		return s, e
	}
	if ds := c.Query("date"); ds != "" {
		if t, err := time.ParseInLocation("2006-01-02", ds, h.cfg.Location); err == nil {
			return h.cfg.DayRange(t)
		}
	}
	return h.cfg.TodayRange()
}
