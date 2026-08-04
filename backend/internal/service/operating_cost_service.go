package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"sub2api-account-monitor/internal/config"
	"sub2api-account-monitor/internal/repository"
)

// 运营成本类别。固定枚举而非自由文本：自由文本会因错别字散成多类，
// 让「这个月买号花了多少」这个问题无法回答，也无法做 i18n。
const (
	OpCostCategoryAccount      = "account"      // 买账号
	OpCostCategorySubscription = "subscription" // 订阅费
	OpCostCategoryServer       = "server"       // 服务器
	OpCostCategoryOther        = "other"        // 其他
)

// ErrNotSelfOperated 目标站点不是自营站（调用方应转 400）。
//
// 非自营站的成本已由上游实扣完整表达，再记一笔运营成本必然重复计算、虚增成本。
// 前置硬校验而非仅靠前端隐藏入口：约束放在唯一写入口才防得住误操作。
var ErrNotSelfOperated = errors.New("仅自营站可记运营成本")

// OperatingCostService 自营站运营成本编排。
//
// 职责边界：类别/金额/日期校验的单一收口点 + 自营站前置校验。
type OperatingCostService struct {
	repo         *repository.OperatingCostRepo
	providerRepo *repository.ProviderRepo
	cfg          *config.Config
}

// NewOperatingCostService 创建 OperatingCostService。
func NewOperatingCostService(repo *repository.OperatingCostRepo,
	providerRepo *repository.ProviderRepo, cfg *config.Config) *OperatingCostService {
	return &OperatingCostService{repo: repo, providerRepo: providerRepo, cfg: cfg}
}

// validCategories 类别白名单。
var validCategories = map[string]bool{
	OpCostCategoryAccount:      true,
	OpCostCategorySubscription: true,
	OpCostCategoryServer:       true,
	OpCostCategoryOther:        true,
}

// today 返回配置时区的今天（YYYY-MM-DD）。
//
// 必须用 cfg.Location 而非 UTC：occurred_on 要与 upstream_key_costs.usage_date
// 同口径（后者由 cfg.Location 决定），否则跨日边界上两笔成本会落到不同日子。
func (s *OperatingCostService) today() string {
	return time.Now().In(s.cfg.Location).Format("2006-01-02")
}

// validateParams 校验并归一入参（唯一收口点）。
func (s *OperatingCostService) validateParams(p *repository.OperatingCostParams) error {
	if p.Category == "" {
		p.Category = OpCostCategoryOther
	}
	if !validCategories[p.Category] {
		return fmt.Errorf("%w: category 须为 account / subscription / server / other", ErrInvalidInput)
	}
	// 金额归一到分，消除前端浮点输入的尾数噪声（复用 credit_service 的 roundAmount）
	p.Amount = roundAmount(p.Amount)
	if p.Amount <= 0 {
		return fmt.Errorf("%w: amount 须大于 0", ErrInvalidInput)
	}

	p.OccurredOn = strings.TrimSpace(p.OccurredOn)
	if p.OccurredOn == "" {
		p.OccurredOn = s.today()
	}
	// 严格校验格式：occurred_on 参与字符串比较的区间查询，
	// 非法格式会让该行永远落在区间外而静默失踪
	if _, err := time.Parse("2006-01-02", p.OccurredOn); err != nil {
		return fmt.Errorf("%w: occurred_on 须为 YYYY-MM-DD 格式", ErrInvalidInput)
	}
	p.Note = strings.TrimSpace(p.Note)
	return nil
}

// requireSelfOperated 校验站点存在且为自营站。
func (s *OperatingCostService) requireSelfOperated(ctx context.Context, providerID int64) error {
	p, err := s.providerRepo.GetByID(ctx, providerID)
	if err != nil {
		return err // ErrNotFound 由 handler 转 404
	}
	if !p.SelfOperated {
		return ErrNotSelfOperated
	}
	return nil
}

// Create 记一笔运营成本。
func (s *OperatingCostService) Create(ctx context.Context, p repository.OperatingCostParams) (*repository.OperatingCost, error) {
	if err := s.requireSelfOperated(ctx, p.ProviderID); err != nil {
		return nil, err
	}
	if err := s.validateParams(&p); err != nil {
		return nil, err
	}
	return s.repo.Create(ctx, p)
}

// List 返回某站点区间内的明细与合计。
//
// 不校验自营：取消自营标记后仍要能看到并清理历史记录，否则数据会成为无法访问的孤儿。
func (s *OperatingCostService) List(ctx context.Context, providerID int64, startDate, endDate string) ([]repository.OperatingCost, float64, error) {
	items, err := s.repo.ListByProvider(ctx, providerID, startDate, endDate)
	if err != nil {
		return nil, 0, err
	}
	// 合计从已取回的明细算出，避免为同一区间再打一次库
	var total float64
	for _, it := range items {
		total += it.Amount
	}
	return items, roundAmount(total), nil
}

// Delete 删除一笔。
func (s *OperatingCostService) Delete(ctx context.Context, id int64) error {
	return s.repo.Delete(ctx, id)
}
