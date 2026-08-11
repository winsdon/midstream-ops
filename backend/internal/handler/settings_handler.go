package handler

import (
	"context"
	"time"

	"sub2api-account-monitor/internal/notify"
	"sub2api-account-monitor/internal/pkg/response"
	"sub2api-account-monitor/internal/service"

	"github.com/gin-gonic/gin"
)

// SettingsHandler 系统设置处理器（策略 + 通知渠道）。
type SettingsHandler struct {
	svc      *service.SettingsService
	notifier *notify.Manager
}

// NewSettingsHandler 创建 SettingsHandler。
func NewSettingsHandler(svc *service.SettingsService, notifier *notify.Manager) *SettingsHandler {
	return &SettingsHandler{svc: svc, notifier: notifier}
}

// GetStrategy GET /settings/strategy
// 附带默认模板与当前可用渠道，供前端展示占位与多选项（默认模板只在后端定义一份）。
func (h *SettingsHandler) GetStrategy(c *gin.Context) {
	response.Success(c, gin.H{
		"strategy":                 h.svc.Strategy(),
		"default_balance_template": service.DefaultBalanceTemplate,
		"default_rate_template":    service.DefaultRateTemplate,
		"default_credit_template":  service.DefaultCreditTemplate,
		"available_channels":       h.notifier.EnabledNames(),
	})
}

// SaveStrategy PUT /settings/strategy
func (h *SettingsHandler) SaveStrategy(c *gin.Context) {
	var req service.StrategySettings
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "参数格式错误")
		return
	}
	st, err := h.svc.SaveStrategy(c.Request.Context(), req)
	if err != nil {
		response.InternalError(c, "保存失败: "+err.Error())
		return
	}
	response.Success(c, st)
}

// notifyChannelsDTO 通知渠道输出（脱敏：secret/token 只回显是否已配置）。
type notifyChannelsDTO struct {
	DingTalk struct {
		Enabled   bool   `json:"enabled"`
		Webhook   string `json:"webhook"`
		HasSecret bool   `json:"has_secret"`
	} `json:"dingtalk"`
	Feishu struct {
		Enabled   bool   `json:"enabled"`
		Webhook   string `json:"webhook"`
		HasSecret bool   `json:"has_secret"`
	} `json:"feishu"`
	Telegram struct {
		Enabled     bool   `json:"enabled"`
		ChatID      string `json:"chat_id"`
		HasBotToken bool   `json:"has_bot_token"`
	} `json:"telegram"`
}

// GetChannels GET /settings/notify
func (h *SettingsHandler) GetChannels(c *gin.Context) {
	ch := h.svc.Channels()
	var dto notifyChannelsDTO
	dto.DingTalk.Enabled = ch.DingTalk.Enabled
	dto.DingTalk.Webhook = ch.DingTalk.Webhook
	dto.DingTalk.HasSecret = ch.DingTalk.Secret != ""
	dto.Feishu.Enabled = ch.Feishu.Enabled
	dto.Feishu.Webhook = ch.Feishu.Webhook
	dto.Feishu.HasSecret = ch.Feishu.Secret != ""
	dto.Telegram.Enabled = ch.Telegram.Enabled
	dto.Telegram.ChatID = ch.Telegram.ChatID
	dto.Telegram.HasBotToken = ch.Telegram.BotToken != ""
	response.Success(c, dto)
}

// notifyChannelsRequest 通知渠道保存请求。
// secret/bot_token 为 nil 表示保留库中原值（前端脱敏展示后未改动时不回传）。
type notifyChannelsRequest struct {
	DingTalk struct {
		Enabled bool    `json:"enabled"`
		Webhook string  `json:"webhook"`
		Secret  *string `json:"secret"`
	} `json:"dingtalk"`
	Feishu struct {
		Enabled bool    `json:"enabled"`
		Webhook string  `json:"webhook"`
		Secret  *string `json:"secret"`
	} `json:"feishu"`
	Telegram struct {
		Enabled  bool    `json:"enabled"`
		BotToken *string `json:"bot_token"`
		ChatID   string  `json:"chat_id"`
	} `json:"telegram"`
}

// merge 把请求合并到现有配置（nil 凭据字段保留原值）。
func (r *notifyChannelsRequest) merge(cur notify.ChannelConfig) notify.ChannelConfig {
	out := notify.ChannelConfig{}
	out.DingTalk.Enabled = r.DingTalk.Enabled
	out.DingTalk.Webhook = r.DingTalk.Webhook
	out.DingTalk.Secret = cur.DingTalk.Secret
	if r.DingTalk.Secret != nil {
		out.DingTalk.Secret = *r.DingTalk.Secret
	}
	out.Feishu.Enabled = r.Feishu.Enabled
	out.Feishu.Webhook = r.Feishu.Webhook
	out.Feishu.Secret = cur.Feishu.Secret
	if r.Feishu.Secret != nil {
		out.Feishu.Secret = *r.Feishu.Secret
	}
	out.Telegram.Enabled = r.Telegram.Enabled
	out.Telegram.ChatID = r.Telegram.ChatID
	out.Telegram.BotToken = cur.Telegram.BotToken
	if r.Telegram.BotToken != nil {
		out.Telegram.BotToken = *r.Telegram.BotToken
	}
	return out
}

// SaveChannels PUT /settings/notify
func (h *SettingsHandler) SaveChannels(c *gin.Context) {
	var req notifyChannelsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "参数格式错误")
		return
	}
	merged := req.merge(h.svc.Channels())
	if err := h.svc.SaveChannels(c.Request.Context(), merged); err != nil {
		response.InternalError(c, "保存失败: "+err.Error())
		return
	}
	response.Success(c, gin.H{"saved": true})
}

// testNotifyRequest 测试发送请求。
type testNotifyRequest struct {
	Channel string `json:"channel" binding:"required"` // dingtalk | feishu | telegram
}

// TestChannel POST /settings/notify/test
// 用当前已保存的配置向指定渠道发一条测试消息。
func (h *SettingsHandler) TestChannel(c *gin.Context) {
	var req testNotifyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "channel 必填（dingtalk|feishu|telegram）")
		return
	}
	n := notify.BuildOne(req.Channel, h.svc.Channels())
	if n == nil {
		response.BadRequest(c, "该渠道未配置完整，请先保存配置")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	if err := n.Send(ctx, notify.Message{
		Title: "测试通知",
		Text:  "这是一条来自 sub2api-monitor 的测试消息，收到即配置成功。",
	}); err != nil {
		response.Success(c, gin.H{"ok": false, "error": err.Error()})
		return
	}
	response.Success(c, gin.H{"ok": true})
}
