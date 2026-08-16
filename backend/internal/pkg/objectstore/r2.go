package objectstore

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// R2 的 SigV4 参数。
//
// region 恒为 "auto"：Cloudflare R2 不分区域，但 SigV4 的凭据作用域里必须有一个
// 非空 region，官方文档指定用 auto。写别的值会得到 403 SignatureDoesNotMatch，
// 而错误信息不会告诉你是 region 的问题。
const (
	r2Region  = "auto"
	r2Service = "s3"

	// unsignedPayload 让签名不覆盖请求体。
	//
	// 【为什么视频必须用它】SigV4 默认要在签名里带请求体的 SHA256，这意味着上传前
	// 必须先完整读一遍内容算哈希。视频动辄几十 MB，要么全量进内存、要么落盘再读
	// 两遍。R2 与 S3 都接受 UNSIGNED-PAYLOAD（TLS 已保证传输完整性），可以边读边传。
	unsignedPayload = "UNSIGNED-PAYLOAD"
)

// R2Config Cloudflare R2 连接参数。
type R2Config struct {
	AccountID       string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	// PublicBaseURL 桶绑定的公开访问域名，如 https://media.example.com（无尾斜杠）。
	//
	// 【必须是自定义域】R2 自带的 *.r2.dev 域有严格的速率限制，生产环境用它会被限流。
	PublicBaseURL string
}

// R2 基于 S3 兼容接口的 R2 上传器。
//
// 【为什么手写签名而不用 aws-sdk-go-v2】本站只需要 PutObject 一个动作。
// 引入官方 SDK 要拉进 15+ 个间接依赖、二进制涨 8MB，而项目当前只有 6 个直接依赖，
// 单二进制交付是明确的取舍。SigV4 的规范是稳定的，配上官方测试向量的单测后
// 维护成本可以忽略。
type R2 struct {
	cfg    R2Config
	client *http.Client
	// endpoint 形如 https://<account>.r2.cloudflarestorage.com
	endpoint string
	host     string
	// now 可被测试替换，用于生成确定的签名时间戳。
	now func() time.Time
}

// NewR2 创建 R2 上传器。
func NewR2(cfg R2Config) *R2 {
	endpoint := fmt.Sprintf("https://%s.r2.cloudflarestorage.com", cfg.AccountID)
	host := fmt.Sprintf("%s.r2.cloudflarestorage.com", cfg.AccountID)
	return &R2{
		cfg: R2Config{
			AccountID:       cfg.AccountID,
			Bucket:          cfg.Bucket,
			AccessKeyID:     cfg.AccessKeyID,
			SecretAccessKey: cfg.SecretAccessKey,
			PublicBaseURL:   strings.TrimRight(strings.TrimSpace(cfg.PublicBaseURL), "/"),
		},
		endpoint: endpoint,
		host:     host,
		// 上传超时给足：视频几十 MB 且跨境，但不能没有上限——
		// 挂死的连接会一直占住转存器的并发槽。
		client: &http.Client{Timeout: 10 * time.Minute},
		now:    time.Now,
	}
}

// Put 上传对象并返回公开 URL。
func (r *R2) Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) (string, error) {
	key = strings.TrimPrefix(strings.TrimSpace(key), "/")
	if key == "" {
		return "", fmt.Errorf("对象键不能为空")
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	target := r.endpoint + "/" + r.cfg.Bucket + "/" + key
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, target, body)
	if err != nil {
		return "", err
	}
	// ContentLength 必须显式设置：http.NewRequest 只能从 *bytes.Buffer 等已知类型
	// 推断长度，对普通 io.Reader 会退化成 chunked 传输，而 S3 兼容接口不接受它。
	req.ContentLength = size
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Host", r.host)
	req.Host = r.host

	if err := r.sign(req, unsignedPayload, r.now().UTC()); err != nil {
		return "", err
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("上传对象存储失败: %w", redactR2(err.Error(), r.cfg))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("对象存储返回 %d: %s", resp.StatusCode,
			redactR2(strings.TrimSpace(string(raw)), r.cfg))
	}

	return r.cfg.PublicBaseURL + "/" + key, nil
}

