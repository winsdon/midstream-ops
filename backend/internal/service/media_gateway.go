package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// browserUA 供应商 sub2api 站点前置 WAF 会拦截非浏览器 UA。
// 与 probe_service 打的是同一类站点，UA 纪律相同。
const browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36"

// 上游超时。图片同步返回通常 5-10s，但非 Grok 的 4K 图可达 1 分钟，
// 文档建议 180-300s；视频提交与状态查询都很快，单独给短超时。
const (
	mediaImageTimeout  = 300 * time.Second
	mediaSubmitTimeout = 60 * time.Second
	mediaStatusTimeout = 30 * time.Second
	// 产物下载不设整体超时：视频文件可能很大，用 context 由调用方控制。
	mediaContentIdleTimeout = 10 * time.Minute
)

// MediaGatewayError 上游返回的错误。
//
// 携带 StatusCode 是因为调用方必须按状态码分流：视频状态查询的 400 是
// 「审核拒绝、钱已扣、任务终结」，而 202 是「还在跑」——两者都不是 200，
// 但语义天差地别。
type MediaGatewayError struct {
	StatusCode int
	Message    string
}

func (e *MediaGatewayError) Error() string {
	return fmt.Sprintf("上游返回 %d: %s", e.StatusCode, e.Message)
}

// MediaGateway 生图 / 生视频网关客户端。
//
// 【与 probe_service 的区别】那里用运营方自己的上游账号凭据打上游 AI 平台；
// 这里用终端用户自己的 API Key 打 sub2api 网关。key 由调用方按次传入，
// 客户端本身不持有任何凭据——避免出现「客户端实例持有某个用户的 key」
// 这种容易在并发下串用户的状态。
type MediaGateway struct {
	baseURL string
	client  *http.Client
}

// NewMediaGateway 创建网关客户端。baseURL 形如 https://api.example.com（无尾斜杠）。
func NewMediaGateway(baseURL string) *MediaGateway {
	return &MediaGateway{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		// 不设 Client.Timeout：产物下载是流式的，整体超时会在传输中途掐断大文件。
		// 各方法用 context.WithTimeout 单独控制。
		client: &http.Client{
			Transport: &http.Transport{
				ResponseHeaderTimeout: mediaContentIdleTimeout,
			},
		},
	}
}

// ImageResult 图片生成结果。
type ImageResult struct {
	// URL 上游 CDN 直链。仅在上游忽略 response_format 时才有值。
	//
	// 【不要依赖它】xAI 走的是自有 CDN（imgen.x.ai），不经网关，国内网络
	// 实测 TCP 连接超时（DNS 解析到被污染地址）。后端与浏览器同处国内时
	// 两边都拉不到，代理也救不了。
	URL string
	// B64 base64 图片数据。这是本站取图的主路径 —— 数据随响应体经网关返回，
	// 全程不碰 CDN，顺带绕开了直链过期问题。
	B64 string
	// MimeType 上游标注的图片类型，用于拼 data URI。缺省按 image/jpeg。
	MimeType string
	// CostTicks 上游实扣（部分平台不返回，为 0）。
	CostTicks int64
}

// imagesResponse 上游 /v1/images/* 的响应。
type imagesResponse struct {
	Created int64 `json:"created"`
	Data    []struct {
		URL      string `json:"url"`
		B64JSON  string `json:"b64_json"`
		MimeType string `json:"mime_type"`
	} `json:"data"`
	Usage struct {
		CostInUSDTicks int64 `json:"cost_in_usd_ticks"`
	} `json:"usage"`
}

