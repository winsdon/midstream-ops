package service

import (
	"context"
	"log"
	"math"
	"strconv"

	"sub2api-account-monitor/internal/repository"
)

// 倍率比较 epsilon。
const rateEpsilon = 1e-9

// RateEntity 一个待 diff 的实体（平台中性）。
type RateEntity struct {
	ID   string // local 用数字 id 字符串；upstream 用分组名
	Name string
	Rate float64
	// Platform 上游分组所属平台（anthropic|openai|gemini|...），仅供前端分类展示。
	// 本站 local 实体与 new-api 上游均无此概念，留空串。
	Platform string
}

// RateService 倍率快照维护（变更驱动：insert-on-change + touch-on-same + mark-deleted）。
// 本站（local，PG 轮询）与上游站点（upstream，随 provider sync）复用同一套分拣逻辑。
type RateService struct {
	rateRepo *repository.RateRepo
	pg       *repository.PG

	// OnRateChanged 每轮检出变化后回调（倍率变更预警）；装配层注入。
	OnRateChanged func([]RateChangeEvent)
}

// NewRateService 创建 RateService。
func NewRateService(rateRepo *repository.RateRepo, pg *repository.PG) *RateService {
	return &RateService{rateRepo: rateRepo, pg: pg}
}

// PollOnce 轮询一次本站 groups/accounts 倍率并分拣落库。
func (s *RateService) PollOnce(ctx context.Context) {
	if !s.pg.Available() {
		return
	}
	groups, err := s.pg.ListGroupRates(ctx)
	if err != nil {
		log.Printf("[rate] 查询 groups 失败: %v", err)
		return
	}
	accounts, err := s.pg.ListAccountRates(ctx)
	if err != nil {
		log.Printf("[rate] 查询 accounts 失败: %v", err)
		return
	}

	var events []RateChangeEvent
	events = append(events, s.Reconcile(ctx, "local", 0, "group", toRateEntities(groups))...)
	events = append(events, s.Reconcile(ctx, "local", 0, "account", toRateEntities(accounts))...)

	if len(events) > 0 && s.OnRateChanged != nil {
		s.OnRateChanged(events)
	}
}

// toRateEntities PG 实体 → 中性实体。
func toRateEntities(list []repository.RateEntity) []RateEntity {
	out := make([]RateEntity, 0, len(list))
	for _, e := range list {
		out = append(out, RateEntity{ID: strconv.FormatInt(e.ID, 10), Name: e.Name, Rate: e.Rate})
	}
	return out
}

// Reconcile 三路分拣（transit-hub group_rate_snapshots 算法）：
//   - 倍率未变        → Touch：延长 last_seen_at（不插行）
//   - 变化 / 新实体 / 复活 → Insert：新行封存一次「真实变化」
//   - 本轮未出现       → MarkDeleted：标记消失
//
// 判定只看 Rate。Name 与 Platform 是随行描述，变了就地更新（Touch），
// 绝不插新行 —— 否则上游改个标签就会在历史里留下一条 1.0 → 1.0 的假变化。
//
// 返回本轮检出的变化事件（首次观察的新实体不算变化）。
func (s *RateService) Reconcile(ctx context.Context, scope string, providerID int64, entityType string, entities []RateEntity) []RateChangeEvent {
	current, err := s.rateRepo.CurrentRows(ctx, scope, providerID, entityType)
	if err != nil {
		log.Printf("[rate] 读取 %s/%s 当前状态失败: %v", scope, entityType, err)
		return nil
	}

	var events []RateChangeEvent
	seen := make(map[string]bool, len(entities))
	for _, e := range entities {
		key := entityType + ":" + e.ID
		seen[key] = true
		old, exists := current[key]

		switch {
		case !exists:
			// 新实体：首行仅建档，不算变化事件
			if err := s.rateRepo.Insert(ctx, scope, providerID, entityType, e.ID, e.Name, e.Rate, e.Platform); err != nil {
				log.Printf("[rate] 插入 %s/%s/%s 失败: %v", scope, entityType, e.ID, err)
			}
		case old.Deleted:
			// 复活：插新行（若倍率与消失前不同，也自然记为一次变化）
			if err := s.rateRepo.Insert(ctx, scope, providerID, entityType, e.ID, e.Name, e.Rate, e.Platform); err != nil {
				log.Printf("[rate] 复活 %s/%s/%s 失败: %v", scope, entityType, e.ID, err)
				continue
			}
			if math.Abs(old.Rate-e.Rate) > rateEpsilon {
				events = append(events, RateChangeEvent{EntityType: entityType, EntityID: e.ID, EntityName: e.Name, UpstreamGroup: e.ID, OldRate: old.Rate, NewRate: e.Rate})
			}
		case math.Abs(old.Rate-e.Rate) > rateEpsilon:
			// 变化：插新行，旧行自动封存为历史区间
			if err := s.rateRepo.Insert(ctx, scope, providerID, entityType, e.ID, e.Name, e.Rate, e.Platform); err != nil {
				log.Printf("[rate] 写入变化 %s/%s/%s 失败: %v", scope, entityType, e.ID, err)
				continue
			}
			log.Printf("[rate] %s %s %s 倍率变化: %.4f -> %.4f", scope, entityType, e.Name, old.Rate, e.Rate)
			events = append(events, RateChangeEvent{EntityType: entityType, EntityID: e.ID, EntityName: e.Name, UpstreamGroup: e.ID, OldRate: old.Rate, NewRate: e.Rate})
		default:
			// 未变：touch 延长确认时刻（顺带同步名称与平台）
			if err := s.rateRepo.Touch(ctx, old.ID, e.Name, e.Platform); err != nil {
				log.Printf("[rate] touch %s/%s/%s 失败: %v", scope, entityType, e.ID, err)
			}
		}
	}

	// 本轮未出现且未标删 → 标记消失
	for key, row := range current {
		if seen[key] || row.Deleted {
			continue
		}
		if err := s.rateRepo.MarkDeleted(ctx, row.ID); err != nil {
			log.Printf("[rate] 标记消失 %s/%s 失败: %v", scope, key, err)
		}
	}
	return events
}

// History 查询倍率变化历史。
func (s *RateService) History(ctx context.Context, f repository.SnapshotFilter) ([]*repository.RateSnapshot, int64, error) {
	return s.rateRepo.History(ctx, f)
}

// CurrentList 查询当前生效倍率列表（分组倍率页）。
func (s *RateService) CurrentList(ctx context.Context, scope string, providerID *int64, includeDeleted bool) ([]*repository.RateSnapshot, error) {
	return s.rateRepo.CurrentList(ctx, scope, providerID, includeDeleted)
}
