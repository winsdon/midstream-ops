package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// Gemini 原生生图（Nano Banana 系列）。
//
// 【为什么不走 /v1/images/*】sub2api 的图片端点只对 openai / grok 分组放行，
// Gemini 系分组调用直接 404。Gemini 生图只能走 /v1beta/models/{model}:generateContent：
// 鉴权用 x-goog-api-key；清晰度与比例放在 generationConfig.imageConfig；
// 图片以 base64 出现在 candidates[].content.parts[].inlineData。
// 见 kaola-doc/docs/guide/image-api.md 的「Gemini 原生格式生图」。

// geminiPart 请求 / 响应共用的 content part。
type geminiPart struct {
	Text       string            `json:"text,omitempty"`
	InlineData *geminiInlineData `json:"inlineData,omitempty"`
	// Thought 为 true 的图是 Gemini 3 Pro Image 推理过程中的草图，不是成品。
	Thought bool `json:"thought,omitempty"`
}

type geminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiGenerateResponse struct {
	Candidates []struct {
		Content struct {
			Parts []geminiPart `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
}

// generateGeminiImage 文生图 / 图生图（withRefs）。
//
// Gemini 一次请求只出一张图，N 张就并发 N 次。参考图先全部下载好再发请求：
// 下载失败时一笔费用都不产生。
func (g *MediaGateway) generateGeminiImage(ctx context.Context, apiKey string, p MediaGenerateParams, withRefs bool) ([]ImageResult, error) {
	parts := []geminiPart{{Text: p.Prompt}}
	if withRefs {
		refs, err := g.geminiRefParts(ctx, p)
		if err != nil {
			return nil, err
		}
		parts = append(parts, refs...)
	}
	body, err := json.Marshal(geminiImageRequest(parts, p))
	if err != nil {
		return nil, err
	}
	path := "/v1beta/models/" + url.PathEscape(p.Model) + ":generateContent"

	n := max(p.N, 1)
	results := make([][]ImageResult, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = g.geminiGenerateOnce(ctx, apiKey, path, body)
		}()
	}
	wg.Wait()

	// 【部分成功照样交付】成功的那几张已经扣费，丢掉等于白花钱；全部失败才报错。
	var out []ImageResult
	var firstErr error
	failed := 0
	for i := range n {
		if errs[i] != nil {
			failed++
			if firstErr == nil {
				firstErr = errs[i]
			}
			continue
		}
		out = append(out, results[i]...)
	}
	if len(out) == 0 {
		return nil, firstErr
	}
	if failed > 0 {
		log.Printf("[media] %s 请求 %d 张，%d 张失败: %v", p.Model, n, failed, firstErr)
	}
	return out, nil
}

// geminiImageRequest 组装 generateContent 请求体。空的尺寸参数不写，交给上游缺省。
func geminiImageRequest(parts []geminiPart, p MediaGenerateParams) map[string]any {
	genCfg := map[string]any{"responseModalities": []string{"TEXT", "IMAGE"}}
	imageCfg := map[string]string{}
	if p.AspectRatio != "" {
		imageCfg["aspectRatio"] = p.AspectRatio
	}
	if p.ImageResolution != "" {
		imageCfg["imageSize"] = p.ImageResolution
	}
	if len(imageCfg) > 0 {
		genCfg["imageConfig"] = imageCfg
	}
	return map[string]any{
		"contents":         []map[string]any{{"role": "user", "parts": parts}},
		"generationConfig": genCfg,
	}
}

// geminiRefParts 下载全部参考图并转成 inlineData。
func (g *MediaGateway) geminiRefParts(ctx context.Context, p MediaGenerateParams) ([]geminiPart, error) {
	urls := allImageURLs(p)
	if len(urls) > mediaMaxRefImages {
		return nil, fmt.Errorf("参考图最多 %d 张", mediaMaxRefImages)
	}
	ctx, cancel := context.WithTimeout(ctx, mediaImageTimeout)
	defer cancel()
	parts := make([]geminiPart, 0, len(urls))
	for i, rawURL := range urls {
		ref, err := g.openRefImage(ctx, rawURL)
		if err != nil {
			return nil, fmt.Errorf("参考图 %d: %w", i+1, err)
		}
		var buf bytes.Buffer
		err = ref.CopyTo(&buf)
		ref.Close()
		if err != nil {
			return nil, fmt.Errorf("参考图 %d: %w", i+1, err)
		}
		parts = append(parts, geminiPart{InlineData: &geminiInlineData{
			MimeType: ref.ContentType,
			Data:     base64.StdEncoding.EncodeToString(buf.Bytes()),
		}})
	}
	return parts, nil
}

// geminiGenerateOnce 发一次 generateContent 并取出成品图。
//
// 与 doJSON 分开写：鉴权头不同（x-goog-api-key 而非 Bearer）。
// 错误脱敏照样走 redactError，它认得 x-goog-api-key。
func (g *MediaGateway) geminiGenerateOnce(ctx context.Context, apiKey, path string, body []byte) ([]ImageResult, error) {
	ctx, cancel := context.WithTimeout(ctx, mediaImageTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-goog-api-key", apiKey)
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s", redactError(err.Error(), req))
	}
	defer resp.Body.Close()
	if err := checkStatus(resp, req); err != nil {
		return nil, err
	}

	var out geminiGenerateResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("解析上游响应失败: %w", err)
	}
	return geminiImages(out)
}

// geminiImages 取出成品图；一张都没有时带上拦截原因与模型回复报错。
func geminiImages(out geminiGenerateResponse) ([]ImageResult, error) {
	var images []ImageResult
	var texts []string
	reason := out.PromptFeedback.BlockReason
	for _, c := range out.Candidates {
		if reason == "" && c.FinishReason != "" && c.FinishReason != "STOP" {
			reason = c.FinishReason
		}
		for _, part := range c.Content.Parts {
			if part.Thought {
				continue
			}
			if part.InlineData != nil && part.InlineData.Data != "" {
				mime := part.InlineData.MimeType
				if mime == "" {
					mime = "image/png"
				}
				images = append(images, ImageResult{B64: part.InlineData.Data, MimeType: mime})
			} else if t := strings.TrimSpace(part.Text); t != "" {
				texts = append(texts, t)
			}
		}
	}
	if len(images) > 0 {
		return images, nil
	}
	msg := "上游未返回图片"
	if reason != "" {
		msg += "（" + reason + "）"
	}
	if len(texts) > 0 {
		msg += "：" + truncate(strings.Join(texts, " "), 200)
	}
	return nil, fmt.Errorf("%s", msg)
}
