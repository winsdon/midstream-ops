package notify

import (
	"context"
	"strings"
	"testing"
)

// 三个渠道的凭据都在 URL 里，而 net/http 的传输层错误会原样带上完整 URL。
// 这条错误同时流向日志与「立即推送测试」接口的响应，必须打码。
//
// 用一个必然连不上的地址触发传输层错误（不依赖任何外部网络）。
func TestRedactEndpointHidesCredentials(t *testing.T) {
	const token = "123456:AA-SECRET-BOT-TOKEN"
	const endpoint = "http://127.0.0.1:1/bot" + token + "/sendPhoto"

	err := redactEndpoint(
		// 模拟 *url.Error 的文案：它会把完整 URL 带进来
		&testURLError{msg: `Post "` + endpoint + `": dial tcp 127.0.0.1:1: connect: connection refused`},
		endpoint,
	)
	if err == nil {
		t.Fatal("want error")
	}
	msg := err.Error()
	if strings.Contains(msg, token) {
		t.Errorf("Bot Token 泄漏进错误文案: %s", msg)
	}
	// 保留主机名，运维仍能看出「连不上哪台机器」
	if !strings.Contains(msg, "127.0.0.1") {
		t.Errorf("打码过度，主机名丢失: %s", msg)
	}
	if !strings.Contains(msg, "connection refused") {
		t.Errorf("打码过度，原因丢失: %s", msg)
	}
}

type testURLError struct{ msg string }

func (e *testURLError) Error() string { return e.msg }

// 端到端：TelegramSendPhoto 连不上时应返回打码后的错误。
func TestTelegramSendPhotoRedactsTokenOnTransportError(t *testing.T) {
	useTelegramBase(t, "http://127.0.0.1:1")
	const token = "123456:AA-SECRET-BOT-TOKEN"

	err := TelegramSendPhoto(context.Background(), token, "-1001", []byte("png"), "cap")
	if err == nil {
		t.Fatal("want error（地址必然连不上）")
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("Bot Token 泄漏进 SendPhoto 错误: %s", err.Error())
	}
}

// Telegram.Send（既有的 sendMessage 路径）同样不能漏 Token。
func TestTelegramSendRedactsTokenOnTransportError(t *testing.T) {
	useTelegramBase(t, "http://127.0.0.1:1")
	const token = "123456:AA-SECRET-BOT-TOKEN"

	tg := &Telegram{cfg: TelegramConfig{BotToken: token, ChatID: "-1001"}}
	err := tg.Send(context.Background(), Message{Title: "t", Text: "x"})
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("Bot Token 泄漏进 Send 错误: %s", err.Error())
	}
}

// postJSON 走的是同一套打码：钉钉/飞书的 webhook 也在 URL 里。
func TestPostJSONRedactsWebhookSecret(t *testing.T) {
	const endpoint = "http://127.0.0.1:1/hook?access_token=SUPER-SECRET-VALUE"
	err := postJSON(context.Background(), endpoint, []byte("{}"), nil)
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(err.Error(), "SUPER-SECRET-VALUE") {
		t.Errorf("webhook 凭据泄漏: %s", err.Error())
	}
}

// 【不能只做原始串的子串替换】net/http 放进文案里的是 req.URL.String()，
// 即重新转义后的形式：token 含空格时 Path 会被编码成 %20，与调用方传入的
// 原始串不再相等。只按 strings.Contains(原始串) 匹配会失配，凭据原样漏出去。
func TestTelegramSendPhotoRedactsTokenWithSpace(t *testing.T) {
	useTelegramBase(t, "http://127.0.0.1:1")
	const token = "123 456:AA-SECRET-BOT-TOKEN"

	err := TelegramSendPhoto(context.Background(), token, "-1001", []byte("png"), "cap")
	if err == nil {
		t.Fatal("want error")
	}
	msg := err.Error()
	// 转义后的两种形态都不能带出凭据
	for _, leak := range []string{token, "123%20456:AA-SECRET-BOT-TOKEN", "AA-SECRET-BOT-TOKEN"} {
		if strings.Contains(msg, leak) {
			t.Errorf("含空格的 Bot Token 泄漏（%q）: %s", leak, msg)
		}
	}
}

// 畸形 token 会让 http.NewRequestWithContext 直接失败，返回的 *url.Error 原文
// 带完整 URL —— 这条路径**不需要网络失败**，且新增的 /test 接口会把它回给浏览器
// 并留在 LastPush 里持续回带。
func TestTelegramSendPhotoRedactsMalformedToken(t *testing.T) {
	useTelegramBase(t, "https://api.telegram.org")
	const token = "12%zz:AAF-SECRET-CANARY"

	err := TelegramSendPhoto(context.Background(), token, "-1001", []byte("png"), "cap")
	if err == nil {
		t.Fatal("want error（畸形 URL 必须报错）")
	}
	if strings.Contains(err.Error(), "AAF-SECRET-CANARY") {
		t.Errorf("畸形 token 原文泄漏: %s", err.Error())
	}
}

// Telegram.Send（sendMessage 路径）同样要挡住畸形 token。
func TestTelegramSendRedactsMalformedToken(t *testing.T) {
	useTelegramBase(t, "https://api.telegram.org")
	const token = "12%zz:AAF-SECRET-CANARY"

	tg := &Telegram{cfg: TelegramConfig{BotToken: token, ChatID: "-1001"}}
	err := tg.Send(context.Background(), Message{Title: "t", Text: "x"})
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(err.Error(), "AAF-SECRET-CANARY") {
		t.Errorf("畸形 token 原文泄漏（sendMessage）: %s", err.Error())
	}
}

