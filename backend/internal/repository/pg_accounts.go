package repository

import (
	"context"

	"sub2api-account-monitor/internal/pkg/keyidentity"
)

// PGAccount 线上 sub2api 账号（探测/聚合用）。
type PGAccount struct {
	ID             int64
	Name           string
	Platform       string
	Type           string
	Status         string
	Schedulable    bool
	RateMultiplier float64
	BaseURL        string // extra->>'base_url'
	APIKey         string // credentials->>'api_key'（仅探测用，绝不外泄）
}

// PGGroup 线上 sub2api 分组。
type PGGroup struct {
	ID             int64
	Name           string
	Platform       string
	Status         string
	RateMultiplier float64
	// SortOrder 是 sub2api 后台「分组管理」里拖拽出来的展示顺序（升序在前，
	// 默认 0）。本站所有列出分组的界面都跟随它，别再按 id 或名字排 ——
	// 运营把常看的组拖到前面，本站顺序必须和后台一致。
	SortOrder int
	// IsExclusive 是「专属分组」标记：管理员授权给特定用户，分组名与倍率属于
	// 内部定价策略，不该出现在面向全体用户的地方（模型广场、渠道状态图）。
	// 过滤放在调用方：同一个线上分组表，广场与状态图对「公开」的定义可能分化。
	IsExclusive bool
}

