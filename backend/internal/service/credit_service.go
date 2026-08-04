package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"sub2api-account-monitor/internal/repository"
)

// 台账分录方向。
const (
	EntryTypeAdvance   = "advance"   // 垫付：运营方替客户充值，增加敞口
	EntryTypeRepayment = "repayment" // 回款：客户付款，减少敞口
)

// 客户状态。
const (
	CustomerStatusActive   = "active"
	CustomerStatusArchived = "archived"
)

// ErrInvalidInput 入参不合法（调用方应转 400）。
var ErrInvalidInput = errors.New("入参不合法")

// CreditAlertNotifier 授信告警下游（由 AlertService 实现）。
//
// 定义在使用方而非实现方，避免 service 包内产生循环依赖，也让测试可传 nil。
type CreditAlertNotifier interface {
	HandleCreditAlert(ev CreditAlertEvent)
}

// CreditService 授信台账编排。
//
// 职责边界：枚举与金额校验的单一收口点 + 写入后触发告警评估。
// 【本模块永不写上游】充值动作由人在 sub2api 后台手动执行，见 012_credit_kyc.sql 注释。
type CreditService struct {
	repo   *repository.CreditRepo
	alerts CreditAlertNotifier // 可为 nil（未配置通知时）
}

// NewCreditService 创建 CreditService。
func NewCreditService(repo *repository.CreditRepo, alerts CreditAlertNotifier) *CreditService {
	return &CreditService{repo: repo, alerts: alerts}
}

// roundAmount 金额归一到分（两位小数），消除前端浮点输入的尾数噪声。
func roundAmount(v float64) float64 { return math.Round(v*100) / 100 }

// validateEntryType 校验分录方向。
func validateEntryType(t string) error {
	if t != EntryTypeAdvance && t != EntryTypeRepayment {
		return fmt.Errorf("%w: entry_type 须为 advance 或 repayment", ErrInvalidInput)
	}
	return nil
}

// validateCustomerParams 校验客户参数。
func validateCustomerParams(p *repository.CustomerParams) error {
	// 手填兜底路径（线上用户列表不可用时）的尾随空格会造出「看起来是同一个人」
	// 的两条档案，UNIQUE 约束拦不住，必须在唯一入口处归一。
	// 刻意不做 ^\d+$ 格式校验：嵌入端 EnsureCustomer 的 userID 取自会话，
	// 历史数据未必是纯数字，加了会让老档案在编辑保存时全部 400。
	p.Sub2apiUserID = strings.TrimSpace(p.Sub2apiUserID)
	if p.Sub2apiUserID == "" {
		return fmt.Errorf("%w: sub2api_user_id 不能为空", ErrInvalidInput)
	}
	if p.Status == "" {
		p.Status = CustomerStatusActive
	}
	if p.Status != CustomerStatusActive && p.Status != CustomerStatusArchived {
		return fmt.Errorf("%w: status 须为 active 或 archived", ErrInvalidInput)
	}
	if p.CreditLimit < 0 {
		return fmt.Errorf("%w: credit_limit 不能为负", ErrInvalidInput)
	}
	p.CreditLimit = roundAmount(p.CreditLimit)
	return nil
}

// ---------- 客户 ----------

// ListCustomers 分页查询客户。
func (s *CreditService) ListCustomers(ctx context.Context, f repository.CustomerFilter) ([]*repository.Customer, int64, error) {
	return s.repo.ListCustomers(ctx, f)
}

// GetCustomer 查询单个客户。
func (s *CreditService) GetCustomer(ctx context.Context, id int64) (*repository.Customer, error) {
	return s.repo.GetCustomer(ctx, id)
}

// Summary 授信总览。
func (s *CreditService) Summary(ctx context.Context) (*repository.CreditSummary, error) {
	return s.repo.Summary(ctx)
}

// EnrolledUserIDs 已建档的 sub2api 用户 ID 集合（建档下拉去重用）。
func (s *CreditService) EnrolledUserIDs(ctx context.Context) (map[string]bool, error) {
	return s.repo.ListEnrolledUserIDs(ctx)
}

