package service

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testSecret = "test-secret-at-least-32-bytes-long-xxxxx"

// signTestToken 用给定密钥与方法签发一个 sub2api 风格的用户 token。
func signTestToken(t *testing.T, secret string, method jwt.SigningMethod, claims jwt.Claims) string {
	t.Helper()
	token := jwt.NewWithClaims(method, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signed
}

func validClaims(userID int64, exp time.Time) *Sub2apiTokenClaims {
	return &Sub2apiTokenClaims{
		UserID: userID,
		Email:  "u@example.com",
		Role:   "admin",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(exp),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
}

func TestSub2apiTokenVerifier_Valid(t *testing.T) {
	v := NewSub2apiTokenVerifier(testSecret)
	tok := signTestToken(t, testSecret, jwt.SigningMethodHS256, validClaims(42, time.Now().Add(time.Hour)))

	claims, err := v.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.UserID != 42 {
		t.Errorf("UserID = %d, want 42", claims.UserID)
	}
	if claims.UserIDString() != "42" {
		t.Errorf("UserIDString = %q, want 42", claims.UserIDString())
	}
	if claims.Email != "u@example.com" {
		t.Errorf("Email = %q", claims.Email)
	}
}

func TestSub2apiTokenVerifier_Rejects(t *testing.T) {
	v := NewSub2apiTokenVerifier(testSecret)

	t.Run("wrong secret", func(t *testing.T) {
		tok := signTestToken(t, "another-secret-at-least-32-bytes-long-yy", jwt.SigningMethodHS256,
			validClaims(1, time.Now().Add(time.Hour)))
		if _, err := v.Verify(tok); err == nil {
			t.Error("token signed with a different secret must be rejected")
		}
	})

	t.Run("expired", func(t *testing.T) {
		tok := signTestToken(t, testSecret, jwt.SigningMethodHS256,
			validClaims(1, time.Now().Add(-time.Minute)))
		if _, err := v.Verify(tok); err == nil {
			t.Error("expired token must be rejected")
		}
	})

	t.Run("missing user_id", func(t *testing.T) {
		tok := signTestToken(t, testSecret, jwt.SigningMethodHS256, &Sub2apiTokenClaims{
			Email:            "u@example.com",
			RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
		})
		if _, err := v.Verify(tok); err == nil {
			t.Error("token without user_id must be rejected")
		}
	})

	t.Run("garbage", func(t *testing.T) {
		if _, err := v.Verify("not-a-jwt"); err == nil {
			t.Error("malformed token must be rejected")
		}
	})

	t.Run("empty secret configured", func(t *testing.T) {
		empty := NewSub2apiTokenVerifier("")
		tok := signTestToken(t, testSecret, jwt.SigningMethodHS256, validClaims(1, time.Now().Add(time.Hour)))
		if _, err := empty.Verify(tok); err == nil {
			t.Error("verifier without secret must reject everything")
		}
	})

	t.Run("alg none is rejected", func(t *testing.T) {
		// 算法混淆攻击：伪造 alg=none 绕过验签。
		token := jwt.NewWithClaims(jwt.SigningMethodNone, validClaims(1, time.Now().Add(time.Hour)))
		tok, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
		if err != nil {
			t.Fatalf("sign none: %v", err)
		}
		if _, err := v.Verify(tok); err == nil {
			t.Error("alg=none token must be rejected")
		}
	})
}

func TestSub2apiTokenVerifier_IgnoresBindingClaims(t *testing.T) {
	// sub2api 的 token 还带 sid / bnd / token_version，本站不校验它们——
	// 带上这些字段的 token 仍应正常通过。
	v := NewSub2apiTokenVerifier(testSecret)
	claims := jwt.MapClaims{
		"user_id":       float64(7),
		"email":         "a@b.c",
		"role":          "user",
		"token_version": float64(1152996332033440745),
		"sid":           "f3593d0bd0ffec6de7b0b28a79bf60e1",
		"bnd":           "dd4d25e200d19c88607974cbe5617f08",
		"exp":           float64(time.Now().Add(time.Hour).Unix()),
	}
	tok := signTestToken(t, testSecret, jwt.SigningMethodHS256, claims)

	got, err := v.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.UserID != 7 {
		t.Errorf("UserID = %d, want 7", got.UserID)
	}
}
