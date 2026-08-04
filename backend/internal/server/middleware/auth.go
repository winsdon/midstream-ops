// Package middleware 提供 gin 中间件。
package middleware

import (
	"strings"

	"sub2api-account-monitor/internal/pkg/response"
	"sub2api-account-monitor/internal/service"

	"github.com/gin-gonic/gin"
)

// Auth 返回 Bearer JWT 校验中间件。
func Auth(authSvc *service.AuthService) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if header == "" {
			response.Unauthorized(c, "缺少 Authorization 头")
			c.Abort()
			return
		}
		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			response.Unauthorized(c, "Authorization 头格式错误，应为 Bearer <token>")
			c.Abort()
			return
		}
		username, err := authSvc.ParseToken(strings.TrimSpace(parts[1]))
		if err != nil {
			response.Unauthorized(c, "token 无效或已过期")
			c.Abort()
			return
		}
		c.Set("username", username)
		c.Next()
	}
}
