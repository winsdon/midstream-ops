package objectstore

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// 浏览器直传所需的桶 CORS。
//
// 预签名 PUT 打的是 R2 S3 端点，不是自定义域。没这条规则时，浏览器会在
// 预检阶段被拦下，表现是前端 uploadFailed、后端日志里什么都没有。
const browserPutCORS = `<?xml version="1.0" encoding="UTF-8"?>
<CORSConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <CORSRule>
    <AllowedOrigin>*</AllowedOrigin>
    <AllowedMethod>GET</AllowedMethod>
    <AllowedMethod>HEAD</AllowedMethod>
    <AllowedMethod>PUT</AllowedMethod>
    <AllowedHeader>*</AllowedHeader>
    <ExposeHeader>ETag</ExposeHeader>
    <MaxAgeSeconds>3600</MaxAgeSeconds>
  </CORSRule>
</CORSConfiguration>`

// EnsureBrowserPutCORS 把浏览器直传所需的 CORS 写到桶上。
//
// 幂等：重复调用只是再写同一份规则。失败不该挡住服务启动——
// 运营方也可以在 Cloudflare 控制台手工配，这里只是少一步。
func (r *R2) EnsureBrowserPutCORS(ctx context.Context) error {
	body := []byte(browserPutCORS)
	sum := md5.Sum(body)
	md5b64 := base64.StdEncoding.EncodeToString(sum[:])

	target := r.endpoint + "/" + r.cfg.Bucket + "?cors"
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", "application/xml")
	req.Header.Set("Content-MD5", md5b64)
	req.Header.Set("Host", r.host)
	req.Host = r.host

	if err := r.sign(req, hexSHA256(body), r.now().UTC()); err != nil {
		return err
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("配置对象存储 CORS 失败: %w", redactR2(err.Error(), r.cfg))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("对象存储 CORS 返回 %d: %s", resp.StatusCode,
			redactR2(strings.TrimSpace(string(raw)), r.cfg))
	}
	return nil
}
