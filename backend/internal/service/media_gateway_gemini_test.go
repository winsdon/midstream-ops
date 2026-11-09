package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const geminiImageOK = `{"candidates":[{"content":{"parts":[
	{"text":"好的"},
	{"inlineData":{"mimeType":"image/png","data":"R0VN"}}
]},"finishReason":"STOP"}]}`

// 文生图：Gemini 原生路径、x-goog-api-key 鉴权、imageConfig 透传、N 次并发各出一张。
func TestGeminiGenerateImageRequestAndFanOut(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1beta/models/gemini-3.1-flash-image:generateContent" {
			t.Errorf("wrong endpoint: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("x-goog-api-key") != testAPIKey {
			t.Error("missing x-goog-api-key")
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("Gemini 路径不应再带 Bearer")
		}
		if r.Header.Get("User-Agent") != browserUA {
			t.Error("missing browser UA")
		}
		var body struct {
			Contents []struct {
				Role  string           `json:"role"`
				Parts []map[string]any `json:"parts"`
			} `json:"contents"`
			GenerationConfig struct {
				ResponseModalities []string          `json:"responseModalities"`
				ImageConfig        map[string]string `json:"imageConfig"`
			} `json:"generationConfig"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("bad body: %v", err)
		}
		if len(body.Contents) != 1 || len(body.Contents[0].Parts) != 1 || body.Contents[0].Parts[0]["text"] != "蓝色机器人" {
			t.Errorf("bad contents: %+v", body.Contents)
		}
		if strings.Join(body.GenerationConfig.ResponseModalities, ",") != "TEXT,IMAGE" {
			t.Errorf("bad modalities: %v", body.GenerationConfig.ResponseModalities)
		}
		if ic := body.GenerationConfig.ImageConfig; ic["aspectRatio"] != "16:9" || ic["imageSize"] != "2K" {
			t.Errorf("bad imageConfig: %v", ic)
		}
		_, _ = io.WriteString(w, geminiImageOK)
	}))
	defer srv.Close()

	got, err := NewMediaGateway(srv.URL).GenerateImage(context.Background(), testAPIKey, MediaGenerateParams{
		Kind: MediaKindText2Image, Model: "gemini-3.1-flash-image", Prompt: "蓝色机器人", N: 3,
		AspectRatio: "16:9", ImageResolution: "2K",
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 || len(got) != 3 {
		t.Fatalf("N=3 应发 3 次请求得 3 张图，实得 calls=%d images=%d", calls.Load(), len(got))
	}
	for _, r := range got {
		if r.B64 != "R0VN" || r.MimeType != "image/png" || r.CostTicks != 0 {
			t.Fatalf("bad result: %+v", r)
		}
	}
}

// Gemini 3 Pro Image 会先吐 thought 草图，那不是成品，不能当结果。
func TestGeminiSkipsThoughtImages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"parts":[
			{"inlineData":{"mimeType":"image/png","data":"RFJBRlQ="},"thought":true},
			{"inlineData":{"mimeType":"image/jpeg","data":"RklOQUw="}}
		]}}]}`)
	}))
	defer srv.Close()

	got, err := NewMediaGateway(srv.URL).GenerateImage(context.Background(), testAPIKey, MediaGenerateParams{
		Model: "gemini-3-pro-image", Prompt: "海报", N: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].B64 != "RklOQUw=" || got[0].MimeType != "image/jpeg" {
		t.Fatalf("应只取成品图，实得 %+v", got)
	}
}

// 没给档位与宽高比时不带 imageConfig，交给上游缺省。
func TestGeminiOmitsEmptyImageConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), "imageConfig") {
			t.Errorf("空参数不应带 imageConfig: %s", raw)
		}
		_, _ = io.WriteString(w, geminiImageOK)
	}))
	defer srv.Close()
	if _, err := NewMediaGateway(srv.URL).GenerateImage(context.Background(), testAPIKey, MediaGenerateParams{
		Model: "gemini-3.1-flash-image", Prompt: "x", N: 1,
	}); err != nil {
		t.Fatal(err)
	}
}

