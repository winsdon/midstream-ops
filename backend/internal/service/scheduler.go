package service

import (
	"context"
	"log"
	"time"

	"sub2api-account-monitor/internal/config"
	"sub2api-account-monitor/internal/repository"

	"github.com/robfig/cron/v3"
)

// Scheduler 全局定时任务调度器（rate 轮询 / probe / 每日清理）。
// 供应商 sync（余额+成本）由 SyncScheduler 按 per-provider timer 调度，不在此处。
type Scheduler struct {
	cron        *cron.Cron
	cfg         *config.Config
	pg          *repository.PG
	rateSvc     *RateService
	probeSvc    *ProbeService
	costSvc     *CostSyncService
	balanceRepo *repository.BalanceRepo
	rateRepo    *repository.RateRepo
	probeRepo   *repository.ProbeRepo
	healthRepo  *repository.HealthRepo

	// mediaSvc 生图任务清理。可选功能，未启用时为 nil。
	//
	// 用 setter 注入而非构造参数：NewScheduler 已有 9 个参数，再加一个可空依赖
	// 会让「必需」与「可选」在签名里混为一谈。
	mediaSvc  *MediaService
	creditSvc *CreditService
	// detectSvc 渠道检测历史清理。同为可选依赖，未注入时该清理项跳过。
	detectSvc *ModelDetectService
}

// SetModelDetectService 注入渠道检测服务，使每日清理覆盖 model_detections。
func (s *Scheduler) SetModelDetectService(svc *ModelDetectService) {
	s.detectSvc = svc
}

// SetMediaService 注入生图服务，使每日清理覆盖 media_tasks。
// 未调用时该清理项自动跳过。
func (s *Scheduler) SetMediaService(svc *MediaService) {
	s.mediaSvc = svc
}

// SetCreditService 注入授信服务，用于周期扫描客户线上余额。
func (s *Scheduler) SetCreditService(svc *CreditService) {
	s.creditSvc = svc
}

// NewScheduler 创建调度器。
func NewScheduler(
	cfg *config.Config,
	pg *repository.PG,
	rateSvc *RateService,
	probeSvc *ProbeService,
	costSvc *CostSyncService,
	balanceRepo *repository.BalanceRepo,
	rateRepo *repository.RateRepo,
	probeRepo *repository.ProbeRepo,
	healthRepo *repository.HealthRepo,
) *Scheduler {
	// 带跳过仍在运行、panic 恢复
	c := cron.New(
		cron.WithChain(
			cron.SkipIfStillRunning(cron.DefaultLogger),
			cron.Recover(cron.DefaultLogger),
		),
	)
	return &Scheduler{
		cron:        c,
		cfg:         cfg,
		pg:          pg,
		rateSvc:     rateSvc,
		probeSvc:    probeSvc,
		costSvc:     costSvc,
		balanceRepo: balanceRepo,
		rateRepo:    rateRepo,
		probeRepo:   probeRepo,
		healthRepo:  healthRepo,
	}
}

// Start 注册并启动所有任务。
func (s *Scheduler) Start() error {
	// 主动探测
	probeEvery := everyMinutes(s.cfg.Probe.IntervalMinutes, 15)
	if _, err := s.cron.AddFunc(probeEvery, func() {
		s.withPG("probe", func(ctx context.Context) { s.probeSvc.RunAllScheduled(ctx) })
	}); err != nil {
		return err
	}

	// 倍率轮询
	rateEvery := everyMinutes(s.cfg.Rates.IntervalMinutes, 5)
	if _, err := s.cron.AddFunc(rateEvery, func() {
		s.withPG("rate", func(ctx context.Context) { s.rateSvc.PollOnce(ctx) })
	}); err != nil {
		return err
	}

	// 每日清理 03:30（robfig/cron 标准 5 字段：分 时 日 月 周）
	if _, err := s.cron.AddFunc("30 3 * * *", s.cleanup); err != nil {
		return err
	}

	if s.creditSvc != nil {
		if _, err := s.cron.AddFunc("@every 5m", func() {
			s.withPG("credit-balance", func(ctx context.Context) { s.creditSvc.WatchUserBalances(ctx) })
		}); err != nil {
			return err
		}
	}

	s.cron.Start()
	log.Printf("[scheduler] 已启动 probe=%s rate=%s credit-balance=5m cleanup=03:30（供应商 sync 由 SyncScheduler 调度）",
		probeEvery, rateEvery)
	return nil
}

