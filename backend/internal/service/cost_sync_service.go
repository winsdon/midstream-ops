package service

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"sub2api-account-monitor/internal/config"
	"sub2api-account-monitor/internal/pkg/keyidentity"
	"sub2api-account-monitor/internal/repository"
)

// CostSyncService 同步上游供应商的真实成本（per-key 实扣）到本地库。
//
// 口径：上游 actual_cost = 倍率折后实扣，即我们真正付给供应商的金额。
// 本站 usage_logs 的 total_cost × account_rate_multiplier 只是本站视角的估算，
// 在账号倍率未维护时会等于官价，与真实支出可差一个数量级，故仅作对照。
type CostSyncService struct {
	providerRepo *repository.ProviderRepo
	costRepo     *repository.UpstreamCostRepo
	pg           *repository.PG
	client       *Sub2apiClient
	newapiClient *NewAPIClient
	tokens       *providerTokenManager
	cfg          *config.Config
}

// NewCostSyncService 创建 CostSyncService。
func NewCostSyncService(
	providerRepo *repository.ProviderRepo,
	costRepo *repository.UpstreamCostRepo,
	pg *repository.PG,
	cfg *config.Config,
) *CostSyncService {
	timeout := time.Duration(cfg.Cost.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	client := NewSub2apiClient(timeout)
	newapiClient := NewNewAPIClient(timeout)
	return &CostSyncService{
		providerRepo: providerRepo,
		costRepo:     costRepo,
		pg:           pg,
		client:       client,
		newapiClient: newapiClient,
		tokens:       newTokenManager(providerRepo, client, newapiClient),
		cfg:          cfg,
	}
}

// backfillDays 首次同步时回补的历史天数（上游逐日接口上限 90）。
const backfillDays = 90

// SyncOneByID 同步指定供应商（手动触发用）。
func (s *CostSyncService) SyncOneByID(ctx context.Context, providerID int64, backfill bool) error {
	p, err := s.providerRepo.GetByID(ctx, providerID)
	if err != nil {
		return err
	}
	if p.BalanceType != "sub2api" {
		return fmt.Errorf("供应商 %s 的余额获取方式为 %s，无法同步上游成本", p.Name, p.BalanceType)
	}
	fingerprints, err := s.accountFingerprints(ctx)
	if err != nil {
		return err
	}
	return s.SyncOne(ctx, p, fingerprints, backfill)
}

// SyncOne 同步单个供应商：拉 key 列表 → 匹配账号 → 拉今日实扣 → 落库。
// backfill=true 时额外拉每个 key 的逐日历史。
func (s *CostSyncService) SyncOne(
	ctx context.Context,
	p *repository.Provider,
	fingerprints map[string]repository.AccountKeyFingerprint,
	backfill bool,
) error {
	state := repository.CostSyncState{ProviderID: p.ID}

	keys, usage, err := s.fetchKeysAndUsage(ctx, p)
	if err != nil {
		msg := truncate(err.Error(), 500)
		state.LastError = &msg
		_ = s.costRepo.SaveSyncState(ctx, state)
		return err
	}

	mappings := make([]repository.UpstreamKeyMapping, 0, len(keys))
	costs := make([]repository.UpstreamKeyCost, 0, len(keys))
	today := time.Now().In(s.cfg.Location).Format("2006-01-02")

	var matched int64
	for _, k := range keys {
		m := repository.UpstreamKeyMapping{
			ProviderID:    p.ID,
			UpstreamKeyID: k.ID,
			KeyName:       k.Name,
			Status:        k.Status,
		}
		if k.Group != nil {
			m.GroupName = strings.TrimSpace(k.Group.Name)
			if p.Platform != "new-api" {
				rate := k.Group.RateMultiplier
				m.RateMultiplier = &rate
			}
		}
		// 按 api_key 明文指纹匹配本站账号；明文不落库、不外传
		var fp string
		if k.Key != "" {
			fp = keyidentity.Fingerprint(k.Key)
			m.KeyFingerprint = fp
		}
		groupName := ""
		if k.Group != nil {
			groupName = k.Group.Name
		}
		if acc, ok := matchCostAccount(fp, k.Name, groupName, fingerprints); ok {
			m.AccountID = &acc.AccountID
			m.AccountName = acc.AccountName
			matched++
		}
		mappings = append(mappings, m)

		if u, ok := usage[k.ID]; ok {
			costs = append(costs, repository.UpstreamKeyCost{
				ProviderID:    p.ID,
				UpstreamKeyID: k.ID,
				KeyName:       k.Name,
				AccountID:     m.AccountID,
				UsageDate:     today,
				ActualCost:    u.TodayActualCost,
				Requests:      u.Requests,
			})
		}
	}

	if err := s.costRepo.UpsertMappings(ctx, mappings); err != nil {
		return fmt.Errorf("写入 key 映射失败: %w", err)
	}
	if err := s.costRepo.UpsertCosts(ctx, costs); err != nil {
		return fmt.Errorf("写入今日成本失败: %w", err)
	}

	now := time.Now()
	state.LastSyncedAt = &now
	state.KeysTotal = int64(len(keys))
	state.KeysMatched = matched

	if backfill {
		if err := s.backfillHistory(ctx, p, keys, mappings); err != nil {
			// 回补失败不影响今日成本：记录告警，下轮重试
			log.Printf("[cost-sync] 供应商 %s 历史回补失败（今日成本已写入）: %v", p.Name, err)
		} else {
			state.BackfilledAt = &now
			log.Printf("[cost-sync] 供应商 %s 历史回补完成（%d 天 × %d keys）", p.Name, backfillDays, len(keys))
		}
	}
	return s.costRepo.SaveSyncState(ctx, state)
}

// backfillHistory 逐 key 拉取历史逐日用量并落库（首次同步用）。
func (s *CostSyncService) backfillHistory(
	ctx context.Context,
	p *repository.Provider,
	keys []ProviderAPIKey,
	mappings []repository.UpstreamKeyMapping,
) error {
	if p.Platform == "new-api" {
		return s.backfillNewAPIHistory(ctx, p, keys, mappings)
	}
	accByKey := make(map[int64]*int64, len(mappings))
	for _, m := range mappings {
		accByKey[m.UpstreamKeyID] = m.AccountID
	}

	sess, err := s.tokens.ensure(ctx, p)
	if err != nil {
		return err
	}

	var firstErr error
	for _, k := range keys {
		items, err := s.client.GetAPIKeyDailyUsage(ctx, p.BaseURL, sess.AccessToken, k.ID, backfillDays)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		costs := make([]repository.UpstreamKeyCost, 0, len(items))
		for _, it := range items {
			costs = append(costs, repository.UpstreamKeyCost{
				ProviderID:    p.ID,
				UpstreamKeyID: k.ID,
				KeyName:       k.Name,
				AccountID:     accByKey[k.ID],
				UsageDate:     it.Date,
				ActualCost:    it.ActualCost,
				OfficialCost:  it.Cost,
				Requests:      it.Requests,
			})
		}
		if err := s.costRepo.UpsertCosts(ctx, costs); err != nil {
			return err
		}
	}
	return firstErr
}

// backfillNewAPIHistory 按本地日界逐日查询最近 90 天的 token 用量。
func (s *CostSyncService) backfillNewAPIHistory(
	ctx context.Context,
	p *repository.Provider,
	keys []ProviderAPIKey,
	mappings []repository.UpstreamKeyMapping,
) error {
	sess, err := s.tokens.ensure(ctx, p)
	if err != nil {
		return err
	}
	quotaPerUnit := s.newAPIQuotaPerUnit(ctx, p)
	tokens := make([]NewAPIToken, 0, len(keys))
	accByKey := make(map[int64]*int64, len(mappings))
	for _, m := range mappings {
		accByKey[m.UpstreamKeyID] = m.AccountID
	}
	for _, key := range keys {
		group := ""
		if key.Group != nil {
			group = key.Group.Name
		}
		tokens = append(tokens, NewAPIToken{ID: key.ID, Name: key.Name, Group: group})
	}

	now := time.Now().In(s.cfg.Location)
	for offset := backfillDays - 1; offset >= 0; offset-- {
		day := now.AddDate(0, 0, -offset)
		start, end := s.cfg.DayRange(day)
		usage, usageErr := s.newapiClient.GetTokensUsage(ctx, p.BaseURL, sess.NewAPI, tokens, start, end, quotaPerUnit)
		if IsUnauthorized(usageErr) {
			sess, err = s.tokens.refresh(ctx, p)
			if err != nil {
				return err
			}
			usage, usageErr = s.newapiClient.GetTokensUsage(ctx, p.BaseURL, sess.NewAPI, tokens, start, end, quotaPerUnit)
		}
		if usageErr != nil {
			return usageErr
		}
		date := day.Format("2006-01-02")
		costs := make([]repository.UpstreamKeyCost, 0, len(keys))
		for _, key := range keys {
			u := usage[key.ID]
			costs = append(costs, repository.UpstreamKeyCost{
				ProviderID:    p.ID,
				UpstreamKeyID: key.ID,
				KeyName:       key.Name,
				AccountID:     accByKey[key.ID],
				UsageDate:     date,
				ActualCost:    u.TodayActualCost,
				Requests:      u.Requests,
			})
		}
		if err := s.costRepo.UpsertCosts(ctx, costs); err != nil {
			return err
		}
	}
	return nil
}

// fetchKeys 只拉上游 key 列表（分组归集用，不打用量接口）。
func (s *CostSyncService) fetchKeys(ctx context.Context, p *repository.Provider) ([]ProviderAPIKey, error) {
	if p.Platform == "new-api" {
		keys, _, err := s.fetchNewAPIKeysAndUsage(ctx, p)
		return keys, err
	}
	sess, err := s.tokens.ensure(ctx, p)
	if err != nil {
		return nil, err
	}
	keys, err := s.client.GetAPIKeys(ctx, p.BaseURL, sess.AccessToken)
	if err != nil {
		if !IsUnauthorized(err) {
			return nil, err
		}
		if sess, err = s.tokens.refresh(ctx, p); err != nil {
			return nil, err
		}
		if keys, err = s.client.GetAPIKeys(ctx, p.BaseURL, sess.AccessToken); err != nil {
			return nil, err
		}
	}
	return keys, nil
}

// fetchKeysAndUsage 拉取 key 列表与今日用量，含一次 401 重登。
func (s *CostSyncService) fetchKeysAndUsage(ctx context.Context, p *repository.Provider) ([]ProviderAPIKey, map[int64]APIKeyUsage, error) {
	if p.Platform == "new-api" {
		return s.fetchNewAPIKeysAndUsage(ctx, p)
	}
	keys, err := s.fetchKeys(ctx, p)
	if err != nil {
		return nil, nil, err
	}
	if len(keys) == 0 {
		return keys, map[int64]APIKeyUsage{}, nil
	}
	sess, err := s.tokens.ensure(ctx, p)
	if err != nil {
		return nil, nil, err
	}

	ids := make([]int64, 0, len(keys))
	for _, k := range keys {
		ids = append(ids, k.ID)
	}
	usage, err := s.client.GetAPIKeysUsage(ctx, p.BaseURL, sess.AccessToken, ids)
	if err != nil {
		return nil, nil, err
	}
	return keys, usage, nil
}

func (s *CostSyncService) fetchNewAPIKeysAndUsage(ctx context.Context, p *repository.Provider) ([]ProviderAPIKey, map[int64]APIKeyUsage, error) {
	sess, err := s.tokens.ensure(ctx, p)
	if err != nil {
		return nil, nil, err
	}
	quotaPerUnit := s.newAPIQuotaPerUnit(ctx, p)
	start, end := s.cfg.TodayRange()

	fetch := func(auth NewAPIAuth) ([]ProviderAPIKey, map[int64]APIKeyUsage, error) {
		tokens, err := s.newapiClient.ListTokens(ctx, p.BaseURL, auth)
		if err != nil {
			return nil, nil, err
		}
		keys := make([]ProviderAPIKey, 0, len(tokens))
		for i := range tokens {
			key, err := s.newapiClient.GetTokenKey(ctx, p.BaseURL, auth, tokens[i].ID)
			if err != nil {
				return nil, nil, err
			}
			providerKey := ProviderAPIKey{
				ID:     tokens[i].ID,
				Name:   tokens[i].Name,
				Key:    key,
				Status: string(tokens[i].Status),
			}
			if strings.TrimSpace(tokens[i].Group) != "" {
				providerKey.Group = &struct {
					Name           string  `json:"name"`
					RateMultiplier float64 `json:"rate_multiplier"`
				}{Name: tokens[i].Group}
			}
			keys = append(keys, providerKey)
		}
		usage, err := s.newapiClient.GetTokensUsage(ctx, p.BaseURL, auth, tokens, start, end, quotaPerUnit)
		if err != nil {
			return nil, nil, err
		}
		return keys, usage, nil
	}

	keys, usage, err := fetch(sess.NewAPI)
	if IsUnauthorized(err) {
		sess, err = s.tokens.refresh(ctx, p)
		if err != nil {
			return nil, nil, err
		}
		return fetch(sess.NewAPI)
	}
	return keys, usage, err
}

func (s *CostSyncService) newAPIQuotaPerUnit(ctx context.Context, p *repository.Provider) float64 {
	// new-api 的 quota_per_unit 可在站点配置中变化；每次成本同步都刷新，失败时客户端
	// 自带 500000 fallback。不能把数据库里的历史/默认值当成当前站点值。
	quotaPerUnit := s.newapiClient.GetQuotaPerUnit(ctx, p.BaseURL)
	p.QuotaPerUnit = quotaPerUnit
	if s.providerRepo != nil {
		_ = s.providerRepo.UpdateSession(ctx, p.ID, p.SessionCookie, p.UpstreamUserID, quotaPerUnit)
	}
	return quotaPerUnit
}

// NeedsBackfillFor 判断单个供应商是否还没做过历史回补（查询失败降级为 false，只同步今日）。
func (s *CostSyncService) NeedsBackfillFor(ctx context.Context, providerID int64) bool {
	m, err := s.costRepo.NeedsBackfill(ctx, []int64{providerID})
	if err != nil {
		return false
	}
	return m[providerID]
}

// KeyCosts 返回某供应商在闭区间内的 per-key 成本明细（供应商详情页用）。
// 只读本地库，不打上游。
func (s *CostSyncService) KeyCosts(ctx context.Context, providerID int64, startDate, endDate string) ([]repository.KeyCostRow, error) {
	return s.costRepo.KeyCosts(ctx, providerID, startDate, endDate)
}

// SyncState 返回某供应商的成本同步状态；无记录时返回零值（LastSyncedAt 为 nil）。
func (s *CostSyncService) SyncState(ctx context.Context, providerID int64) (repository.CostSyncState, error) {
	states, err := s.costRepo.SyncStates(ctx)
	if err != nil {
		return repository.CostSyncState{}, err
	}
	st, ok := states[providerID]
	if !ok {
		return repository.CostSyncState{ProviderID: providerID}, nil
	}
	return st, nil
}

// accountFingerprints 返回 sha256(api_key) → 账号 的映射。
func (s *CostSyncService) accountFingerprints(ctx context.Context) (map[string]repository.AccountKeyFingerprint, error) {
	list, err := s.pg.ListAccountKeyFingerprints(ctx)
	if err != nil {
		return nil, err
	}
	return uniqueAccountFingerprints(list), nil
}

// uniqueAccountFingerprints 对重复 key 使用 AccountID=0 的歧义哨兵，禁止静默后写覆盖前写。
func uniqueAccountFingerprints(list []repository.AccountKeyFingerprint) map[string]repository.AccountKeyFingerprint {
	out := make(map[string]repository.AccountKeyFingerprint, len(list))
	for _, f := range list {
		if f.Fingerprint == "" {
			continue
		}
		if _, exists := out[f.Fingerprint]; exists {
			out[f.Fingerprint] = repository.AccountKeyFingerprint{Fingerprint: f.Fingerprint}
			continue
		}
		out[f.Fingerprint] = f
	}
	return out
}

// matchCostAccount 优先按指纹匹配。指纹重复时明确拒绝 fallback；仅在指纹不存在时，
// 才按去掉「【供应商】」前缀后的账号名与 token 名/group 做唯一匹配。
func matchCostAccount(
	fingerprint, tokenName, group string,
	accounts map[string]repository.AccountKeyFingerprint,
) (repository.AccountKeyFingerprint, bool) {
	if fingerprint != "" {
		if account, exists := accounts[fingerprint]; exists {
			return account, account.AccountID > 0
		}
	}
	aliases := []string{strings.TrimSpace(tokenName), strings.TrimSpace(group)}
	candidates := make(map[int64]repository.AccountKeyFingerprint)
	for _, account := range accounts {
		if account.AccountID <= 0 {
			continue
		}
		accountAlias := costAccountAlias(account.AccountName)
		for _, alias := range aliases {
			if alias != "" && strings.EqualFold(accountAlias, alias) {
				candidates[account.AccountID] = account
				break
			}
		}
	}
	if len(candidates) != 1 {
		return repository.AccountKeyFingerprint{}, false
	}
	for _, account := range candidates {
		return account, true
	}
	return repository.AccountKeyFingerprint{}, false
}

// KeyGroupHit 上游分组下用 key 指纹（与成本明细同一套规则）匹配到的本站账号。
type KeyGroupHit struct {
	AccountID   int64
	AccountName string
}

// groupAccountsByKeys 按上游 key 所属分组归集本站账号。
// 匹配规则与成本明细完全一致：优先 sha256(api_key)，失败才回退唯一别名。
func groupAccountsByKeys(keys []ProviderAPIKey, fingerprints map[string]repository.AccountKeyFingerprint) map[string][]KeyGroupHit {
	out := map[string][]KeyGroupHit{}
	seen := map[string]map[int64]struct{}{}
	for _, k := range keys {
		group := ""
		if k.Group != nil {
			group = strings.TrimSpace(k.Group.Name)
		}
		if group == "" {
			continue
		}
		fp := ""
		if k.Key != "" {
			fp = keyidentity.Fingerprint(k.Key)
		}
		acc, ok := matchCostAccount(fp, k.Name, group, fingerprints)
		if !ok {
			continue
		}
		if seen[group] == nil {
			seen[group] = map[int64]struct{}{}
		}
		if _, dup := seen[group][acc.AccountID]; dup {
			continue
		}
		seen[group][acc.AccountID] = struct{}{}
		out[group] = append(out[group], KeyGroupHit{AccountID: acc.AccountID, AccountName: acc.AccountName})
	}
	return out
}

// GroupLinkedAccount 分组弹窗里展示的本站账号（不含密钥）。
type GroupLinkedAccount struct {
	ID             int64    `json:"id"`
	Name           string   `json:"name"`
	Platform       string   `json:"platform"`
	Status         string   `json:"status"`
	RateMultiplier float64  `json:"rate_multiplier"`
	Groups         []string `json:"groups"`
}

// GroupAccountBucket 一个上游分组下匹配到的本站账号。
type GroupAccountBucket struct {
	Group    string               `json:"group"`
	Accounts []GroupLinkedAccount `json:"accounts"`
}

// GroupAccountsResult 按上游 key 归组的本站账号。
type GroupAccountsResult struct {
	Items  []GroupAccountBucket `json:"items"`
	Source string               `json:"source"` // live_keys | stored_map
	Error  string               `json:"error,omitempty"`
}

// AccountsByUpstreamGroup 按成本明细同一套 key 匹配，给出每个上游分组对应哪些本站账号。
// 优先打上游拉 key 列表；凭据不齐或拉取失败时，回退到上次成本同步写入的映射。
func (s *CostSyncService) AccountsByUpstreamGroup(ctx context.Context, providerID int64) (GroupAccountsResult, error) {
	p, err := s.providerRepo.GetByID(ctx, providerID)
	if err != nil {
		return GroupAccountsResult{}, err
	}

	var hits map[string][]KeyGroupHit
	source := "stored_map"
	fetchErr := ""

	if p.CredentialsReady() {
		if keys, kErr := s.fetchKeys(ctx, p); kErr != nil {
			fetchErr = kErr.Error()
		} else {
			fps, fErr := s.accountFingerprints(ctx)
			if fErr != nil {
				return GroupAccountsResult{}, fErr
			}
			hits = groupAccountsByKeys(keys, fps)
			source = "live_keys"
		}
	} else {
		fetchErr = "站点凭据未配置，无法拉取上游 key"
	}

	if source != "live_keys" {
		stored, sErr := s.costRepo.MappedAccountsByGroup(ctx, providerID)
		if sErr != nil {
			return GroupAccountsResult{}, sErr
		}
		hits = map[string][]KeyGroupHit{}
		for g, list := range stored {
			for _, h := range list {
				hits[g] = append(hits[g], KeyGroupHit{AccountID: h.AccountID, AccountName: h.AccountName})
			}
		}
		source = "stored_map"
	}

	hydrated, err := s.hydrateGroupHits(ctx, hits)
	if err != nil {
		return GroupAccountsResult{}, err
	}
	return GroupAccountsResult{Items: hydrated, Source: source, Error: fetchErr}, nil
}

func (s *CostSyncService) hydrateGroupHits(ctx context.Context, hits map[string][]KeyGroupHit) ([]GroupAccountBucket, error) {
	names := make([]string, 0, len(hits))
	for g := range hits {
		names = append(names, g)
	}
	sort.Strings(names)

	byID := map[int64]repository.PGAccount{}
	groupMap := map[int64][]string{}
	if s.pg != nil && s.pg.Available() {
		if accs, err := s.pg.ListActiveAccounts(ctx); err == nil {
			for _, a := range accs {
				byID[a.ID] = a
			}
		}
		if gm, err := s.pg.AccountGroups(ctx); err == nil {
			groupMap = gm
		}
	}

	out := make([]GroupAccountBucket, 0, len(names))
	for _, g := range names {
		accs := make([]GroupLinkedAccount, 0, len(hits[g]))
		for _, h := range hits[g] {
			row := GroupLinkedAccount{ID: h.AccountID, Name: h.AccountName, Groups: []string{}}
			if a, ok := byID[h.AccountID]; ok {
				row.Name = a.Name
				row.Platform = a.Platform
				row.Status = a.Status
				row.RateMultiplier = a.RateMultiplier
				row.Groups = groupMap[a.ID]
				if row.Groups == nil {
					row.Groups = []string{}
				}
			}
			accs = append(accs, row)
		}
		out = append(out, GroupAccountBucket{Group: g, Accounts: accs})
	}
	return out, nil
}

// costAccountAlias 去掉账号名的【】前缀，得到与上游 key 名比对用的别名。
//
// 【与供应商归属无关，勿随前缀归属一起删】这里做的是「上游 key 名 ↔ 本站账号名」
// 的模糊配对，是指纹匹配失败时的成本匹配 fallback。供应商归属已改由
// provider_accounts 表决定（013 迁移），但成本匹配仍需要这个别名 ——
// 删掉会让上游 key 匹配率下降，成本显示为缺失。
func costAccountAlias(name string) string {
	name = strings.TrimSpace(name)
	if strings.HasPrefix(name, "【") {
		if end := strings.Index(name, "】"); end >= 0 {
			return strings.TrimSpace(name[end+len("】"):])
		}
	}
	return name
}

// Cleanup 清理超过保留期的成本记录。
func (s *CostSyncService) Cleanup(ctx context.Context) {
	days := s.cfg.Cost.RetentionDays
	if days <= 0 {
		days = 180
	}
	before := time.Now().In(s.cfg.Location).AddDate(0, 0, -days).Format("2006-01-02")
	if n, err := s.costRepo.DeleteOlderThan(ctx, before); err != nil {
		log.Printf("[cost-sync] 清理历史成本失败: %v", err)
	} else if n > 0 {
		log.Printf("[cost-sync] 清理历史成本 %d 行（早于 %s）", n, before)
	}
}
