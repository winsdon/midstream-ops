package service

import (
	"strings"
	"testing"
	"time"

	"sub2api-account-monitor/internal/notify"
	"sub2api-account-monitor/internal/repository"
)

// newTestAlertService 构造无外部依赖的 AlertService：
// 渠道配置为空，Manager 的 notifiers 为空列表，send 是安全的空操作。
func newTestAlertService(st StrategySettings) *AlertService {
	return &AlertService{
		settings:  &SettingsService{strategy: st},
		notifier:  notify.NewManager(notify.ChannelConfig{}),
		lastAlert: make(map[int64]time.Time),
	}
}

// TestHandleSyncOutcomeBalanceAlert 断言告警是否触发。
//
// 判据用 lastAlert 而非拦截通知：写入 lastAlert 是「决定发送」这一决策的
// 确定性副作用，且发生在异步 send 之前，不受 goroutine 时序影响。
func TestHandleSyncOutcomeBalanceAlert(t *testing.T) {
	strategy := StrategySettings{
		BalanceAlertEnabled:     true,
		BalanceNotifyChannels:   []string{"dingtalk"},
		DefaultBalanceThreshold: 100,
	}
	balance := func(v float64) *float64 { return &v }

	cases := []struct {
		name      string
		provider  *repository.Provider
		balance   *float64
		wantAlert bool
	}{
		{
			name:      "跌破阈值触发告警",
			provider:  &repository.Provider{ID: 1, Name: "walk", RechargeRate: 1},
			balance:   balance(50),
			wantAlert: true,
		},
		{
			name:      "站点静音后不触发",
			provider:  &repository.Provider{ID: 2, Name: "pet", RechargeRate: 1, IgnoreBalanceAlert: true},
			balance:   balance(50),
			wantAlert: false,
		},
		{
			name:      "静音优先于站点自定义阈值",
			provider:  &repository.Provider{ID: 3, Name: "mi", RechargeRate: 1, LowBalanceThreshold: 999, IgnoreBalanceAlert: true},
			balance:   balance(50),
			wantAlert: false,
		},
		{
			name:      "余额充足不触发",
			provider:  &repository.Provider{ID: 4, Name: "ok", RechargeRate: 1},
			balance:   balance(200),
			wantAlert: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := newTestAlertService(strategy)
			a.HandleSyncOutcome(SyncOutcome{
				Provider: c.provider,
				Snapshot: &repository.BalanceSnapshot{ProviderID: c.provider.ID, Balance: c.balance},
			})
			a.mu.Lock()
			_, alerted := a.lastAlert[c.provider.ID]
			a.mu.Unlock()
			if alerted != c.wantAlert {
				t.Errorf("告警触发 = %v, 期望 %v", alerted, c.wantAlert)
			}
		})
	}
}

func TestRenderBalanceTemplate(t *testing.T) {
	cases := []struct {
		name      string
		tpl       string
		siteName  string
		balance   float64
		threshold float64
		wantParts []string
	}{
		{
			name:      "空模板用默认",
			tpl:       "",
			siteName:  "walk",
			balance:   8.5,
			threshold: 10,
			wantParts: []string{"walk", "8.50", "10.00"},
		},
		{
			name:      "仅空白也视为空模板",
			tpl:       "   ",
			siteName:  "pet",
			balance:   1.2,
			threshold: 5,
			wantParts: []string{"pet", "1.20", "5.00"},
		},
		{
			name:      "自定义模板三变量全替换",
			tpl:       "{siteName} 余额 {balance} 低于 {threshold}",
			siteName:  "哈基米",
			balance:   3.456,
			threshold: 20,
			wantParts: []string{"哈基米 余额 3.46 低于 20.00"},
		},
		{
			name:      "变量可重复出现",
			tpl:       "{siteName}/{siteName} {balance}",
			siteName:  "x",
			balance:   1,
			threshold: 2,
			wantParts: []string{"x/x 1.00"},
		},
		{
			name:      "无变量模板原样返回",
			tpl:       "余额不足，请充值",
			siteName:  "y",
			balance:   1,
			threshold: 2,
			wantParts: []string{"余额不足，请充值"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := renderBalanceTemplate(c.tpl, c.siteName, c.balance, c.threshold)
			for _, part := range c.wantParts {
				if !strings.Contains(got, part) {
					t.Errorf("渲染结果缺少 %q\n实际: %s", part, got)
				}
			}
			// 未替换的占位符残留说明变量名写错
			for _, ph := range []string{"{siteName}", "{balance}", "{threshold}"} {
				if strings.Contains(got, ph) {
					t.Errorf("占位符 %s 未被替换: %s", ph, got)
				}
			}
		})
	}
}

func TestRenderRateTemplate(t *testing.T) {
	t.Run("上调方向", func(t *testing.T) {
		got := renderRateTemplate("{entityName} {direction} {oldRate}→{newRate}", "vip", 1.0, 1.5)
		if !strings.Contains(got, "上调") {
			t.Errorf("倍率变大应为「上调」，实际: %s", got)
		}
		if !strings.Contains(got, "1.0000") || !strings.Contains(got, "1.5000") {
			t.Errorf("倍率应保留 4 位小数，实际: %s", got)
		}
	})

	t.Run("下调方向", func(t *testing.T) {
		got := renderRateTemplate("{direction}", "vip", 2.0, 0.5)
		if !strings.Contains(got, "下调") {
			t.Errorf("倍率变小应为「下调」，实际: %s", got)
		}
	})

	t.Run("空模板用默认且无残留占位符", func(t *testing.T) {
		got := renderRateTemplate("", "分组A", 1, 2)
		if !strings.Contains(got, "分组A") {
			t.Errorf("默认模板应含实体名，实际: %s", got)
		}
		for _, ph := range []string{"{entityName}", "{oldRate}", "{newRate}", "{direction}"} {
			if strings.Contains(got, ph) {
				t.Errorf("占位符 %s 未被替换: %s", ph, got)
			}
		}
	})
}
