package middleware

import (
	"strings"

	"sub2api-account-monitor/internal/pkg/response"
	"sub2api-account-monitor/internal/service"

	"github.com/gin-gonic/gin"
)

// EmbedUserIDKey 是嵌入会话用户 ID 在 gin.Context 中的键。
const EmbedUserIDKey = "embed_user_id"

// EmbedEmailKey 是嵌入会话用户邮箱在 gin.Context 中的键。
//
// 与 UserID 同源（都来自 sub2api token 的 claims），仅在首次惰性建客户记录时
// 用作展示字段。绝不可当身份用——身份判定只认 EmbedUserIDKey。
const EmbedEmailKey = "embed_email"

// EmbedSession 返回嵌入会话校验中间件。
//
// 与 Auth 的区别：校验的是 monitor 为 iframe 嵌入场景自签的短期 session token，
// 而非管理员登录 JWT。会话由 /embed/plaza/session 用 sub2api 透传 token 换取。
// 错误信息使用 i18n key，由前端翻译后展示。
func EmbedSession(store *service.EmbedSessionStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		if store == nil {
			response.Unauthorized(c, "plaza.errors.sessionInvalid")
			c.Abort()
			return
		}
		header := c.GetHeader("Authorization")
		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			response.Unauthorized(c, "plaza.errors.sessionMissing")
			c.Abort()
			return
		}
		sess, ok := store.Get(strings.TrimSpace(parts[1]))
		if !ok {
			response.Unauthorized(c, "plaza.errors.sessionExpired")
			c.Abort()
			return
		}
		c.Set(EmbedUserIDKey, sess.UserID)
		c.Set(EmbedEmailKey, sess.Email)
		c.Next()
	}
}
