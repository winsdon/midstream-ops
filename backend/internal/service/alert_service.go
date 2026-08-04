package service

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"sub2api-account-monitor/internal/notify"
)

// balanceAlertCooldown 余额预警冷却：同一供应商 1 小时内只发一次。
const balanceAlertCooldown = time.Hour

// AlertService 预警编排：余额预警（带冷却与恢复清零）+ 倍率变更预警。
// 只依赖 SettingsService（开关/阈值/模板/渠道）与 notify.Manager（发送），
// 由装配层挂到 ProviderSyncService.AfterSync 与 RateService.OnRateChanged。
type AlertService struct {
	settings *SettingsService
	notifier *notify.Manager

	mu        sync.Mutex
	lastAlert map[int64]time.Time // provider_id → 上次余额预警时刻
}

// NewAlertService 创建 AlertService。
func NewAlertService(settings *SettingsService, notifier *notify.Manager) *AlertService {
	return &AlertService{
		settings:  settings,
		notifier:  notifier,
		lastAlert: make(map[int64]time.Time),
	}
}

// HandleSyncOutcome AfterSync 钩子：检查余额预警。
func (a *AlertService) HandleSyncOutcome(o SyncOutcome) {
	st := a.settings.Strategy()
	if !st.BalanceAlertEnabled || len(st.BalanceNotifyChannels) == 0 {
		return
	}
	p := o.Provider
	if p == nil || o.Snapshot == nil || o.Snapshot.Balance == nil {
		return
	}
	if p.IgnoreBalanceAlert {
		return // 站点级静音：长期低余额/待弃用站点不再反复触发
	}
	// 阈值优先级：供应商自身阈值 > 0 时用它，否则用全局默认
	threshold := p.LowBalanceThreshold
	if threshold <= 0 {
		threshold = st.DefaultBalanceThreshold
	}
	if threshold <= 0 {
		return // 两处都没配阈值 = 不预警
	}

	rate := p.RechargeRate
	if rate <= 0 {
		rate = 1
	}
	cny := *o.Snapshot.Balance * rate

	if cny >= threshold {
		// 恢复到阈值上方：清冷却，下次跌破立即告警
		a.mu.Lock()
		delete(a.lastAlert, p.ID)
		a.mu.Unlock()
		return
	}

	a.mu.Lock()
	last, ok := a.lastAlert[p.ID]
	if ok && time.Since(last) < balanceAlertCooldown {
		a.mu.Unlock()
		return
	}
	a.lastAlert[p.ID] = time.Now()
	a.mu.Unlock()

	text := renderBalanceTemplate(st.BalanceTemplate, p.Name, cny, threshold)
	go a.send(st.BalanceNotifyChannels, notify.Message{Title: "余额预警", Text: text})
	log.Printf("[alert] 余额预警: %s ¥%.2f < ¥%.2f", p.Name, cny, threshold)
}

// renderBalanceTemplate 渲染余额预警文案（空模板用默认）。
// 支持变量：{siteName} {balance} {threshold}，金额统一两位小数。
func renderBalanceTemplate(tpl, siteName string, balance, threshold float64) string {
	if strings.TrimSpace(tpl) == "" {
		tpl = DefaultBalanceTemplate
	}
	return strings.NewReplacer(
		"{siteName}", siteName,
		"{balance}", fmt.Sprintf("%.2f", balance),
		"{threshold}", fmt.Sprintf("%.2f", threshold),
	).Replace(tpl)
}

// renderRateTemplate 渲染倍率变更文案（空模板用默认）。
// 支持变量：{entityName} {oldRate} {newRate} {direction}。
func renderRateTemplate(tpl, entityName string, oldRate, newRate float64) string {
	if strings.TrimSpace(tpl) == "" {
		tpl = DefaultRateTemplate
	}
	direction := "上调"
	if newRate < oldRate {
		direction = "下调"
	}
	return strings.NewReplacer(
		"{entityName}", entityName,
		"{oldRate}", fmt.Sprintf("%.4f", oldRate),
		"{newRate}", fmt.Sprintf("%.4f", newRate),
		"{direction}", direction,
	).Replace(tpl)
}

// RateChangeEvent 一次倍率变化（RateService 检出变化时发出）。
type RateChangeEvent struct {
	EntityType    string // group | account
	EntityID      string // 原始实体 id（upstream scope 为分组名，自动调价按此匹配）
	EntityName    string // 展示名（通知里可能带供应商前缀）
	UpstreamGroup string // upstream scope 的分组名（= EntityID，语义别名）
	OldRate       float64
	NewRate       float64
}

// HandleRateChanges 倍率变更钩子：合并同轮变化为一条通知。
func (a *AlertService) HandleRateChanges(events []RateChangeEvent) {
	st := a.settings.Strategy()
	if len(events) == 0 || !st.RateAlertEnabled || len(st.RateNotifyChannels) == 0 {
		return
	}
	var b strings.Builder
	for _, e := range events {
		b.WriteString("- ")
		b.WriteString(renderRateTemplate(st.RateTemplate, e.EntityName, e.OldRate, e.NewRate))
		b.WriteString("\n")
	}
	msg := notify.Message{
		Title: fmt.Sprintf("倍率变更（%d 项）", len(events)),
		Text:  b.String(),
	}
	go a.send(st.RateNotifyChannels, msg)
	log.Printf("[alert] 倍率变更预警: %d 项", len(events))
}

// renderCreditTemplate 渲染授信预警文案（空模板用默认）。
// 支持变量：{customerName} {band} {outstanding} {limit} {available}，金额统一两位小数。
func renderCreditTemplate(tpl, customerName string, band int, outstanding, limit, available float64) string {
	if strings.TrimSpace(tpl) == "" {
		tpl = DefaultCreditTemplate
	}
	return strings.NewReplacer(
		"{customerName}", customerName,
		"{band}", fmt.Sprintf("%d", band),
		"{outstanding}", fmt.Sprintf("%.2f", outstanding),
		"{limit}", fmt.Sprintf("%.2f", limit),
		"{available}", fmt.Sprintf("%.2f", available),
	).Replace(tpl)
}

// HandleCreditAlert 授信额度升档钩子。
//
// 【无状态】边沿判定与闩锁都在 CreditService 里完成（闩锁落 customers.alert_level），
// 这里只负责渲染与投递——不要在此加内存 map，那会与库里的闩锁形成两个真相源。
func (a *AlertService) HandleCreditAlert(ev CreditAlertEvent) {
	st := a.settings.Strategy()
	if !st.CreditAlertEnabled || len(st.CreditNotifyChannels) == 0 {
		return
	}
	text := renderCreditTemplate(st.CreditTemplate, ev.CustomerName, ev.Band,
		ev.Outstanding, ev.CreditLimit, ev.Available)
	title := "授信额度预警"
	if ev.Band >= creditBandOverflow {
		title = "授信额度超限"
	}
	go a.send(st.CreditNotifyChannels, notify.Message{Title: title, Text: text})
	// 日志只打客户名与金额，不打 KYC 身份信息
	log.Printf("[alert] 授信预警: %s 达 %d%% (敞口 %.2f / 额度 %.2f)",
		ev.CustomerName, ev.Band, ev.Outstanding, ev.CreditLimit)
}

// send 异步定向发送（独立超时，不阻塞采集链路）。
func (a *AlertService) send(channels []string, msg notify.Message) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	a.notifier.SendTo(ctx, channels, msg)
}