// applyImageSizeParams 把尺寸相关参数按模型的尺寸模式写进 fields。
//
// 【两套参数必须互斥】sub2api 网关的 sanitizeGrokMediaForwardBody 在转发 Grok
// 图片请求前会主动删掉 size 字段——这正是「Grok 传 size 没用、恒出 1024×1024」
// 的真正原因，不是上游忽略了它。Grok 认的是 xAI 原生的 aspect_ratio +
// resolution，网关对这两个字段原样透传。
//
// 因此：aspect_ratio 模式绝不能带 size（会触发网关的删除分支，行为不确定），
// size 模式绝不能带 aspect_ratio（OpenAI 格式端点不认这个字段）。
//
// fields 用 map[string]any 而非直接写两处，是为了让 JSON 与 multipart 两条
// 提交路径共用同一份参数装配逻辑——它们过去各写一遍，正是 size 只在其中一条
// 路径被处理的温床。
func applyImageSizeParams(fields map[string]any, p MediaGenerateParams) {
	switch MediaSizeModeOf(p.Model) {
	case SizeModeAspectRatio:
		if p.AspectRatio != "" {
			fields["aspect_ratio"] = p.AspectRatio
		}
		if p.ImageResolution != "" {
			fields["resolution"] = p.ImageResolution
		}
	case SizeModePixelSize:
		if p.Size != "" {
			fields["size"] = p.Size
		}
		if p.Quality != "" {
			fields["quality"] = p.Quality
		}
	}
}

// GenerateImage 文生图（同步）。
func (g *MediaGateway) GenerateImage(ctx context.Context, apiKey string, p MediaGenerateParams) ([]ImageResult, error) {
	payload := map[string]any{
		"model":  p.Model,
		"prompt": p.Prompt,
		"n":      p.N,
		// 【必须 b64_json，不能用 url】url 返回的是 xAI 自有 CDN（imgen.x.ai）
		// 直链，不经网关：国内网络 DNS 被污染、TCP 连接超时，后端与浏览器都拉不到，
		// 加代理也没用（代理进程同样在国内）。b64 让图片数据随响应体经网关返回，
		// 后端拿到字节后转存 R2，前端凭 R2 URL 长期可见。
		// 见 kaola-doc/docs/guide/grok-media.md 的「国内网络建议使用 b64_json」。
		"response_format": "b64_json",
	}
	applyImageSizeParams(payload, p)

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, mediaImageTimeout)
	defer cancel()

	resp, err := g.doJSON(ctx, http.MethodPost, "/v1/images/generations", apiKey, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return parseImagesResponse(resp)
}

// EditImage 图生图（同步，multipart）。
//
// files 的每一项是一张参考图。OpenAI 格式支持重复 image 字段传多张；
// Grok 只取第一张。
//
// 【流式转发，不落盘不全量进内存】用 io.Pipe 把 multipart 编码与上传串起来：
// 前端上传的文件边读边写给上游。用户可能传几十 MB 的图，全量缓冲在并发下会 OOM。
func (g *MediaGateway) EditImage(ctx context.Context, apiKey string, p MediaGenerateParams, files []MediaUploadFile) ([]ImageResult, error) {
	if len(files) == 0 {
		return nil, fmt.Errorf("缺少参考图")
	}

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)

	go func() {
		// 任何一步失败都要 CloseWithError，否则读端会一直阻塞到 context 超时。
		writeErr := func() error {
			fields := map[string]any{
				"model":  p.Model,
				"prompt": p.Prompt,
				// 与 GenerateImage 同因：xAI CDN 直链国内不可达，必须要 b64。
				"response_format": "b64_json",
			}
			applyImageSizeParams(fields, p)
			if p.N > 0 {
				fields["n"] = strconv.Itoa(p.N)
			}
			for k, v := range fields {
				if err := mw.WriteField(k, fmt.Sprint(v)); err != nil {
					return err
				}
			}
			for _, f := range files {
				part, err := mw.CreateFormFile("image", f.Filename)
				if err != nil {
					return err
				}
				if _, err := io.Copy(part, f.Reader); err != nil {
					return err
				}
			}
			return mw.Close()
		}()
		_ = pw.CloseWithError(writeErr)
	}()

	ctx, cancel := context.WithTimeout(ctx, mediaImageTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL+"/v1/images/edits", pr)
	if err != nil {
		return nil, err
	}
	g.setAuthHeaders(req, apiKey)
	req.Header.Set("Content-Type", mw.FormDataContentType())

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

