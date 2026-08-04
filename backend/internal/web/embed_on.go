//go:build embed

package web

import (
	"embed"
	"io"
	"io/fs"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

//go:embed all:dist
var frontendFS embed.FS

// HasEmbeddedFrontend 报告是否嵌入了前端产物。
func HasEmbeddedFrontend() bool {
	_, err := frontendFS.ReadFile("dist/index.html")
	return err == nil
}

// Middleware 返回 SPA 静态资源服务中间件。
// 命中 /api/、/health 则放行；存在的静态文件直接服务；其余路径回退 index.html。
func Middleware() gin.HandlerFunc {
	distFS, err := fs.Sub(frontendFS, "dist")
	if err != nil {
		panic("无法读取嵌入的前端目录: " + err.Error())
	}
	fileServer := http.FileServer(http.FS(distFS))

	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/api/") || path == "/health" {
			c.Next()
			return
		}

		cleanPath := strings.TrimPrefix(path, "/")
		if cleanPath == "" {
			cleanPath = "index.html"
		}

		if f, err := distFS.Open(cleanPath); err == nil {
			_ = f.Close()
			fileServer.ServeHTTP(c.Writer, c.Request)
			c.Abort()
			return
		}

		serveIndexHTML(c, distFS)
	}
}

func serveIndexHTML(c *gin.Context, fsys fs.FS) {
	file, err := fsys.Open("index.html")
	if err != nil {
		c.String(http.StatusNotFound, "前端资源不存在")
		c.Abort()
		return
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(file)
	if err != nil {
		c.String(http.StatusInternalServerError, "读取 index.html 失败")
		c.Abort()
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", content)
	c.Abort()
}
