// Package secretbox 提供凭据的 AES-256-GCM 加密落库能力。
//
// 密钥来自环境变量 MONITOR_CREDENTIALS_KEY（base64 编码的 32 字节）。
// 未配置时退化为明文直通并在启动时大声警告 —— 保证老部署平滑升级，
// 配置密钥后新写入自动加密，读取时按前缀识别新旧格式。
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
)

// envKey 密钥环境变量名。
const envKey = "MONITOR_CREDENTIALS_KEY"

// prefix 密文标记前缀（区分明文旧数据）。
const prefix = "enc:v1:"

// Box 加解密器。零值（无密钥）为明文直通。
type Box struct {
	aead cipher.AEAD
}

// FromEnv 从环境变量装载密钥；未配置时返回明文直通 Box 并告警。
func FromEnv() *Box {
	raw := strings.TrimSpace(os.Getenv(envKey))
	if raw == "" {
		log.Printf("[警告] 未配置 %s，凭据将以明文存储。建议生成密钥：openssl rand -base64 32", envKey)
		return &Box{}
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(key) != 32 {
		log.Printf("[警告] %s 无效（须为 base64 的 32 字节），凭据将以明文存储", envKey)
		return &Box{}
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		log.Printf("[警告] 初始化 AES 失败: %v，凭据将以明文存储", err)
		return &Box{}
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		log.Printf("[警告] 初始化 GCM 失败: %v，凭据将以明文存储", err)
		return &Box{}
	}
	log.Printf("凭据加密已启用（AES-256-GCM）")
	return &Box{aead: aead}
}

// Enabled 是否启用了加密。
func (b *Box) Enabled() bool { return b != nil && b.aead != nil }

// Seal 加密明文；未启用时原样返回。空串恒为空串。
func (b *Box) Seal(plain string) string {
	if plain == "" || !b.Enabled() {
		return plain
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		// 随机源失败极罕见；宁可明文也不能丢数据
		log.Printf("[secretbox] 生成 nonce 失败，本次明文存储: %v", err)
		return plain
	}
	sealed := b.aead.Seal(nonce, nonce, []byte(plain), nil)
	return prefix + base64.StdEncoding.EncodeToString(sealed)
}

// Open 解密；识别前缀自动兼容明文旧数据。
func (b *Box) Open(stored string) (string, error) {
	if stored == "" || !strings.HasPrefix(stored, prefix) {
		return stored, nil // 明文旧数据或空值
	}
	if !b.Enabled() {
		return "", errors.New("数据已加密但未配置 " + envKey)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, prefix))
	if err != nil {
		return "", fmt.Errorf("密文格式错误: %w", err)
	}
	ns := b.aead.NonceSize()
	if len(raw) < ns {
		return "", errors.New("密文长度不足")
	}
	plain, err := b.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", fmt.Errorf("解密失败（密钥不匹配？）: %w", err)
	}
	return string(plain), nil
}

// MustOpen 解密；失败时返回空串并记日志（调用方把空凭据当「未配置」处理）。
func (b *Box) MustOpen(stored string) string {
	plain, err := b.Open(stored)
	if err != nil {
		log.Printf("[secretbox] %v", err)
		return ""
	}
	return plain
}
