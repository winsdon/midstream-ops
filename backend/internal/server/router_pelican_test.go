package server

import (
	"net/http/httptest"
	"strings"
	"sub2api-account-monitor/internal/config"
	"sub2api-account-monitor/internal/handler"
	"sub2api-account-monitor/internal/service"
	"testing"
)

func TestPelicanRouteAuthentication(t *testing.T) {
	sessions := service.NewEmbedSessionStore(0)
	defer sessions.Close()
	h := handler.NewPelicanHandler(nil)
	h.SetIssuer(handler.NewEmbedSessionIssuer(nil, sessions, "test"))
	r := NewRouter(&config.Config{}, nil, &Handlers{Auth: handler.NewAuthHandler(nil), Pelican: h, EmbedPelican: h, EmbedSessions: sessions, EmbedFrameOrigin: "https://sub.example"})
	for _, path := range []string{"/api/v1/pelican/config", "/api/v1/pelican/groups", "/api/v1/pelican/results", "/api/v1/pelican/results/1", "/api/v1/embed/pelican/results", "/api/v1/embed/pelican/results/1", "/api/v1/embed/pelican/filters"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 401 {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/embed/pelican/session", strings.NewReader(`{}`)))
	if w.Code != 400 {
		t.Fatalf("session must be public, got %d", w.Code)
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/embed/pelican", nil))
	if w.Header().Get("Content-Security-Policy") != "frame-ancestors https://sub.example" {
		t.Fatal("missing frame policy")
	}
}
