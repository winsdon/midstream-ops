package handler

import (
	"sub2api-account-monitor/internal/pkg/response"
	"sub2api-account-monitor/internal/repository"
	"sub2api-account-monitor/internal/service"

	"github.com/gin-gonic/gin"
)

// PlazaHandler 模型广场嵌入页处理器。
//
// 该页面由 sub2api 以 iframe 嵌入，用户身份来自 sub2api 透传的 token —— 用共享密钥
// 本地验签，不回调 sub2api（其会话绑定会因服务端 IP/UA 不符而拒绝，并踢用户下线）。
// 错误信息统一用 i18n key，由前端翻译后展示。
//
// 内嵌 *EmbedSessionIssuer 后直接获得 CreateSession 方法（Issue 的别名见下），
// 换会话逻辑与 KYC 自助页共用一份实现。
type PlazaHandler struct {
	svc    *service.PlazaService
	issuer *EmbedSessionIssuer
	pg     *repository.PG
}

// NewPlazaHandler 创建 PlazaHandler。
func NewPlazaHandler(
	svc *service.PlazaService,
	verifier *service.Sub2apiTokenVerifier,
	sessions *service.EmbedSessionStore,
	pg *repository.PG,
) *PlazaHandler {
	return &PlazaHandler{
		svc:    svc,
		issuer: NewEmbedSessionIssuer(verifier, sessions, "plaza"),
		pg:     pg,
	}
}

// CreateSession 用 sub2api token 换取 monitor 短期会话。
// POST /api/v1/embed/plaza/session（免鉴权）
func (h *PlazaHandler) CreateSession(c *gin.Context) { h.issuer.Issue(c) }

// Models 返回模型广场数据。
// GET /api/v1/embed/plaza/models（需嵌入会话）
func (h *PlazaHandler) Models(c *gin.Context) {
	if h.svc == nil {
		response.ServiceUnavailable(c, "plaza.errors.notConfigured")
		return
	}
	if h.pg != nil && !h.pg.Available() {
		response.ServiceUnavailable(c, "plaza.errors.databaseUnavailable")
		return
	}
	data, err := h.svc.Build(c.Request.Context())
	if err != nil {
		response.InternalError(c, "plaza.errors.loadFailed")
		return
	}
	response.Success(c, data)
}
