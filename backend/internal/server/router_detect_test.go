package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"sub2api-account-monitor/internal/config"
	"sub2api-account-monitor/internal/handler"
	"sub2api-account-monitor/internal/service"
)

// newDetectTestRouter 构造注册了渠道检测路由的最小路由。
// 依赖全为 nil：本测试只关心路由树成型与鉴权生效。
func newDetectTestRouter() http.Handler {
	h := &Handlers{
		Auth:        handler.NewAuthHandler(nil),
		Detect:      handler.NewModelDetectHandler(service.NewModelDetectService(nil, nil, nil, nil)),
		PGAvailable: func() bool { return false },
	}
	return NewRouter(&config.Config{}, nil, h)
}

// 检测路由必须全部落在鉴权组内：它们会真实调用上游并消耗额度，
// 任何一条漏到免鉴权分支都等于把别人的额度开放给公网。
func TestDetectRoutesRequireAuth(t *testing.T) {
	r := newDetectTestRouter() // 注册本身若与既有路由冲突，NewRouter 会 panic

	cases := []struct {
		name   string
		method string
		path   string
	}{
		{"检测项清单", http.MethodGet, "/api/v1/detect/checks"},
		{"可检测账号", http.MethodGet, "/api/v1/detect/accounts"},
		{"发起检测", http.MethodPost, "/api/v1/detect/run"},
		{"作业快照", http.MethodGet, "/api/v1/detect/jobs/abc"},
		{"取消作业", http.MethodPost, "/api/v1/detect/jobs/abc/cancel"},
		{"重试失败项", http.MethodPost, "/api/v1/detect/jobs/abc/retry"},
		{"历史列表", http.MethodGet, "/api/v1/detect/history"},
		{"历史详情", http.MethodGet, "/api/v1/detect/history/1"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(c.method, c.path, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code == http.StatusNotFound {
				t.Fatalf("%s %s 未注册（404）", c.method, c.path)
			}
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("%s %s 期望 401，实得 %d", c.method, c.path, w.Code)
			}
		})
	}
}

// Detect 为 nil 时整段不注册，且不影响其他路由。
func TestDetectRoutesSkippedWhenNil(t *testing.T) {
	h := &Handlers{Auth: handler.NewAuthHandler(nil), PGAvailable: func() bool { return false }}
	r := NewRouter(&config.Config{}, nil, h)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/detect/checks", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("未装配 Detect 时应 404，实得 %d", w.Code)
	}

	// 健康检查不受影响
	req = httptest.NewRequest(http.MethodGet, "/health", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("/health 期望 200，实得 %d", w.Code)
	}
}
