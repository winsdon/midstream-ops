package service

import (
	"crypto/subtle"
	"errors"
	"strings"
	"time"

	"sub2api-account-monitor/internal/config"
	"sub2api-account-monitor/internal/pkg/jwtutil"

	"golang.org/x/crypto/bcrypt"
)

// ErrInvalidCredentials 用户名或密码错误。
var ErrInvalidCredentials = errors.New("用户名或密码错误")

// AuthService 处理登录认证与 token 签发。
type AuthService struct {
	cfg     *config.AuthConfig
	jwt     *jwtutil.Manager
	isBcrypt bool
}

// NewAuthService 创建 AuthService。
func NewAuthService(cfg *config.AuthConfig, jwtMgr *jwtutil.Manager) *AuthService {
	pw := cfg.Password
	isBcrypt := strings.HasPrefix(pw, "$2a$") || strings.HasPrefix(pw, "$2b$") || strings.HasPrefix(pw, "$2y$")
	return &AuthService{cfg: cfg, jwt: jwtMgr, isBcrypt: isBcrypt}
}

// LoginResult 登录成功结果。
type LoginResult struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	Username  string    `json:"username"`
}

// Login 校验用户名密码，成功则签发 JWT。
func (s *AuthService) Login(username, password string) (*LoginResult, error) {
	if subtle.ConstantTimeCompare([]byte(username), []byte(s.cfg.Username)) != 1 {
		return nil, ErrInvalidCredentials
	}
	if !s.checkPassword(password) {
		return nil, ErrInvalidCredentials
	}
	token, expiresAt, err := s.jwt.Sign(username)
	if err != nil {
		return nil, err
	}
	return &LoginResult{Token: token, ExpiresAt: expiresAt, Username: username}, nil
}

// checkPassword 根据配置（明文/bcrypt）校验密码。
func (s *AuthService) checkPassword(password string) bool {
	if s.isBcrypt {
		return bcrypt.CompareHashAndPassword([]byte(s.cfg.Password), []byte(password)) == nil
	}
	return subtle.ConstantTimeCompare([]byte(password), []byte(s.cfg.Password)) == 1
}

// ParseToken 解析 token，返回用户名。
func (s *AuthService) ParseToken(tokenStr string) (string, error) {
	claims, err := s.jwt.Parse(tokenStr)
	if err != nil {
		return "", err
	}
	return claims.Username, nil
}
