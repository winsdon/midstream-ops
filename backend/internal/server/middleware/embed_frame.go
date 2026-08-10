package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// embedPagePaths 是允许被 sub2api iframe 嵌入的前端页面路径白名单。
//
// 刻意逐条列举而非用 "/embed/" 前缀通配：前缀匹配会让将来任何新增的 /embed/*
// 页面自动获得可嵌入权限，这是隐式的安全降级——新增页面必须在此显式登记。
var embedPagePaths = []string{"/embed/plaza", "/embed/kyc", "/embed/media"}

// EmbedFrameHeaders 为嵌入页下发 CSP frame-ancestors。
//
// 只对嵌入页 GET 请求生效，其余路径不受影响。允许的 origin 来自配置文件而非请求
// 参数——请求方传入的 src_host 不可信。未配置时下发 'none'，即拒绝被任何站点嵌套。
//
// 调用方必须确认至少有一个嵌入页已启用后再挂载本中间件（见 NewRouter）。嵌入页
// 全部关闭时挂载它，会对未注册的 /embed/* 路径下发 'none'，看起来像「白名单没配」，
// 实际是「功能没开」——这两种状态在排查时极易混淆。
//
// 刻意不设 X-Frame-Options：该头不支持动态指定单个 origin（只有 DENY/SAMEORIGIN），
// 与「仅允许某个 sub2api 站点嵌入」的需求冲突，CSP frame-ancestors 是唯一正解。
func EmbedFrameHeaders(allowedOrigin string) gin.HandlerFunc {
	origin := strings.TrimSpace(allowedOrigin)
	return func(c *gin.Context) {
		if c.Request.Method != http.MethodGet || !isEmbedPagePath(c.Request.URL.Path) {
			c.Next()
			return
		}
		// 嵌入页 URL 上曾带过 sub2api token，避免通过 Referer 外泄。
		c.Header("Referrer-Policy", "no-referrer")
		if origin == "" {
			c.Header("Content-Security-Policy", "frame-ancestors 'none'")
		} else {
			c.Header("Content-Security-Policy", "frame-ancestors "+origin)
		}
		c.Next()
	}
}

func isEmbedPagePath(path string) bool {
	for _, p := range embedPagePaths {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}
