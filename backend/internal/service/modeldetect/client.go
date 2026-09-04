package modeldetect

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// browserUA 供应商 sub2api 站点前置 WAF 会拦非浏览器 UA，必须伪装浏览器。
const browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36"

// redacted 报告中替换凭据的占位符。
const redacted = "••••••••"

// maxRawChars 单次响应留在报告里的原文上限。够核对 signature 与错误报文，又不至于把库撑爆。
const maxRawChars = 8000

// maxResponseBytes 读取响应体的硬上限，防止被超长响应拖死。
const maxResponseBytes = 4 << 20

// interestingHeaders 会保留进报告的响应头。其余一律丢弃——它们既是隐私也是噪音。
var interestingHeaders = map[string]bool{
	"content-type": true, "server": true, "via": true,
	"request-id": true, "x-request-id": true, "anthropic-request-id": true,
	"cf-ray": true, "x-amzn-requestid": true, "x-amzn-errortype": true,
	"x-oneapi-request-id": true, "x-new-api-version": true, "retry-after": true,
}

// SSEEvent 一条解析后的 SSE 事件。
type SSEEvent struct {
	Event string         `json:"event"`
	Type  string         `json:"type"`
	Data  map[string]any `json:"-"`
}

// Exchange 一次请求-响应的完整记录（已脱敏，可直接进报告）。
type Exchange struct {
	Kind           string            `json:"kind"`
	Method         string            `json:"method"`
	URL            string            `json:"url"`
	RequestHeaders map[string]string `json:"request_headers"`
	RequestBody    any               `json:"request_body,omitempty"`
	Status         int               `json:"status"`
	DurationMs     int64             `json:"duration_ms"`
	TTFTMs         *int64            `json:"ttft_ms,omitempty"`
	ChunkCount     int               `json:"chunk_count,omitempty"`
	Headers        map[string]string `json:"headers"`
	Raw            string            `json:"raw"`
	NetworkError   string            `json:"network_error,omitempty"`

	// JSON 与 Events 只在进程内供 check 判读，不进报告。
	JSON   map[string]any `json:"-"`
	Events []SSEEvent     `json:"-"`
}

// OK 表示 HTTP 2xx。
func (e *Exchange) OK() bool { return e.Status >= 200 && e.Status < 300 }

// ErrorText 汇总错误线索（网络错误 + 原文），供正则匹配错误类型。
func (e *Exchange) ErrorText() string {
	return strings.ToLower(e.NetworkError + " " + e.Raw)
}

// Client 面向单个目标的 HTTP 客户端。
type Client struct {
	target Target
	http   *http.Client
}

// NewClient 创建客户端。流式请求要读完整段流，整体超时放宽为 3 倍，
// 「等首字节」由 ResponseHeaderTimeout 单独控制（与 ProbeService 同一套约定）。
func NewClient(t Target) *Client {
	transport := &http.Transport{
		TLSHandshakeTimeout:   20 * time.Second,
		ResponseHeaderTimeout: t.Timeout,
		IdleConnTimeout:       30 * time.Second,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   16,
		MaxConnsPerHost:       16,
	}
	return &Client{
		target: t,
		http:   &http.Client{Timeout: t.Timeout*3 + 10*time.Second, Transport: transport},
	}
}

// Target 返回本客户端的目标（只读用途）。
func (c *Client) Target() Target { return c.target }

// headers 组装请求头。附加头最后写入，允许调用方覆盖默认值（如 anthropic-version）。
func (c *Client) headers(stream bool) map[string]string {
	h := map[string]string{
		"content-type":      "application/json",
		"anthropic-version": "2023-06-01",
		"user-agent":        browserUA,
	}
	if stream {
		h["accept"] = "text/event-stream"
	} else {
		h["accept"] = "application/json"
	}
	if c.target.AuthMode == AuthAPIKey || c.target.AuthMode == AuthBoth {
		h["x-api-key"] = c.target.APIKey
	}
	if c.target.AuthMode == AuthBearer || c.target.AuthMode == AuthBoth {
		h["authorization"] = "Bearer " + c.target.APIKey
	}
	for k, v := range c.target.ExtraHeaders {
		if k != "" && v != "" {
			h[strings.ToLower(k)] = v
		}
	}
	return h
}

// redactHeaders 把凭据类请求头替换成占位符。
func redactHeaders(h map[string]string) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		lk := strings.ToLower(k)
		if strings.Contains(lk, "api-key") || strings.Contains(lk, "apikey") ||
			strings.Contains(lk, "authorization") || strings.Contains(lk, "token") ||
			strings.Contains(lk, "secret") || lk == "cookie" {
			out[k] = redacted
			continue
		}
		out[k] = v
	}
	return out
}

// redact 抹掉文本里出现的密钥明文。短 key 不替换，避免把正常文本打成马赛克。
func (c *Client) redact(s string) string {
	key := c.target.APIKey
	if len(key) < 8 {
		return s
	}
	return strings.ReplaceAll(s, key, redacted)
}

