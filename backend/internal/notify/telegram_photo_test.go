package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	testBotToken = "123456:AA-test-token"
	testChatID   = "-1001234567890"
)

// useTelegramBase 把包级 base 指向 httptest server，用例结束后还原。
// 这些用例不能 t.Parallel()：telegramAPIBase 是包级变量，并行会互相踩。
func useTelegramBase(t *testing.T, base string) {
	t.Helper()
	prev := telegramAPIBase
	telegramAPIBase = base
	t.Cleanup(func() { telegramAPIBase = prev })
}

// capturedPhoto 记录服务端收到的 sendPhoto 请求内容。
type capturedPhoto struct {
	method      string
	path        string
	chatID      string
	caption     string
	hasCaption  bool
	fileName    string
	contentType string
	photo       []byte
}

func newSendPhotoServer(t *testing.T, status int, respBody string) (*httptest.Server, <-chan capturedPhoto) {
	t.Helper()
	gotCh := make(chan capturedPhoto, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := capturedPhoto{method: r.Method, path: r.URL.Path}
		// 先把请求内容捕获完再回响应，避免用例读到半截数据。
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("解析 multipart 失败: %v", err)
		} else {
			if v := r.MultipartForm.Value["chat_id"]; len(v) > 0 {
				got.chatID = v[0]
			}
			if v, ok := r.MultipartForm.Value["caption"]; ok {
				got.hasCaption = true
				if len(v) > 0 {
					got.caption = v[0]
				}
			}
			f, fh, err := r.FormFile("photo")
			if err != nil {
				t.Errorf("读取 photo 字段失败: %v", err)
			} else {
				defer func() { _ = f.Close() }()
				got.fileName = fh.Filename
				got.contentType = fh.Header.Get("Content-Type")
				got.photo, _ = io.ReadAll(f)
			}
		}
		gotCh <- got

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(respBody))
	}))
	t.Cleanup(srv.Close)
	return srv, gotCh
}

// 成功路径：URL / multipart 字段 / 图片字节与 Content-Type / caption 全部要对。
func TestTelegramSendPhotoSuccess(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0x01, 0x02, 0x03}
	srv, gotCh := newSendPhotoServer(t, http.StatusOK, `{"ok":true,"result":{"message_id":42}}`)
	useTelegramBase(t, srv.URL)

	err := TelegramSendPhoto(context.Background(), testBotToken, testChatID, png, "账户余额预警")
	if err != nil {
		t.Fatalf("期望发送成功，实际报错: %v", err)
	}

	got := <-gotCh
	if got.method != http.MethodPost {
		t.Errorf("method = %q, 期望 POST", got.method)
	}
	wantPath := "/bot" + testBotToken + "/sendPhoto"
	if got.path != wantPath {
		t.Errorf("path = %q, 期望 %q", got.path, wantPath)
	}
	if got.chatID != testChatID {
		t.Errorf("chat_id = %q, 期望 %q", got.chatID, testChatID)
	}
	if got.fileName != "chart.png" {
		t.Errorf("photo 文件名 = %q, 期望带 .png 的 chart.png", got.fileName)
	}
	if got.contentType != "image/png" {
		t.Errorf("photo Content-Type = %q, 期望 image/png", got.contentType)
	}
	if string(got.photo) != string(png) {
		t.Errorf("photo 字节 = %v, 期望 %v", got.photo, png)
	}
	if !got.hasCaption || got.caption != "账户余额预警" {
		t.Errorf("caption = %q (存在=%v), 期望 账户余额预警", got.caption, got.hasCaption)
	}
}

// caption 为空时整个字段都不该传（Telegram 拒绝空 caption）。
func TestTelegramSendPhotoEmptyCaptionOmitted(t *testing.T) {
	srv, gotCh := newSendPhotoServer(t, http.StatusOK, `{"ok":true}`)
	useTelegramBase(t, srv.URL)

	if err := TelegramSendPhoto(context.Background(), testBotToken, testChatID, []byte("png"), ""); err != nil {
		t.Fatalf("期望发送成功，实际报错: %v", err)
	}

	got := <-gotCh
	if got.hasCaption {
		t.Errorf("caption 字段不应出现，实际 = %q", got.caption)
	}
}

// 400 + JSON：Telegram 的真实原因在 description 里，必须带进错误文案。
func TestTelegramSendPhotoBadRequestKeepsDescription(t *testing.T) {
	srv, _ := newSendPhotoServer(t, http.StatusBadRequest,
		`{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`)
	useTelegramBase(t, srv.URL)

	err := TelegramSendPhoto(context.Background(), testBotToken, "bad-chat", []byte("png"), "")
	if err == nil {
		t.Fatal("期望报错，实际返回 nil")
	}
	if !strings.Contains(err.Error(), "Bad Request: chat not found") {
		t.Errorf("错误文案丢了 description: %v", err)
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("错误文案应带 HTTP 状态码: %v", err)
	}
}

