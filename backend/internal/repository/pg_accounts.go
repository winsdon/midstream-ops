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

// ListGroups 返回所有未删除分组。
func (p *PG) ListGroups(ctx context.Context) ([]PGGroup, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT id, COALESCE(name,''), COALESCE(platform,''), COALESCE(status,''),
		       COALESCE(rate_multiplier,1)
		FROM groups WHERE deleted_at IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PGGroup
	for rows.Next() {
		var g PGGroup
		if err := rows.Scan(&g.ID, &g.Name, &g.Platform, &g.Status, &g.RateMultiplier); err != nil {
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
