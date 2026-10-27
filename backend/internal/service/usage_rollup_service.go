package service

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"sub2api-account-monitor/internal/config"
	"sub2api-account-monitor/internal/repository"
)

// rollupMaxInitialDays 首次结算最多往回抢救的天数。
// sub2api 日志约保留一个月，这里留足余量；更早的日志本来也已不在。
const rollupMaxInitialDays = 60

// UsageRollupService 把 sub2api usage_logs 按天结算进本地 usage_daily。
//
// 当天数据还在变，不落汇总；每天凌晨结算「已经完整过去」的日子。结算进度
// （settled_through）同时是统计读路径的分界：之前读汇总，之后实时查。
type UsageRollupService struct {
	pg   *repository.PG
	repo *repository.UsageDailyRepo
	cfg  *config.Config
	// mu 防止启动补跑与定时任务并发结算同一批日子（结果幂等，但白跑一遍大聚合）
	mu sync.Mutex
}

// NewUsageRollupService 创建 UsageRollupService。
func NewUsageRollupService(pg *repository.PG, repo *repository.UsageDailyRepo, cfg *config.Config) *UsageRollupService {
	return &UsageRollupService{pg: pg, repo: repo, cfg: cfg}
}

// SettlePending 结算 settled_through 之后、今天之前的所有日子。
//
// 逐日聚合，每成功一天推进一次进度；某天失败即停，下次从这天接着结算。
// 不是「只结昨天」：任务漏跑、停机、失败过的日子都会在下一次自动补上。
func (s *UsageRollupService) SettlePending(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	loc := s.cfg.Location
	today := dayStart(time.Now().In(loc))
	from, err := s.firstPendingDay(ctx, today)
	if err != nil {
		return err
	}
	days := pendingDays(from, today)
	for i, day := range days {
		date := day.Format("2006-01-02")
		start, end := s.cfg.DayRange(day)
		rows, err := s.pg.AggregateUsageForRollup(ctx, s.cfg.Timezone, start, end)
		if err != nil {
			return fmt.Errorf("聚合 %s 用量失败: %w", date, err)
		}
		if err := s.repo.Upsert(ctx, rows); err != nil {
			return fmt.Errorf("写入 %s 日结失败: %w", date, err)
		}
		if err := s.repo.SetSettledThrough(ctx, date); err != nil {
			return fmt.Errorf("推进结算进度到 %s 失败: %w", date, err)
		}
		log.Printf("[usage-rollup] 已结算 %s（%d 行，%d/%d）", date, len(rows), i+1, len(days))
	}
	return nil
}

// firstPendingDay 返回第一个待结算日（站点时区 0 点）。
// 从未结算时从 usage_logs 最早那天开始（最多回看 rollupMaxInitialDays 天）；
// 日志为空时返回 today，即无事可做。
func (s *UsageRollupService) firstPendingDay(ctx context.Context, today time.Time) (time.Time, error) {
	through, err := s.repo.SettledThrough(ctx)
	if err != nil {
		return time.Time{}, fmt.Errorf("读取结算进度失败: %w", err)
	}
	if through != "" {
		t, err := time.ParseInLocation("2006-01-02", through, today.Location())
		if err != nil {
			return time.Time{}, fmt.Errorf("结算进度格式异常 %q: %w", through, err)
		}
		return t.AddDate(0, 0, 1), nil
	}
	earliest, ok, err := s.pg.EarliestUsageAt(ctx)
	if err != nil {
		return time.Time{}, fmt.Errorf("查询最早日志失败: %w", err)
	}
	if !ok {
		return today, nil
	}
	from := dayStart(earliest.In(today.Location()))
	if floor := today.AddDate(0, 0, -rollupMaxInitialDays); from.Before(floor) {
		from = floor
	}
	return from, nil
}

// pendingDays 列出 [from, today) 的每一天（均为所在时区 0 点）。用 AddDate 而非 +24h，夏令时也不会错位。
func pendingDays(from, today time.Time) []time.Time {
	var out []time.Time
	for d := from; d.Before(today); d = d.AddDate(0, 0, 1) {
		out = append(out, d)
	}
	return out
}

// dayStart 返回 t 所在时区当天 0 点。
func dayStart(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}
