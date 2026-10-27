package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"sub2api-account-monitor/internal/config"
	"sub2api-account-monitor/internal/handler"
)

// 本站连接迁到 /self 后：新路由须注册（未登录 401），调价映射的 /pricing/* 须已下线（404）。
func TestSelfRoutesReplacePricing(t *testing.T) {
	h := &Handlers{
		Auth:        handler.NewAuthHandler(nil),
		Self:        handler.NewSelfHandler(nil, nil),
		PGAvailable: func() bool { return false },
	}
	r := NewRouter(&config.Config{}, nil, h)

	cases := []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/api/v1/self", http.StatusUnauthorized},
		{http.MethodPut, "/api/v1/self", http.StatusUnauthorized},
		{http.MethodGet, "/api/v1/self/local-groups", http.StatusUnauthorized},
		{http.MethodGet, "/api/v1/pricing/self", http.StatusNotFound},
		{http.MethodGet, "/api/v1/pricing/rules", http.StatusNotFound},
		{http.MethodGet, "/api/v1/pricing/mapped", http.StatusNotFound},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(c.method, c.path, nil))
		if w.Code != c.want {
			t.Errorf("%s %s = %d, want %d", c.method, c.path, w.Code, c.want)
		}
	}
}
