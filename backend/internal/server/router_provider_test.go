package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"sub2api-account-monitor/internal/config"
	"sub2api-account-monitor/internal/handler"
)

// newProviderTestRouter 构造只注册 provider 路由的最小路由。
// handler 依赖全为 nil：本测试只关心路由树是否成型、鉴权是否生效，
// 未授权请求在中间件层就被 abort，不会触达业务逻辑。
func newProviderTestRouter(t *testing.T) http.Handler {
	t.Helper()
	h := &Handlers{
		Auth:        handler.NewAuthHandler(nil),
		Provider:    handler.NewProviderHandler(nil, nil, nil, nil, nil, nil, &config.Config{}, nil),
		PGAvailable: func() bool { return false },
	}
	return NewRouter(&config.Config{}, nil, h)
}

// TestRefreshAllRouteCoexistsWithIDRoutes 全量刷新的静态路径与 :id 通配路径共存。
//
// /providers/balance/refresh-all 与 /providers/:id/balance/refresh 在同一层级上
// 一个是静态段、一个是通配符。gin 支持这种共存，但对注册顺序和路径形状敏感——
// 改动 provider 路由时若不慎，轻则新路径被 :id 吞掉（"balance" 被当成 id），
// 重则 NewRouter 直接 panic。本测试把这个边界钉死。
func TestRefreshAllRouteCoexistsWithIDRoutes(t *testing.T) {
	r := newProviderTestRouter(t) // 注册本身若冲突，NewRouter 会 panic

	cases := []struct {
		name   string
		method string
		path   string
	}{
		{"全量刷新", http.MethodPost, "/api/v1/providers/balance/refresh-all"},
		{"单站点刷新", http.MethodPost, "/api/v1/providers/1/balance/refresh"},
		{"手动录入余额", http.MethodPut, "/api/v1/providers/1/balance"},
		{"余额历史", http.MethodGet, "/api/v1/providers/1/balance/history"},
		{"分组账号", http.MethodGet, "/api/v1/providers/1/group-accounts"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(c.method, c.path, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code == http.StatusNotFound {
				t.Errorf("%s %s 未注册（404）", c.method, c.path)
			}
			// 无 Authorization 头应被鉴权中间件拦住，说明请求确实路由到了受保护的处理器
			if w.Code != http.StatusUnauthorized {
				t.Errorf("%s %s 期望 401，实得 %d", c.method, c.path, w.Code)
			}
		})
	}
}
