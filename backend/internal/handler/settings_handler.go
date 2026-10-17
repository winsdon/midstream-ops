package handler

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"sub2api-account-monitor/internal/notify"
	"sub2api-account-monitor/internal/pkg/response"
	"sub2api-account-monitor/internal/service"

	"github.com/gin-gonic/gin"
)

// SettingsHandler 系统设置处理器（策略 + 通知渠道 + 渠道状态推送）。
type SettingsHandler struct {
	svc *service.SettingsService
	// notifier 提供「当前启用渠道」给策略页做多选；未启用时也能读配置。
	notifier *notify.Manager
	// statusPush 渠道状态图的组装/推送服务（渲染预览与立即推送都要它）。
	statusPush *service.StatusPushService
}

// NewSettingsHandler 创建 SettingsHandler。
func NewSettingsHandler(svc *service.SettingsService, notifier *notify.Manager, statusPush *service.StatusPushService) *SettingsHandler {
	return &SettingsHandler{svc: svc, notifier: notifier, statusPush: statusPush}
}

// GetStrategy GET /settings/strategy
// 附带默认模板与当前可用渠道，供前端展示占位与多选项（默认模板只在后端定义一份）。
func (h *SettingsHandler) GetStrategy(c *gin.Context) {
	response.Success(c, gin.H{
		"strategy":                      h.svc.Strategy(),
		"default_balance_template":      service.DefaultBalanceTemplate,
		"default_rate_template":         service.DefaultRateTemplate,
		"default_credit_template":       service.DefaultCreditTemplate,
		"default_user_balance_template": service.DefaultUserBalanceTemplate,
		"available_channels":            h.notifier.EnabledNames(),
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

// statusPushGroupDTO 分组排除勾选项。
type statusPushGroupDTO struct {
	ID             int64   `json:"id"`
	Name           string  `json:"name"`
	RateMultiplier float64 `json:"rate_multiplier"`
	Status         string  `json:"status"`
}

// statusPushDTO GET /settings/status-push 的响应体。
//
// 一次返回设置 + 可排除分组 + 上次推送结果：设置页只需要一个请求就能画完，
// 三个数据源都在同一个服务里，拆成三个接口只会多两次往返和三个 loading 态。
type statusPushDTO struct {
	Settings service.StatusPushSettings `json:"settings"`
	// TargetChatIDs 与 settings.chat_ids 同值，单独给一份是因为前端表单直接用它渲染。
	TargetChatIDs []string `json:"target_chat_ids"`
	// AvailableGroups 可推送的公开分组（active 且非专属，按后台 sort_order 排）。
	// 刻意不滤掉「已排除」的组，否则运营取消勾选的入口就消失了。
	AvailableGroups []statusPushGroupDTO `json:"available_groups"`
	// ExcludedGroupIDs 当前排除集（与 settings 同值，前端直接用）。
	ExcludedGroupIDs []int64 `json:"excluded_group_ids"`
	// TelegramBotTokenSet 报告 Bot Token 是否已配置（不下发 token 本身）。
	TelegramBotTokenSet bool                      `json:"telegram_bot_token_set"`
	LastPush            *service.StatusPushResult `json:"last_push"`
	IntervalOptions     []int                     `json:"interval_options"`
	WindowOptions       []int                     `json:"window_options"`
}

// GetStatusPush GET /settings/status-push
func (h *SettingsHandler) GetStatusPush(c *gin.Context) {
	st := h.svc.StatusPush()
	dto := statusPushDTO{
		Settings:            st,
		TargetChatIDs:       st.ChatIDs,
		ExcludedGroupIDs:    st.ExcludedGroupIDs,
		TelegramBotTokenSet: h.svc.Channels().Telegram.BotToken != "",
		IntervalOptions:     service.StatusPushIntervalOptions,
		WindowOptions:       service.StatusPushWindowOptions,
	}
	if h.statusPush != nil {
		dto.LastPush = h.statusPush.LastPush()
		// 分组列表来自远端库。不可用时返回 503（可重试）而不是 500：设置本身
		// 存在本地 monitor 库、仍然读得到，把整个设置页判成「服务端出错」
		// 会让运营以为配置坏了。与 /stability/passive 等只读页的降级语义一致。
		if !h.pgAvailable() {
			response.ServiceUnavailable(c, "线上数据库暂不可用，无法列出分组")
			return
		}
		groups, err := h.statusPush.PublicGroups(c.Request.Context())
		if err != nil {
			response.InternalError(c, "读取分组失败: "+err.Error())
			return
		}
		dto.AvailableGroups = make([]statusPushGroupDTO, 0, len(groups))
		for _, g := range groups {
			dto.AvailableGroups = append(dto.AvailableGroups, statusPushGroupDTO{
				ID:             g.ID,
				Name:           g.Name,
				RateMultiplier: g.RateMultiplier,
				Status:         g.Status,
			})
		}
	}
	// 前端下拉要能直接 map（顺序即档位顺序），这里不再做 nil 兜底：
	// 两个选项列表都是包级非 nil 变量，判空是永远不会命中的死代码。
	response.Success(c, dto)
}

// SaveStatusPush PUT /settings/status-push
// chat_id 格式在保存时就校验：等整点推送失败可能要半小时后才发现。
func (h *SettingsHandler) SaveStatusPush(c *gin.Context) {
	var req service.StatusPushSettings
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "参数格式错误")
		return
	}
	if err := service.ValidateStatusPushChatIDs(req.ChatIDs); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	st, err := h.svc.SaveStatusPush(c.Request.Context(), req)
	if err != nil {
		response.InternalError(c, "保存失败: "+err.Error())
		return
	}
	response.Success(c, st)
}

// PreviewStatusPush GET /settings/status-push/preview?window_minutes=&excluded_group_ids=
//
// 直接输出 image/png 而不是 JSON envelope：前端把它塞进 <img> / createObjectURL，
// 包一层 base64 会白白多 33% 体积和一次解码。
//
// 两个可选参数用于「预览还没保存的改动」：运营把窗口从 90 改成 180 后点预览，
// 看到的必须是他刚选的窗口 —— 只按已保存设置渲染会让人以为改的没生效。
// 参数缺省或非法时回落到已保存的设置。
func (h *SettingsHandler) PreviewStatusPush(c *gin.Context) {
	if h.statusPush == nil {
		response.ServiceUnavailable(c, "渠道推送服务未启用")
		return
	}
	// 与其它只读页一致：远端库不可用是 503（可重试），不是 500（本站出错）。
	if !h.pgAvailable() {
		response.ServiceUnavailable(c, "线上数据库暂不可用")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	override := service.StatusPushPreviewOverride{}
	if v := c.Query("window_minutes"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			override.WindowMinutes = &n
		}
	}
	// 空串是合法的「本次预览不排除任何分组」，因此用 GetQuery 区分「没传」与「传空」。
	if raw, ok := c.GetQuery("excluded_group_ids"); ok {
		ids := []int64{}
		for _, part := range strings.Split(raw, ",") {
			if v, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64); err == nil {
				ids = append(ids, v)
			}
		}
		override.ExcludedGroupIDs = &ids
	}

	png, err := h.statusPush.Preview(ctx, override)
	if err != nil {
		response.InternalError(c, "渲染失败: "+err.Error())
		return
	}
	c.Data(http.StatusOK, "image/png", png)
}

// TestStatusPush POST /settings/status-push/test
// 立即推送一次；单个群失败不影响其它群，结果逐群返回。
func (h *SettingsHandler) TestStatusPush(c *gin.Context) {
	if h.statusPush == nil {
		response.ServiceUnavailable(c, "渠道推送服务未启用")
		return
	}
	if !h.pgAvailable() {
		response.ServiceUnavailable(c, "线上数据库暂不可用")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
	defer cancel()
	res := h.statusPush.PushNow(ctx)
	response.Success(c, res)
}

// pgAvailable 报告远端库是否可用，用于把「库不可用」判成 503（可重试）而不是 500。
// 调用点都在 `h.statusPush == nil` 分支之后，因此这里假定 statusPush 非 nil。
func (h *SettingsHandler) pgAvailable() bool {
	return h.statusPush.PGAvailable()
}
