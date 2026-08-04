package handler

import (
	"errors"

	"sub2api-account-monitor/internal/pkg/response"
	"sub2api-account-monitor/internal/repository"
	"sub2api-account-monitor/internal/server/middleware"
	"sub2api-account-monitor/internal/service"

	"github.com/gin-gonic/gin"
)

// EmbedKycHandler KYC 自助嵌入页处理器。
//
// 由 sub2api 以 iframe 嵌入，客户填写自己的身份资料并提交送审。
//
// 【身份铁律】客户身份只从会话上下文取（middleware.EmbedUserIDKey），
// 绝不取自请求体或 URL —— 请求体里连 user_id 字段都不定义，
// 让「传了也没用」在类型层面成立，而不是靠调用方自觉忽略。
type EmbedKycHandler struct {
	svc    *service.CreditService
	issuer *EmbedSessionIssuer
}

// NewEmbedKycHandler 创建 EmbedKycHandler。
func NewEmbedKycHandler(
	svc *service.CreditService,
	verifier *service.Sub2apiTokenVerifier,
	sessions *service.EmbedSessionStore,
) *EmbedKycHandler {
	return &EmbedKycHandler{
		svc:    svc,
		issuer: NewEmbedSessionIssuer(verifier, sessions, "embed-kyc"),
	}
}

// CreateSession POST /api/v1/embed/kyc/session（免鉴权）
func (h *EmbedKycHandler) CreateSession(c *gin.Context) { h.issuer.Issue(c) }

// embedKycDTO 客户侧 KYC 输出。
//
// 刻意不复用管理端的 kycDTO：那份含 reviewed_by（内部人员用户名），
// 复用会让「管理端加一个内部字段」自动泄漏到客户侧。两份结构体的重复
// 是有意的隔离成本，不是 DRY 违规。
//
// review_note 保留 —— 驳回理由必须让客户看到，否则无从修正。
type embedKycDTO struct {
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
	ReviewNote  string  `json:"review_note"`
}

func toEmbedKycDTO(k *repository.CustomerKYC) embedKycDTO {
	return embedKycDTO{
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
		ReviewNote:  k.ReviewNote,
	}
}

// emptyEmbedKycDTO 尚未录入时的空白档案。
func emptyEmbedKycDTO() embedKycDTO {
	return embedKycDTO{
		SubjectType: service.SubjectTypeIndividual,
		Status:      service.KYCStatusDraft,
	}
}

// embedUserID 从会话上下文取客户身份。空值视为中间件未生效，直接 401。
func embedUserID(c *gin.Context) (string, bool) {
	uid := c.GetString(middleware.EmbedUserIDKey)
	if uid == "" {
		response.Unauthorized(c, "plaza.errors.sessionInvalid")
		return "", false
	}
	return uid, true
}

// GetProfile GET /api/v1/embed/kyc/profile
//
// 永不建客户记录：只是打开页面看一眼，不该在库里留下一条空客户。
// 客户不存在或尚未填写，都返回同一份空白档案 —— 客户侧不需要区分。
func (h *EmbedKycHandler) GetProfile(c *gin.Context) {
	if h.svc == nil {
		response.ServiceUnavailable(c, "plaza.errors.notConfigured")
		return
	}
	uid, ok := embedUserID(c)
	if !ok {
		return
	}

	cust, err := h.svc.GetCustomerBySub2apiID(c.Request.Context(), uid)
	if errors.Is(err, repository.ErrNotFound) {
		response.Success(c, emptyEmbedKycDTO())
		return
	}
	if err != nil {
		response.InternalError(c, "plaza.errors.loadFailed")
		return
	}

	k, err := h.svc.GetKYC(c.Request.Context(), cust.ID)
	if errors.Is(err, repository.ErrNotFound) {
		response.Success(c, emptyEmbedKycDTO())
		return
	}
	if err != nil {
		response.InternalError(c, "plaza.errors.loadFailed")
		return
	}
	response.Success(c, toEmbedKycDTO(k))
}

// embedKycReq 客户侧提交请求体。
//
// 【不含 user_id】身份取自会话，见类型注释。也不含 status —— 状态迁移
// 只由 submit 开关驱动，客户无法把自己直接改成 approved。
type embedKycReq struct {
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

	Submit bool `json:"submit"`
}

func (r embedKycReq) toParams() repository.KYCParams {
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

// SaveProfile PUT /api/v1/embed/kyc/profile
//
// 客户记录在此惰性创建（GET 不建）：只有真的提交了资料才值得占一行。
// 已通过审核的档案禁止客户自行改动 —— 授信依据一旦被批准就该冻结，
// 需要变更走管理端。
func (h *EmbedKycHandler) SaveProfile(c *gin.Context) {
	if h.svc == nil {
		response.ServiceUnavailable(c, "plaza.errors.notConfigured")
		return
	}
	uid, ok := embedUserID(c)
	if !ok {
		return
	}
	var req embedKycReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "credit.kyc.errors.badRequest")
		return
	}

	ctx := c.Request.Context()
	cust, err := h.svc.EnsureCustomer(ctx, uid, c.GetString(middleware.EmbedEmailKey))
	if err != nil {
		respondEmbedKycErr(c, err)
		return
	}

	existing, err := h.svc.GetKYC(ctx, cust.ID)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		response.InternalError(c, "plaza.errors.loadFailed")
		return
	}
	if existing != nil && existing.Status == service.KYCStatusApproved {
		response.BadRequest(c, "credit.kyc.errors.locked")
		return
	}

	k, err := h.svc.SaveKYC(ctx, cust.ID, req.toParams(), req.Submit)
	if err != nil {
		respondEmbedKycErr(c, err)
		return
	}
	response.Success(c, toEmbedKycDTO(k))
}

// respondEmbedKycErr 客户侧错误映射。
//
// 与管理端 respondCreditErr 的区别：入参错误只回一个笼统的 i18n key，
// 不把后端的中文校验详情（含字段名）透给客户端 —— 必填项校验前端已做过一遍，
// 走到这里说明是绕过 UI 的调用，没有给出精确提示的必要。
func respondEmbedKycErr(c *gin.Context, err error) {
	if errors.Is(err, service.ErrInvalidInput) {
		response.BadRequest(c, "credit.kyc.errors.invalid")
		return
	}
	response.InternalError(c, "plaza.errors.loadFailed")
}
