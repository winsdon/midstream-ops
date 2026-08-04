package handler

import (
	"log"

	"sub2api-account-monitor/internal/pkg/response"
	"sub2api-account-monitor/internal/service"

	"github.com/gin-gonic/gin"
)

// embedSessionRequest 换会话请求体。
type embedSessionRequest struct {
	// Sub2apiToken 是 sub2api 通过 iframe URL 透传的用户 JWT。
	Sub2apiToken string `json:"sub2api_token"`
	// UserID 是 URL 上透传的用户 ID，仅用于与 token 内的身份比对，不作为身份依据。
	UserID string `json:"user_id"`
}

type embedSessionResponse struct {
	SessionToken string `json:"session_token"`
	ExpiresIn    int    `json:"expires_in"`
}

// EmbedSessionIssuer 用 sub2api 透传的 token 换取 monitor 短期会话。
//
// 模型广场与 KYC 自助页共用同一套换会话协议，故抽成独立类型由各 handler 内嵌 ——
// 验签与身份比对是安全关键路径，两份实现必然漂移（改了一处忘了另一处）。
//
// 本地验签而非回调 sub2api：线上 sub2api 的会话绑定（JWT 的 bnd = sha256(客户端IP+UA)）
// 使服务端直连必然校验失败，且失败时它会撤销该用户整个会话家族、把人从浏览器踢下线。
//
// 错误信息统一用 plaza.errors.* 这组 i18n key：它们全是会话/网络层的通用文案，
// 两个嵌入页共用一份翻译，不为每个页面复制一遍。
type EmbedSessionIssuer struct {
	verifier *service.Sub2apiTokenVerifier
	sessions *service.EmbedSessionStore
	// logTag 区分日志来源，便于排查是哪个入口在被伪造探测
	logTag string
}

// NewEmbedSessionIssuer 创建换会话器。
func NewEmbedSessionIssuer(
	verifier *service.Sub2apiTokenVerifier,
	sessions *service.EmbedSessionStore,
	logTag string,
) *EmbedSessionIssuer {
	return &EmbedSessionIssuer{verifier: verifier, sessions: sessions, logTag: logTag}
}

// Issue 处理换会话请求（免鉴权入口）。
func (i *EmbedSessionIssuer) Issue(c *gin.Context) {
	var req embedSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Sub2apiToken == "" {
		response.BadRequest(c, "plaza.errors.missingParams")
		return
	}
	if i.verifier == nil || i.sessions == nil {
		response.ServiceUnavailable(c, "plaza.errors.notConfigured")
		return
	}

	claims, err := i.verifier.Verify(req.Sub2apiToken)
	if err != nil {
		// 不打印 token 明文。
		log.Printf("[%s] token 验签失败: %v", i.logTag, err)
		response.Unauthorized(c, "plaza.errors.tokenInvalid")
		return
	}
	// URL 上的 user_id 与 token 内身份不符 → 视为伪造。
	if req.UserID != "" && req.UserID != claims.UserIDString() {
		log.Printf("[%s] user_id 不匹配 url=%q token=%q", i.logTag, req.UserID, claims.UserIDString())
		response.Unauthorized(c, "plaza.errors.userMismatch")
		return
	}

	token, expiresIn, err := i.sessions.Create(claims.UserIDString(), claims.Email)
	if err != nil {
		response.InternalError(c, "plaza.errors.sessionCreateFailed")
		return
	}
	response.Success(c, embedSessionResponse{SessionToken: token, ExpiresIn: expiresIn})
}