// Post 发送非流式请求。
func (c *Client) Post(ctx context.Context, kind string, body map[string]any) *Exchange {
	return c.do(ctx, http.MethodPost, kind, body, false)
}

// PostStream 发送流式请求（body 里需自带 stream:true）。
func (c *Client) PostStream(ctx context.Context, kind string, body map[string]any) *Exchange {
	return c.do(ctx, http.MethodPost, kind, body, true)
}

// Get 发送 GET 请求（仅 /v1/models 用）。
func (c *Client) Get(ctx context.Context, kind string) *Exchange {
	return c.do(ctx, http.MethodGet, kind, nil, false)
}

func (c *Client) do(ctx context.Context, method, kind string, body map[string]any, stream bool) *Exchange {
	url := endpoint(c.target.BaseURL, kind)
	hdr := c.headers(stream)
	ex := &Exchange{
		Kind:           kind,
		Method:         method,
		URL:            url,
		RequestHeaders: redactHeaders(hdr),
		RequestBody:    body,
		Headers:        map[string]string{},
	}

	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			ex.NetworkError = "请求体序列化失败: " + err.Error()
			return ex
		}
		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		ex.NetworkError = "构造请求失败: " + err.Error()
		return ex
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}

	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		ex.DurationMs = time.Since(start).Milliseconds()
		ex.NetworkError = c.redact("请求失败: " + err.Error())
		return ex
	}
	defer func() { _ = resp.Body.Close() }()

	ex.Status = resp.StatusCode
	for k, v := range resp.Header {
		lk := strings.ToLower(k)
		if interestingHeaders[lk] || strings.HasPrefix(lk, "anthropic-") || strings.HasPrefix(lk, "x-amzn-") {
			ex.Headers[lk] = strings.Join(v, ", ")
		}
	}

	raw, ttft, chunks, readErr := readBody(resp.Body, start)
	ex.DurationMs = time.Since(start).Milliseconds()
	ex.ChunkCount = chunks
	if ttft != nil {
		ex.TTFTMs = ttft
	}
	if readErr != nil {
		ex.NetworkError = c.redact("读取响应失败: " + readErr.Error())
	}

	// 先脱敏再解析：断言与摘要都是从 JSON/事件里取的文本，
	// 只擦 Raw 挡不住「响应里回显了 key」这种情况。
	clean := c.redact(raw)
	ex.Raw = truncateRunes(clean, maxRawChars)
	if stream {
		ex.Events = parseSSE(clean)
		// 流式错误响应体通常是 JSON，一并尝试解析，方便判读错误类型。
		if !ex.OK() {
			ex.JSON = parseJSONObject(clean)
		}
	} else {
		ex.JSON = parseJSONObject(clean)
	}
	return ex
}

// readBody 读取响应体，同时记录首字节时间与网络分块数（用于证明真实增量传输）。
func readBody(r io.Reader, start time.Time) (string, *int64, int, error) {
	var sb strings.Builder
	buf := make([]byte, 32*1024)
	var ttft *int64
	chunks := 0
	total := 0
	for {
		n, err := r.Read(buf[:])
		if n > 0 {
			if ttft == nil {
				ms := time.Since(start).Milliseconds()
				ttft = &ms
			}
			chunks++
			total += n
			if total > maxResponseBytes {
				sb.Write(buf[:n])
				return sb.String(), ttft, chunks, io.ErrShortBuffer
			}
			sb.Write(buf[:n])
		}
		if err == io.EOF {
			return sb.String(), ttft, chunks, nil
		}
		if err != nil {
			return sb.String(), ttft, chunks, err
		}
	}
}

// parseJSONObject 尽力把响应体解析成对象；非 JSON 或数组返回 nil。
func parseJSONObject(raw string) map[string]any {
	var v map[string]any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil
	}
	return v
}

// parseSSE 解析 SSE 流。事件名与 data.type 都保留——两者不一致本身就是可疑信号。
func parseSSE(raw string) []SSEEvent {
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	var events []SSEEvent
	for _, block := range strings.Split(normalized, "\n\n") {
		if strings.TrimSpace(block) == "" {
			continue
		}
		name := ""
		var dataLines []string
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "event:"):
				name = strings.TrimSpace(line[6:])
			case strings.HasPrefix(line, "data:"):
				dataLines = append(dataLines, strings.TrimLeft(line[5:], " "))
			}
		}
		if len(dataLines) == 0 {
			continue
		}
		text := strings.Join(dataLines, "\n")
		if text == "[DONE]" {
			continue
		}
		ev := SSEEvent{Event: name, Data: parseJSONObject(text)}
		ev.Type = str(ev.Data["type"])
		events = append(events, ev)
	}
	return events
}

// truncateRunes 按 rune 边界截断，避免把 UTF-8 切碎。
func truncateRunes(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	count := 0
	for i := range s {
		if count == limit {
			return s[:i] + "…（已截断）"
		}
		count++
	}
	return s
}
