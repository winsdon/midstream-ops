package service

import (
	"context"
	"log"
	"sync"
	"time"

	"sub2api-account-monitor/internal/repository"
)

// syncTaskTimeout 单个供应商一次同步的超时（含可能的历史回补）。
const syncTaskTimeout = 15 * time.Minute

// SyncScheduler 供应商同步调度器：每供应商一个一次性 timer，同步完成后
// 重排下一次 —— 天然错峰、防重入、防任务堆积（移植 transit-hub 模式）。
//
// 与 cron 全局任务的分工：balance+cost 已合并进本调度器；rate 轮询、
// probe、每日清理仍留在 cron（见 scheduler.go）。
type SyncScheduler struct {
	providerRepo  *repository.ProviderRepo
	collectorRepo *repository.CollectorStateRepo
	syncSvc       *ProviderSyncService

	mu       sync.Mutex
	timers   map[int64]*time.Timer
	interval time.Duration
	enabled  bool
	stopped  bool
}

// NewSyncScheduler 创建调度器（未启动）。
func NewSyncScheduler(
	providerRepo *repository.ProviderRepo,
	collectorRepo *repository.CollectorStateRepo,
	syncSvc *ProviderSyncService,
) *SyncScheduler {
	return &SyncScheduler{
		providerRepo:  providerRepo,
		collectorRepo: collectorRepo,
		syncSvc:       syncSvc,
		timers:        make(map[int64]*time.Timer),
	}
}

// Configure 应用刷新配置并重建全部定时器（启动与设置热更新共用）。
func (s *SyncScheduler) Configure(enabled bool, interval time.Duration) {
	if interval < time.Minute {
		interval = time.Minute
	}
	s.mu.Lock()
	s.enabled = enabled
	s.interval = interval
	s.clearAllLocked()
	s.mu.Unlock()

	if !enabled {
		log.Printf("[sync-scheduler] 自动刷新已关闭")
		return
	}
	s.rebuild()
	log.Printf("[sync-scheduler] 自动刷新已开启 interval=%s", interval)
}

// rebuild 为所有可采集供应商排班（带索引错峰：i×interval/n 的初始偏移）。
func (s *SyncScheduler) rebuild() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	providers, err := s.providerRepo.ListCollectable(ctx)
	if err != nil {
		log.Printf("[sync-scheduler] 查询供应商失败: %v", err)
		return
	}
	n := len(providers)
	if n == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, p := range providers {
		// 初始错峰：把 n 个供应商均匀铺在一个周期内，避免同刻齐发打上游
		offset := s.interval * time.Duration(i) / time.Duration(n)
		s.scheduleLocked(p.ID, offset)
	}
}

// scheduleLocked 为供应商排一次性 timer（须持有 s.mu）。
func (s *SyncScheduler) scheduleLocked(providerID int64, delay time.Duration) {
	if s.stopped || !s.enabled {
		return
	}
	if t, ok := s.timers[providerID]; ok {
		t.Stop()
	}
	s.timers[providerID] = time.AfterFunc(delay, func() { s.run(providerID) })
}

// run 执行一次同步并重排下一次。
func (s *SyncScheduler) run(providerID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), syncTaskTimeout)
	defer cancel()

	delay := s.currentInterval()

	// 退避检查：collector_state 记录的解禁时刻未到则跳过本轮，直接排到解禁点
	st, err := s.collectorRepo.Get(ctx, providerID, taskSync)
	if err == nil && st.NextEligibleAt != nil {
		if wait := time.Until(*st.NextEligibleAt); wait > 0 {
			if wait > delay {
				delay = wait
			}
			s.reschedule(providerID, delay)
			return
		}
	}

	if _, err := s.syncSvc.SyncOne(ctx, providerID, false, false); err != nil {
		// 供应商被删除或类型改变：不再排班
		if err == repository.ErrNotFound {
			s.cancel(providerID)
			return
		}
		log.Printf("[sync-scheduler] 供应商 %d 同步失败: %v", providerID, err)
	}
	s.reschedule(providerID, delay)
}

// reschedule 同步收尾后排下一次。
func (s *SyncScheduler) reschedule(providerID int64, delay time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scheduleLocked(providerID, delay)
}

// currentInterval 读取当前刷新间隔。
func (s *SyncScheduler) currentInterval() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.interval
}

// OnProviderChanged 供应商新增/编辑后调用：立即为其排班（下个周期生效）。
func (s *SyncScheduler) OnProviderChanged(providerID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.enabled {
		return
	}
	s.scheduleLocked(providerID, s.interval)
}

// OnManualSync 手动同步完成后调用：重置该供应商的自动计时（transit-hub 同款语义）。
func (s *SyncScheduler) OnManualSync(providerID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.enabled {
		return
	}
	s.scheduleLocked(providerID, s.interval)
}

// OnManualSyncAll 全量手动刷新完成后调用：重建全部排班。
//
// 复用 rebuild 而非逐个 OnManualSync，是为了保留错峰偏移——刚全量刷完的站点若
// 统一按 interval 重排，下一轮会同刻齐发，正是 rebuild 要避免的形态。
func (s *SyncScheduler) OnManualSyncAll() {
	s.mu.Lock()
	enabled := s.enabled && !s.stopped
	s.mu.Unlock()
	if !enabled {
		return
	}
	s.rebuild()
}

// cancel 取消某供应商的排班（删除供应商时调用）。
func (s *SyncScheduler) cancel(providerID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.timers[providerID]; ok {
		t.Stop()
		delete(s.timers, providerID)
	}
}

// OnProviderDeleted 供应商删除后调用。
func (s *SyncScheduler) OnProviderDeleted(providerID int64) { s.cancel(providerID) }

// clearAllLocked 停掉全部 timer（须持有 s.mu）。
func (s *SyncScheduler) clearAllLocked() {
	for id, t := range s.timers {
		t.Stop()
		delete(s.timers, id)
	}
}

// Stop 停止调度器（进程退出时调用；运行中的同步由其自身 ctx 超时收尾）。
func (s *SyncScheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
	s.clearAllLocked()
}
