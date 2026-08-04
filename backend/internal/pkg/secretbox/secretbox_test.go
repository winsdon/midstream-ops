package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"strings"
	"testing"
)

// newTestBox 构造带固定密钥的 Box（不走环境变量）。
func newTestBox(t *testing.T) *Box {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	return &Box{aead: aead}
}

func TestSealOpenRoundtrip(t *testing.T) {
	b := newTestBox(t)
	tests := []string{"password123", "含中文的密码", "sk-" + strings.Repeat("x", 100), "a"}
	for _, plain := range tests {
		sealed := b.Seal(plain)
		if !strings.HasPrefix(sealed, prefix) {
			t.Fatalf("密文缺少前缀: %q", sealed)
		}
		if sealed == plain {
			t.Fatalf("未加密: %q", plain)
		}
		got, err := b.Open(sealed)
		if err != nil {
			t.Fatalf("解密失败: %v", err)
		}
		if got != plain {
			t.Fatalf("往返不一致: got %q want %q", got, plain)
		}
	}
}

func TestEmptyStringPassthrough(t *testing.T) {
	b := newTestBox(t)
	if b.Seal("") != "" {
		t.Fatal("空串应恒为空串")
	}
	if got, err := b.Open(""); err != nil || got != "" {
		t.Fatalf("空串解密应为空串, got %q err %v", got, err)
	}
}

func TestPlaintextLegacyPassthrough(t *testing.T) {
	b := newTestBox(t)
	// 无前缀的旧明文数据原样返回
	got, err := b.Open("legacy-plain-password")
	if err != nil || got != "legacy-plain-password" {
		t.Fatalf("明文旧数据应原样返回, got %q err %v", got, err)
	}
}

func TestDisabledBoxPassthrough(t *testing.T) {
	b := &Box{} // 未配置密钥
	if b.Enabled() {
		t.Fatal("零值 Box 不应启用")
	}
	if b.Seal("secret") != "secret" {
		t.Fatal("未启用时应明文直通")
	}
	// 但读到密文时必须报错（不能静默返回密文当明文用）
	if _, err := b.Open(prefix + "deadbeef"); err == nil {
		t.Fatal("未配置密钥读密文应报错")
	}
}

func TestWrongKeyFails(t *testing.T) {
	b1 := newTestBox(t)
	sealed := b1.Seal("secret")

	// 另一把密钥
	key2 := make([]byte, 32)
	for i := range key2 {
		key2[i] = byte(255 - i)
	}
	block, _ := aes.NewCipher(key2)
	aead, _ := cipher.NewGCM(block)
	b2 := &Box{aead: aead}

	if _, err := b2.Open(sealed); err == nil {
		t.Fatal("密钥不匹配应解密失败")
	}
	if got := b2.MustOpen(sealed); got != "" {
		t.Fatalf("MustOpen 失败时应返回空串, got %q", got)
	}
}

func TestNonceUniqueness(t *testing.T) {
	b := newTestBox(t)
	// 同一明文两次加密应产生不同密文（随机 nonce）
	a := b.Seal("same-input")
	c := b.Seal("same-input")
	if a == c {
		t.Fatal("两次加密不应产生相同密文")
	}
}