// 被安全拦截或只回文本时，错误要带上原因，用户才知道该改提示词。
func TestGeminiNoImageReportsReason(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"我无法生成这张图片"}]},"finishReason":"IMAGE_SAFETY"}]}`)
	}))
	defer srv.Close()

	_, err := NewMediaGateway(srv.URL).GenerateImage(context.Background(), testAPIKey, MediaGenerateParams{
		Model: "gemini-3.1-flash-image", Prompt: "x", N: 1,
	})
	if err == nil || !strings.Contains(err.Error(), "IMAGE_SAFETY") || !strings.Contains(err.Error(), "我无法生成这张图片") {
		t.Fatalf("错误应包含 finishReason 与模型回复，实得 %v", err)
	}
}

// 部分成功：成功的图已经扣费，必须交给用户；全部失败才报错。
func TestGeminiPartialFailureKeepsSuccesses(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"code":429,"message":"busy","status":"RESOURCE_EXHAUSTED"}}`)
			return
		}
		_, _ = io.WriteString(w, geminiImageOK)
	}))
	defer srv.Close()

	got, err := NewMediaGateway(srv.URL).GenerateImage(context.Background(), testAPIKey, MediaGenerateParams{
		Model: "gemini-3.1-flash-image", Prompt: "x", N: 2,
	})
	if err != nil || len(got) != 1 {
		t.Fatalf("应返回成功的 1 张，实得 %d 张, err=%v", len(got), err)
	}
}

// 上游错误要透出 message，且绝不能带出 API key。
func TestGeminiErrorsNeverLeakAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprintf(w, `{"error":{"code":400,"message":"bad key %s","status":"INVALID_ARGUMENT"}}`, testAPIKey)
	}))
	defer srv.Close()

	_, err := NewMediaGateway(srv.URL).GenerateImage(context.Background(), testAPIKey, MediaGenerateParams{
		Model: "gemini-3.1-flash-image", Prompt: "x", N: 2,
	})
	var gwErr *MediaGatewayError
	if err == nil || strings.Contains(err.Error(), testAPIKey) {
		t.Fatalf("错误不应泄露 key: %v", err)
	}
	if !errors.As(err, &gwErr) || gwErr.StatusCode != http.StatusBadRequest || !strings.Contains(gwErr.Message, "bad key") {
		t.Fatalf("应透出上游 400 与 message，实得 %v", err)
	}
}

// 图生图：参考图下载后以 inlineData(base64) 跟在提示词后面，顺序不变；
// 下载只做一次，N 次请求共用；参考图主机拿不到 key。
func TestGeminiEditImageInlinesReferences(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body struct {
			Contents []struct {
				Parts []struct {
					Text       string `json:"text"`
					InlineData *struct {
						MimeType string `json:"mimeType"`
						Data     string `json:"data"`
					} `json:"inlineData"`
				} `json:"parts"`
			} `json:"contents"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("bad body: %v", err)
		}
		parts := body.Contents[0].Parts
		if len(parts) != 3 || parts[0].Text != "同角色海报" {
			t.Errorf("parts 应为 文本+2 张参考图，实得 %+v", parts)
			return
		}
		for i, part := range parts[1:] {
			want := base64.StdEncoding.EncodeToString([]byte(string(pngHeader) + fmt.Sprint(i)))
			if part.InlineData == nil || part.InlineData.MimeType != "image/png" || part.InlineData.Data != want {
				t.Errorf("参考图 %d 内容不对: %+v", i, part.InlineData)
			}
		}
		_, _ = io.WriteString(w, geminiImageOK)
	}))
	defer srv.Close()

	var downloads atomic.Int32
	g := NewMediaGateway(srv.URL)
	g.refClient = &http.Client{Transport: mediaRefRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("x-goog-api-key") != "" || r.Header.Get("Authorization") != "" {
			t.Error("API key leaked to reference host")
		}
		i := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), ".png")
		downloads.Add(1)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(pngHeader) + i)), Header: make(http.Header)}, nil
	})}
	urls := []string{"https://refs.example.com/0.png", "https://refs.example.com/1.png"}
	got, err := g.EditImage(context.Background(), testAPIKey, MediaGenerateParams{
		Kind: MediaKindImage2Image, Model: "gemini-3-pro-image", Prompt: "同角色海报", N: 2,
		ImageURL: urls[0], ImageURLs: urls, ImageResolution: "1K",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || calls.Load() != 2 || downloads.Load() != 2 {
		t.Fatalf("images=%d calls=%d downloads=%d", len(got), calls.Load(), downloads.Load())
	}
}

// 参考图下载失败时不能发出任何计费请求。
func TestGeminiEditDownloadFailureDoesNotSubmit(t *testing.T) {
	g := NewMediaGateway("https://gateway.example.com")
	g.client.Transport = mediaRefRoundTripper(func(*http.Request) (*http.Response, error) {
		t.Error("must not submit")
		return nil, fmt.Errorf("unexpected submission")
	})
	g.refClient = &http.Client{Transport: mediaRefRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("<html>error</html>")), Header: make(http.Header)}, nil
	})}
	if _, err := g.EditImage(context.Background(), testAPIKey, MediaGenerateParams{
		Model: "gemini-3.1-flash-image", Prompt: "x", N: 1, ImageURL: "https://refs.example.com/a.png",
	}); err == nil {
		t.Fatal("expected download error")
	}
}
