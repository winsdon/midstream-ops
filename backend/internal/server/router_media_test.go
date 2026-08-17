package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"sub2api-account-monitor/internal/config"
	"sub2api-account-monitor/internal/handler"
	"sub2api-account-monitor/internal/service"
)

// newMediaTestRouter 构造只启用生图页的最小路由。
//
// handler 依赖全为 nil：未授权请求在中间件层就被 abort，不触达业务逻辑。
// 这也顺带验证了「只开生图页」这个组合能正常工作——CSP 挂载判据若漏加
// EmbedMedia 条件，本测试的 CSP 断言就会失败。
func newMediaTestRouter(t *testing.T, frameOrigin string) http.Handler {
	t.Helper()
	cfg := &config.Config{}
	sessions := service.NewEmbedSessionStore(0)
	t.Cleanup(sessions.Close)

	h := &Handlers{
		Auth:             handler.NewAuthHandler(nil),
		EmbedMedia:       handler.NewEmbedMediaHandler(nil, nil, sessions, nil),
		EmbedSessions:    sessions,
		EmbedFrameOrigin: frameOrigin,
		PGAvailable:      func() bool { return false },
	}
	return NewRouter(cfg, nil, h)
}

// 换会话端点必须免鉴权：此时用户还没有 monitor 会话。
func TestMediaSessionEndpointIsPublic(t *testing.T) {
	r := newMediaTestRouter(t, "")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/embed/media/session", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusNotFound {
		t.Fatal("换会话路由未注册")
	}
	if w.Code == http.StatusUnauthorized {
		t.Fatal("换会话端点必须保持免鉴权")
	}
}

// 所有业务端点都必须要求嵌入会话。
//
// 【为什么逐个端点都测】提交端点会花用户的钱、产物端点能读产物，
// 任何一个漏挂 EmbedSession 中间件都是越权。
func TestMediaEndpointsRequireSession(t *testing.T) {
	r := newMediaTestRouter(t, "")

	endpoints := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/embed/media/keys"},
		{http.MethodPost, "/api/v1/embed/media/generate"},
		{http.MethodPost, "/api/v1/embed/media/uploads/prepare"},
		{http.MethodPost, "/api/v1/embed/media/edits"},
		{http.MethodGet, "/api/v1/embed/media/tasks"},
		{http.MethodDelete, "/api/v1/embed/media/tasks/1"},
		{http.MethodGet, "/api/v1/embed/media/tasks/1/content"},
	}

	for _, ep := range endpoints {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			// 无凭据
			req := httptest.NewRequest(ep.method, ep.path, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("无凭据应返回 401，实得 %d", w.Code)
			}

			// 伪造凭据
			req = httptest.NewRequest(ep.method, ep.path, nil)
			req.Header.Set("Authorization", "Bearer forged-token")
			w = httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("未知会话应返回 401，实得 %d", w.Code)
			}
		})
	}
}

// CSP frame-ancestors 必须覆盖 /embed/media。
//
// 【这是最容易漏的一处】新增嵌入页要同步登记 middleware.embedPagePaths，
// 漏了的话浏览器会直接拒绝渲染 iframe，且报错信息（frame-ancestors 'none'）
// 看起来像配置问题而非代码问题，排查成本极高。
func TestMediaEmbedFrameHeaders(t *testing.T) {
	t.Run("配置了 origin 时下发白名单", func(t *testing.T) {
		r := newMediaTestRouter(t, "https://sub2api.example.com")
		req := httptest.NewRequest(http.MethodGet, "/embed/media", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		want := "frame-ancestors https://sub2api.example.com"
		if got := w.Header().Get("Content-Security-Policy"); got != want {
			t.Fatalf("CSP 应为 %q，实得 %q", want, got)
		}
		// 嵌入页 URL 上曾带过 sub2api token，必须阻断 Referer 外泄
		if got := w.Header().Get("Referrer-Policy"); got != "no-referrer" {
			t.Fatalf("Referrer-Policy 应为 no-referrer，实得 %q", got)
		}
		// X-Frame-Options 无法指定单个 origin，设了会覆盖 CSP 的允许
		if got := w.Header().Get("X-Frame-Options"); got != "" {
			t.Fatalf("不应下发 X-Frame-Options，实得 %q", got)
		}
	})

	t.Run("未配置 origin 时拒绝嵌入", func(t *testing.T) {
		r := newMediaTestRouter(t, "")
		req := httptest.NewRequest(http.MethodGet, "/embed/media", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if got := w.Header().Get("Content-Security-Policy"); got != "frame-ancestors 'none'" {
			t.Fatalf("未配置时应 fail-closed，实得 %q", got)
		}
	})

	t.Run("非嵌入路径不受影响", func(t *testing.T) {
		r := newMediaTestRouter(t, "https://sub2api.example.com")
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if got := w.Header().Get("Content-Security-Policy"); got != "" {
			t.Fatalf("非嵌入路径不应下发 CSP，实得 %q", got)
		}
	})
}

// 生图页关闭时整段路由不注册（handler 为 nil）。
func TestMediaRoutesNotRegisteredWhenDisabled(t *testing.T) {
	cfg := &config.Config{}
	sessions := service.NewEmbedSessionStore(0)
	t.Cleanup(sessions.Close)

	h := &Handlers{
		Auth:          handler.NewAuthHandler(nil),
		Plaza:         handler.NewPlazaHandler(nil, nil, sessions, nil),
		EmbedSessions: sessions,
		PGAvailable:   func() bool { return false },
	}
	r := NewRouter(cfg, nil, h)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/embed/media/session", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("功能关闭时路由不应注册，实得 %d", w.Code)
	}
}
