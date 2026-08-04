package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"sub2api-account-monitor/internal/notify"
	"sub2api-account-monitor/internal/repository"
)

// 设置域 key。
const (
	settingsKeyStrategy = "strategy"
	settingsKeyNotify   = "notify"
)

// minRefreshIntervalSeconds 数据刷新频率下限（防打爆上游）。
const minRefreshIntervalSeconds = 60

// DefaultBalanceTemplate 余额预警默认文案。
// 只在后端定义一份，前端通过 GET /settings/strategy 取回展示，避免两处漂移。
const DefaultBalanceTemplate = "【余额预警】{siteName} 站点余额（CNY）已不足 {threshold} 元，当前余额为 {balance} 元。"

// DefaultRateTemplate 倍率变更默认文案。
const DefaultRateTemplate = "【倍率变更】{entityName} 倍率由 {oldRate} 调整为 {newRate}。"

// DefaultCreditTemplate 授信额度预警默认文案。
const DefaultCreditTemplate = "【授信预警】客户 {customerName} 已用额度达 {band}%，当前敞口 ${outstanding} / 授信 ${limit}，剩余可垫付 ${available}。"

// StrategySettings 自动化与策略配置。
type StrategySettings struct {
	// 数据刷新频率（供应商 sync：余额+成本+分组倍率）
	RefreshEnabled         bool `json:"refresh_enabled"`
	RefreshIntervalSeconds int  `json:"refresh_interval_seconds"`

	// 余额预警：balance × recharge_rate 折 CNY 低于阈值时通知
	BalanceAlertEnabled bool `json:"balance_alert_enabled"`
	// DefaultBalanceThreshold 全局触发金额（CNY）；供应商自身阈值 > 0 时优先用供应商的
	DefaultBalanceThreshold float64 `json:"default_balance_threshold"`
	// BalanceNotifyChannels 接收余额预警的渠道名（dingtalk|feishu|telegram）；空 = 不发送
	BalanceNotifyChannels []string `json:"balance_notify_channels"`
	// BalanceTemplate 自定义文案，支持 {siteName} {balance} {threshold}；空则用默认模板
	BalanceTemplate string `json:"balance_template"`

	// 倍率变更预警：监控范围内分组倍率任何变动即通知
	RateAlertEnabled   bool     `json:"rate_alert_enabled"`
	RateNotifyChannels []string `json:"rate_notify_channels"`
	RateTemplate       string   `json:"rate_template"`

	// 授信额度预警：客户垫付敞口达授信额度 80% / 100% 时通知（升档才发，不阻断业务）
	CreditAlertEnabled   bool     `json:"credit_alert_enabled"`
	CreditNotifyChannels []string `json:"credit_notify_channels"`
	// CreditTemplate 支持 {customerName} {band} {outstanding} {limit} {available}；空则用默认模板
	CreditTemplate string `json:"credit_template"`
}

// normalize 填充默认值并夹紧下限。
func (s *StrategySettings) normalize() {
	if s.RefreshIntervalSeconds < minRefreshIntervalSeconds {
		s.RefreshIntervalSeconds = minRefreshIntervalSeconds
	}
	if s.DefaultBalanceThreshold < 0 {
		s.DefaultBalanceThreshold = 0
	}
	if s.BalanceNotifyChannels == nil {
		s.BalanceNotifyChannels = []string{}
	}
	if s.RateNotifyChannels == nil {
		s.RateNotifyChannels = []string{}
	}
	if s.CreditNotifyChannels == nil {
		s.CreditNotifyChannels = []string{}
	}
}

// defaultStrategy 缺省策略。
// 刷新默认开启（间隔 10 分钟）：与旧版 cron 自动采集行为保持连续，升级不静默停采；
// 预警默认关闭（需先配置通知渠道）。
func defaultStrategy() StrategySettings {
	return StrategySettings{
		RefreshEnabled:          true,
		RefreshIntervalSeconds:  600,
		DefaultBalanceThreshold: 10,
		BalanceNotifyChannels:   []string{},
		RateNotifyChannels:      []string{},
		CreditNotifyChannels:    []string{},
	}
}

// SettingsService 系统设置的读写与热更新分发。
type SettingsService struct {
	repo *repository.SettingsRepo

	mu       sync.RWMutex
	strategy StrategySettings
	channels notify.ChannelConfig

	// OnStrategyChanged 策略变化回调（调度器重建定时器）；装配层注入。
	OnStrategyChanged func(StrategySettings)
	// OnNotifyChanged 通知配置变化回调（Manager 重建渠道）；装配层注入。
	OnNotifyChanged func(notify.ChannelConfig)
}

// NewSettingsService 创建并从库加载当前设置。
func NewSettingsService(repo *repository.SettingsRepo) (*SettingsService, error) {
	s := &SettingsService{repo: repo, strategy: defaultStrategy()}
	if err := s.load(context.Background()); err != nil {
		return nil, err
	}
	return s, nil
}

// load 从 SQLite 加载两域设置（缺失时用默认值）。
func (s *SettingsService) load(ctx context.Context) error {
	raw, err := s.repo.Get(ctx, settingsKeyStrategy)
	if err != nil {
		return fmt.Errorf("读取策略设置失败: %w", err)
	}
	if raw != "" {
		var st StrategySettings
		if err := json.Unmarshal([]byte(raw), &st); err != nil {
			return fmt.Errorf("解析策略设置失败: %w", err)
		}
		st.normalize()
		s.strategy = st
	}

	raw, err = s.repo.Get(ctx, settingsKeyNotify)
	if err != nil {
		return fmt.Errorf("读取通知设置失败: %w", err)
	}
	if raw != "" {
		var ch notify.ChannelConfig
		if err := json.Unmarshal([]byte(raw), &ch); err != nil {
			return fmt.Errorf("解析通知设置失败: %w", err)
		}
		s.channels = ch
	}
	return nil
}

// Strategy 返回当前策略设置。
func (s *SettingsService) Strategy() StrategySettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.strategy
}

// Channels 返回当前通知渠道配置。
func (s *SettingsService) Channels() notify.ChannelConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.channels
}

// SaveStrategy 保存策略并触发热更新回调。
func (s *SettingsService) SaveStrategy(ctx context.Context, st StrategySettings) (StrategySettings, error) {
	st.normalize()
	raw, err := json.Marshal(st)
	if err != nil {
		return st, err
	}
	if err := s.repo.Set(ctx, settingsKeyStrategy, string(raw)); err != nil {
		return st, fmt.Errorf("保存策略设置失败: %w", err)
	}
	s.mu.Lock()
	s.strategy = st
	cb := s.OnStrategyChanged
	s.mu.Unlock()
	if cb != nil {
		cb(st)
	}
	return st, nil
}

// SaveChannels 保存通知渠道并触发热更新回调。
func (s *SettingsService) SaveChannels(ctx context.Context, ch notify.ChannelConfig) error {
	raw, err := json.Marshal(ch)
	if err != nil {
		return err
	}
	if err := s.repo.Set(ctx, settingsKeyNotify, string(raw)); err != nil {
		return fmt.Errorf("保存通知设置失败: %w", err)
	}
	s.mu.Lock()
	s.channels = ch
	cb := s.OnNotifyChanged
	s.mu.Unlock()
	if cb != nil {
		cb(ch)
	}
	return nil
}
