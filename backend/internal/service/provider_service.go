package service

import (
	"context"
	"errors"
	"sort"
	"strings"

	"sub2api-account-monitor/internal/repository"
)

// ProviderService 供应商业务逻辑。
type ProviderService struct {
	repo     *repository.ProviderRepo
	linkRepo *repository.ProviderAccountRepo
	pg       *repository.PG
}

// NewProviderService 创建 ProviderService。
func NewProviderService(repo *repository.ProviderRepo,
	linkRepo *repository.ProviderAccountRepo, pg *repository.PG) *ProviderService {
	return &ProviderService{repo: repo, linkRepo: linkRepo, pg: pg}
}

// Count 返回供应商数量。
func (s *ProviderService) Count(ctx context.Context) int { return s.repo.Count(ctx) }

// Repo 暴露底层 repo（供 balance/probe service 复用）。
func (s *ProviderService) Repo() *repository.ProviderRepo { return s.repo }

// LinkRepo 暴露关联表 repo（供 handler 直接读写关联）。
func (s *ProviderService) LinkRepo() *repository.ProviderAccountRepo { return s.linkRepo }

// SubAccount 供应商子账号（本站视角，绝不外泄 key）。
type SubAccount struct {
	ID             int64    `json:"id"`
	Name           string   `json:"name"`
	Platform       string   `json:"platform"`
	Type           string   `json:"type"`
	Status         string   `json:"status"`
	Schedulable    bool     `json:"schedulable"`
	RateMultiplier float64  `json:"rate_multiplier"`
	Groups         []string `json:"groups"`
}