// CreateCustomer 新建客户。
func (s *CreditService) CreateCustomer(ctx context.Context, p repository.CustomerParams) (*repository.Customer, error) {
	if err := validateCustomerParams(&p); err != nil {
		return nil, err
	}
	return s.repo.CreateCustomer(ctx, p)
}

// UpdateCustomer 编辑客户。
//
// credit_limit 是告警比例的分母，改动后必须重新评估告警档位——这是五个写入点之一。
func (s *CreditService) UpdateCustomer(ctx context.Context, id int64, p repository.CustomerParams) (*repository.Customer, error) {
	if err := validateCustomerParams(&p); err != nil {
		return nil, err
	}
	c, err := s.repo.UpdateCustomer(ctx, id, p)
	if err != nil {
		return nil, err
	}
	s.evaluateAlert(ctx, c)
	return c, nil
}

// ArchiveCustomer 归档客户。
func (s *CreditService) ArchiveCustomer(ctx context.Context, id int64) error {
	return s.repo.ArchiveCustomer(ctx, id)
}

// EnsureCustomer 按 sub2api 用户 ID 取客户，不存在则惰性创建（嵌入页首次提交用）。
//
// userID 必须来自会话上下文，绝不可取自请求体或 URL。
func (s *CreditService) EnsureCustomer(ctx context.Context, userID, email string) (*repository.Customer, error) {
	if userID == "" {
		return nil, fmt.Errorf("%w: 缺少用户标识", ErrInvalidInput)
	}
	c, err := s.repo.GetCustomerBySub2apiID(ctx, userID)
	if err == nil {
		return c, nil
	}
	if !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}
	return s.repo.CreateCustomer(ctx, repository.CustomerParams{
		Sub2apiUserID: userID,
		Email:         email,
		Status:        CustomerStatusActive,
	})
}

// GetCustomerBySub2apiID 按 sub2api 用户 ID 查询（不创建）。
func (s *CreditService) GetCustomerBySub2apiID(ctx context.Context, userID string) (*repository.Customer, error) {
	return s.repo.GetCustomerBySub2apiID(ctx, userID)
}

// ---------- 台账 ----------

// AppendEntryInput 记一笔台账的入参。
type AppendEntryInput struct {
	CustomerID  int64
	EntryType   string
	Amount      float64
	OccurredAt  *time.Time // nil = 现在
	Note        string
	ExternalRef string
	Operator    string
}

// AppendEntry 记一笔垫付或回款。写入点 1、2。
func (s *CreditService) AppendEntry(ctx context.Context, in AppendEntryInput) (*repository.Customer, error) {
	if err := validateEntryType(in.EntryType); err != nil {
		return nil, err
	}
	amount := roundAmount(in.Amount)
	if amount <= 0 {
		return nil, fmt.Errorf("%w: amount 必须大于 0（方向由 entry_type 决定）", ErrInvalidInput)
	}
	occurred := time.Now()
	if in.OccurredAt != nil {
		occurred = *in.OccurredAt
	}
	c, err := s.repo.AppendEntry(ctx, repository.EntryParams{
		CustomerID:  in.CustomerID,
		EntryType:   in.EntryType,
		Amount:      amount,
		Currency:    "USD",
		OccurredAt:  occurred,
		Note:        in.Note,
		ExternalRef: in.ExternalRef,
		Operator:    in.Operator,
	})
	if err != nil {
		return nil, err
	}
	s.evaluateAlert(ctx, c)
	return c, nil
}

// ListEntries 分页查询某客户台账。
func (s *CreditService) ListEntries(ctx context.Context, customerID int64, page, pageSize int) ([]*repository.LedgerEntry, int64, error) {
	return s.repo.ListEntries(ctx, customerID, page, pageSize)
}

