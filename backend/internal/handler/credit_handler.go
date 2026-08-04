package handler

import (
	"errors"
	"strconv"
	"time"

	"sub2api-account-monitor/internal/pkg/response"
	"sub2api-account-monitor/internal/repository"
	"sub2api-account-monitor/internal/service"

	"github.com/gin-gonic/gin"
)

// CreditHandler 授信台账处理器（管理端）。
type CreditHandler struct {
	svc *service.CreditService
	pg  *repository.PG // 仅为建档下拉读线上 users 表
}

// NewCreditHandler 创建 CreditHandler。
func NewCreditHandler(svc *service.CreditService, pg *repository.PG) *CreditHandler {
	return &CreditHandler{svc: svc, pg: pg}
}

// customerDTO 客户输出（管理端）。
//
// 含 admin_note，因此绝不可用于客户侧接口——嵌入端有独立的精简 DTO。
type customerDTO struct {
	ID            int64   `json:"id"`
	Sub2apiUserID string  `json:"sub2api_user_id"`
	DisplayName   string  `json:"display_name"`
	Email         string  `json:"email"`
	Note          string  `json:"note"`
	AdminNote     string  `json:"admin_note"`
	CreditLimit   float64 `json:"credit_limit"`
	Outstanding   float64 `json:"outstanding"`
	Available     float64 `json:"available"`
	UsageRatio    float64 `json:"usage_ratio"`
	Status        string  `json:"status"`
	AlertLevel    int     `json:"alert_level"`
	AlertAt       *string `json:"alert_at"`
	LastEntryAt   *string `json:"last_entry_at"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
}

// ledgerEntryDTO 台账分录输出。
type ledgerEntryDTO struct {
	ID          int64   `json:"id"`
	CustomerID  int64   `json:"customer_id"`
	EntryType   string  `json:"entry_type"`
	Amount      float64 `json:"amount"`
	Currency    string  `json:"currency"`
	OccurredAt  string  `json:"occurred_at"`
	Note        string  `json:"note"`
	ExternalRef string  `json:"external_ref"`
	Operator    string  `json:"operator"`
	ReversedOf  *int64  `json:"reversed_of"`
	CreatedAt   string  `json:"created_at"`
}

func fmtTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

func toCustomerDTO(c *repository.Customer) customerDTO {
	return customerDTO{
		ID:            c.ID,
		Sub2apiUserID: c.Sub2apiUserID,
		DisplayName:   c.DisplayName,
		Email:         c.Email,
		Note:          c.Note,
		AdminNote:     c.AdminNote,
		CreditLimit:   c.CreditLimit,
		Outstanding:   c.Outstanding,
		Available:     c.Available(),
		UsageRatio:    c.UsageRatio(),
		Status:        c.Status,
		AlertLevel:    c.AlertLevel,
		AlertAt:       fmtTimePtr(c.AlertAt),
		LastEntryAt:   fmtTimePtr(c.LastEntryAt),
		CreatedAt:     c.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:     c.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func toLedgerDTO(e *repository.LedgerEntry) ledgerEntryDTO {
	return ledgerEntryDTO{
		ID:          e.ID,
		CustomerID:  e.CustomerID,
		EntryType:   e.EntryType,
		Amount:      e.Amount,
		Currency:    e.Currency,
		OccurredAt:  e.OccurredAt.UTC().Format(time.RFC3339),
		Note:        e.Note,
		ExternalRef: e.ExternalRef,
		Operator:    e.Operator,
		ReversedOf:  e.ReversedOf,
		CreatedAt:   e.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// respondCreditErr 统一错误映射：入参不合法 → 400，已建档 → 409，记录不存在 → 404，其余 500。
func respondCreditErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidInput):
		response.BadRequest(c, err.Error())
	case errors.Is(err, repository.ErrDuplicate):
		// 该 sub2api 用户已建档：语义是「冲突」而非「入参错」，前端据此引导去编辑
		response.Conflict(c, "该 sub2api 用户已建档，请直接编辑其档案")
	case errors.Is(err, repository.ErrNotFound):
		response.NotFound(c, "客户或分录不存在")
	default:
		response.InternalError(c, err.Error())
	}
}

// operatorOf 取当前管理员用户名（Auth 中间件写入）。
func operatorOf(c *gin.Context) string { return c.GetString("username") }

// Summary GET /credit/summary
func (h *CreditHandler) Summary(c *gin.Context) {
	s, err := h.svc.Summary(c.Request.Context())
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, s)
}

// sub2apiUserDTO 建档下拉的用户选项。
//
// 不含 password_hash 等任何敏感字段；balance 仅作定额度时的参考。
type sub2apiUserDTO struct {
	// ID 是字符串：customers.sub2api_user_id 是 TEXT 列，若回数字，前端提交时
	// 后端 JSON 解码会直接 400。在最靠近数据源处定型，省掉前端每处 String()。
	ID        string  `json:"id"`
	Email     string  `json:"email"`
	Role      string  `json:"role"`
	Balance   float64 `json:"balance"`
	Status    string  `json:"status"`
	CreatedAt string  `json:"created_at"`
	Enrolled  bool    `json:"enrolled"` // 已建档：前端禁选，避免撞 UNIQUE
}

// Sub2apiUsers GET /credit/sub2api-users —— 建档下拉数据源（读线上 users 表）。
//
// PG 不可用时返回 503，前端据此降级为手填输入框：线上库挂了就建不了档是不可接受的。
func (h *CreditHandler) Sub2apiUsers(c *gin.Context) {
	if !h.pg.Available() {
		response.ServiceUnavailable(c, "线上数据库暂不可用")
		return
	}
	users, err := h.pg.ListUsers(c.Request.Context())
	if err != nil {
		response.InternalError(c, "查询失败: "+err.Error())
		return
	}
	enrolled, err := h.svc.EnrolledUserIDs(c.Request.Context())
	if err != nil {
		// 已建档标记只是防误操作的增强项，失败时退化为「全部可选」，
		// 真撞 UNIQUE 由 409 兜底，不因此让整个下拉不可用
		enrolled = map[string]bool{}
	}
	out := make([]sub2apiUserDTO, 0, len(users))
	for _, u := range users {
		id := strconv.FormatInt(u.ID, 10)
		out = append(out, sub2apiUserDTO{
			ID: id, Email: u.Email, Role: u.Role, Balance: u.Balance,
			Status: u.Status, CreatedAt: u.CreatedAt, Enrolled: enrolled[id],
		})
	}
	response.Success(c, gin.H{"items": out})
}

// ListCustomers GET /credit/customers
func (h *CreditHandler) ListCustomers(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)
	items, total, err := h.svc.ListCustomers(c.Request.Context(), repository.CustomerFilter{
		Keyword:  c.Query("keyword"),
		Status:   c.Query("status"),
		Sort:     c.Query("sort"),
		Order:    c.Query("order"),
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	out := make([]customerDTO, 0, len(items))
	for _, it := range items {
		out = append(out, toCustomerDTO(it))
	}
	response.Paginated(c, out, total, page, pageSize)
}

// GetCustomer GET /credit/customers/:id
func (h *CreditHandler) GetCustomer(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	cust, err := h.svc.GetCustomer(c.Request.Context(), id)
	if err != nil {
		respondCreditErr(c, err)
		return
	}
	response.Success(c, toCustomerDTO(cust))
}

// customerReq 新建/编辑客户请求体。
type customerReq struct {
	Sub2apiUserID string  `json:"sub2api_user_id"`
	DisplayName   string  `json:"display_name"`
	Email         string  `json:"email"`
	Note          string  `json:"note"`
	AdminNote     string  `json:"admin_note"`
	CreditLimit   float64 `json:"credit_limit"`
	Status        string  `json:"status"`
}

func (r customerReq) toParams() repository.CustomerParams {
	return repository.CustomerParams{
		Sub2apiUserID: r.Sub2apiUserID,
		DisplayName:   r.DisplayName,
		Email:         r.Email,
		Note:          r.Note,
		AdminNote:     r.AdminNote,
		CreditLimit:   r.CreditLimit,
		Status:        r.Status,
	}
}

// CreateCustomer POST /credit/customers
func (h *CreditHandler) CreateCustomer(c *gin.Context) {
	var req customerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求体格式错误")
		return
	}
	cust, err := h.svc.CreateCustomer(c.Request.Context(), req.toParams())
	if err != nil {
		respondCreditErr(c, err)
		return
	}
	response.Success(c, toCustomerDTO(cust))
}

// UpdateCustomer PUT /credit/customers/:id
func (h *CreditHandler) UpdateCustomer(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var req customerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求体格式错误")
		return
	}
	cust, err := h.svc.UpdateCustomer(c.Request.Context(), id, req.toParams())
	if err != nil {
		respondCreditErr(c, err)
		return
	}
	response.Success(c, toCustomerDTO(cust))
}

// ArchiveCustomer DELETE /credit/customers/:id（归档，不物理删除）
func (h *CreditHandler) ArchiveCustomer(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	if err := h.svc.ArchiveCustomer(c.Request.Context(), id); err != nil {
		respondCreditErr(c, err)
		return
	}
	response.Success(c, gin.H{"archived": true})
}

// Recalc POST /credit/customers/:id/recalc
func (h *CreditHandler) Recalc(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	cust, err := h.svc.RecalcCustomer(c.Request.Context(), id)
	if err != nil {
		respondCreditErr(c, err)
		return
	}
	response.Success(c, toCustomerDTO(cust))
}

// RecalcAll POST /credit/recalc
func (h *CreditHandler) RecalcAll(c *gin.Context) {
	n, err := h.svc.RecalcAll(c.Request.Context())
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, gin.H{"recalculated": n})
}

// ListLedger GET /credit/customers/:id/ledger
func (h *CreditHandler) ListLedger(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	page, pageSize := response.ParsePagination(c)
	items, total, err := h.svc.ListEntries(c.Request.Context(), id, page, pageSize)
	if err != nil {
		respondCreditErr(c, err)
		return
	}
	out := make([]ledgerEntryDTO, 0, len(items))
	for _, it := range items {
		out = append(out, toLedgerDTO(it))
	}
	response.Paginated(c, out, total, page, pageSize)
}

// entryReq 记一笔台账请求体。
type entryReq struct {
	EntryType   string  `json:"entry_type"`
	Amount      float64 `json:"amount"`
	OccurredAt  string  `json:"occurred_at"` // RFC3339，空 = 现在
	Note        string  `json:"note"`
	ExternalRef string  `json:"external_ref"`
}

// AppendEntry POST /credit/customers/:id/ledger
func (h *CreditHandler) AppendEntry(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var req entryReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求体格式错误")
		return
	}
	var occurred *time.Time
	if req.OccurredAt != "" {
		t, err := time.Parse(time.RFC3339, req.OccurredAt)
		if err != nil {
			response.BadRequest(c, "occurred_at 须为 RFC3339 格式")
			return
		}
		occurred = &t
	}
	cust, err := h.svc.AppendEntry(c.Request.Context(), service.AppendEntryInput{
		CustomerID:  id,
		EntryType:   req.EntryType,
		Amount:      req.Amount,
		OccurredAt:  occurred,
		Note:        req.Note,
		ExternalRef: req.ExternalRef,
		Operator:    operatorOf(c),
	})
	if err != nil {
		respondCreditErr(c, err)
		return
	}
	response.Success(c, toCustomerDTO(cust))
}

// ReverseEntry POST /credit/ledger/:id/reverse
func (h *CreditHandler) ReverseEntry(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	cust, err := h.svc.ReverseEntry(c.Request.Context(), id, operatorOf(c))
	if err != nil {
		respondCreditErr(c, err)
		return
	}
	response.Success(c, toCustomerDTO(cust))
}
