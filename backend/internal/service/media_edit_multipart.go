package service

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"time"
)

// Resolve and pin public IPs at dial time, including after redirects.
func newMediaRefClient() *http.Client {
	return &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second,
			DialContext: dialPublicMediaRef,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("参考图重定向过多")
			}
			return validateMediaRefURL(req.URL)
		},
	}
}

func publicMediaRefIP(ip net.IP) bool {
	shared := &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() &&
		!ip.IsLinkLocalUnicast() && !shared.Contains(ip)
}

func dialPublicMediaRef(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("参考图域名解析失败")
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("参考图域名没有可用地址")
	}
	for _, addr := range ips {
		if !publicMediaRefIP(addr.IP) {
			return nil, fmt.Errorf("参考图须为公网地址")
		}
	}
	dialer := net.Dialer{Timeout: 10 * time.Second}
	for _, addr := range ips {
		conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(addr.IP.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		err = dialErr
	}
	return nil, err
}

func validateMediaRefURL(u *url.URL) error {
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return fmt.Errorf("参考图须为公网 http(s) 地址")
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && !publicMediaRefIP(ip) {
		return fmt.Errorf("参考图须为公网地址")
	}
	return nil
}

// Prepare on disk before submitting, so download failures never start a
// billable request and four 20 MB references do not need to live in RAM.
func (g *MediaGateway) editImageMultipart(ctx context.Context, apiKey string, p MediaGenerateParams) ([]ImageResult, error) {
	urls := allImageURLs(p)
	if len(urls) > mediaMaxRefImages {
		return nil, fmt.Errorf("参考图最多 %d 张", mediaMaxRefImages)
	}
	ctx, cancel := context.WithTimeout(ctx, mediaImageTimeout)
	defer cancel()
	body, err := os.CreateTemp("", "media-edit-*.multipart")
	if err != nil {
		return nil, fmt.Errorf("准备参考图上传失败: %w", err)
	}
	defer func() { body.Close(); os.Remove(body.Name()) }()
	w := multipart.NewWriter(body)
	fields := map[string]any{"model": p.Model, "prompt": p.Prompt, "response_format": "b64_json"}
	if p.N > 0 {
		fields["n"] = p.N
	}
	applyImageParams(fields, p)
	for name, value := range fields {
		if err := w.WriteField(name, fmt.Sprint(value)); err != nil {
			return nil, err
		}
	}
	for i, rawURL := range urls {
		if err := g.writeEditReference(ctx, w, rawURL, i); err != nil {
			return nil, fmt.Errorf("参考图 %d: %w", i+1, err)
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	size, err := body.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, err
	}
	if _, err := body.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL+"/v1/images/edits", body)
	if err != nil {
		return nil, err
	}
	req.ContentLength = size
	g.setAuthHeaders(req, apiKey)
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s", redactError(err.Error(), req))
	}
	defer resp.Body.Close()
	if err := checkStatus(resp, req); err != nil {
		return nil, err
	}
	return parseImagesResponse(resp)
}

func (g *MediaGateway) writeEditReference(ctx context.Context, w *multipart.Writer, rawURL string, index int) error {
	ref, err := g.openRefImage(ctx, rawURL)
	if err != nil {
		return err
	}
	defer ref.Close()
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf("form-data; name=\"image\"; filename=\"ref-%d%s\"", index, allowedRefImageTypes[ref.ContentType]))
	h.Set("Content-Type", ref.ContentType)
	part, err := w.CreatePart(h)
	if err != nil {
		return err
	}
	return ref.CopyTo(part)
}

// refImage 一张已通过校验、正在下载中的参考图。
type refImage struct {
	ContentType string
	reader      io.Reader
	body        io.Closer
}

func (r *refImage) Close() error { return r.body.Close() }

// CopyTo 把图片内容写入 dst，超过 maxRefImageBytes 即报错。
func (r *refImage) CopyTo(dst io.Writer) error {
	n, err := io.Copy(dst, io.LimitReader(r.reader, maxRefImageBytes+1))
	if err != nil {
		return fmt.Errorf("读取参考图失败")
	}
	if n > maxRefImageBytes {
		return fmt.Errorf("参考图超过 20MB")
	}
	return nil
}

// openRefImage 校验地址、经公网限定的 refClient 下载参考图并嗅探类型。
// GPT multipart 与 Gemini inlineData 两条路径共用，公网与大小限制只维护一份。
func (g *MediaGateway) openRefImage(ctx context.Context, rawURL string) (*refImage, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("参考图地址无效")
	}
	if err := validateMediaRefURL(u); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("参考图地址无效")
	}
	// Reference hosts must never receive the gateway API key.
	req.Header.Set("User-Agent", browserUA)
	resp, err := g.refClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载失败")
	}
	fail := func(err error) (*refImage, error) {
		resp.Body.Close()
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return fail(fmt.Errorf("下载失败（HTTP %d）", resp.StatusCode))
	}
	if resp.ContentLength > maxRefImageBytes {
		return fail(fmt.Errorf("参考图超过 20MB"))
	}
	reader, sniffed, err := sniffRefImage(resp.Body)
	if err != nil {
		return fail(err)
	}
	ct, ok := normalizeImageType(sniffed)
	if !ok {
		return fail(ErrMediaBadImageType)
	}
	return &refImage{ContentType: ct, reader: reader, body: resp.Body}, nil
}
