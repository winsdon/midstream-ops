package handler

import (
	"errors"
	"time"

	"sub2api-account-monitor/internal/pkg/response"
	"sub2api-account-monitor/internal/repository"
	"sub2api-account-monitor/internal/service"

	"github.com/gin-gonic/gin"
)

// kycDTO KYC 资料输出（管理端）。
//
// 【含完整 PII】仅供管理端与「本人」使用。嵌入端复用本 DTO 是安全的
// （字段全是客户自己填的身份信息，无 admin_note 这类内部字段），
// 但审核轨迹里的 reviewed_by 是内部人员用户名，客户侧须裁剪掉。
type kycDTO struct {
	CustomerID    int64  `json:"customer_id"`
	SubjectType   string `json:"subject_type"`
	Status        string `json:"status"`
	CountryRegion string `json:"country_region"`
	IDType        string `json:"id_type"`

	LegalName string `json:"legal_name"`
	IDNumber  string `json:"id_number"`
	BirthDate string `json:"birth_date"`
	Address   string `json:"address"`

	CompanyName string `json:"company_name"`
	RegNumber   string `json:"reg_number"`
	LegalRep    string `json:"legal_rep"`
	RegAddress  string `json:"reg_address"`
	TaxNumber   string `json:"tax_number"`

	ContactName  string `json:"contact_name"`
	ContactPhone string `json:"contact_phone"`
	ContactEmail string `json:"contact_email"`

	BankName    string `json:"bank_name"`
	BankAccount string `json:"bank_account"`
	BankHolder  string `json:"bank_holder"`

	SubmittedAt *string `json:"submitted_at"`
	ReviewedAt  *string `json:"reviewed_at"`
	ReviewedBy  string  `json:"reviewed_by"`
	ReviewNote  string  `json:"review_note"`
	UpdatedAt   string  `json:"updated_at"`
}

func toKYCDTO(k *repository.CustomerKYC) kycDTO {
	return kycDTO{
		CustomerID:    k.CustomerID,
		SubjectType:   k.SubjectType,
		Status:        k.Status,
		CountryRegion: k.CountryRegion,
		IDType:        k.IDType,

		LegalName: k.LegalName,
		IDNumber:  k.IDNumber,
		BirthDate: k.BirthDate,
		Address:   k.Address,

		CompanyName: k.CompanyName,
		RegNumber:   k.RegNumber,
		LegalRep:    k.LegalRep,
		RegAddress:  k.RegAddress,
		TaxNumber:   k.TaxNumber,

		ContactName:  k.ContactName,
		ContactPhone: k.ContactPhone,
		ContactEmail: k.ContactEmail,

		BankName:    k.BankName,
		BankAccount: k.BankAccount,
		BankHolder:  k.BankHolder,

		SubmittedAt: fmtTimePtr(k.SubmittedAt),
		ReviewedAt:  fmtTimePtr(k.ReviewedAt),
		ReviewedBy:  k.ReviewedBy,
		ReviewNote:  k.ReviewNote,
		UpdatedAt:   k.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// emptyKYCDTO 尚未录入时的空白档案。
//
// GET 在无记录时返回它而非 404：前端表单需要一份可绑定的初始结构，
// 让「还没填」与「客户不存在」在 UI 上是两件事。
func emptyKYCDTO(customerID int64) kycDTO {
	return kycDTO{
		CustomerID:  customerID,
		SubjectType: service.SubjectTypeIndividual,
		Status:      service.KYCStatusDraft,
	}
}

// GetKYC GET /credit/customers/:id/kyc
func (h *CreditHandler) GetKYC(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	// 客户必须存在——否则任意 id 都会返回一份空白档案，掩盖调用方的错误
	if _, err := h.svc.GetCustomer(c.Request.Context(), id); err != nil {
		respondCreditErr(c, err)
		return
	}
	k, err := h.svc.GetKYC(c.Request.Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		response.Success(c, emptyKYCDTO(id))
		return
	}
	if err != nil {
		respondCreditErr(c, err)
		return
	}
	response.Success(c, toKYCDTO(k))
}

// kycReq KYC 保存请求体。
//
// 不含 status：状态迁移只由 submit 开关与审核接口驱动，
// 客户端无法直接把自己改成 approved。
type kycReq struct {
	SubjectType   string `json:"subject_type"`
	CountryRegion string `json:"country_region"`
	IDType        string `json:"id_type"`

	LegalName string `json:"legal_name"`
	IDNumber  string `json:"id_number"`
	BirthDate string `json:"birth_date"`
	Address   string `json:"address"`

	CompanyName string `json:"company_name"`
	RegNumber   string `json:"reg_number"`
	LegalRep    string `json:"legal_rep"`
	RegAddress  string `json:"reg_address"`
	TaxNumber   string `json:"tax_number"`

	ContactName  string `json:"contact_name"`
	ContactPhone string `json:"contact_phone"`
	ContactEmail string `json:"contact_email"`

	BankName    string `json:"bank_name"`
	BankAccount string `json:"bank_account"`
	BankHolder  string `json:"bank_holder"`

	// Submit=true 表示提交送审（置 pending 并做必填校验）
	Submit bool `json:"submit"`
}

func (r kycReq) toParams() repository.KYCParams {
	return repository.KYCParams{
		SubjectType:   r.SubjectType,
		CountryRegion: r.CountryRegion,
		IDType:        r.IDType,

		LegalName: r.LegalName,
		IDNumber:  r.IDNumber,
		BirthDate: r.BirthDate,
		Address:   r.Address,

		CompanyName: r.CompanyName,
		RegNumber:   r.RegNumber,
		LegalRep:    r.LegalRep,
		RegAddress:  r.RegAddress,
		TaxNumber:   r.TaxNumber,

		ContactName:  r.ContactName,
		ContactPhone: r.ContactPhone,
		ContactEmail: r.ContactEmail,

		BankName:    r.BankName,
		BankAccount: r.BankAccount,
		BankHolder:  r.BankHolder,
	}
}

// SaveKYC PUT /credit/customers/:id/kyc
func (h *CreditHandler) SaveKYC(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var req kycReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求体格式错误")
		return
	}
	k, err := h.svc.SaveKYC(c.Request.Context(), id, req.toParams(), req.Submit)
	if err != nil {
		respondCreditErr(c, err)
		return
	}
	response.Success(c, toKYCDTO(k))
}

// kycReviewReq 审核请求体。
type kycReviewReq struct {
	Status string `json:"status"` // approved | rejected
	Note   string `json:"note"`
}

// ReviewKYC POST /credit/customers/:id/kyc/review
func (h *CreditHandler) ReviewKYC(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var req kycReviewReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求体格式错误")
		return
	}
	k, err := h.svc.ReviewKYC(c.Request.Context(), id, req.Status, operatorOf(c), req.Note)
	if err != nil {
		respondCreditErr(c, err)
		return
	}
	response.Success(c, toKYCDTO(k))
}
