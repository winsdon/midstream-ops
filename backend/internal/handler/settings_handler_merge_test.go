package handler

import (
	"testing"

	"sub2api-account-monitor/internal/notify"
)

// merge 的「保留原值」语义只覆盖指针字段（secret / bot_token）。
// webhook / enabled / chat_id 都是普通值，**传空就是清空**。
//
// 【为什么要单独立一条用例】这里有一个很容易踩的坑：保存请求体里少填一个
// webhook，并不会「保持原样」，而是把既有的告警渠道**静默清掉** —— 验收时用
// 一份「只测 telegram token」的请求体打过真实接口，就把线上 Feishu 的 webhook
// 抹掉了，而响应只回 {"saved":true}。用例把这条契约钉死，谁改 merge 都会先看到它。
func TestMergeChannelsSemantics(t *testing.T) {
	prev := notify.ChannelConfig{}
	prev.DingTalk.Enabled = true
	prev.DingTalk.Webhook = "https://oapi.dingtalk.com/robot/send?access_token=DT"
	prev.DingTalk.Secret = "DT-SECRET"
	prev.Feishu.Enabled = true
	prev.Feishu.Webhook = "https://open.feishu.cn/open-apis/bot/v2/hook/FEISHU"
	prev.Telegram.BotToken = "123:ABC"
	prev.Telegram.ChatID = "-1001"

	t.Run("webhook 传空串是显式清空，不是保持原值", func(t *testing.T) {
		var req notifyChannelsRequest
		req.DingTalk.Enabled = false
		req.DingTalk.Webhook = ""
		req.Feishu.Enabled = false
		req.Feishu.Webhook = ""
		// secret / bot_token 留 nil

		got := req.merge(prev)
		if got.DingTalk.Webhook != "" || got.Feishu.Webhook != "" {
			t.Errorf("webhook 空串应清空: dingtalk=%q feishu=%q", got.DingTalk.Webhook, got.Feishu.Webhook)
		}
		if got.DingTalk.Enabled || got.Feishu.Enabled {
			t.Error("enabled=false 应生效")
		}
		if got.Telegram.ChatID != "" {
			t.Errorf("chat_id 空串应清空，得到 %q", got.Telegram.ChatID)
		}
	})

	t.Run("指针字段为 nil 才表示保留原值", func(t *testing.T) {
		var req notifyChannelsRequest
		req.DingTalk.Enabled = true
		req.DingTalk.Webhook = prev.DingTalk.Webhook
		req.DingTalk.Secret = nil // nil = 不改
		req.Feishu.Enabled = true
		req.Feishu.Webhook = prev.Feishu.Webhook
		req.Feishu.Secret = nil
		req.Telegram.BotToken = nil // nil = 不改

		got := req.merge(prev)
		if got.DingTalk.Secret != "DT-SECRET" {
			t.Errorf("nil secret 应保留原值，得到 %q", got.DingTalk.Secret)
		}
		if got.Telegram.BotToken != "123:ABC" {
			t.Errorf("nil bot_token 应保留原值，得到 %q", got.Telegram.BotToken)
		}
		if got.Feishu.Secret != "" {
			t.Errorf("原值为空仍应为空，得到 %q", got.Feishu.Secret)
		}
	})

	t.Run("显式传新值覆盖原值", func(t *testing.T) {
		newSecret := "NEW-SECRET"
		newToken := "456:DEF"
		var req notifyChannelsRequest
		req.DingTalk.Secret = &newSecret
		req.Telegram.BotToken = &newToken

		got := req.merge(prev)
		if got.DingTalk.Secret != newSecret {
			t.Errorf("显式 secret 应覆盖，得到 %q", got.DingTalk.Secret)
		}
		if got.Telegram.BotToken != newToken {
			t.Errorf("显式 bot_token 应覆盖，得到 %q", got.Telegram.BotToken)
		}
	})

	t.Run("传空串清空 secret（与 nil 区分）", func(t *testing.T) {
		empty := ""
		var req notifyChannelsRequest
		req.DingTalk.Secret = &empty

		got := req.merge(prev)
		if got.DingTalk.Secret != "" {
			t.Errorf("空串指针应清空 secret，得到 %q", got.DingTalk.Secret)
		}
	})
}

// 保存渠道时空 webhook 会清空既有配置 —— 上面已锁语义，这里再锁一条「危险面」提示：
// 只改一个渠道、其它渠道字段缺省时，别的渠道会被一起清掉。这是当前的既定契约
// （前端总是回传完整表单，所以正常路径安全），但 API 直接调用方必须知道。
func TestMergeChannelsClearsOmittedChannels(t *testing.T) {
	prev := notify.ChannelConfig{}
	prev.Feishu.Enabled = true
	prev.Feishu.Webhook = "https://open.feishu.cn/hook/REAL"

	// 请求体里完全没提 feishu（零值）
	var req notifyChannelsRequest
	got := req.merge(prev)

	if got.Feishu.Enabled || got.Feishu.Webhook != "" {
		t.Fatalf("既定契约：缺省的渠道会被清空（前端总是回传完整表单）；"+
			"若这里失败说明 merge 改成了「部分更新」，是好事，请同步更新本用例与 README。"+
			" 实际: enabled=%v webhook=%q", got.Feishu.Enabled, got.Feishu.Webhook)
	}
}