// AccountsOf 返回供应商显式关联的子账号（provider_accounts 为唯一真相）。
//
// 按 provider_id 而非 name：关联表用 id 关联，name 只是展示用。
// 关联表里有但远端已删的账号不出现在结果里 —— 悬垂 id 读时过滤，不做级联清理。
func (s *ProviderService) AccountsOf(ctx context.Context, providerID int64) ([]SubAccount, error) {
	links, err := s.linkRepo.ListByProvider(ctx, providerID)
	if err != nil {
		return nil, err
	}
	if len(links) == 0 {
		return []SubAccount{}, nil
	}
	accs, err := s.pg.ListActiveAccounts(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]repository.PGAccount, len(accs))
	for _, a := range accs {
		byID[a.ID] = a
	}
	groupMap, err := s.pg.AccountGroups(ctx)
	if err != nil {
		// 分组信息非关键，失败时降级为空
		groupMap = map[int64][]string{}
	}
	out := make([]SubAccount, 0, len(links))
	for _, l := range links {
		a, ok := byID[l.AccountID]
		if !ok {
			continue // 远端已删：不展示，也不自动清理关联（留痕便于排查）
		}
		out = append(out, SubAccount{
			ID:             a.ID,
			Name:           a.Name,
			Platform:       a.Platform,
			Type:           a.Type,
			Status:         a.Status,
			Schedulable:    a.Schedulable,
			RateMultiplier: a.RateMultiplier,
			Groups:         groupMap[a.ID],
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// AccountCountMap 返回 provider_id → 关联账号数。
//
// 只读本地关联表：原实现每次供应商列表请求都全表拉一遍远端 PG accounts
// （handler List 每次都调），远端查询由此降为 0 次。
// 代价是计数含「远端已删但关联未清」的悬垂 id，属可接受的少量高估。
func (s *ProviderService) AccountCountMap(ctx context.Context) (map[int64]int, error) {
	return s.linkRepo.CountByProvider(ctx)
}

// ScanPrefix 按账号名【】前缀给出的建站建议。
//
// 前缀已不再决定归属（provider_accounts 才是唯一真相），此处仅作为批量建站的
// 便利入口：历史账号名大多带前缀，据此可一次猜出站点清单并顺带写好关联。
type ScanPrefix struct {
	Prefix       string  `json:"prefix"`
	AccountCount int     `json:"account_count"`
	Exists       bool    `json:"exists"`      // 是否已录入
	AccountIDs   []int64 `json:"account_ids"` // 该前缀下的账号，导入时一并写关联
	// BaseURL 该前缀下账号连接的站点地址，作为导入表单的预填值。
	// 组内地址不唯一时按用户选择取任意一个（Go map 遍历顺序随机），
	// 故同一份数据两次扫描可能给出不同地址 —— URLCount > 1 时前端须提示用户核对。
	BaseURL string `json:"base_url"`
	// URLCount 该前缀下出现过的不同站点地址数（0 = 账号都没填地址）。
	URLCount int `json:"url_count"`
}

// ScanPrefixes 扫描 accounts.name 的【】前缀并去重。
func (s *ProviderService) ScanPrefixes(ctx context.Context) ([]ScanPrefix, error) {
	accs, err := s.pg.ListActiveAccounts(ctx)
	if err != nil {
		return nil, err
	}
	existing, err := s.repo.ListNames(ctx)
	if err != nil {
		return nil, err
	}
	existingLower := make(map[string]bool)
	for n := range existing {
		existingLower[strings.ToLower(n)] = true
	}

	countMap := make(map[string]int)           // lower prefix -> count
	displayMap := make(map[string]string)      // lower prefix -> 首次出现的原始大小写
	idMap := make(map[string][]int64)          // lower prefix -> 账号 id
	urlMap := make(map[string]map[string]bool) // lower prefix -> 归一化地址集合
	for _, a := range accs {
		if name, ok := ParseProviderName(a.Name); ok {
			lower := strings.ToLower(name)
			countMap[lower]++
			idMap[lower] = append(idMap[lower], a.ID)
			if _, seen := displayMap[lower]; !seen {
				displayMap[lower] = name
			}
			// 与「按站点地址」页签同一套归一化，两个入口给出的地址才对得上
			if u := NormalizeBaseURL(a.BaseURL); u != "" {
				if urlMap[lower] == nil {
					urlMap[lower] = map[string]bool{}
				}
				urlMap[lower][u] = true
			}
		}
	}

	out := make([]ScanPrefix, 0, len(countMap))
	for lower, cnt := range countMap {
		var pick string
		for u := range urlMap[lower] { // 任意取一个：调用方已知地址可能不唯一
			pick = u
			break
		}
		out = append(out, ScanPrefix{
			Prefix:       displayMap[lower],
			AccountCount: cnt,
			Exists:       existingLower[lower],
			AccountIDs:   idMap[lower],
			BaseURL:      pick,
			URLCount:     len(urlMap[lower]),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		// 未录入的排前面，再按账号数降序
		if out[i].Exists != out[j].Exists {
			return !out[i].Exists
		}
		return out[i].AccountCount > out[j].AccountCount
	})
	return out, nil
}

// ScanURLGroups 按账号 base_url 归组，供「关联账号」与批量建站使用。
//
// 与 ScanPrefixes 并列的另一种发现方式：前者依赖【】命名习惯，本方法只看
// 账号实际连的是哪个站，对没有命名约定的场景同样可用。
func (s *ProviderService) ScanURLGroups(ctx context.Context) ([]URLGroup, error) {
	accs, err := s.pg.ListActiveAccounts(ctx)
	if err != nil {
		return nil, err
	}
	links, err := s.linkRepo.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	nameByID, err := s.repo.NameByID(ctx)
	if err != nil {
		return nil, err
	}

	linkedTo := make(map[int64]string, len(links))
	for _, l := range links {
		linkedTo[l.AccountID] = nameByID[l.ProviderID]
	}

	// 已建站点按规范化 base_url 索引，让归组能标出「这个站已经建过了」
	providers, err := s.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	providerByURL := make(map[string]string, len(providers))
	for _, p := range providers {
		if u := NormalizeBaseURL(p.BaseURL); u != "" {
			providerByURL[u] = p.Name
		}
	}

	return GroupAccountsByURL(accs, linkedTo, providerByURL), nil
}

// ImportItem 一条导入项：站点名 + 要一并关联的账号。
type ImportItem struct {
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	// BalanceType 余额获取方式（sub2api | manual | none）。空串回退 none。
	// 仅对新建站点生效：已存在的站点不改其采集方式，那是用户在编辑页的决定。
	BalanceType string  `json:"balance_type"`
	AccountIDs  []int64 `json:"account_ids"`
}

// ImportResult 导入结果。
type ImportResult struct {
	Created []string `json:"created"`
	Skipped []string `json:"skipped"`
	Linked  int      `json:"linked"` // 写入的关联条数
	// CreatedIDs 新建站点的 id，供调用方排班（缺凭据的站点由 ListCollectable 兜底过滤）。
	CreatedIDs []int64 `json:"-"`
}

// Import 批量建站并顺带写入账号关联。
//
// 站点已存在时只跳过建站，关联照写：用户勾选的意图是「让这些账号归这个站」，
// 站点早就建过不该让关联一起被跳过。已存在但地址为空的站点会补上本次带来的
// 地址 —— 早期按前缀导入的站点全是空地址，让它们在补关联时顺带补齐，
// 而不是逼用户再去编辑页填一遍。
func (s *ProviderService) Import(ctx context.Context, items []ImportItem) (*ImportResult, error) {
	result := &ImportResult{Created: []string{}, Skipped: []string{}}

	// 账号名用于填关联表的冗余列（展示与排查用）。PG 不可用时留空，不影响关联本身。
	nameByID := map[int64]string{}
	if accs, err := s.pg.ListActiveAccounts(ctx); err == nil {
		for _, a := range accs {
			nameByID[a.ID] = a.Name
		}
	}

	var links []repository.ProviderAccount
	for _, it := range items {
		name := strings.TrimSpace(it.Name)
		if name == "" {
			continue
		}
		baseURL := strings.TrimRight(strings.TrimSpace(it.BaseURL), "/")
		balanceType := it.BalanceType
		if !importBalanceTypes[balanceType] {
			balanceType = "none"
		}
		p, err := s.repo.GetByName(ctx, name)
		switch {
		case err == nil:
			result.Skipped = append(result.Skipped, name)
			// 只补空地址，不覆盖已有值：用户可能特意填了带路径的地址
			if p.BaseURL == "" && baseURL != "" {
				if updated, uErr := s.repo.UpdateBaseURL(ctx, p.ID, baseURL); uErr == nil {
					p = updated
				}
			}
		case errors.Is(err, repository.ErrNotFound):
			p, err = s.repo.Create(ctx, repository.CreateParams{
				Name:        name,
				BalanceType: balanceType,
				BaseURL:     baseURL,
			})
			if err != nil {
				// 并发/大小写冲突时跳过
				result.Skipped = append(result.Skipped, name)
				continue
			}
			result.Created = append(result.Created, name)
			result.CreatedIDs = append(result.CreatedIDs, p.ID)
		default:
			return nil, err
		}
		for _, aid := range it.AccountIDs {
			links = append(links, repository.ProviderAccount{
				ProviderID:  p.ID,
				AccountID:   aid,
				AccountName: nameByID[aid],
			})
		}
	}
	if len(links) > 0 {
		if err := s.linkRepo.LinkMany(ctx, links); err != nil {
			return nil, err
		}
		result.Linked = len(links)
	}
	return result, nil
}

// importBalanceTypes 导入允许的余额获取方式（与 handler 的 validBalanceTypes 同集合）。
var importBalanceTypes = map[string]bool{"sub2api": true, "manual": true, "none": true}
