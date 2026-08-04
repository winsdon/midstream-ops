package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"sub2api-account-monitor/internal/config"
	"sub2api-account-monitor/internal/handler"
)

// newOpCostTestRouter 构造注册了 provider + 运营成本路由的最小路由。
// handler 依赖全为 nil：本测试只关心路由树成型与鉴权生效，未授权请求在中间件层即被 abort。
func newOpCostTestRouter(t *testing.T) http.Handler {
	t.Helper()
	h := &Handlers{
		Auth:        handler.NewAuthHandler(nil),
		Provider:    handler.NewProviderHandler(nil, nil, nil, nil, nil, nil, &config.Config{}, nil),
		OpCost:      handler.NewOperatingCostHandler(nil, &config.Config{}),
		PGAvailable: func() bool { return false },
	}
	return NewRouter(&config.Config{}, nil, h)
}

// 运营成本路由与既有 provider 路由共存。
//
// /providers/:id/operating-costs 与同层的 /providers/:id/costs 都挂在 :id 之下，
// 而 DELETE /operating-costs/:id 是顶层新段。gin 对注册顺序与路径形状敏感，
// 冲突时轻则被既有通配吞掉、重则 NewRouter 直接 panic，本测试把边界钉死。
func TestOperatingCostRoutesRegistered(t *testing.T) {
	r := newOpCostTestRouter(t) // 注册本身若冲突，NewRouter 会 panic

	cases := []struct {
		name   string
		method string
		path   string
	}{
		{"明细列表", http.MethodGet, "/api/v1/providers/1/operating-costs"},
		{"记一笔", http.MethodPost, "/api/v1/providers/1/operating-costs"},
		{"删除一笔", http.MethodDelete, "/api/v1/operating-costs/1"},
		// 回归：既有的 per-key 成本明细路由不能被新路由影响
		{"上游成本明细仍在", http.MethodGet, "/api/v1/providers/1/costs"},
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

// OpCost 为 nil 时不注册运营成本路由，且不影响其他路由（与既有 if h.X != nil 惯例一致）。
func TestOperatingCostRoutesSkippedWhenNil(t *testing.T) {
	h := &Handlers{
		Auth:        handler.NewAuthHandler(nil),
		Provider:    handler.NewProviderHandler(nil, nil, nil, nil, nil, nil, &config.Config{}, nil),
		PGAvailable: func() bool { return false },
	}
	r := NewRouter(&config.Config{}, nil, h)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/providers/1/operating-costs", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("OpCost 为 nil 时应 404，实得 %d", w.Code)
	}
}
