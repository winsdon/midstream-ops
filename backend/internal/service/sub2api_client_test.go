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