// MediaUploadFile 一个待转发的上传文件。
type MediaUploadFile struct {
	Filename string
	Reader   io.Reader
}

// SubmitVideo 提交视频生成任务，返回上游 request_id。
//
// 【调用成功即已扣费】上游在提交成功那一刻就计费，即便后续内容审核拒绝也不退还。
// 调用方必须在调用前落 pending 记录，调用后立即把 request_id 落库。
func (g *MediaGateway) SubmitVideo(ctx context.Context, apiKey string, p MediaGenerateParams) (string, error) {
	payload := map[string]any{
		"model":      p.Model,
		"prompt":     p.Prompt,
		"resolution": p.Resolution,
		"duration":   p.Duration,
	}
	if p.AspectRatio != "" {
		payload["aspect_ratio"] = p.AspectRatio
	}
	if p.Stream {
		payload["stream"] = true
	}
	// 图生视频的参考图：字段是**嵌套对象** image.url，且须公网可达。
	//
	// 【绝不能写成顶层 image_url】上游对未知字段静默丢弃：HTTP 200、request_id
	// 照发、费用照扣，但产物完全不参考图片，等于用户白花钱做了次文生视频。
	// 对照实验（同模型、同参考图，只改字段形状）：
	//   {"image_url": "…"}      → 产出与参考图毫无关系
	//   {"image": {"url": "…"}} → 产出忠实还原参考图
	// 供应商文档 kaola-doc grok-media.md 原写作顶层 image_url（与 xAI 官方
	// docs.x.ai 的 image-to-video 不符），已同步订正。
	//
	// 另注：传 multipart 上游返回 415，参考图只能走 JSON URL。
	if p.ImageURL != "" {
		payload["image"] = map[string]any{"url": p.ImageURL}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(ctx, mediaSubmitTimeout)
	defer cancel()

	resp, err := g.doJSON(ctx, http.MethodPost, "/v1/videos/generations", apiKey, body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var out struct {
		RequestID string `json:"request_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("解析上游响应失败: %w", err)
	}
	if out.RequestID == "" {
		return "", fmt.Errorf("上游未返回 request_id")
	}
	return out.RequestID, nil
}

// VideoStatus 视频任务状态。
type VideoStatus struct {
	// Done 为 true 表示已完成（HTTP 200）；false 表示仍在进行（HTTP 202）。
	Done      bool
	Progress  int
	CostTicks int64
	// ResultURL 上游返回的产物 URL，供任务记录持久化。
	ResultURL string
	// Duration 上游返回的实际时长（秒）。
	Duration int
}

// videoStatusResponse 上游 GET /v1/videos/{id} 的响应。
type videoStatusResponse struct {
	Model    string `json:"model"`
	Status   string `json:"status"`
	Progress int    `json:"progress"`
	Usage    struct {
		CostInUSDTicks int64 `json:"cost_in_usd_ticks"`
	} `json:"usage"`
	Video struct {
		Duration int    `json:"duration"`
		URL      string `json:"url"`
	} `json:"video"`
}

// QueryVideo 查询视频任务状态。
//
// 【三态分流】上游用 HTTP 状态码而非响应体表达进度：
//   - 202 进行中，body 里有 progress
//   - 200 已完成，body 里有 usage 与 video.url
//   - 400 内容审核拒绝——终态失败，且费用不退还
//
// 400 必须当成终态而不是「可重试的错误」，否则会无限轮询一个永远不会成功的任务。
func (g *MediaGateway) QueryVideo(ctx context.Context, apiKey, requestID string) (*VideoStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, mediaStatusTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		g.baseURL+"/v1/videos/"+requestID, nil)
	if err != nil {
		return nil, err
	}
	g.setAuthHeaders(req, apiKey)

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s", redactError(err.Error(), req))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return nil, upstreamError(resp, req)
	}

	var out videoStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("解析上游响应失败: %w", err)
	}

	st := &VideoStatus{
		Done:      resp.StatusCode == http.StatusOK,
		Progress:  out.Progress,
		CostTicks: out.Usage.CostInUSDTicks,
		ResultURL: out.Video.URL,
		Duration:  out.Video.Duration,
	}
	if st.Done && st.Progress == 0 {
		st.Progress = 100
	}
	return st, nil
}

// OpenVideoContent 打开视频产物流。调用方负责 Close 返回的 ReadCloser。
//
// 【为什么必须由后端代理】该端点强制要求 Authorization 头，而浏览器的
// <video src> 标签无法携带自定义请求头。前端拿不到明文 key（也不该拿到），
// 因此只能由后端带 key 取流、再转发给浏览器。
//
// 返回 Content-Type 供调用方原样透传。
func (g *MediaGateway) OpenVideoContent(ctx context.Context, apiKey, requestID string) (io.ReadCloser, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		g.baseURL+"/v1/videos/"+requestID+"/content", nil)
	if err != nil {
		return nil, "", err
	}
	g.setAuthHeaders(req, apiKey)

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("%s", redactError(err.Error(), req))
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, "", upstreamError(resp, req)
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "video/mp4"
	}
	return resp.Body, ct, nil
}

// doJSON 发起 JSON 请求并校验状态码。成功时返回未关闭的响应，由调用方 Close。
func (g *MediaGateway) doJSON(ctx context.Context, method, path, apiKey string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, g.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	g.setAuthHeaders(req, apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s", redactError(err.Error(), req))
	}
	if err := checkStatus(resp, req); err != nil {
		resp.Body.Close()
		return nil, err
	}
	return resp, nil
}

// setAuthHeaders 设置认证与 UA。
//
// 【不重试】与 probe_service 的重试纪律不同：那是只读探测，重试无副作用；
// 这里每次调用都可能扣费，网络超时后上游任务可能已经创建成功，
// 重试等于二次扣费。宁可让用户看到失败并自己决定是否重试。
func (g *MediaGateway) setAuthHeaders(req *http.Request, apiKey string) {
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "application/json")
}

// checkStatus 非 2xx 时构造脱敏错误。
func checkStatus(resp *http.Response, req *http.Request) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return upstreamError(resp, req)
}

// upstreamError 读取错误响应体并脱敏。
//
// 上游的错误格式未在文档中给出 JSON schema，只保证有状态码与可读 message，
// 因此先尝试按 OpenAI 风格 {"error":{"message":...}} 解析，失败则取原始文本。
func upstreamError(resp *http.Response, req *http.Request) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	msg := strings.TrimSpace(string(raw))

	var parsed struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &parsed) == nil && parsed.Error.Message != "" {
		msg = parsed.Error.Message
	}
	if msg == "" {
		msg = resp.Status
	}
	return &MediaGatewayError{
		StatusCode: resp.StatusCode,
		Message:    redactError(msg, req),
	}
}

// parseImagesResponse 解析图片响应。
func parseImagesResponse(resp *http.Response) ([]ImageResult, error) {
	var out imagesResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("解析上游响应失败: %w", err)
	}
	if len(out.Data) == 0 {
		return nil, fmt.Errorf("上游未返回图片数据")
	}
	results := make([]ImageResult, 0, len(out.Data))
	for _, d := range out.Data {
		mime := d.MimeType
		if mime == "" {
			mime = "image/jpeg"
		}
		results = append(results, ImageResult{
			URL:       d.URL,
			B64:       d.B64JSON,
			MimeType:  mime,
			CostTicks: out.Usage.CostInUSDTicks,
		})
	}
	// 两者皆空说明上游给了空壳响应 —— 钱已经扣了却拿不到图，
	// 必须报错让任务落 failed，而不是留一条「成功但打不开」的记录。
	if results[0].B64 == "" && results[0].URL == "" {
		return nil, fmt.Errorf("上游未返回图片内容")
	}
	return results, nil
}