// postJSON 的构造失败路径同样要打码（畸形 webhook）。
//
// 畸形转义要放在 Path 才必然解析失败：url 包对 RawQuery 不做转义校验，
// 放查询串里会「构造成功」然后真的去连那台主机（单测不该打外网）。
func TestPostJSONRedactsMalformedWebhook(t *testing.T) {
	const endpoint = "http://127.0.0.1:1/hook/12%zz-SECRET"
	err := postJSON(context.Background(), endpoint, []byte("{}"), nil)
	if err == nil {
		t.Fatal("want error（畸形 URL 必须报错）")
	}
	if strings.Contains(err.Error(), "12%zz-SECRET") {
		t.Errorf("畸形 webhook 凭据泄漏: %s", err.Error())
	}
}

// 【控制字符是最刁的一条】url.Error.Error() 是 `%s %q: %s`，URL 位置被 %q
// **转义**过：token 尾随换行（粘贴常见，前端输入框没有 trim）在文案里变成字面量
// `\n`，于是「原始串」匹配不上；而 url.Error 的 URL 字段虽含真实换行，
// 也匹配不上被转义后的文案。两个都替才盖得住。
//
// 这条路径不需要网络失败：控制字符让 url 解析当场报错。
func TestRedactEndpointHandlesControlCharacters(t *testing.T) {
	cases := []struct {
		name     string
		endpoint string
		secret   string
	}{
		{
			name:     "token 尾随换行（Telegram）",
			endpoint: "https://api.telegram.org/bot123456:AA-REAL-BOT-SECRET\n/sendMessage",
			secret:   "AA-REAL-BOT-SECRET",
		},
		{
			name:     "token 中间制表符",
			endpoint: "https://api.telegram.org/bot123\t456:AA-REAL-BOT-SECRET/sendPhoto",
			secret:   "AA-REAL-BOT-SECRET",
		},
		{
			name:     "webhook 带回车换行（钉钉）",
			endpoint: "https://oapi.dingtalk.com/robot/send?access_token=DT-SECRET\r\n",
			secret:   "DT-SECRET",
		},
		{
			name:     "飞书 hook 带换行",
			endpoint: "https://open.feishu.cn/open-apis/bot/v2/hook/FEISHU-SECRET\n",
			secret:   "FEISHU-SECRET",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// 走真实构造路径：*url.Error 由 http 包自己产生
			err := postJSON(context.Background(), c.endpoint, []byte("{}"), nil)
			if err == nil {
				t.Fatal("want error")
			}
			msg := err.Error()
			for _, leak := range []string{
				c.secret,
				c.endpoint,
				// %q 形态下的整串（\n 等被转义成字面量）
				strings.ReplaceAll(c.endpoint, "\n", `\n`),
				strings.ReplaceAll(c.endpoint, "\r", `\r`),
			} {
				if leak != "" && strings.Contains(msg, leak) {
					t.Errorf("凭据泄漏（%q）: %s", leak, msg)
				}
			}
			// 打码不应把失败原因也吞掉
			if !strings.Contains(msg, "control character") && !strings.Contains(msg, "invalid") {
				t.Errorf("打码过度，原因丢失: %s", msg)
			}
		})
	}
}

// 端到端：控制字符 token 经 Telegram.Send（告警广播路径）也不得泄漏。
// 这条是上一轮验证者实际复现的泄漏路径：预警日志与 /settings/notify/test 响应。
func TestTelegramSendRedactsTokenWithControlChar(t *testing.T) {
	useTelegramBase(t, "https://api.telegram.org")
	const token = "123456:AA-REAL-BOT-SECRET\n"

	tg := &Telegram{cfg: TelegramConfig{BotToken: token, ChatID: "-1001"}}
	err := tg.Send(context.Background(), Message{Title: "t", Text: "x"})
	if err == nil {
		t.Fatal("want error")
	}
	msg := err.Error()
	if strings.Contains(msg, "AA-REAL-BOT-SECRET") {
		t.Errorf("控制字符 token 泄漏（Send）: %s", msg)
	}
	if strings.Contains(msg, `AA-REAL-BOT-SECRET\n`) {
		t.Errorf("控制字符 token 的 %%q 形态泄漏（Send）: %s", msg)
	}
}

// SendPhoto 同样。
func TestTelegramSendPhotoRedactsTokenWithControlChar(t *testing.T) {
	useTelegramBase(t, "https://api.telegram.org")
	const token = "123456:AA-REAL-BOT-SECRET\n"

	err := TelegramSendPhoto(context.Background(), token, "-1001", []byte("png"), "cap")
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(err.Error(), "AA-REAL-BOT-SECRET") {
		t.Errorf("控制字符 token 泄漏（SendPhoto）: %s", err.Error())
	}
}

// replaceAllNonEmpty 不能因为 old 为空把文案插成乱码。
func TestReplaceAllNonEmptySkipsEmptyOld(t *testing.T) {
	if got := replaceAllNonEmpty("abc", "", "X"); got != "abc" {
		t.Errorf("空 old 不该改动文案: %q", got)
	}
	if got := replaceAllNonEmpty("a-b", "-", "_"); got != "a_b" {
		t.Errorf("正常替换失败: %q", got)
	}
	if got := replaceAllNonEmpty("", "a", "b"); got != "" {
		t.Errorf("空输入应返回空: %q", got)
	}
}
