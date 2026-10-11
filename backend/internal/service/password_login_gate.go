package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"sub2api-account-monitor/internal/repository"
)

const (
	// passwordLoginGap 同一站点两次密码登录的最短间隔。
	// 上游登录接口按分钟或 20 分钟窗口限流，自动采集不能每个周期都打。
	passwordLoginGap = 30 * time.Minute
	// passwordLoginHostGap 同一主机上不同账号的密码登录间隔。限流按出口 IP 计数。
	passwordLoginHostGap = time.Minute
)

type manualSyncKey struct{}

func withManualUpstreamSync(ctx context.Context) context.Context {
	return context.WithValue(ctx, manualSyncKey{}, true)
}

func manualUpstreamSync(ctx context.Context) bool {
	v, _ := ctx.Value(manualSyncKey{}).(bool)
	return v
}

// passwordLoginGate 进程内的密码登录节流。手动刷新不走这里，方便改完凭据立刻重试。
type passwordLoginGate struct {
	mu     sync.Mutex
	byHost map[string]time.Time
	byID   map[int64]time.Time
}

func (g *passwordLoginGate) take(ctx context.Context, p *repository.Provider) error {
	if manualUpstreamSync(ctx) {
		return nil
	}
	host := upstreamHost(p.BaseURL)
	now := time.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.byID == nil {
		g.byID = map[int64]time.Time{}
		g.byHost = map[string]time.Time{}
	}
	if last, ok := g.byID[p.ID]; ok && now.Sub(last) < passwordLoginGap {
		return fmt.Errorf("%w: 该站点 30 分钟内已尝试过密码登录", ErrRateLimited)
	}
	if last, ok := g.byHost[host]; ok && now.Sub(last) < passwordLoginHostGap {
		return fmt.Errorf("%w: 同一上游主机密码登录间隔过短", ErrRateLimited)
	}
	g.byID[p.ID] = now
	g.byHost[host] = now
	return nil
}
