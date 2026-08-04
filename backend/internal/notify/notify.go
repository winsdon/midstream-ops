// Package notify 提供机器人通知渠道（钉钉 / 飞书 / Telegram）。
//
// 设计：Notifier 小接口 + Manager 扇出。采集/预警侧只依赖 Manager，
// 渠道配置变化时整体重建 notifiers，避免逐渠道热更新的状态管理。
package notify

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"
)

// Message 一条通知。
type Message struct {
	Title string
	Text  string
}

// Notifier 单个通知渠道。
type Notifier interface {
	Name() string
	Send(ctx context.Context, msg Message) error
}

// ChannelConfig 三渠道配置（settings 中 notify 域的 JSON 结构）。
type ChannelConfig struct {
	DingTalk DingTalkConfig `json:"dingtalk"`
	Feishu   FeishuConfig   `json:"feishu"`
	Telegram TelegramConfig `json:"telegram"`
}

// httpClient 渠道共享的 HTTP 客户端。
var httpClient = &http.Client{Timeout: 10 * time.Second}

// Build 根据配置构造启用的渠道列表。
func Build(cfg ChannelConfig) []Notifier {
	var out []Notifier
	if cfg.DingTalk.Enabled && cfg.DingTalk.Webhook != "" {
		out = append(out, &DingTalk{cfg: cfg.DingTalk})
	}
	if cfg.Feishu.Enabled && cfg.Feishu.Webhook != "" {
		out = append(out, &Feishu{cfg: cfg.Feishu})
	}
	if cfg.Telegram.Enabled && cfg.Telegram.BotToken != "" && cfg.Telegram.ChatID != "" {
		out = append(out, &Telegram{cfg: cfg.Telegram})
	}
	return out
}

// BuildOne 构造单个渠道（测试发送用，允许未启用的配置）。
func BuildOne(channel string, cfg ChannelConfig) Notifier {
	switch channel {
	case "dingtalk":
		if cfg.DingTalk.Webhook != "" {
			return &DingTalk{cfg: cfg.DingTalk}
		}
	case "feishu":
		if cfg.Feishu.Webhook != "" {
			return &Feishu{cfg: cfg.Feishu}
		}
	case "telegram":
		if cfg.Telegram.BotToken != "" && cfg.Telegram.ChatID != "" {
			return &Telegram{cfg: cfg.Telegram}
		}
	}
	return nil
}

// Manager 通知渠道管理器：持有当前启用的渠道，配置变化时整体替换。
type Manager struct {
	mu        sync.RWMutex
	notifiers []Notifier
}

// NewManager 创建 Manager。
func NewManager(cfg ChannelConfig) *Manager {
	return &Manager{notifiers: Build(cfg)}
}

// Reload 用新配置重建渠道列表。
func (m *Manager) Reload(cfg ChannelConfig) {
	m.mu.Lock()
	m.notifiers = Build(cfg)
	m.mu.Unlock()
}

// Broadcast 向所有启用渠道发送；单渠道失败仅记日志，不中断其余渠道。
func (m *Manager) Broadcast(ctx context.Context, msg Message) {
	m.mu.RLock()
	list := m.notifiers
	m.mu.RUnlock()
	for _, n := range list {
		if err := n.Send(ctx, msg); err != nil {
			log.Printf("[notify] %s 发送失败: %v", n.Name(), err)
		}
	}
}

// SendTo 只向指定渠道发送（渠道名 = Notifier.Name()）。
// names 为空时不发送任何消息 —— 预警配置里没选渠道即视为不通知。
func (m *Manager) SendTo(ctx context.Context, names []string, msg Message) {
	if len(names) == 0 {
		return
	}
	want := make(map[string]struct{}, len(names))
	for _, n := range names {
		want[n] = struct{}{}
	}

	m.mu.RLock()
	list := m.notifiers
	m.mu.RUnlock()
	for _, n := range list {
		if _, ok := want[n.Name()]; !ok {
			continue
		}
		if err := n.Send(ctx, msg); err != nil {
			log.Printf("[notify] %s 发送失败: %v", n.Name(), err)
		}
	}
}

// EnabledNames 返回当前已启用的渠道名（供前端展示可选项）。
func (m *Manager) EnabledNames() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.notifiers))
	for _, n := range m.notifiers {
		out = append(out, n.Name())
	}
	return out
}
