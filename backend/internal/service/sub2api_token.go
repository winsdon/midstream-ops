package service

import (
	"errors"
	"strconv"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// ErrSub2apiTokenInvalid sub2api 用户 token 验签失败或已过期。
var ErrSub2apiTokenInvalid = errors.New("sub2api token invalid")

// Sub2apiTokenClaims 是 sub2api 签发的用户 JWT 中本站关心的声明。
//
// sub2api 还会带 token_version / sid / bnd 等字段，本站刻意不校验它们：
//   - bnd 是 sha256(客户端IP + UA) 的会话指纹，只在 sub2api 自己的请求上下文中成立；
//   - token_version 需要查 sub2api 的用户表才能比对。
//
// 这意味着本站无法感知「用户改密码后 token 被吊销」这类实时状态，旧 token 在
// 自然过期前仍可访问模型广场。鉴于广场是只读的价格展示页、不含账号与密钥数据，
// 这个窗口可以接受；换取的是零副作用（回调 sub2api 校验会因指纹不符而把用户踢下线）。
type Sub2apiTokenClaims struct {
	UserID int64  `json:"user_id"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	jwt.RegisteredClaims
}

// Sub2apiTokenVerifier 用共享密钥本地验签 sub2api 用户 token。
type Sub2apiTokenVerifier struct {
	secret []byte
}

// NewSub2apiTokenVerifier 创建验签器。secret 须与 sub2api 的 jwt.secret 一致。
func NewSub2apiTokenVerifier(secret string) *Sub2apiTokenVerifier {
	return &Sub2apiTokenVerifier{secret: []byte(secret)}
}

// Verify 校验签名与有效期，返回用户身份。
// 限定 HMAC 签名族，防止算法混淆攻击（如伪造 alg=none 或 RS256）。
func (v *Sub2apiTokenVerifier) Verify(tokenStr string) (*Sub2apiTokenClaims, error) {
	if len(v.secret) == 0 {
		return nil, ErrSub2apiTokenInvalid
	}
	claims := &Sub2apiTokenClaims{}
	token, err := jwt.ParseWithClaims(strings.TrimSpace(tokenStr), claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrSub2apiTokenInvalid
		}
		return v.secret, nil
	})
	if err != nil || !token.Valid {
		return nil, ErrSub2apiTokenInvalid
	}
	if claims.UserID <= 0 {
		return nil, ErrSub2apiTokenInvalid
	}
	return claims, nil
}

// UserIDString 返回字符串形式的用户 ID（与 URL 透传的 user_id 比对用）。
func (c *Sub2apiTokenClaims) UserIDString() string {
	return strconv.FormatInt(c.UserID, 10)
}
