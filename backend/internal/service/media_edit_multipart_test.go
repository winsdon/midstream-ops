package service

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

type mediaRefRoundTripper func(*http.Request) (*http.Response, error)

func (f mediaRefRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestEditImageGPTMultipart(t *testing.T) {
	for _, model := range []string{"gpt-image-1.5", "gpt-image-2", "gpt-image-2.5", "gpt-image-2.5-flare", "gpt-image-2.5-sunburst", "gpt-image-2.6"} {
		for _, count := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s/%d", model, count), func(t *testing.T) {
				downloads, submissions := 0, 0
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					submissions++
					if r.URL.Path != "/v1/images/edits" || r.Method != http.MethodPost {
						t.Errorf("wrong endpoint: %s %s", r.Method, r.URL.Path)
					}
					if r.Header.Get("Authorization") != "Bearer "+testAPIKey {
						t.Error("missing gateway authorization")
					}
					if r.ContentLength <= 0 {
						t.Error("missing body length")
					}
					if err := r.ParseMultipartForm(1 << 20); err != nil {
						t.Errorf("not multipart: %v", err)
						w.WriteHeader(400)
						return
					}
					defer r.MultipartForm.RemoveAll()
					for key, want := range map[string]string{"model": model, "prompt": "做一版深色主题", "size": "1024x576", "quality": "high", "n": "1", "response_format": "b64_json"} {
						if got := r.FormValue(key); got != want {
							t.Errorf("%s=%q, want %q", key, got, want)
						}
					}
					for _, key := range []string{"images", "image_url", "image_urls", "resolution", "duration", "aspect_ratio"} {
						if _, ok := r.MultipartForm.Value[key]; ok {
							t.Errorf("unexpected field %s", key)
						}
					}
					files := r.MultipartForm.File["image"]
					if len(files) != count {
						t.Errorf("got %d files, want %d", len(files), count)
					}
					for i, file := range files {
						if file.Header.Get("Content-Type") != "image/png" {
							t.Errorf("wrong MIME: %v", file.Header)
						}
						f, err := file.Open()
						if err != nil {
							t.Error(err)
							return
						}
						data, err := io.ReadAll(f)
						f.Close()
						if err != nil || string(data) != string(pngHeader)+fmt.Sprint(i) {
							t.Errorf("reference %d content mismatch", i)
						}
					}
					_, _ = io.WriteString(w, `{"data":[{"b64_json":"RURJVA=="}]}`)
				}))
				defer srv.Close()
				g := NewMediaGateway(srv.URL)
				g.refClient = &http.Client{Transport: mediaRefRoundTripper(func(r *http.Request) (*http.Response, error) {
					if r.Header.Get("Authorization") != "" {
						t.Error("API key leaked to reference host")
					}
					wantPath := fmt.Sprintf("/%d.png", downloads)
					if r.URL.Path != wantPath {
						t.Errorf("reference order: %s, want %s", r.URL.Path, wantPath)
					}
					data := string(pngHeader) + fmt.Sprint(downloads)
					downloads++
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(data)), Header: make(http.Header)}, nil
				})}
				urls := []string{"https://refs.example.com/0.png"}
				if count == 2 {
					urls = append(urls, "https://refs.example.com/1.png")
				}
				got, err := g.EditImage(context.Background(), testAPIKey, MediaGenerateParams{
					Model: model, Prompt: "做一版深色主题", N: 1, Size: "1024x576", Quality: "high",
					Resolution: "480p", Duration: 8, ImageURL: urls[0], ImageURLs: urls,
				})
				if err != nil {
					t.Fatal(err)
				}
				if len(got) != 1 || got[0].B64 != "RURJVA==" {
					t.Fatalf("bad result: %v", got)
				}
				if downloads != count || submissions != 1 {
					t.Fatalf("downloads=%d submissions=%d", downloads, submissions)
				}
			})
		}
	}
}

func TestEditImageGPTDownloadFailuresDoNotSubmit(t *testing.T) {
	t.Setenv("TMP", t.TempDir())
	t.Setenv("TMPDIR", t.TempDir())
	for _, tc := range []struct {
		name, body string
		status     int
		length     int64
	}{
		{"missing", "", 404, 0},
		{"empty", "", 200, 0},
		{"not-image", "<html>error</html>", 200, 0},
		{"oversize-header", string(pngHeader), 200, maxRefImageBytes + 1},
		{"oversize-stream", string(pngHeader) + strings.Repeat("x", maxRefImageBytes), 200, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewMediaGateway("https://gateway.example.com")
			g.client.Transport = mediaRefRoundTripper(func(*http.Request) (*http.Response, error) {
				t.Error("must not submit")
				return nil, fmt.Errorf("unexpected submission")
			})
			g.refClient = &http.Client{Transport: mediaRefRoundTripper(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, ContentLength: tc.length, Body: io.NopCloser(strings.NewReader(tc.body)), Header: http.Header{"Content-Type": []string{"image/png"}}}, nil
			})}
			if _, err := g.EditImage(context.Background(), testAPIKey, MediaGenerateParams{Model: "gpt-image-2.5-flare", ImageURL: "https://refs.example.com/a.png"}); err == nil {
				t.Fatal("expected download error")
			}
			entries, err := os.ReadDir(os.TempDir())
			if err != nil || len(entries) != 0 {
				t.Fatalf("temporary upload not cleaned up: %v, %v", entries, err)
			}
		})
	}
}

func TestMediaRefBlocksPrivateTargetsAndRedirects(t *testing.T) {
	for _, raw := range []string{"http://127.0.0.1/a", "http://10.0.0.1/a", "http://169.254.169.254/a", "http://[::1]/a", "http://[::ffff:192.168.1.1]/a", "http://100.64.0.1/a", "file:///tmp/a", "https://user:pass@example.com/a"} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if validateMediaRefURL(u) == nil {
			t.Errorf("accepted %s", raw)
		}
		if newMediaRefClient().CheckRedirect(&http.Request{URL: u}, nil) == nil {
			t.Errorf("accepted redirect %s", raw)
		}
	}
	for _, addr := range []string{"127.0.0.1:80", "localhost:80", "10.0.0.1:80", "[::1]:80"} {
		conn, err := dialPublicMediaRef(context.Background(), "tcp", addr)
		if conn != nil {
			conn.Close()
		}
		if err == nil || !strings.Contains(err.Error(), "公网") {
			t.Errorf("did not block %s: %v", addr, err)
		}
	}
	if !publicMediaRefIP(net.ParseIP("1.1.1.1")) {
		t.Error("public IP rejected")
	}
}