// ListActiveAccounts 返回所有未删除账号（聚合/前缀扫描用，不含 api_key）。
// base_url 取 credentials->>'base_url'（上游转发地址），回退 extra。
func (p *PG) ListActiveAccounts(ctx context.Context) ([]PGAccount, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT id, COALESCE(name,''), COALESCE(platform,''), COALESCE(type,''),
		       COALESCE(status,''), COALESCE(schedulable,false),
		       COALESCE(rate_multiplier,1),
		       COALESCE(NULLIF(credentials->>'base_url',''), extra->>'base_url', '')
		FROM accounts WHERE deleted_at IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PGAccount
	for rows.Next() {
		var a PGAccount
		var rm float64
		if err := rows.Scan(&a.ID, &a.Name, &a.Platform, &a.Type, &a.Status, &a.Schedulable, &rm, &a.BaseURL); err != nil {
			return nil, err
		}
		a.RateMultiplier = rm
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListProbeCandidates 返回可主动探测的 api_key 账号（含 api_key，仅后端内部使用）。
func (p *PG) ListProbeCandidates(ctx context.Context) ([]PGAccount, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT id, COALESCE(name,''), COALESCE(platform,''), COALESCE(type,''),
		       COALESCE(status,''), COALESCE(schedulable,false),
		       COALESCE(rate_multiplier,1),
		       COALESCE(NULLIF(credentials->>'base_url',''), extra->>'base_url', ''),
		       COALESCE(credentials->>'api_key','')
		FROM accounts
		WHERE deleted_at IS NULL AND type IN ('api_key','apikey') AND status = 'active'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PGAccount
	for rows.Next() {
		var a PGAccount
		if err := rows.Scan(&a.ID, &a.Name, &a.Platform, &a.Type, &a.Status, &a.Schedulable, &a.RateMultiplier, &a.BaseURL, &a.APIKey); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListAccountGroupIDs 返回每个可探测账号所属的分组 id（account_id -> []group_id）。
//
// 归属只看三件事：账号真挂在 account_groups 上、分组启用且未删除、平台匹配
// （composite 分组接受全部平台）。刻意不看 a.schedulable 与 is_exclusive ——
// 鹈鹕测试是拿账号自己的 base_url 与 Key 直连上游，不经 sub2api 调度，临时停调的
// 账号照样能测；专属分组也一直允许当作品归属（Groups 只排除非 active 的分组）。
// 注意分组下拉里的模型提示来自 ListGroupAvailableModels（那份口径含 schedulable），
// 所以停调账号的模型名可能不在提示里，模型仍可直接手填。
func (p *PG) ListAccountGroupIDs(ctx context.Context) (map[int64][]int64, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT a.id, g.id
		FROM accounts a
		JOIN account_groups ag ON ag.account_id = a.id
		JOIN groups g         ON g.id = ag.group_id
		WHERE a.deleted_at IS NULL AND a.type IN ('api_key','apikey') AND a.status = 'active'
		  AND g.deleted_at IS NULL AND g.status = 'active'
		  AND (g.platform = 'composite' OR a.platform = g.platform)
		ORDER BY a.id, g.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64][]int64)
	for rows.Next() {
		var accountID, groupID int64
		if err := rows.Scan(&accountID, &groupID); err != nil {
			return nil, err
		}
		out[accountID] = append(out[accountID], groupID)
	}
	return out, rows.Err()
}

// AccountKeyFingerprint 账号的 api_key 指纹（用于与上游 key 匹配，不含明文）。
type AccountKeyFingerprint struct {
	AccountID   int64
	AccountName string
	Fingerprint string // sha256(api_key) 十六进制
}

// ListAccountKeyFingerprints 返回所有未删除账号的 api_key 指纹。
// 明文只在本函数内参与哈希计算，不返回给调用方。
func (p *PG) ListAccountKeyFingerprints(ctx context.Context) ([]AccountKeyFingerprint, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT id, COALESCE(name,''), COALESCE(credentials->>'api_key','')
		FROM accounts
		WHERE deleted_at IS NULL AND COALESCE(credentials->>'api_key','') <> ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AccountKeyFingerprint
	for rows.Next() {
		var f AccountKeyFingerprint
		var key string
		if err := rows.Scan(&f.AccountID, &f.AccountName, &key); err != nil {
			return nil, err
		}
		f.Fingerprint = keyidentity.Fingerprint(key)
		out = append(out, f)
	}
	return out, rows.Err()
}

// ListGroups 返回所有未删除分组，按 sub2api 后台的展示顺序（sort_order 升序）排列。
//
// 排序刻意放在 SQL 而不是调用方：这是「分组在线上后台长什么样」的属性，不是某个
// 下拉框的偏好。此前没有 ORDER BY，PG 返回的是物理顺序 —— 建组 / 改组 / 后台拖拽
// 排序都会让它漂移，同一个界面刷新两次都可能不一样。sort_order 相同的组用 id 兜底，
// 保证顺序稳定可复现。
//
// sort_order 走 to_jsonb 而非直接取列：它是 sub2api 后加的列，老 schema 没有。
// 直接 SELECT 会让整个分组下拉 500，而排序只是显示问题 —— 老 schema 应退化成
// 「顺序不保证」，不该升级成「列不出分组」。同 pg_media_keys.go 的 models_list_config。
// is_exclusive 走同一个写法，理由相同（也是后加的列）。
//
// 【必须用 ->> 而不是 ->】这两列的取值都可能为 NULL（未拖拽排序过的分组、
// 老数据的 is_exclusive）。to_jsonb(row) 会把 NULL 列编码成 JSON null，而
// `(jsonb null)::int` 直接抛 SQLSTATE 22023；`->>` 对「键不存在」和「JSON null」
// 都返回 SQL NULL，COALESCE 才能按文档所说的那样折成 0 / false。
func (p *PG) ListGroups(ctx context.Context) ([]PGGroup, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT g.id, COALESCE(g.name,''), COALESCE(g.platform,''), COALESCE(g.status,''),
		       COALESCE(g.rate_multiplier,1),
		       COALESCE((to_jsonb(g) ->> 'sort_order')::int, 0),
		       COALESCE((to_jsonb(g) ->> 'is_exclusive')::boolean, false)
		FROM groups g WHERE g.deleted_at IS NULL
		ORDER BY COALESCE((to_jsonb(g) ->> 'sort_order')::int, 0) ASC, g.id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PGGroup
	for rows.Next() {
		var g PGGroup
		if err := rows.Scan(&g.ID, &g.Name, &g.Platform, &g.Status, &g.RateMultiplier, &g.SortOrder, &g.IsExclusive); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// RateEntity 倍率实体（groups/accounts 通用）。
type RateEntity struct {
	ID   int64
	Name string
	Rate float64
}

// ListGroupRates 返回所有未删除分组的倍率。
func (p *PG) ListGroupRates(ctx context.Context) ([]RateEntity, error) {
	return p.listRates(ctx, `SELECT id, COALESCE(name,''), COALESCE(rate_multiplier,1) FROM groups WHERE deleted_at IS NULL`)
}

// ListAccountRates 返回所有未删除账号的倍率。
func (p *PG) ListAccountRates(ctx context.Context) ([]RateEntity, error) {
	return p.listRates(ctx, `SELECT id, COALESCE(name,''), COALESCE(rate_multiplier,1) FROM accounts WHERE deleted_at IS NULL`)
}

func (p *PG) listRates(ctx context.Context, query string) ([]RateEntity, error) {
	rows, err := p.pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RateEntity
	for rows.Next() {
		var e RateEntity
		if err := rows.Scan(&e.ID, &e.Name, &e.Rate); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// AccountGroups 返回 account_groups 多对多映射（account_id -> []group_name）。
func (p *PG) AccountGroups(ctx context.Context) (map[int64][]string, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT ag.account_id, COALESCE(g.name,'')
		FROM account_groups ag
		JOIN groups g ON g.id = ag.group_id AND g.deleted_at IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64][]string)
	for rows.Next() {
		var accID int64
		var gname string
		if err := rows.Scan(&accID, &gname); err != nil {
			return nil, err
		}
		out[accID] = append(out[accID], gname)
	}
	return out, rows.Err()
}
