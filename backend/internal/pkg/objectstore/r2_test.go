package objectstore

import (
	"context"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const (
	testAccessKey = "AKIAIOSFODNN7EXAMPLE"
	// AWS 文档的示例密钥，注意是 "DENG+bPx" 而非 "DENG/bPx" ——
	// 抄错一个字符，下面的向量断言就会失败并让人误以为派生链写错了。
	testSecretKey = "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"
)

// AWS 官方 SigV4 文档给出的密钥派生测试向量。
//
// 【为什么必须钉死这个】签名错了的唯一表现是 403 SignatureDoesNotMatch，
// 上游不会告诉你错在哪一步。有了这个向量，派生链一旦被改坏，测试立刻指出
// 是密钥派生的问题，而不是让人对着 403 从头猜。
//
// 向量出自 https://docs.aws.amazon.com/general/latest/gr/signature-v4-examples.html
// （region=us-east-1, service=iam, date=20150830）。本包的常量是 auto/s3，
// 故这里直接调用底层的 hmac 链而非 signingKey()。
func TestSigningKeyDerivationMatchesAWSVector(t *testing.T) {
	kDate := hmacSHA256([]byte("AWS4"+testSecretKey), []byte("20150830"))
	kRegion := hmacSHA256(kDate, []byte("us-east-1"))
	kService := hmacSHA256(kRegion, []byte("iam"))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))

	const want = "c4afb1cc5771d871763a393e44b703571b55cc28424d1a5e86da6ed3c154a4b9"
	if got := hex.EncodeToString(kSigning); got != want {
		t.Fatalf("密钥派生链与 AWS 官方向量不符\n want %s\n  got %s", want, got)
	}
}

// SHA256 是签名的第一环，搞错编码后面全错。
func TestHexSHA256(t *testing.T) {
	// 空串的 SHA256 —— SigV4 里表示「无请求体」的固定值
	const emptyHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got := hexSHA256(nil); got != emptyHash {
		t.Fatalf("空串哈希错误: %s", got)
	}
}

// 规范化 header 必须包含 host。
//
// 【这是最容易漏的一条】Go 把 Host 放在 Request 结构体字段而不是 Header map 里，
// 遍历 Header 拿不到它。漏掉 host 的表现是 403，而错误信息完全不提这件事。
func TestCanonicalizeHeadersIncludesHost(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPut, "https://acc.r2.cloudflarestorage.com/bucket/a.png", nil)
	req.Host = "acc.r2.cloudflarestorage.com"
	req.Header.Set("Content-Type", "image/png")
	req.Header.Set("X-Amz-Date", "20260101T000000Z")
	// Authorization 是签名的产物，不能参与签名本身
	req.Header.Set("Authorization", "stale-value")

	signed, canonical := canonicalizeHeaders(req)

	if !strings.Contains(signed, "host") {
		t.Fatalf("SignedHeaders 必须含 host，实得 %s", signed)
	}
	if strings.Contains(signed, "authorization") {
		t.Fatalf("SignedHeaders 不得含 authorization，实得 %s", signed)
	}
	// 必须按字典序
	if signed != "content-type;host;x-amz-date" {
		t.Fatalf("SignedHeaders 应按字典序排列，实得 %s", signed)
	}
	if !strings.Contains(canonical, "host:acc.r2.cloudflarestorage.com\n") {
		t.Fatalf("规范 header 缺少 host 行:\n%s", canonical)
	}
}

// 对象键含空格 / 中文时，签名用的路径必须与实际请求行的编码完全一致。
func TestCanonicalURIUsesEscapedPath(t *testing.T) {
	req, err := http.NewRequest(http.MethodPut,
		"https://acc.r2.cloudflarestorage.com/bucket/media/a%20b/%E5%9B%BE.png", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := canonicalURI(req.URL)
	if got != "/bucket/media/a%20b/%E5%9B%BE.png" {
		t.Fatalf("规范路径应保持转义形态，实得 %s", got)
	}
}

// Authorization 头的整体形态：四个部分齐全、scope 用 auto/s3。
func TestSignProducesWellFormedAuthorization(t *testing.T) {
	r := NewR2(R2Config{
		AccountID: "acc", Bucket: "b",
		AccessKeyID: testAccessKey, SecretAccessKey: testSecretKey,
		PublicBaseURL: "https://cdn.example.com",
	})

	req, _ := http.NewRequest(http.MethodPut, "https://acc.r2.cloudflarestorage.com/b/x.png", nil)
	req.Host = "acc.r2.cloudflarestorage.com"
	req.Header.Set("Content-Type", "image/png")

	fixed := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	if err := r.sign(req, unsignedPayload, fixed); err != nil {
		t.Fatalf("签名失败: %v", err)
	}

	auth := req.Header.Get("Authorization")
	for _, want := range []string{
		"AWS4-HMAC-SHA256 ",
		"Credential=" + testAccessKey + "/20260815/auto/s3/aws4_request",
		"SignedHeaders=",
		"Signature=",
	} {
		if !strings.Contains(auth, want) {
			t.Fatalf("Authorization 缺少 %q:\n%s", want, auth)
		}
	}
	if req.Header.Get("X-Amz-Date") != "20260815T120000Z" {
		t.Fatalf("X-Amz-Date 格式错误: %s", req.Header.Get("X-Amz-Date"))
	}
	if req.Header.Get("X-Amz-Content-Sha256") != unsignedPayload {
		t.Fatalf("X-Amz-Content-Sha256 应为 UNSIGNED-PAYLOAD，实得 %s",
			req.Header.Get("X-Amz-Content-Sha256"))
	}
}

// 相同输入必须得到相同签名——签名过程里混进任何非确定性（如未固定的时间戳）
// 都会让排障变成猜谜。
func TestSignIsDeterministic(t *testing.T) {
	r := NewR2(R2Config{
		AccountID: "acc", Bucket: "b",
		AccessKeyID: testAccessKey, SecretAccessKey: testSecretKey,
	})
	fixed := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)

	sigOf := func() string {
		req, _ := http.NewRequest(http.MethodPut, "https://acc.r2.cloudflarestorage.com/b/x.png", nil)
		req.Host = "acc.r2.cloudflarestorage.com"
		req.Header.Set("Content-Type", "image/png")
		_ = r.sign(req, unsignedPayload, fixed)
		return req.Header.Get("Authorization")
	}
	if a, b := sigOf(), sigOf(); a != b {
		t.Fatalf("相同输入的签名不一致:\n%s\n%s", a, b)
	}
}

