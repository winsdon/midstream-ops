package notify

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
)

// TelegramSendPhoto 通过 Bot API sendPhoto 发送一张 PNG 图片（可选 caption）。
//
// 单独作为包级函数（而不是 Telegram 的方法）是因为调用方是 service 层：
// 它手里只有 settings 里的 bot token / chat_id，并没有构造好的 Notifier 实例。
// 这里刻意复用 notify.go 的包级 httpClient，保持超时/连接复用与其它渠道一致。
func TelegramSendPhoto(ctx context.Context, botToken, chatID string, png []byte, caption string) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	if err := w.WriteField("chat_id", chatID); err != nil {
		return fmt.Errorf("构造 sendPhoto 请求失败: %w", err)
	}

	// 不能用 w.CreateFormFile：它会把图片的 Content-Type 固定成 application/octet-stream，
	// 而 Telegram 对上传文件类型敏感，必须显式声明 image/png。
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="photo"; filename="chart.png"`)
	h.Set("Content-Type", "image/png")
	part, err := w.CreatePart(h)
	if err != nil {
		return fmt.Errorf("构造 sendPhoto 请求失败: %w", err)
	}
	if _, err := part.Write(png); err != nil {
		return fmt.Errorf("写入图片数据失败: %w", err)
	}

	// caption 为空时整个字段都不传，避免 Telegram 报「caption 不能为空字符串」类错误。
	if caption != "" {
		if err := w.WriteField("caption", caption); err != nil {
			return fmt.Errorf("构造 sendPhoto 请求失败: %w", err)
		}
	}
	// 必须先 Close 才能拿到带结束 boundary 的完整 body。
	if err := w.Close(); err != nil {
		return fmt.Errorf("构造 sendPhoto 请求失败: %w", err)
	}

	api := telegramAPIBase + "/bot" + botToken + "/sendPhoto"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, api, &buf)
	if err != nil {
		// 畸形 token（含 % 或空格）会让 URL 解析失败，而 *url.Error 原文带完整
		// URL —— 同样必须打码，见 redactEndpoint 的说明。
		return redactEndpoint(err, api)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := httpClient.Do(req)
	if err != nil {
		// 传输层错误会带上完整 URL（含 /bot<token>），必须打码后才能外传，
		// 见 redactEndpoint 的说明。
		return redactEndpoint(fmt.Errorf("请求失败: %w", err), api)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	// 与 postJSON 共用同一套失败语义，保证「非 200 也能看到 Telegram 的 description」。
	return checkResponse(resp.StatusCode, raw, checkTelegramResp)
}
