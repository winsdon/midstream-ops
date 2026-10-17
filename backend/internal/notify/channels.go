package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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

// telegramAPIBase Telegram API 根地址。
// 抽成包级变量是为了让测试用 httptest.Server 替换（Send / SendPhoto 都从这里拼 URL），
// 否则单测只能真连 api.telegram.org。
var telegramAPIBase = "https://api.telegram.org"

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
	api := telegramAPIBase + "/bot" + t.cfg.BotToken + "/sendMessage"
	body, _ := json.Marshal(map[string]any{
		"chat_id": t.cfg.ChatID,
		"text":    msg.Title + "\n" + msg.Text,
	})
	return postJSON(ctx, api, body, checkTelegramResp)
}

// telegramResp Telegram 通用响应体（sendMessage / sendPhoto 同构）。
type telegramResp struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
	Parameters  struct {
		// 普通群升级为超级群后 chat_id 会变，Bot API 在这返回新 id。
		// 用指针而不是 int64，是为了区分「字段不存在」和「值恰好为 0」。
		MigrateToChatID *int64 `json:"migrate_to_chat_id"`
	} `json:"parameters"`
}

// checkTelegramResp 把 Telegram 的业务响应转成 error。
// Telegram 的失败原因只写在响应体（description）里，HTTP 状态码甚至可能是 200，
// 所以必须解析响应体，上层日志才能看到「为什么失败」而不是一句 HTTP 400。
func checkTelegramResp(raw []byte) error {
	var r telegramResp
	if err := json.Unmarshal(raw, &r); err != nil {
		// 响应体不是 Bot API 结构（常见于网关/反代返回 HTML 错误页）。
		// 这种情况不能当成成功，把原文截断后带回，便于排查。
		return fmt.Errorf("telegram 响应解析失败: %v, 原文: %s", err, truncateBody(raw))
	}
	if r.OK {
		return nil
	}
	desc := strings.TrimSpace(r.Description)
	if desc == "" {
		// 极少数情况（例如只有 error_code 没有描述）也要给出可读文案，避免空错误。
		desc = "响应体未给出 description"
	}
	if r.Parameters.MigrateToChatID != nil {
		// 群升级后旧 chat_id 永久失效，把新 id 原样写进错误，运营可直接复制到配置里。
		return fmt.Errorf("telegram 返回错误: %s；群已升级，新 chat_id = %d，请更新配置",
			desc, *r.Parameters.MigrateToChatID)
	}
	return fmt.Errorf("telegram 返回错误: %s", desc)
}

// maxErrBodyLen 错误文案里回带的响应体上限（按 rune 计）。
// 够看清原因，又不会把整页网关 HTML 灌进日志。
const maxErrBodyLen = 256

// truncateBody 生成可放进错误文案的响应体摘要：去空白、截断、空体给出占位文案。
func truncateBody(raw []byte) string {
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return "(空响应体)"
	}
	// 按 rune 截断，避免把一个多字节字符切成乱码。
	rs := []rune(s)
	if len(rs) > maxErrBodyLen {
		return string(rs[:maxErrBodyLen]) + "…(已截断)"
	}
	return s
}

// postJSON 发送 JSON POST 并用 check 校验业务响应。
func postJSON(ctx context.Context, url string, body []byte, check func(raw []byte) error) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		// 构造请求失败也是 *url.Error，同样带完整 URL（畸形 token 走这条），必须打码。
		return redactEndpoint(err, url)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return redactEndpoint(fmt.Errorf("请求失败: %w", err), url)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return checkResponse(resp.StatusCode, raw, check)
}

// redactEndpoint 把错误文案里的请求地址替换成不含凭据的形式。
//
// 【为什么必须有】三个渠道的凭据都写在 URL 里：Telegram 是 /bot<token>/…，
// 钉钉是 ?access_token=…&sign=…，飞书是 /hook/<uuid>。而 net/http 返回的
// *url.Error（构造请求失败与传输层失败都是它）会**原样带上完整 URL**
// （"Post \"http://…/bot123:ABC/sendPhoto\": …"）。这类错误有两个出口：写给运维
// 看的日志（通常还会外发到 Loki/ELK），以及测试发送逐渠道回给浏览器的 error
// 字段。等于把机器人的完全控制权写进日志和 HTTP 响应。
//
// 【为什么要替三种形态】调用方手里的原始串，与文案里出现的串未必相同：
//   - url.Error.Error() 是 `%s %q: %s`，URL 位置是 **%q 转义过**的。token 含
//     换行等控制字符时（粘贴常见），文案里是字面量 `\n`，原始串一个也匹配不上。
//   - net/url 会把 Path 里的字符重新编码（`123 456` → `123%20456`），与原始串不等。
//
// 所以三种形态都替一遍：原始串、%q 形态、以及 *url.Error 自己记的 URL 字段。
// 兜底不是「以防万一」，前两种都有真实的失配场景。
//
// 只保留 scheme://host：既有足够信息定位「连不上哪台机器」，又不含任何凭据。
// 路径与查询整体替换成 ***，因为凭据在这两处的位置各渠道不同，逐个字段猜容易漏。
func redactEndpoint(err error, endpoint string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()

	masked := maskedEndpoint(endpoint)
	// *url.Error 的 URL 字段就是被写进文案的那一串（含重新编码后的形态）
	var uerr *url.Error
	if errors.As(err, &uerr) && uerr.URL != "" {
		msg = replaceAllNonEmpty(msg, uerr.URL, maskedEndpoint(uerr.URL))
	}
	// 调用方传入的原始串，以及它在 %q 下的形态（控制字符会变成 `\n` 这类字面量）
	msg = replaceAllNonEmpty(msg, endpoint, masked)
	msg = replaceAllNonEmpty(msg, strconv.Quote(endpoint), masked)

	return errors.New(msg)
}

// replaceAllNonEmpty 跳过空串：strings.ReplaceAll 把空串当「在每两个 rune
// 之间插入」，这里若 endpoint 为空会把文案插成一团乱码。
func replaceAllNonEmpty(s, old, new string) string {
	if s == "" || old == "" {
		return s
	}
	return strings.ReplaceAll(s, old, new)
}

// maskedEndpoint 返回只含 scheme://host 的地址；解析失败时整串打码。
func maskedEndpoint(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return "***"
	}
	return u.Scheme + "://" + u.Host + "/***"
}

// checkResponse 统一「业务校验 + HTTP 状态码」的判定顺序。
// 先给渠道自己的 check 解析响应体：Telegram 会用 4xx 携带真实原因（chat 不存在、群已升级等），
// 直接按状态码短路的旧写法会把这些信息全丢掉；再用状态码兜底，
// 保证任何非 200 都返回非空错误，且响应体原文被截断进错误信息。
// 钉钉/飞书正常走 200，业务错误由各自 check 返回，语义不变。
func checkResponse(statusCode int, raw []byte, check func(raw []byte) error) error {
	var checkErr error
	if check != nil {
		checkErr = check(raw)
	}
	if statusCode != http.StatusOK {
		if checkErr != nil {
			return fmt.Errorf("HTTP %d: %w", statusCode, checkErr)
		}
		return fmt.Errorf("HTTP %d: %s", statusCode, truncateBody(raw))
	}
	return checkErr
}