// 端到端：Put 发出的请求形态正确，且返回公开 URL 而非内部 endpoint。
func TestPutSendsSignedRequestAndReturnsPublicURL(t *testing.T) {
	var gotMethod, gotPath, gotAuth, gotType string
	var gotLen int64
	var gotBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotMethod = req.Method
		gotPath = req.URL.Path
		gotAuth = req.Header.Get("Authorization")
		gotType = req.Header.Get("Content-Type")
		gotLen = req.ContentLength
		gotBody, _ = io.ReadAll(req.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := NewR2(R2Config{
		AccountID: "acc", Bucket: "media-artifacts",
		AccessKeyID: testAccessKey, SecretAccessKey: testSecretKey,
		PublicBaseURL: "https://cdn.example.com/", // 尾斜杠应被规范化掉
	})
	// 指向本地测试服务器，但保留 R2 的 Host 头以验证签名走的是真实 host
	r.endpoint = srv.URL

	body, size := BytesBody([]byte("PNGDATA"))
	url, err := r.Put(context.Background(), "media/2026/08/42/0.png", body, size, "image/png")
	if err != nil {
		t.Fatalf("上传失败: %v", err)
	}

	if gotMethod != http.MethodPut {
		t.Fatalf("应为 PUT，实得 %s", gotMethod)
	}
	if gotPath != "/media-artifacts/media/2026/08/42/0.png" {
		t.Fatalf("路径应含桶名与对象键，实得 %s", gotPath)
	}
	if !strings.HasPrefix(gotAuth, "AWS4-HMAC-SHA256 ") {
		t.Fatalf("缺少 SigV4 签名: %s", gotAuth)
	}
	if gotType != "image/png" {
		t.Fatalf("Content-Type 未透传: %s", gotType)
	}
	// 【必须是确定长度而非 chunked】S3 兼容接口不接受 chunked 传输
	if gotLen != 7 {
		t.Fatalf("ContentLength 应为 7（不能走 chunked），实得 %d", gotLen)
	}
	if string(gotBody) != "PNGDATA" {
		t.Fatalf("请求体错误: %s", gotBody)
	}
	// 返回的必须是公开域名，不能是内部 endpoint —— 后者需要签名才能访问
	if url != "https://cdn.example.com/media/2026/08/42/0.png" {
		t.Fatalf("应返回公开 URL，实得 %s", url)
	}
}

// 上游报错时必须带上状态码，且绝不泄露凭据。
func TestPutRedactsCredentialsInErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		// 上游把 access key 回显在错误里——这种情况真实存在
		_, _ = io.WriteString(w, "SignatureDoesNotMatch for key "+testAccessKey+
			" secret "+testSecretKey)
	}))
	defer srv.Close()

	r := NewR2(R2Config{
		AccountID: "acc", Bucket: "b",
		AccessKeyID: testAccessKey, SecretAccessKey: testSecretKey,
		PublicBaseURL: "https://cdn.example.com",
	})
	r.endpoint = srv.URL

	body, size := BytesBody([]byte("x"))
	_, err := r.Put(context.Background(), "a.png", body, size, "image/png")
	if err == nil {
		t.Fatal("403 应返回错误")
	}
	msg := err.Error()
	if !strings.Contains(msg, "403") {
		t.Fatalf("错误应含状态码: %s", msg)
	}
	for _, secret := range []string{testAccessKey, testSecretKey} {
		if strings.Contains(msg, secret) {
			t.Fatalf("错误信息泄露了凭据: %s", msg)
		}
	}
}

// 空对象键应在发请求之前就被拒——否则会 PUT 到桶根路径上。
func TestPutRejectsEmptyKey(t *testing.T) {
	r := NewR2(R2Config{AccountID: "acc", Bucket: "b", PublicBaseURL: "https://cdn.example.com"})
	body, size := BytesBody([]byte("x"))
	if _, err := r.Put(context.Background(), "  ", body, size, "image/png"); err == nil {
		t.Fatal("空对象键应被拒绝")
	}
}
