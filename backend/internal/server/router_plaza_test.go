package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"sub2api-account-monitor/internal/config"
	"sub2api-account-monitor/internal/handler"
	"sub2api-account-monitor/internal/service"
)

// newTestRouter 构造一个只启用模型广场的最小路由，用于验证嵌入路由的鉴权边界。
func newTestRouter(t *testing.T, frameOrigin string) http.Handler {
	t.Helper()
	cfg := &config.Config{}
	sessions := service.NewEmbedSessionStore(0)
	t.Cleanup(sessions.Close)

	// handler 依赖全为 nil：本测试只关心路由是否注册、中间件是否拦截，
	// 不触达业务逻辑（未授权请求在中间件层就被 abort）。
	h := &Handlers{
		Auth:             handler.NewAuthHandler(nil),
		Plaza:            handler.NewPlazaHandler(nil, nil, sessions, nil),
		EmbedSessions:    sessions,
		EmbedFrameOrigin: frameOrigin,
		PGAvailable:      func() bool { return false },
	}
	return NewRouter(cfg, nil, h)
}

func TestEmbedSessionEndpointIsPublic(t *testing.T) {
	// 换会话端点必须免鉴权：此时用户还没有 monitor 会话，
	// 若被全局鉴权拦住，整个嵌入流程无从开始。
	r := newTestRouter(t, "")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/embed/plaza/session", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusNotFound {
		t.Fatal("embed session route must be registered")
	}
	if w.Code == http.StatusUnauthorized {
		t.Fatal("embed session endpoint must remain public (no Authorization required)")
	}
}

func TestEmbedModelsRequiresSession(t *testing.T) {
	r := newTestRouter(t, "")

	t.Run("no header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/embed/plaza/models", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", w.Code)
		}
	})

	t.Run("unknown session token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/embed/plaza/models", nil)
		req.Header.Set("Authorization", "Bearer nonexistent")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", w.Code)
		}
	})
}

func TestEmbedFrameHeaders(t *testing.T) {
	t.Run("configured origin", func(t *testing.T) {
		r := newTestRouter(t, "https://sub.example.com")
		req := httptest.NewRequest(http.MethodGet, "/embed/plaza", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if got := w.Header().Get("Content-Security-Policy"); got != "frame-ancestors https://sub.example.com" {
			t.Errorf("CSP = %q", got)
		}
		if got := w.Header().Get("Referrer-Policy"); got != "no-referrer" {
			t.Errorf("Referrer-Policy = %q, want no-referrer", got)
		}
		// X-Frame-Options 不支持动态单 origin，必须不设置，否则会覆盖 CSP 的允许。
		if got := w.Header().Get("X-Frame-Options"); got != "" {
			t.Errorf("X-Frame-Options should not be set, got %q", got)
		}
	})

	t.Run("unconfigured origin denies framing", func(t *testing.T) {
		r := newTestRouter(t, "")
		req := httptest.NewRequest(http.MethodGet, "/embed/plaza", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if got := w.Header().Get("Content-Security-Policy"); got != "frame-ancestors 'none'" {
			t.Errorf("CSP = %q, want frame-ancestors 'none'", got)
		}
	})

	t.Run("other paths unaffected", func(t *testing.T) {
		r := newTestRouter(t, "https://sub.example.com")
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if got := w.Header().Get("Content-Security-Policy"); got != "" {
			t.Errorf("non-embed path should not get CSP, got %q", got)
		}
	})
}
