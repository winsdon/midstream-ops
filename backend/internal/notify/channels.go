package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// DingTalkConfig 钉钉机器人配置。
type DingTalkConfig struct {
	Enabled bool   `json:"enabled"`
	Webhook string `json:"webhook"`
	Secret  string `json:"secret"` // 加签密钥（可选）
}

// DingTalk 钉钉自定义机器人（markdown 消息，支持加签）。
type DingTalk struct {
	cfg DingTalkConfig
}

// Name 渠道名。
func (d *DingTalk) Name() string { return "dingtalk" }

// Send 发送 markdown 消息。
func (d *DingTalk) Send(ctx context.Context, msg Message) error {
	webhook := d.cfg.Webhook
	// 加签：timestamp + "\n" + secret 做 HmacSHA256，Base64 后 URL 编码
	if d.cfg.Secret != "" {
		ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
		mac := hmac.New(sha256.New, []byte(d.cfg.Secret))
		mac.Write([]byte(ts + "\n" + d.cfg.Secret))
		sign := url.QueryEscape(base64.StdEncoding.EncodeToString(mac.Sum(nil)))
		sep := "?"
		if bytes.ContainsRune([]byte(webhook), '?') {
			sep = "&"
		}
		webhook += sep + "timestamp=" + ts + "&sign=" + sign
	}

	body, _ := json.Marshal(map[string]any{
		"msgtype": "markdown",
		"markdown": map[string]string{
			"title": msg.Title,
			"text":  "### " + msg.Title + "\n\n" + msg.Text,
		},
	})
	return postJSON(ctx, webhook, body, func(raw []byte) error {
		var r struct {
			ErrCode int    `json:"errcode"`
			ErrMsg  string `json:"errmsg"`
		}
		if err := json.Unmarshal(raw, &r); err == nil && r.ErrCode != 0 {
			return fmt.Errorf("钉钉返回错误 %d: %s", r.ErrCode, r.ErrMsg)
		}
		return nil
	})
}

// FeishuConfig 飞书机器人配置。
type FeishuConfig struct {
	Enabled bool   `json:"enabled"`
	Webhook string `json:"webhook"`
	Secret  string `json:"secret"` // 签名校验密钥（可选）
}

// Feishu 飞书自定义机器人（文本消息，支持签名校验）。
type Feishu struct {
	cfg FeishuConfig
}

// Name 渠道名。
func (f *Feishu) Name() string { return "feishu" }

// Send 发送文本消息。
func (f *Feishu) Send(ctx context.Context, msg Message) error {
	payload := map[string]any{
		"msg_type": "text",
		"content":  map[string]string{"text": msg.Title + "\n" + msg.Text},
	}
	// 签名：timestamp + "\n" + secret 作为 HmacSHA256 的 key，空串为签名内容
	if f.cfg.Secret != "" {
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		mac := hmac.New(sha256.New, []byte(ts+"\n"+f.cfg.Secret))
		payload["timestamp"] = ts
		payload["sign"] = base64.StdEncoding.EncodeToString(mac.Sum(nil))
	}

	body, _ := json.Marshal(payload)
	return postJSON(ctx, f.cfg.Webhook, body, func(raw []byte) error {
		var r struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
		}
		if err := json.Unmarshal(raw, &r); err == nil && r.Code != 0 {
			return fmt.Errorf("飞书返回错误 %d: %s", r.Code, r.Msg)
		}
		return nil
	})
}

// TelegramConfig Telegram 机器人配置。
type TelegramConfig struct {
	Enabled  bool   `json:"enabled"`
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
}

// Telegram Telegram Bot（sendMessage）。
type Telegram struct {
	cfg TelegramConfig
}

// Name 渠道名。
func (t *Telegram) Name() string { return "telegram" }

// Send 发送文本消息。
func (t *Telegram) Send(ctx context.Context, msg Message) error {
	api := "https://api.telegram.org/bot" + t.cfg.BotToken + "/sendMessage"
	body, _ := json.Marshal(map[string]any{
		"chat_id": t.cfg.ChatID,
		"text":    msg.Title + "\n" + msg.Text,
	})
	return postJSON(ctx, api, body, func(raw []byte) error {
		var r struct {
			OK          bool   `json:"ok"`
			Description string `json:"description"`
		}
		if err := json.Unmarshal(raw, &r); err == nil && !r.OK {
			return fmt.Errorf("telegram 返回错误: %s", r.Description)
		}
		return nil
	})
}

// postJSON 发送 JSON POST 并用 check 校验业务响应。
func postJSON(ctx context.Context, url string, body []byte, check func(raw []byte) error) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if check != nil {
		return check(raw)
	}
	return nil
}