// ReverseEntry 冲正一条分录。写入点 3。
//
// 台账只追加不改删：冲正写一笔等额反向分录并用 reversed_of 指向原分录，
// 保留完整审计轨迹。同一分录只能冲正一次。
func (s *CreditService) ReverseEntry(ctx context.Context, entryID int64, operator string) (*repository.Customer, error) {
	orig, err := s.repo.GetEntry(ctx, entryID)
	if err != nil {
		return nil, err
	}
	if orig.ReversedOf != nil {
		return nil, fmt.Errorf("%w: 冲正分录不能再被冲正", ErrInvalidInput)
	}
	done, err := s.repo.HasReversal(ctx, entryID)
	if err != nil {
		return nil, err
	}
	if done {
		return nil, fmt.Errorf("%w: 该分录已冲正过", ErrInvalidInput)
	}

	reverseType := EntryTypeRepayment
	if orig.EntryType == EntryTypeRepayment {
		reverseType = EntryTypeAdvance
	}
	c, err := s.repo.AppendEntry(ctx, repository.EntryParams{
		CustomerID:  orig.CustomerID,
		EntryType:   reverseType,
		Amount:      orig.Amount,
		Currency:    orig.Currency,
		OccurredAt:  time.Now(),
		Note:        fmt.Sprintf("冲正 #%d", entryID),
		ExternalRef: orig.ExternalRef,
		Operator:    operator,
		ReversedOf:  &entryID,
	})
	if err != nil {
		return nil, err
	}
	s.evaluateAlert(ctx, c)
	return c, nil
}

// RecalcCustomer 重算单客户敞口（幂等）。写入点 4。
func (s *CreditService) RecalcCustomer(ctx context.Context, id int64) (*repository.Customer, error) {
	c, err := s.repo.RecalcCustomer(ctx, id)
	if err != nil {
		return nil, err
	}
	s.evaluateAlert(ctx, c)
	return c, nil
}

// RecalcAll 重算全部客户敞口（幂等）。写入点 5。
func (s *CreditService) RecalcAll(ctx context.Context) (int, error) {
	ids, err := s.repo.RecalcAll(ctx)
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if c, err := s.repo.GetCustomer(ctx, id); err == nil {
			s.evaluateAlert(ctx, c)
		}
	}
	return len(ids), nil
}

// ---------- 告警评估 ----------

// 告警档位阈值。
const (
	creditBandWarning  = 80  // 敞口达授信额度 80%
	creditBandOverflow = 100 // 敞口达/超授信额度
)

// CreditAlertEvent 一次授信告警（升档时发出）。
type CreditAlertEvent struct {
	CustomerID   int64
	CustomerName string // display_name，为空时回落 sub2api_user_id
	CreditLimit  float64
	Outstanding  float64
	Available    float64
	Band         int // 80 | 100
}

// creditBand 按敞口/额度比计算告警档位。未授信（limit ≤ 0）恒为 0。
func creditBand(outstanding, limit float64) int {
	if limit <= 0 {
		return 0
	}
	ratio := outstanding / limit
	switch {
	case ratio >= 1.0:
		return creditBandOverflow
	case ratio >= 0.8:
		return creditBandWarning
	default:
		return 0
	}
}

// evaluateAlert 边沿触发的档位评估：仅升档发通知，降档静默改写闩锁。
//
// 必须在每个改变「敞口或授信额度」的写入点之后调用，共五处：
// AppendEntry（垫付/回款）、ReverseEntry（冲正）、UpdateCustomer（改额度，分母变了）、
// RecalcCustomer、RecalcAll。漏一处就漏告警。
//
// 闩锁 alert_level 落库而非存内存：授信告警没有周期任务自愈，
// 内存闩锁重启即丢会导致重复告警（与 AlertService.lastAlert 的前提不同）。
func (s *CreditService) evaluateAlert(ctx context.Context, c *repository.Customer) {
	if c == nil {
		return
	}
	band := creditBand(c.Outstanding, c.CreditLimit)
	if band == c.AlertLevel {
		return
	}

	fire := band > c.AlertLevel
	var firedAt *time.Time
	if fire {
		now := time.Now()
		firedAt = &now
	}
	// 降档也要写：闩锁必须跟随实际档位下降，否则回款后再次冲高不会重新告警
	if err := s.repo.SetAlertLevel(ctx, c.ID, band, firedAt); err != nil {
		return
	}
	c.AlertLevel = band

	if !fire || s.alerts == nil {
		return
	}
	name := c.DisplayName
	if name == "" {
		name = c.Sub2apiUserID
	}
	s.alerts.HandleCreditAlert(CreditAlertEvent{
		CustomerID:   c.ID,
		CustomerName: name,
		CreditLimit:  c.CreditLimit,
		Outstanding:  c.Outstanding,
		Available:    c.Available(),
		Band:         band,
	})
}
