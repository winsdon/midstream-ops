package handler

import (
	"errors"
	"time"

	"sub2api-account-monitor/internal/config"
	"sub2api-account-monitor/internal/pkg/response"
	"sub2api-account-monitor/internal/repository"
	"sub2api-account-monitor/internal/service"

	"github.com/gin-gonic/gin"
)

// OperatingCostHandler 自营站运营成本处理器。
type OperatingCostHandler struct {
	svc *service.OperatingCostService
	cfg *config.Config
}

// NewOperatingCostHandler 创建 OperatingCostHandler。
func NewOperatingCostHandler(svc *service.OperatingCostService, cfg *config.Config) *OperatingCostHandler {
	return &OperatingCostHandler{svc: svc, cfg: cfg}
}

// operatingCostDTO 运营成本输出。
type operatingCostDTO struct {
	ID         int64   `json:"id"`
	ProviderID int64   `json:"provider_id"`
	Category   string  `json:"category"`
	Amount     float64 `json:"amount"`
	Currency   string  `json:"currency"`
	OccurredOn string  `json:"occurred_on"`
	Note       string  `json:"note"`
	Operator   string  `json:"operator"`
	CreatedAt  string  `json:"created_at"`
}

func toOperatingCostDTO(c repository.OperatingCost) operatingCostDTO {
	return operatingCostDTO{
		ID:         c.ID,
		ProviderID: c.ProviderID,
		Category:   c.Category,
		Amount:     c.Amount,
		Currency:   c.Currency,
		OccurredOn: c.OccurredOn,
		Note:       c.Note,
		Operator:   c.Operator,
		CreatedAt:  c.CreatedAt,
	}
}

// respondOpCostErr 统一错误映射：入参不合法/非自营站 → 400，记录不存在 → 404，其余 500。
func respondOpCostErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidInput):
		response.BadRequest(c, err.Error())
	case errors.Is(err, service.ErrNotSelfOperated):
		// 语义是「该站点的成本已由上游实扣表达，再记会重复计算」，不是入参格式错
		response.BadRequest(c, err.Error())
	case errors.Is(err, repository.ErrNotFound):
		response.NotFound(c, "站点或记录不存在")
	default:
		response.InternalError(c, err.Error())
	}
}

// operatingCostRequest 记一笔的入参。
type operatingCostRequest struct {
	Category   string  `json:"category"`
	Amount     float64 `json:"amount"`
	OccurredOn string  `json:"occurred_on"` // YYYY-MM-DD，留空取今天
	Note       string  `json:"note"`
}

// List GET /providers/:id/operating-costs?start=&end=
//
// 区间缺省为「本月至今」：运营成本按月结算是最常见的口径，
// 且避免默认拉全量历史让弹窗打开变慢。
func (h *OperatingCostHandler) List(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	start, end := h.resolveDates(c)
	items, total, err := h.svc.List(c.Request.Context(), id, start, end)
	if err != nil {
		respondOpCostErr(c, err)
		return
	}
	out := make([]operatingCostDTO, 0, len(items))
	for _, it := range items {
		out = append(out, toOperatingCostDTO(it))
	}
	response.Success(c, gin.H{
		"items": out,
		"total": total,
		"start": start,
		"end":   end,
	})
}

// Create POST /providers/:id/operating-costs
func (h *OperatingCostHandler) Create(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var req operatingCostRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求体格式不正确")
		return
	}
	cost, err := h.svc.Create(c.Request.Context(), repository.OperatingCostParams{
		ProviderID: id,
		Category:   req.Category,
		Amount:     req.Amount,
		OccurredOn: req.OccurredOn,
		Note:       req.Note,
		Operator:   operatorOf(c),
	})
	if err != nil {
		respondOpCostErr(c, err)
		return
	}
	response.Success(c, toOperatingCostDTO(*cost))
}

// Delete DELETE /operating-costs/:id
func (h *OperatingCostHandler) Delete(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	if err := h.svc.Delete(c.Request.Context(), id); err != nil {
		respondOpCostErr(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": id})
}

// resolveDates 解析 start/end（YYYY-MM-DD 闭区间），缺省为本月至今。
//
// 非法格式回落默认值而非报错：这里是展示区间，静默给个合理默认比 400 更友好；
// 写入路径的 occurred_on 则严格校验（见 OperatingCostService.validateParams）。
func (h *OperatingCostHandler) resolveDates(c *gin.Context) (string, string) {
	const layout = "2006-01-02"
	now := time.Now().In(h.cfg.Location)
	start := now.AddDate(0, 0, -now.Day()+1).Format(layout)
	end := now.Format(layout)

	if v := c.Query("start"); v != "" {
		if _, err := time.Parse(layout, v); err == nil {
			start = v
		}
	}
	if v := c.Query("end"); v != "" {
		if _, err := time.Parse(layout, v); err == nil {
			end = v
		}
	}
	if start > end {
		start, end = end, start
	}
	return start, end
}