// withPG 任务包装：PG 不可用时跳过。
func (s *Scheduler) withPG(name string, fn func(ctx context.Context)) {
	s.withPGTimeout(name, 2*time.Minute, fn)
}

// withPGTimeout 同 withPG，可指定超时。
func (s *Scheduler) withPGTimeout(name string, timeout time.Duration, fn func(ctx context.Context)) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if !s.pg.Available() {
		// 尝试重连
		_ = s.pg.Ping(ctx)
		if !s.pg.Available() {
			log.Printf("[scheduler] %s 跳过：PG 不可用", name)
			return
		}
	}
	fn(ctx)
}

// cleanup 按 retention_days 清理历史数据。
func (s *Scheduler) cleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	now := time.Now()

	s.costSvc.Cleanup(ctx)

	if d := s.cfg.Balance.RetentionDays; d > 0 {
		if n, err := s.balanceRepo.DeleteOlderThan(ctx, now.AddDate(0, 0, -d)); err == nil && n > 0 {
			log.Printf("[cleanup] balance_snapshots 删除 %d 行", n)
		}
	}
	if d := s.cfg.Probe.RetentionDays; d > 0 {
		if n, err := s.probeRepo.DeleteOlderThan(ctx, now.AddDate(0, 0, -d)); err == nil && n > 0 {
			log.Printf("[cleanup] probe_results 删除 %d 行", n)
		}
	}
	if d := s.cfg.Rates.RetentionDays; d > 0 {
		if n, err := s.rateRepo.DeleteOlderThan(ctx, now.AddDate(0, 0, -d)); err == nil && n > 0 {
			log.Printf("[cleanup] rate_snapshots 删除 %d 行", n)
		}
	}
	// 健康事件与探测预算：随 probe 保留期清理
	if d := s.cfg.Probe.RetentionDays; d > 0 {
		if n, err := s.healthRepo.DeleteEventsOlderThan(ctx, now.AddDate(0, 0, -d)); err == nil && n > 0 {
			log.Printf("[cleanup] health_events 删除 %d 行", n)
		}
		beforeDay := now.In(s.cfg.Location).AddDate(0, 0, -7).Format("2006-01-02")
		_ = s.healthRepo.CleanupBudget(ctx, beforeDay)
	}
	// 生图 / 生视频任务记录（功能未启用时 mediaSvc 为 nil）
	if s.mediaSvc != nil {
		s.mediaSvc.Cleanup(ctx, s.cfg.Media.TaskRetentionDays)
	}
	// 渠道检测历史：随 probe 保留期清理（同属「探测类留痕」，没必要多一个配置项）
	if s.detectSvc != nil {
		s.detectSvc.Cleanup(ctx, s.cfg.Probe.RetentionDays)
	}
}

// Stop 停止调度器（等待运行中的任务完成）。
func (s *Scheduler) Stop() {
	ctx := s.cron.Stop()
	<-ctx.Done()
	log.Println("[scheduler] 已停止")
}

// everyMinutes 生成 @every Nm 表达式，兜底默认值。
func everyMinutes(minutes, def int) string {
	if minutes <= 0 {
		minutes = def
	}
	if minutes < 1 {
		minutes = 1
	}
	return "@every " + itoaMin(minutes)
}

func itoaMin(n int) string {
	if n < 60 {
		return itoaInt(n) + "m"
	}
	h := n / 60
	m := n % 60
	if m == 0 {
		return itoaInt(h) + "h"
	}
	return itoaInt(h) + "h" + itoaInt(m) + "m"
}

func itoaInt(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
