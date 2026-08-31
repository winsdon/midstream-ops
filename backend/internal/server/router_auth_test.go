package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"sub2api-account-monitor/internal/config"
	"sub2api-account-monitor/internal/handler"
	"sub2api-account-monitor/internal/pkg/jwtutil"
	"sub2api-account-monitor/internal/pkg/response"
	"sub2api-account-monitor/internal/service"
)

func newAuthTestRouter(t *testing.T) (*service.AuthService, http.Handler) {
	t.Helper()
	cfg := &config.AuthConfig{
		Username:            "admin",
		Password:            "secret",
		RefreshTokenTTLDays: 30,
	}
	jwtMgr := jwtutil.New(strings.Repeat("k", 32), time.Hour)
	svc := service.NewAuthService(cfg, jwtMgr, service.NewMemRefreshTokens())
	h := &Handlers{
		Auth:        handler.NewAuthHandler(svc),
		PGAvailable: func() bool { return false },
	}
	return svc, NewRouter(&config.Config{}, svc, h)
}

func TestAuthRefreshRouteIsPublic(t *testing.T) {
	_, r := newAuthTestRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", strings.NewReader(`{"refresh_token":"rt_nope"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("无 Bearer 的 /auth/refresh 期望 401（说明免鉴权且已注册），实得 %d body=%s", w.Code, w.Body.String())
	}
}

func TestAuthLogoutRouteIsPublic(t *testing.T) {
	_, r := newAuthTestRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("/auth/logout 期望 200，实得 %d body=%s", w.Code, w.Body.String())
	}
}

func TestLoginRefreshMeRoundTrip(t *testing.T) {
	_, r := newAuthTestRouter(t)

	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"secret"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	loginW := httptest.NewRecorder()
	r.ServeHTTP(loginW, loginReq)
	if loginW.Code != http.StatusOK {
		t.Fatalf("login %d %s", loginW.Code, loginW.Body.String())
	}
	var loginBody response.Response
	if err := json.Unmarshal(loginW.Body.Bytes(), &loginBody); err != nil {
		t.Fatal(err)
	}
	data, _ := loginBody.Data.(map[string]any)
	if data == nil {
		t.Fatalf("login data = %#v", loginBody.Data)
	}
	refresh, _ := data["refresh_token"].(string)
	if !strings.HasPrefix(refresh, "rt_") {
		t.Fatalf("refresh_token = %#v", data["refresh_token"])
	}

	buf, _ := json.Marshal(map[string]string{"refresh_token": refresh})
	refReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", bytes.NewReader(buf))
	refReq.Header.Set("Content-Type", "application/json")
	refW := httptest.NewRecorder()
	r.ServeHTTP(refW, refReq)
	if refW.Code != http.StatusOK {
		t.Fatalf("refresh %d %s", refW.Code, refW.Body.String())
	}
	var refBody response.Response
	if err := json.Unmarshal(refW.Body.Bytes(), &refBody); err != nil {
		t.Fatal(err)
	}
	refData, _ := refBody.Data.(map[string]any)
	token, _ := refData["token"].(string)
	if token == "" {
		t.Fatal("续期未返回 token")
	}

	meReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	meReq.Header.Set("Authorization", "Bearer "+token)
	meW := httptest.NewRecorder()
	r.ServeHTTP(meW, meReq)
	if meW.Code != http.StatusOK {
		t.Fatalf("me %d %s", meW.Code, meW.Body.String())
	}
}