// sign 按 AWS Signature Version 4 给请求加上 Authorization 头。
//
// 实现遵循 https://docs.aws.amazon.com/general/latest/gr/sigv4_signing.html 的四步：
// 规范请求 → 待签字符串 → 签名密钥 → 签名。R2 与 S3 在这一层完全兼容。
func (r *R2) sign(req *http.Request, payloadHash string, t time.Time) error {
	amzDate := t.Format("20060102T150405Z")
	dateStamp := t.Format("20060102")

	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)

	signedHeaders, canonicalHeaders := canonicalizeHeaders(req)

	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI(req.URL),
		canonicalQuery(req.URL),
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	scope := strings.Join([]string{dateStamp, r2Region, r2Service, "aws4_request"}, "/")
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		hexSHA256([]byte(canonicalRequest)),
	}, "\n")

	signature := hex.EncodeToString(hmacSHA256(
		signingKey(r.cfg.SecretAccessKey, dateStamp), []byte(stringToSign)))

	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		r.cfg.AccessKeyID, scope, signedHeaders, signature))
	return nil
}

// canonicalizeHeaders 生成签名用的 header 列表与规范化 header 串。
//
// 【Host 必须手工加入】Go 的 http.Request 把 Host 放在结构体字段而不是 Header map
// 里，遍历 Header 拿不到它。而 SigV4 规定 host 必须参与签名——漏掉它得到的是
// 403 而不是任何有用的提示。
func canonicalizeHeaders(req *http.Request) (signedHeaders, canonicalHeaders string) {
	values := map[string]string{"host": req.Host}
	for name, vs := range req.Header {
		lower := strings.ToLower(name)
		// Authorization 是签名的产物，不能参与签名本身
		if lower == "authorization" {
			continue
		}
		values[lower] = strings.TrimSpace(strings.Join(vs, ","))
	}

	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)

	var sb strings.Builder
	for _, name := range names {
		sb.WriteString(name)
		sb.WriteByte(':')
		sb.WriteString(values[name])
		sb.WriteByte('\n')
	}
	return strings.Join(names, ";"), sb.String()
}

// canonicalURI 返回签名用的路径。
//
// 【用 EscapedPath 而非 Path】对象键里可能含空格或中文，签名必须用与实际请求行
// 完全一致的编码形式。用已解码的 Path 会让含特殊字符的键必然 403。
func canonicalURI(u *url.URL) string {
	p := u.EscapedPath()
	if p == "" {
		return "/"
	}
	return p
}

// canonicalQuery 返回按名称排序的规范化查询串。本站的 PutObject 不带查询参数，
// 但按规范实现能让这份签名逻辑对将来的其它操作同样成立。
func canonicalQuery(u *url.URL) string {
	q := u.Query()
	if len(q) == 0 {
		return ""
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(q))
	for _, k := range keys {
		vs := append([]string(nil), q[k]...)
		sort.Strings(vs)
		for _, v := range vs {
			parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(v))
		}
	}
	return strings.Join(parts, "&")
}

// signingKey 派生签名密钥：四层 HMAC 链，每层用上一层的结果当密钥。
func signingKey(secret, dateStamp string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), []byte(dateStamp))
	kRegion := hmacSHA256(kDate, []byte(r2Region))
	kService := hmacSHA256(kRegion, []byte(r2Service))
	return hmacSHA256(kService, []byte("aws4_request"))
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func hexSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// redactR2 抹掉错误信息里的凭据。
//
// 【必须有】上传失败时错误里可能带上完整 URL 或上游回显的凭据片段，
// 而这些错误会进日志。与 media_gateway 的 redactError 同一纪律。
func redactR2(msg string, cfg R2Config) error {
	for _, secret := range []string{cfg.SecretAccessKey, cfg.AccessKeyID} {
		if secret != "" {
			msg = strings.ReplaceAll(msg, secret, "***")
		}
	}
	return fmt.Errorf("%s", msg)
}

// 编译期断言：R2 必须满足 Uploader。
var _ Uploader = (*R2)(nil)

// bytesReaderSize 是给调用方的便利函数：从 []byte 构造带长度的 reader。
// 图片走这条路径（内容已在内存里），视频走流式路径。
func BytesBody(data []byte) (io.Reader, int64) {
	return bytes.NewReader(data), int64(len(data))
}
