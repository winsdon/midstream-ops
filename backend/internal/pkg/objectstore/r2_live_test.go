package objectstore_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"sub2api-account-monitor/internal/pkg/objectstore"
)

// TestR2LiveUpload 用真实 R2 凭据验证「上传 → 公开 URL 读回」闭环。
//
// 【为什么需要这条真机测试】签名错了的唯一表现是 403，且签名链里每一个字节
// 都重要——本机的 mock 测试只能证明「签名结构对」，证明不了「上游真的接受
// 这个签名」。AWS 官方测试向量钉住的是 HMAC 派生，这一步钉的是整条 HTTP 链路。
//
// 凭据不硬编码在源码里，从环境变量读取（镜像 CI / 本地手动跑）：
//
//	R2_LIVE_ACCOUNT_ID / R2_LIVE_BUCKET / R2_LIVE_ACCESS_KEY /
//	R2_LIVE_SECRET_KEY / R2_LIVE_PUBLIC_URL
//
// 任一缺失即 Skip，避免在无凭据的环境里留下「伪造失败」的误报。
func TestR2LiveUpload(t *testing.T) {
	cfg := objectstore.R2Config{}
	if v := tEnv("R2_LIVE_ACCOUNT_ID"); v != "" {
		cfg.AccountID = v
	}
	if v := tEnv("R2_LIVE_BUCKET"); v != "" {
		cfg.Bucket = v
	}
	if v := tEnv("R2_LIVE_ACCESS_KEY"); v != "" {
		cfg.AccessKeyID = v
	}
	if v := tEnv("R2_LIVE_SECRET_KEY"); v != "" {
		cfg.SecretAccessKey = v
	}
	if v := tEnv("R2_LIVE_PUBLIC_URL"); v != "" {
		cfg.PublicBaseURL = v
	}
	if cfg.AccountID == "" || cfg.Bucket == "" || cfg.AccessKeyID == "" ||
		cfg.SecretAccessKey == "" || cfg.PublicBaseURL == "" {
		t.Skip("未设置 R2_LIVE_* 环境变量，跳过真机上传验证")
	}

	r := objectstore.NewR2(cfg)

	key := fmt.Sprintf("test/verify-%d.txt", time.Now().Unix())
	content := "r2-upload-verify OK " + time.Now().Format(time.RFC3339)
	body, size := objectstore.BytesBody([]byte(content))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	url, err := r.Put(ctx, key, body, size, "text/plain; charset=utf-8")
	if err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	t.Logf("上传成功: %s", url)

	// 验证公开 URL 确实能读回内容 —— 证明公开访问域名配置正确，
	// 而不只是「上传成功」（上传只要求 S3 endpoint 可达，与公开域名无关）。
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("读取公开 URL 失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("公开 URL 返回 %d，预期 200", resp.StatusCode)
	}
	got, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(got), "r2-upload-verify") {
		t.Fatalf("读回内容不匹配: %q", string(got))
	}
	t.Logf("读回成功: %s", got)
}

func TestR2LivePresign(t *testing.T) {
	cfg := objectstore.R2Config{}
	if v := tEnv("R2_LIVE_ACCOUNT_ID"); v != "" {
		cfg.AccountID = v
	}
	if v := tEnv("R2_LIVE_BUCKET"); v != "" {
		cfg.Bucket = v
	}
	if v := tEnv("R2_LIVE_ACCESS_KEY"); v != "" {
		cfg.AccessKeyID = v
	}
	if v := tEnv("R2_LIVE_SECRET_KEY"); v != "" {
		cfg.SecretAccessKey = v
	}
	if v := tEnv("R2_LIVE_PUBLIC_URL"); v != "" {
		cfg.PublicBaseURL = v
	}
	if cfg.AccountID == "" || cfg.Bucket == "" || cfg.AccessKeyID == "" ||
		cfg.SecretAccessKey == "" || cfg.PublicBaseURL == "" {
		t.Skip("未设置 R2_LIVE_* 环境变量，跳过真机预签名验证")
	}

	r := objectstore.NewR2(cfg)
	key := fmt.Sprintf("test/presign-%d.txt", time.Now().Unix())
	content := "presign-ok"
	uploadURL, publicURL, err := r.PresignPut(key, "text/plain; charset=utf-8", time.Minute)
	if err != nil {
		t.Fatalf("预签名失败: %v", err)
	}

	req, err := http.NewRequest(http.MethodPut, uploadURL, strings.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("预签名 PUT 失败: %v", err)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("预签名 PUT 返回 %d: %s", resp.StatusCode, body)
	}

	got, err := http.Get(publicURL)
	if err != nil {
		t.Fatalf("读公开 URL 失败: %v", err)
	}
	defer got.Body.Close()
	raw, _ := io.ReadAll(got.Body)
	if got.StatusCode != http.StatusOK || !strings.Contains(string(raw), "presign-ok") {
		t.Fatalf("公开读回失败 %d: %q", got.StatusCode, raw)
	}
}

func tEnv(name string) string {
	return os.Getenv(name)
}
