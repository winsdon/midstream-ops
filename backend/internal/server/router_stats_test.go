package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"sub2api-account-monitor/internal/config"
	"sub2api-account-monitor/internal/handler"
)

func newStatsTestRouter(t *testing.T) http.Handler {
	t.Helper()
	h := &Handlers{
		Auth:        handler.NewAuthHandler(nil),
		Stats:       handler.NewStatsHandler(nil, &config.Config{}, nil),
		PGAvailable: func() bool { return false },
	}
	return NewRouter(&config.Config{}, nil, h)
}

func TestStatsUserRouteRegistered(t *testing.T) {
	r := newStatsTestRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/stats/users", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code == http.StatusNotFound {
		t.Fatal("GET /api/v1/stats/users 未注册")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("期望 401，实得 %d", w.Code)
	}
}
