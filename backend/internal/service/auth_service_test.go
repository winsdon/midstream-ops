package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"sub2api-account-monitor/internal/config"
	"sub2api-account-monitor/internal/pkg/jwtutil"
	"sub2api-account-monitor/internal/repository"
)

func testAuthService(t *testing.T, mutate ...func(*config.AuthConfig)) *AuthService {
	t.Helper()
	cfg := &config.AuthConfig{
		Username:            "admin",
		Password:            "secret",
		RefreshTokenTTLDays: 30,
	}
	for _, fn := range mutate {
		fn(cfg)
	}
	jwtMgr := jwtutil.New(strings.Repeat("k", 32), time.Hour)
	return NewAuthService(cfg, jwtMgr, NewMemRefreshTokens())
}

func TestLoginIssuesRefreshToken(t *testing.T) {
	svc := testAuthService(t)
	got, err := svc.Login(context.Background(), "admin", "secret")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if got.Token == "" {
		t.Fatal("access token 为空")
	}
	if !strings.HasPrefix(got.RefreshToken, "rt_") {
		t.Errorf("refresh_token = %q, 应以 rt_ 开头", got.RefreshToken)
	}
	if got.ExpiresIn != 3600 {
		t.Errorf("expires_in = %d, want 3600", got.ExpiresIn)
	}
	if got.Username != "admin" {
		t.Errorf("username = %q", got.Username)
	}
	if _, err := svc.ParseToken(got.Token); err != nil {
		t.Errorf("签发的 access token 无法解析: %v", err)
	}
}

func TestLoginRejectsBadPassword(t *testing.T) {
	svc := testAuthService(t)
	_, err := svc.Login(context.Background(), "admin", "wrong")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("err = %v, want ErrInvalidCredentials", err)
	}
}

func TestRefreshRotatesToken(t *testing.T) {
	svc := testAuthService(t)
	first, err := svc.Login(context.Background(), "admin", "secret")
	if err != nil {
		t.Fatal(err)
	}

	second, err := svc.Refresh(context.Background(), first.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if second.RefreshToken == first.RefreshToken {
		t.Fatal("refresh token 未轮转")
	}
	if _, err := svc.ParseToken(second.Token); err != nil {
		t.Errorf("新 access token 无法解析: %v", err)
	}

	_, err = svc.Refresh(context.Background(), first.RefreshToken)
	if !errors.Is(err, ErrRefreshTokenInvalid) {
		t.Errorf("旧 refresh 再用不该成功, err=%v", err)
	}
}

func TestRefreshRejectsBadPrefix(t *testing.T) {
	svc := testAuthService(t)
	_, err := svc.Refresh(context.Background(), "not-a-refresh")
	if !errors.Is(err, ErrRefreshTokenInvalid) {
		t.Errorf("err = %v, want ErrRefreshTokenInvalid", err)
	}
}

func TestRefreshRejectsUnknownToken(t *testing.T) {
	svc := testAuthService(t)
	_, err := svc.Refresh(context.Background(), "rt_"+strings.Repeat("ab", 32))
	if !errors.Is(err, ErrRefreshTokenInvalid) {
		t.Errorf("err = %v, want ErrRefreshTokenInvalid", err)
	}
}

func TestRefreshRejectsExpired(t *testing.T) {
	svc := testAuthService(t)
	first, err := svc.Login(context.Background(), "admin", "secret")
	if err != nil {
		t.Fatal(err)
	}
	hash := hashRefreshToken(first.RefreshToken)
	rec, err := svc.tokens.Get(context.Background(), hash)
	if err != nil {
		t.Fatal(err)
	}
	rec.ExpiresAt = time.Now().Add(-time.Minute)
	if err := svc.tokens.Insert(context.Background(), *rec); err != nil {
		t.Fatal(err)
	}

	_, err = svc.Refresh(context.Background(), first.RefreshToken)
	if !errors.Is(err, ErrRefreshTokenExpired) {
		t.Errorf("err = %v, want ErrRefreshTokenExpired", err)
	}
}

func TestRefreshRejectsPasswordChange(t *testing.T) {
	tokens := NewMemRefreshTokens()
	cfg := &config.AuthConfig{Username: "admin", Password: "old", RefreshTokenTTLDays: 30}
	jwtMgr := jwtutil.New(strings.Repeat("k", 32), time.Hour)
	svc := NewAuthService(cfg, jwtMgr, tokens)
	first, err := svc.Login(context.Background(), "admin", "old")
	if err != nil {
		t.Fatal(err)
	}

	cfg.Password = "new"
	_, err = svc.Refresh(context.Background(), first.RefreshToken)
	if !errors.Is(err, ErrRefreshTokenInvalid) {
		t.Errorf("改密后 refresh err = %v, want ErrRefreshTokenInvalid", err)
	}
}

func TestLogoutRevokesRefresh(t *testing.T) {
	svc := testAuthService(t)
	first, err := svc.Login(context.Background(), "admin", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Logout(context.Background(), first.RefreshToken); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	_, err = svc.Refresh(context.Background(), first.RefreshToken)
	if !errors.Is(err, ErrRefreshTokenInvalid) {
		t.Errorf("登出后 refresh err = %v, want ErrRefreshTokenInvalid", err)
	}
}

func TestLogoutEmptyIsNoop(t *testing.T) {
	svc := testAuthService(t)
	if err := svc.Logout(context.Background(), ""); err != nil {
		t.Errorf("空 logout 应成功: %v", err)
	}
}

func TestParseTokenStillWorksWithoutStore(t *testing.T) {
	jwtMgr := jwtutil.New(strings.Repeat("k", 32), time.Hour)
	svc := NewAuthService(&config.AuthConfig{Username: "admin", Password: "x"}, jwtMgr, nil)
	tok, _, err := jwtMgr.Sign("admin")
	if err != nil {
		t.Fatal(err)
	}
	name, err := svc.ParseToken(tok)
	if err != nil || name != "admin" {
		t.Errorf("ParseToken = %q %v", name, err)
	}
}

func TestMemRotateMissing(t *testing.T) {
	s := NewMemRefreshTokens()
	err := s.Rotate(context.Background(), "missing", repository.RefreshToken{TokenHash: "n"})
	if !errors.Is(err, repository.ErrRefreshTokenNotFound) {
		t.Errorf("err = %v", err)
	}
}
