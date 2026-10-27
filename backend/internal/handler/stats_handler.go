package handler

import (
	"sub2api-account-monitor/internal/config"
	"sub2api-account-monitor/internal/pkg/response"
	"sub2api-account-monitor/internal/repository"
	"sub2api-account-monitor/internal/service"

	"github.com/gin-gonic/gin"
)

// StatsHandler 收益统计处理器。
type StatsHandler struct {
	statsSvc *service.StatsService
	cfg      *config.Config
	pg       *repository.PG
}

// NewStatsHandler 创建 StatsHandler。
func NewStatsHandler(statsSvc *service.StatsService, cfg *config.Config, pg *repository.PG) *StatsHandler {
	return &StatsHandler{statsSvc: statsSvc, cfg: cfg, pg: pg}
}

// ByProvider GET /stats/providers?start=&end=
func (h *StatsHandler) ByProvider(c *gin.Context) {
	if !h.pg.Available() {
		response.ServiceUnavailable(c, "线上数据库暂不可用")
		return
	}
	start, end := resolveDayRange(c, h.cfg)
	stats, err := h.statsSvc.ByProvider(c.Request.Context(), start, end)
	if err != nil {
		response.InternalError(c, "查询失败: "+err.Error())
		return
	}
	startDate, endDate := dateBounds(h.cfg, start, end)
	response.Success(c, gin.H{
		"start": startDate,
		"end":   endDate,
		"items": stats,
	})
}

// ByGroup GET /stats/groups?start=&end=
func (h *StatsHandler) ByGroup(c *gin.Context) {
	if !h.pg.Available() {
		response.ServiceUnavailable(c, "线上数据库暂不可用")
		return
	}
	start, end := resolveDayRange(c, h.cfg)
	stats, err := h.statsSvc.ByGroup(c.Request.Context(), start, end)
	if err != nil {
		response.InternalError(c, "查询失败: "+err.Error())
		return
	}
	startDate, endDate := dateBounds(h.cfg, start, end)
	response.Success(c, gin.H{
		"start": startDate,
		"end":   endDate,
		"items": stats,
	})
}

// ByUser GET /stats/users?start=&end=
func (h *StatsHandler) ByUser(c *gin.Context) {
	if !h.pg.Available() {
		response.ServiceUnavailable(c, "线上数据库暂不可用")
		return
	}
	start, end := resolveDayRange(c, h.cfg)
	stats, err := h.statsSvc.ByUser(c.Request.Context(), start, end)
	if err != nil {
		response.InternalError(c, "查询失败: "+err.Error())
		return
	}
	startDate, endDate := dateBounds(h.cfg, start, end)
	response.Success(c, gin.H{
		"start": startDate,
		"end":   endDate,
		"items": stats,
	})
}
