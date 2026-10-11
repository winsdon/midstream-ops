package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSub2apiLoginReturnsRefreshToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/login" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"code":0,"data":{"access_token":"at","refresh_token":"rt","expires_in":1800}}`))
	}))
	t.Cleanup(srv.Close)

	got, err := NewSub2apiClient(3*time.Second).Login(context.Background(), srv.URL, "u@example.com", "pw")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if got.AccessToken != "at" {
		t.Errorf("AccessToken = %q, want at", got.AccessToken)
	}
	if got.RefreshToken != "rt" {
		t.Errorf("RefreshToken = %q, want rt（密码登录必须带回 refresh_token）", got.RefreshToken)
	}
}

func TestSub2apiLogin429IsNotRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"code":1,"message":"too many requests"}`))
	}))
	t.Cleanup(srv.Close)

	_, err := NewSub2apiClient(3*time.Second).Login(context.Background(), srv.URL, "u@example.com", "pw")
	if err == nil {
		t.Fatal("429 时应返回错误")
	}
	if IsLoginRejected(err) {
		t.Errorf("429 不该视为登录被拒: %v", err)
	}
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("429 应是 ErrRateLimited，实际: %v", err)
	}
}

func TestSub2apiLoginTurnstileAndHTMLAreNotRejected(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		wantErr error
	}{
		{
			name:    "turnstile",
			status:  http.StatusBadRequest,
			body:    `{"code":400,"message":"turnstile verification failed","reason":"TURNSTILE_VERIFICATION_FAILED"}`,
			wantErr: ErrRateLimited,
		},
		{
			name:    "html 403",
			status:  http.StatusForbidden,
			body:    "<html><body>blocked</body></html>",
			wantErr: ErrUpstreamHTML,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.body))
			}))
			t.Cleanup(srv.Close)

			_, err := NewSub2apiClient(3*time.Second).Login(context.Background(), srv.URL, "u@example.com", "pw")
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, 期望 %v", err, c.wantErr)
			}
			if IsLoginRejected(err) {
				t.Fatalf("不该视为登录被拒: %v", err)
			}
		})
	}
}

func TestSub2apiRefresh401IsInvalidAnd500IsNot(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":401,"message":"invalid refresh token","reason":"REFRESH_TOKEN_INVALID"}`))
	}))
	t.Cleanup(dead.Close)
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"code":502,"message":"bad gateway"}`))
	}))
	t.Cleanup(down.Close)

	_, err := NewSub2apiClient(3*time.Second).RefreshToken(context.Background(), dead.URL, "rt")
	if !errors.Is(err, ErrRefreshInvalid) {
		t.Fatalf("401 err = %v, 期望 ErrRefreshInvalid", err)
	}
	if IsLoginRejected(err) {
		t.Fatalf("刷新失效不该包 ErrLoginRejected: %v", err)
	}

	_, err = NewSub2apiClient(3*time.Second).RefreshToken(context.Background(), down.URL, "rt")
	if err == nil || errors.Is(err, ErrRefreshInvalid) || errors.Is(err, ErrLoginRejected) {
		t.Fatalf("502 应是普通失败, err = %v", err)
	}
}

func TestSub2apiRefresh429IsNotRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"code":1,"message":"too many requests"}`))
	}))
	t.Cleanup(srv.Close)

	_, err := NewSub2apiClient(3*time.Second).RefreshToken(context.Background(), srv.URL, "rt")
	if err == nil {
		t.Fatal("429 时应返回错误")
	}
	if IsLoginRejected(err) {
		t.Errorf("429 不该视为登录被拒: %v", err)
	}
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("429 应是 ErrRateLimited，实际: %v", err)
	}
}
