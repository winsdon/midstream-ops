package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"sub2api-account-monitor/internal/config"
	"sub2api-account-monitor/internal/pkg/jwtutil"
	"sub2api-account-monitor/internal/repository"

	"golang.org/x/crypto/bcrypt"
)

var (
	// ErrInvalidCredentials 用户名或密码错误。
	ErrInvalidCredentials = errors.New("用户名或密码错误")
	// ErrRefreshTokenInvalid refresh token 不存在、格式错或已吊销。
	ErrRefreshTokenInvalid = errors.New("refresh token 无效")
	// ErrRefreshTokenExpired refresh token 已过期。
	ErrRefreshTokenExpired = errors.New("refresh token 已过期")
)

const refreshTokenPrefix = "rt_"

// refreshTokenStore 管理员 refresh token 持久化。生产走 PG，测试走内存。
type refreshTokenStore interface {
	Insert(ctx context.Context, rec repository.RefreshToken) error
	Get(ctx context.Context, tokenHash string) (*repository.RefreshToken, error)
	Delete(ctx context.Context, tokenHash string) error
	Rotate(ctx context.Context, oldHash string, rec repository.RefreshToken) error
}

// AuthService 处理登录认证与 token 签发。
type AuthService struct {
	cfg      *config.AuthConfig
	jwt      *jwtutil.Manager
	tokens   refreshTokenStore
	isBcrypt bool
}

// NewAuthService 创建 AuthService。tokens 为 nil 时仍能 ParseToken，但 Login/Refresh 会失败。
func NewAuthService(cfg *config.AuthConfig, jwtMgr *jwtutil.Manager, tokens refreshTokenStore) *AuthService {
	pw := cfg.Password
	isBcrypt := strings.HasPrefix(pw, "$2a$") || strings.HasPrefix(pw, "$2b$") || strings.HasPrefix(pw, "$2y$")
	return &AuthService{cfg: cfg, jwt: jwtMgr, tokens: tokens, isBcrypt: isBcrypt}
}

// LoginResult 登录 / 续期成功结果。
type LoginResult struct {
	Token        string    `json:"token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresIn    int       `json:"expires_in"`
	ExpiresAt    time.Time `json:"expires_at"`
	Username     string    `json:"username"`
}

// Login 校验用户名密码，成功则签发 access JWT + refresh token。
func (s *AuthService) Login(ctx context.Context, username, password string) (*LoginResult, error) {
	if subtle.ConstantTimeCompare([]byte(username), []byte(s.cfg.Username)) != 1 {
		return nil, ErrInvalidCredentials
	}
	if !s.checkPassword(password) {
		return nil, ErrInvalidCredentials
	}
	return s.issuePair(ctx, username, "")
}

// Refresh 用 refresh token 轮转出新的 token 对。旧 refresh 立刻失效。
func (s *AuthService) Refresh(ctx context.Context, raw string) (*LoginResult, error) {
	if s.tokens == nil {
		return nil, ErrRefreshTokenInvalid
	}
	if !strings.HasPrefix(raw, refreshTokenPrefix) {
		return nil, ErrRefreshTokenInvalid
	}
	hash := hashRefreshToken(raw)
	rec, err := s.tokens.Get(ctx, hash)
	if err != nil {
		if errors.Is(err, repository.ErrRefreshTokenNotFound) {
			return nil, ErrRefreshTokenInvalid
		}
		return nil, err
	}
	if time.Now().After(rec.ExpiresAt) {
		_ = s.tokens.Delete(ctx, hash)
		return nil, ErrRefreshTokenExpired
	}
	if rec.PasswordFP != passwordFingerprint(s.cfg.Password) || rec.Username != s.cfg.Username {
		_ = s.tokens.Delete(ctx, hash)
		return nil, ErrRefreshTokenInvalid
	}
	return s.issuePair(ctx, rec.Username, hash)
}

// Logout 撤销指定 refresh token。空串或已不存在都视为成功。
func (s *AuthService) Logout(ctx context.Context, raw string) error {
	if s.tokens == nil || raw == "" {
		return nil
	}
	return s.tokens.Delete(ctx, hashRefreshToken(raw))
}

func (s *AuthService) issuePair(ctx context.Context, username, oldHash string) (*LoginResult, error) {
	if s.tokens == nil {
		return nil, errors.New("refresh token store not configured")
	}
	token, expiresAt, err := s.jwt.Sign(username)
	if err != nil {
		return nil, err
	}
	raw, rec, err := newRefreshRecord(username, passwordFingerprint(s.cfg.Password), s.refreshTTL())
	if err != nil {
		return nil, err
	}
	if oldHash == "" {
		err = s.tokens.Insert(ctx, rec)
	} else {
		err = s.tokens.Rotate(ctx, oldHash, rec)
	}
	if err != nil {
		return nil, err
	}
	return &LoginResult{
		Token:        token,
		RefreshToken: raw,
		ExpiresIn:    int(s.jwt.TTL().Seconds()),
		ExpiresAt:    expiresAt,
		Username:     username,
	}, nil
}

func (s *AuthService) refreshTTL() time.Duration {
	days := s.cfg.RefreshTokenTTLDays
	if days <= 0 {
		days = 30
	}
	return time.Duration(days) * 24 * time.Hour
}

func (s *AuthService) checkPassword(password string) bool {
	if s.isBcrypt {
		return bcrypt.CompareHashAndPassword([]byte(s.cfg.Password), []byte(password)) == nil
	}
	return subtle.ConstantTimeCompare([]byte(password), []byte(s.cfg.Password)) == 1
}

// ParseToken 解析 access token，返回用户名。
func (s *AuthService) ParseToken(tokenStr string) (string, error) {
	claims, err := s.jwt.Parse(tokenStr)
	if err != nil {
		return "", err
	}
	return claims.Username, nil
}

func newRefreshRecord(username, passwordFP string, ttl time.Duration) (string, repository.RefreshToken, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", repository.RefreshToken{}, fmt.Errorf("generate refresh token: %w", err)
	}
	raw := refreshTokenPrefix + hex.EncodeToString(b)
	return raw, repository.RefreshToken{
		TokenHash:  hashRefreshToken(raw),
		Username:   username,
		PasswordFP: passwordFP,
		ExpiresAt:  time.Now().Add(ttl),
	}, nil
}

func hashRefreshToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func passwordFingerprint(password string) string {
	sum := sha256.Sum256([]byte(password))
	return hex.EncodeToString(sum[:])
}
