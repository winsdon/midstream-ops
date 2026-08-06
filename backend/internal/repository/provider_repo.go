package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"sub2api-account-monitor/internal/pkg/secretbox"
)

// ErrNotFound 记录不存在。
var ErrNotFound = errors.New("记录不存在")

// Provider 供应商模型。
// 凭据字段（LoginPassword/AccessToken/RefreshToken/SessionCookie）在仓储层
// 透明加解密：内存中恒为明文，落库时按 secretbox 配置加密。
type Provider struct {
	ID                 int64
	Name               string
	Note               string
	BalanceType        string // sub2api | manual | none（sub2api 泛指「API 自动采集」）
	Platform           string // sub2api | new-api
	AuthMode           string // password | token | user_key
	Role               string // upstream（被监控的上游站）| self（自己站点，调价用）
	BaseURL            string
	LoginEmail         string
	LoginPassword      string
	AccessToken        string
	RefreshToken       string // sub2api token 模式
	SessionCookie      string // new-api password 模式登录产物
	UpstreamUserID     string // new-api：New-Api-User 头
	QuotaPerUnit       float64 // new-api：quota → USD 换算
	TokenExpiresAt     *time.Time
	LowBalanceThreshold float64
	ProbeEnabled       bool
	ProbeModel         *string
	LastBalance        *float64
	LastBalanceAt      *time.Time
	LastBalanceError   *string
	RechargeRate       float64 // 充值倍率：balance × recharge_rate = 折合 CNY
	LoginFailures      int     // 连续登录被拒次数（4xx）
	LoginCooldownUntil *time.Time
	IgnoreBalanceAlert bool    // 站点级静音：不推余额告警（采集照常）
	// SelfOperated 自营站：本站自己经营的上游，其上游实扣是左手倒右手，
	// 不计入成本；真实支出改由 provider_operating_costs 手工记账。
	SelfOperated       bool
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// ProviderRepo 供应商存储。
type ProviderRepo struct {
	db  *sql.DB
	box *secretbox.Box
}

// NewProviderRepo 创建 ProviderRepo（box 为凭据加解密器）。
func NewProviderRepo(s *SQLite, box *secretbox.Box) *ProviderRepo {
	return &ProviderRepo{db: s.DB(), box: box}
}

// nowUTC 返回当前 UTC 时间 RFC3339。
func nowUTC() string { return time.Now().UTC().Format(time.RFC3339) }

// parseTime 解析 RFC3339（可空）。
func parseTimePtr(s sql.NullString) *time.Time {
	if !s.Valid || s.String == "" {
		return nil
	}
	if t, err := time.Parse(time.RFC3339, s.String); err == nil {
		return &t
	}
	return nil
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

const providerCols = `id, name, note, balance_type, platform, auth_mode, role, base_url, login_email, login_password,
	access_token, refresh_token, session_cookie, upstream_user_id, quota_per_unit,
	token_expires_at, low_balance_threshold, probe_enabled, probe_model,
	last_balance, last_balance_at, last_balance_error, recharge_rate, login_failures, login_cooldown_until,
	ignore_balance_alert, self_operated, created_at, updated_at`

// scanProvider 扫描一行并透明解密凭据字段。
// 注意：Scan 参数顺序与 providerCols 是手工维持的隐式契约，无编译期保护，
// 新增列必须在两处同序追加，否则所有 SELECT 会静默错位。
func (r *ProviderRepo) scanProvider(row interface{ Scan(...any) error }) (*Provider, error) {
	var p Provider
	var tokenExp, lastBalAt sql.NullString
	var probeModel, lastBalErr sql.NullString
	var lastBal sql.NullFloat64
	var loginCooldown sql.NullString
	var createdAt, updatedAt string
	var probeEnabled, ignoreBalanceAlert, selfOperated int
	err := row.Scan(&p.ID, &p.Name, &p.Note, &p.BalanceType, &p.Platform, &p.AuthMode, &p.Role, &p.BaseURL, &p.LoginEmail, &p.LoginPassword,
		&p.AccessToken, &p.RefreshToken, &p.SessionCookie, &p.UpstreamUserID, &p.QuotaPerUnit,
		&tokenExp, &p.LowBalanceThreshold, &probeEnabled, &probeModel,
		&lastBal, &lastBalAt, &lastBalErr, &p.RechargeRate, &p.LoginFailures, &loginCooldown,
		&ignoreBalanceAlert, &selfOperated, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	// 凭据解密（明文旧数据原样返回）
	p.LoginPassword = r.box.MustOpen(p.LoginPassword)
	p.AccessToken = r.box.MustOpen(p.AccessToken)
	p.RefreshToken = r.box.MustOpen(p.RefreshToken)
	p.SessionCookie = r.box.MustOpen(p.SessionCookie)

	p.TokenExpiresAt = parseTimePtr(tokenExp)
	p.ProbeEnabled = probeEnabled != 0
	p.IgnoreBalanceAlert = ignoreBalanceAlert != 0
	p.SelfOperated = selfOperated != 0
	if probeModel.Valid {
		pm := probeModel.String
		p.ProbeModel = &pm
	}
	if lastBal.Valid {
		lb := lastBal.Float64
		p.LastBalance = &lb
	}
	p.LastBalanceAt = parseTimePtr(lastBalAt)
	if lastBalErr.Valid {
		e := lastBalErr.String
		p.LastBalanceError = &e
	}
	p.LoginCooldownUntil = parseTimePtr(loginCooldown)
	p.CreatedAt = parseTime(createdAt)
	p.UpdatedAt = parseTime(updatedAt)
	return &p, nil
}

// List 返回全部上游供应商（按 name 排序；不含 role='self' 的本站记录）。
func (r *ProviderRepo) List(ctx context.Context) ([]*Provider, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+providerCols+` FROM providers WHERE role = 'upstream' ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Provider
	for rows.Next() {
		p, err := r.scanProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetByID 按 ID 查询。
func (r *ProviderRepo) GetByID(ctx context.Context, id int64) (*Provider, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+providerCols+` FROM providers WHERE id = ?`, id)
	p, err := r.scanProvider(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// GetByName 按名称查询。
func (r *ProviderRepo) GetByName(ctx context.Context, name string) (*Provider, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+providerCols+` FROM providers WHERE name = ?`, name)
	p, err := r.scanProvider(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// Count 返回供应商总数。
func (r *ProviderRepo) Count(ctx context.Context) int {
	var n int
	_ = r.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM providers`).Scan(&n)
	return n
}

// ListNames 返回所有供应商名集合。
func (r *ProviderRepo) ListNames(ctx context.Context) (map[string]bool, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT name FROM providers`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]bool)
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out[n] = true
	}
	return out, rows.Err()
}

// NameByID 返回 provider_id → name（统计归并与关联展示用）。
//
// 不过滤 role：调用方拿它做 id→名字的查表，__self__ 混在里面无害，
// 且过滤掉会让引用了 self 的关联查不到名字而显示成空。
func (r *ProviderRepo) NameByID(ctx context.Context) (map[int64]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, name FROM providers`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]string)
	for rows.Next() {
		var id int64
		var n string
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// SelfOperatedIDs 返回自营站的 id 集合。
//
// 供统计层判断「该账号的成本缺失是否属于设计如此」：自营站的上游实扣被有意计 0，
// 且站点可能根本没接上游采集（balance_type=none），其账号永远匹配不到成本行。
// 不加区分会让「⚠ 成本不完整」变成永久噪音。
func (r *ProviderRepo) SelfOperatedIDs(ctx context.Context) (map[int64]bool, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id FROM providers WHERE self_operated = 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]bool)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// CreateParams 新建供应商参数。
type CreateParams struct {
	Name                string
	Note                string
	BalanceType         string
	Platform            string // sub2api | new-api
	AuthMode            string // password | token | user_key
	BaseURL             string
	LoginEmail          string
	LoginPassword       string
	AccessToken         string // token/user_key 模式：直填令牌
	RefreshToken        string // sub2api token 模式
	UpstreamUserID      string // new-api：New-Api-User
	LowBalanceThreshold float64
	RechargeRate        float64
	ProbeEnabled        bool
	ProbeModel          *string
	IgnoreBalanceAlert  bool
	SelfOperated        bool
}

// normalizePlatformAuth 填充平台/认证模式默认值。
func normalizePlatformAuth(platform, authMode string) (string, string) {
	if platform == "" {
		platform = "sub2api"
	}
	if authMode == "" {
		authMode = "password"
	}
	return platform, authMode
}

// Create 新建供应商。
func (r *ProviderRepo) Create(ctx context.Context, cp CreateParams) (*Provider, error) {
	probeEnabled := 0
	if cp.ProbeEnabled {
		probeEnabled = 1
	}
	ignoreBalanceAlert := 0
	if cp.IgnoreBalanceAlert {
		ignoreBalanceAlert = 1
	}
	selfOperated := 0
	if cp.SelfOperated {
		selfOperated = 1
	}
	if cp.RechargeRate <= 0 {
		cp.RechargeRate = 1
	}
	platform, authMode := normalizePlatformAuth(cp.Platform, cp.AuthMode)
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO providers (name, note, balance_type, platform, auth_mode, base_url, login_email, login_password,
			access_token, refresh_token, upstream_user_id,
			low_balance_threshold, recharge_rate, probe_enabled, probe_model, ignore_balance_alert,
			self_operated, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		cp.Name, cp.Note, cp.BalanceType, platform, authMode, cp.BaseURL, cp.LoginEmail, r.box.Seal(cp.LoginPassword),
		r.box.Seal(cp.AccessToken), r.box.Seal(cp.RefreshToken), cp.UpstreamUserID,
		cp.LowBalanceThreshold, cp.RechargeRate, probeEnabled, cp.ProbeModel, ignoreBalanceAlert,
		selfOperated, nowUTC(), nowUTC())
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return r.GetByID(ctx, id)
}

// UpdateParams 编辑供应商参数（凭据指针 nil = 不修改）。
type UpdateParams struct {
	Name                string
	Note                string
	BalanceType         string
	Platform            string
	AuthMode            string
	BaseURL             string
	LoginEmail          string
	LoginPassword       *string // nil = 不修改
	AccessToken         *string // nil = 不修改（token/user_key 模式）
	RefreshToken        *string // nil = 不修改
	UpstreamUserID      string
	LowBalanceThreshold float64
	RechargeRate        float64
	ProbeEnabled        bool
	ProbeModel          *string
	IgnoreBalanceAlert  bool
	SelfOperated        bool
}

// Update 编辑供应商。任一凭据变化时清 token 缓存与登录冷却（须重新建立会话）。
func (r *ProviderRepo) Update(ctx context.Context, id int64, up UpdateParams) (*Provider, error) {
	probeEnabled := 0
	if up.ProbeEnabled {
		probeEnabled = 1
	}
	ignoreBalanceAlert := 0
	if up.IgnoreBalanceAlert {
		ignoreBalanceAlert = 1
	}
	selfOperated := 0
	if up.SelfOperated {
		selfOperated = 1
	}
	if up.RechargeRate <= 0 {
		up.RechargeRate = 1
	}
	platform, authMode := normalizePlatformAuth(up.Platform, up.AuthMode)

	// 基础字段
	_, err := r.db.ExecContext(ctx, `
		UPDATE providers SET name=?, note=?, balance_type=?, platform=?, auth_mode=?, base_url=?, login_email=?,
			upstream_user_id=?, low_balance_threshold=?, recharge_rate=?, probe_enabled=?, probe_model=?,
			ignore_balance_alert=?, self_operated=?, updated_at=?
		WHERE id=?`,
		up.Name, up.Note, up.BalanceType, platform, authMode, up.BaseURL, up.LoginEmail,
		up.UpstreamUserID, up.LowBalanceThreshold, up.RechargeRate, probeEnabled, up.ProbeModel,
		ignoreBalanceAlert, selfOperated, nowUTC(), id)
	if err != nil {
		return nil, err
	}

	// 凭据字段：任一提供即更新并重置会话状态
	credChanged := false
	if up.LoginPassword != nil {
		if _, err := r.db.ExecContext(ctx, `UPDATE providers SET login_password=? WHERE id=?`,
			r.box.Seal(*up.LoginPassword), id); err != nil {
			return nil, err
		}
		credChanged = true
	}
	if up.AccessToken != nil {
		if _, err := r.db.ExecContext(ctx, `UPDATE providers SET access_token=? WHERE id=?`,
			r.box.Seal(*up.AccessToken), id); err != nil {
			return nil, err
		}
		credChanged = true
	}
	if up.RefreshToken != nil {
		if _, err := r.db.ExecContext(ctx, `UPDATE providers SET refresh_token=? WHERE id=?`,
			r.box.Seal(*up.RefreshToken), id); err != nil {
			return nil, err
		}
		credChanged = true
	}
	if credChanged {
		// token 模式下 access_token 本身就是凭据，只清过期时间与冷却、会话 cookie
		if _, err := r.db.ExecContext(ctx, `
			UPDATE providers SET token_expires_at=NULL, session_cookie='',
				login_failures=0, login_cooldown_until=NULL, updated_at=? WHERE id=?`,
			nowUTC(), id); err != nil {
			return nil, err
		}
		// password 模式：access_token 是登录产物，须一并清除（token/user_key 模式勿清）
		if up.AccessToken == nil && authMode == "password" {
			if _, err := r.db.ExecContext(ctx, `UPDATE providers SET access_token='' WHERE id=?`, id); err != nil {
				return nil, err
			}
		}
	}
	return r.GetByID(ctx, id)
}

// Delete 删除供应商（级联删快照；probe_results 的 provider_id 置 NULL）。
func (r *ProviderRepo) Delete(ctx context.Context, id int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE probe_results SET provider_id = NULL WHERE provider_id = ?`, id); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM providers WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

// UpdateToken 缓存供应商站点 token（加密落库）。
func (r *ProviderRepo) UpdateToken(ctx context.Context, id int64, token string, expiresAt *time.Time) error {
	var expStr *string
	if expiresAt != nil {
		s := expiresAt.UTC().Format(time.RFC3339)
		expStr = &s
	}
	_, err := r.db.ExecContext(ctx, `UPDATE providers SET access_token=?, token_expires_at=?, updated_at=? WHERE id=?`,
		r.box.Seal(token), expStr, nowUTC(), id)
	return err
}

// UpdateTokenPair 缓存 access+refresh token（sub2api token 模式刷新后回写）。
func (r *ProviderRepo) UpdateTokenPair(ctx context.Context, id int64, accessToken, refreshToken string, expiresAt *time.Time) error {
	var expStr *string
	if expiresAt != nil {
		s := expiresAt.UTC().Format(time.RFC3339)
		expStr = &s
	}
	_, err := r.db.ExecContext(ctx, `UPDATE providers SET access_token=?, refresh_token=?, token_expires_at=?, updated_at=? WHERE id=?`,
		r.box.Seal(accessToken), r.box.Seal(refreshToken), expStr, nowUTC(), id)
	return err
}

// UpdateSession 缓存 new-api 会话（Cookie + 用户 ID + quota 换算系数）。
func (r *ProviderRepo) UpdateSession(ctx context.Context, id int64, cookie, userID string, quotaPerUnit float64) error {
	_, err := r.db.ExecContext(ctx, `UPDATE providers SET session_cookie=?, upstream_user_id=?, quota_per_unit=?, updated_at=? WHERE id=?`,
		r.box.Seal(cookie), userID, quotaPerUnit, nowUTC(), id)
	return err
}

// ClearToken 清除缓存 token 与会话。
func (r *ProviderRepo) ClearToken(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `UPDATE providers SET access_token='', token_expires_at=NULL, session_cookie='', updated_at=? WHERE id=?`,
		nowUTC(), id)
	return err
}

// RecordLoginRejected 登录被拒（4xx）：递增失败计数并写入冷却截止时刻。
// 返回递增后的次数。
func (r *ProviderRepo) RecordLoginRejected(ctx context.Context, id int64, cooldownUntil time.Time) (int, error) {
	_, err := r.db.ExecContext(ctx, `
		UPDATE providers SET login_failures = login_failures + 1, login_cooldown_until = ?, updated_at = ? WHERE id = ?`,
		cooldownUntil.UTC().Format(time.RFC3339), nowUTC(), id)
	if err != nil {
		return 0, err
	}
	var n int
	err = r.db.QueryRowContext(ctx, `SELECT login_failures FROM providers WHERE id = ?`, id).Scan(&n)
	return n, err
}

// SetLoginCooldown 覆盖冷却截止时刻（按最新失败次数二次计算后写入）。
func (r *ProviderRepo) SetLoginCooldown(ctx context.Context, id int64, cooldownUntil time.Time) error {
	_, err := r.db.ExecContext(ctx, `UPDATE providers SET login_cooldown_until = ?, updated_at = ? WHERE id = ?`,
		cooldownUntil.UTC().Format(time.RFC3339), nowUTC(), id)
	return err
}

// ClearLoginCooldown 登录成功或手动触发后清零冷却。
func (r *ProviderRepo) ClearLoginCooldown(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `UPDATE providers SET login_failures = 0, login_cooldown_until = NULL, updated_at = ? WHERE id = ?`,
		nowUTC(), id)
	return err
}

// UpdateBalanceCache 更新余额冗余缓存列。
func (r *ProviderRepo) UpdateBalanceCache(ctx context.Context, id int64, balance *float64, balanceErr *string) error {
	var balAt *string
	if balance != nil {
		s := nowUTC()
		balAt = &s
	}
	_, err := r.db.ExecContext(ctx, `UPDATE providers SET last_balance=?, last_balance_at=COALESCE(?, last_balance_at), last_balance_error=?, updated_at=? WHERE id=?`,
		balance, balAt, balanceErr, nowUTC(), id)
	return err
}

// credentialsReadySQL 凭据齐备判据：缺凭据的站点连一次请求都发不出去。
//
// balance_type='sub2api' 表达的是「想自动采集」的意图，凭据齐备与否才决定「采不采得动」——
// 快捷导入建出来的站天然只有地址没有账密，若仅凭意图就排班，每轮都会在
// provider_token.go 的凭据校验处失败，连续失败计数累积、退避拉长、健康点全红，
// 真正坏掉的站反而被这批噪音淹没。判据与各认证模式的实际必需字段一一对应：
//
//	sub2api + password → email + password（loginSub2api）
//	sub2api + token    → refresh_token 或 access_token（refreshSub2apiToken）
//	new-api + password → email + password（loginNewAPI）
//	new-api + user_key → access_token + upstream_user_id（ensureNewAPI）
//
// 凭据空值落库恒为空串（secretbox.Seal("") == ""），故 <> '' 判空成立。
const credentialsReadySQL = `base_url <> '' AND (
		(auth_mode = 'password' AND login_email <> '' AND login_password <> '')
	 OR (auth_mode = 'token'    AND (refresh_token <> '' OR access_token <> ''))
	 OR (auth_mode = 'user_key' AND access_token <> '' AND upstream_user_id <> '')
	)`

// UpdateBaseURL 补写站点地址（快捷导入回填历史空地址站点用）。
//
// 刻意不清凭据/冷却：这是补齐而非变更，与 Update 的凭据变更语义不同。
func (r *ProviderRepo) UpdateBaseURL(ctx context.Context, id int64, baseURL string) (*Provider, error) {
	if _, err := r.db.ExecContext(ctx,
		`UPDATE providers SET base_url=?, updated_at=? WHERE id=?`,
		baseURL, nowUTC(), id); err != nil {
		return nil, err
	}
	return r.GetByID(ctx, id)
}

// CredentialsReady 与 credentialsReadySQL 等价的内存判据（两者须同步修改）。
//
// 供 DTO 输出用：前端据此把「缺凭据」与「采集失败」画成两种健康态，
// 而不是让用户对着一个红点猜是没配还是配错了。
func (p *Provider) CredentialsReady() bool {
	if p.BaseURL == "" {
		return false
	}
	switch p.AuthMode {
	case "password":
		return p.LoginEmail != "" && p.LoginPassword != ""
	case "token":
		return p.RefreshToken != "" || p.AccessToken != ""
	case "user_key":
		return p.AccessToken != "" && p.UpstreamUserID != ""
	}
	return false
}

// ListCollectable 返回需要自动采集的供应商（balance_type='sub2api'、凭据齐备、仅上游站）。
func (r *ProviderRepo) ListCollectable(ctx context.Context) ([]*Provider, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+providerCols+` FROM providers
		WHERE balance_type = 'sub2api' AND role = 'upstream' AND `+credentialsReadySQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Provider
	for rows.Next() {
		p, err := r.scanProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetSelf 返回自己站点（role='self'，最多一条；无则 ErrNotFound）。
func (r *ProviderRepo) GetSelf(ctx context.Context) (*Provider, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+providerCols+` FROM providers WHERE role = 'self' LIMIT 1`)
	p, err := r.scanProvider(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// UpsertSelf 保存自己站点连接（只保留一条 self 记录）。
// 凭据留空（nil）时保留原值。
func (r *ProviderRepo) UpsertSelf(ctx context.Context, baseURL, email string, password *string) (*Provider, error) {
	existing, err := r.GetSelf(ctx)
	if errors.Is(err, ErrNotFound) {
		pwd := ""
		if password != nil {
			pwd = *password
		}
		res, insErr := r.db.ExecContext(ctx, `
			INSERT INTO providers (name, note, balance_type, platform, auth_mode, role, base_url, login_email, login_password, created_at, updated_at)
			VALUES ('__self__', '本站（调价用）', 'none', 'sub2api', 'password', 'self', ?, ?, ?, ?, ?)`,
			baseURL, email, r.box.Seal(pwd), nowUTC(), nowUTC())
		if insErr != nil {
			return nil, insErr
		}
		id, idErr := res.LastInsertId()
		if idErr != nil {
			return nil, idErr
		}
		return r.GetByID(ctx, id)
	}
	if err != nil {
		return nil, err
	}

	if password != nil {
		_, err = r.db.ExecContext(ctx, `
			UPDATE providers SET base_url=?, login_email=?, login_password=?,
				access_token='', token_expires_at=NULL, login_failures=0, login_cooldown_until=NULL, updated_at=?
			WHERE id=?`,
			baseURL, email, r.box.Seal(*password), nowUTC(), existing.ID)
	} else {
		_, err = r.db.ExecContext(ctx, `UPDATE providers SET base_url=?, login_email=?, updated_at=? WHERE id=?`,
			baseURL, email, nowUTC(), existing.ID)
	}
	if err != nil {
		return nil, err
	}
	return r.GetByID(ctx, existing.ID)
}

// ListProbeEnabled 返回开启探测的供应商（仅上游站）。
func (r *ProviderRepo) ListProbeEnabled(ctx context.Context) ([]*Provider, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+providerCols+` FROM providers WHERE probe_enabled = 1 AND role = 'upstream'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Provider
	for rows.Next() {
		p, err := r.scanProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
