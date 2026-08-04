//go:build !embed

package web

import "github.com/gin-gonic/gin"

// HasEmbeddedFrontend 非 embed 构建恒为 false。
func HasEmbeddedFrontend() bool { return false }

// Middleware 非 embed 构建下不服务前端（开发期由 vite dev server 提供）。
func Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
	}
}