// 群升级成超级群：HTTP 200 但 ok=false 且 parameters.migrate_to_chat_id，必须给出新 id。
func TestTelegramSendPhotoChatMigrated(t *testing.T) {
	srv, _ := newSendPhotoServer(t, http.StatusOK,
		`{"ok":false,"error_code":400,"description":"Bad Request: group chat was upgraded to a supergroup chat","parameters":{"migrate_to_chat_id":-1009876543210}}`)
	useTelegramBase(t, srv.URL)

	err := TelegramSendPhoto(context.Background(), testBotToken, testChatID, []byte("png"), "")
	if err == nil {
		t.Fatal("期望报错，实际返回 nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "-1009876543210") {
		t.Errorf("错误文案应包含新 chat_id: %v", err)
	}
	if !strings.Contains(msg, "群已升级") || !strings.Contains(msg, "新 chat_id") {
		t.Errorf("错误文案应明确说明群已升级并给出新 chat_id: %v", err)
	}
}

// 非 200 且响应体不是 JSON：要带状态码、错误非空、不 panic。
func TestTelegramSendPhotoNon200NonJSON(t *testing.T) {
	srv, _ := newSendPhotoServer(t, http.StatusInternalServerError, "<html>502 Bad Gateway</html>")
	useTelegramBase(t, srv.URL)

	err := TelegramSendPhoto(context.Background(), testBotToken, testChatID, []byte("png"), "")
	if err == nil {
		t.Fatal("期望报错，实际返回 nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("错误文案应带 HTTP 状态码: %v", err)
	}
	if strings.TrimSpace(err.Error()) == "" || strings.HasSuffix(err.Error(), ": ") {
		t.Errorf("错误文案不应为空: %q", err.Error())
	}
}

// 超长响应体：截断进错误文案，不能把整页 HTML 灌进日志。
func TestTelegramSendPhotoLongBodyTruncated(t *testing.T) {
	srv, _ := newSendPhotoServer(t, http.StatusBadGateway, strings.Repeat("x", 4096))
	useTelegramBase(t, srv.URL)

	err := TelegramSendPhoto(context.Background(), testBotToken, testChatID, []byte("png"), "")
	if err == nil {
		t.Fatal("期望报错，实际返回 nil")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("错误文案应带 HTTP 状态码: %v", err)
	}
	if len(err.Error()) > 1000 {
		t.Errorf("响应体应被截断，实际错误长度 = %d", len(err.Error()))
	}
}

// 非 200 但响应体是合法 JSON（check 返回 nil）：状态码兜底，仍要有非空错误。
func TestTelegramSendPhotoNon200WithJSONBody(t *testing.T) {
	srv, _ := newSendPhotoServer(t, http.StatusBadGateway, `{"ok":true}`)
	useTelegramBase(t, srv.URL)

	err := TelegramSendPhoto(context.Background(), testBotToken, testChatID, []byte("png"), "")
	if err == nil {
		t.Fatal("非 200 必须报错，实际返回 nil")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("错误文案应带 HTTP 状态码: %v", err)
	}
}

// 空响应体：错误文案不能退化成空串。
func TestTelegramSendPhotoEmptyBodyError(t *testing.T) {
	srv, _ := newSendPhotoServer(t, http.StatusInternalServerError, "")
	useTelegramBase(t, srv.URL)

	err := TelegramSendPhoto(context.Background(), testBotToken, testChatID, []byte("png"), "")
	if err == nil {
		t.Fatal("期望报错，实际返回 nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("错误文案应带 HTTP 状态码: %v", err)
	}
	if !strings.Contains(err.Error(), "空响应体") {
		t.Errorf("空响应体应有占位文案: %v", err)
	}
}

// 回归：Telegram.Send 也走 telegramAPIBase，且 ok=false 仍然报错。
func TestTelegramSendUsesBaseAndReportsError(t *testing.T) {
	type captured struct {
		path string
		body map[string]any
	}
	gotCh := make(chan captured, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		gotCh <- captured{path: r.URL.Path, body: body}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`))
	}))
	defer srv.Close()
	useTelegramBase(t, srv.URL)

	tg := &Telegram{cfg: TelegramConfig{BotToken: testBotToken, ChatID: testChatID}}
	err := tg.Send(context.Background(), Message{Title: "标题", Text: "正文"})
	if err == nil {
		t.Fatal("ok=false 必须报错，实际返回 nil")
	}
	if !strings.Contains(err.Error(), "bot was blocked by the user") {
		t.Errorf("错误文案丢了 description: %v", err)
	}

	got := <-gotCh
	if want := "/bot" + testBotToken + "/sendMessage"; got.path != want {
		t.Errorf("path = %q, 期望 %q", got.path, want)
	}
	if got.body["chat_id"] != testChatID {
		t.Errorf("chat_id = %v, 期望 %q", got.body["chat_id"], testChatID)
	}
	if got.body["text"] != "标题\n正文" {
		t.Errorf("text = %v, 期望 标题\\n正文", got.body["text"])
	}
}

// 回归：ok=true 时 Telegram.Send 不报错。
func TestTelegramSendSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	}))
	defer srv.Close()
	useTelegramBase(t, srv.URL)

	tg := &Telegram{cfg: TelegramConfig{BotToken: testBotToken, ChatID: testChatID}}
	if err := tg.Send(context.Background(), Message{Title: "t", Text: "b"}); err != nil {
		t.Fatalf("期望成功，实际报错: %v", err)
	}
}

// 回归：钉钉/飞书在 HTTP 非 200 时仍返回错误（postJSON 语义不能被改坏）。
func TestPostJSONNon200StillErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"errcode":310000,"errmsg":"keywords not in content"}`))
	}))
	defer srv.Close()

	err := postJSON(context.Background(), srv.URL, []byte(`{}`), func(raw []byte) error {
		var r struct {
			ErrCode int    `json:"errcode"`
			ErrMsg  string `json:"errmsg"`
		}
		if e := json.Unmarshal(raw, &r); e == nil && r.ErrCode != 0 {
			return fmt.Errorf("钉钉返回错误 %d: %s", r.ErrCode, r.ErrMsg)
		}
		return nil
	})
	if err == nil {
		t.Fatal("非 200 必须报错，实际返回 nil")
	}
	if !strings.Contains(err.Error(), "310000") {
		t.Errorf("业务错误应被保留: %v", err)
	}
}
