package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"sub2api-account-monitor/internal/pkg/keyidentity"
	"sub2api-account-monitor/internal/repository"
	"sub2api-account-monitor/internal/service/modeldetect"
)

var ErrPelicanUnavailable = errors.New("plaza.errors.databaseUnavailable")
var ErrPelicanInvalid = errors.New("pelican.errors.invalid")

// 目标凭据来源：本站已建档上游账号，或手填网关地址 + Key。两者都直连上游。
const (
	PelicanSourceAccount = "account"
	PelicanSourceManual  = "manual"
)

// 手填地址与 Key 的上限，避免超长垃圾进请求体。
const (
	pelicanMaxBaseURLLen = 512
	pelicanMaxAPIKeyLen  = 512
)

type pelicanPG interface {
	ListGroups(context.Context) ([]repository.PGGroup, error)
	ListGroupAvailableModels(context.Context) ([]repository.PGGroupModels, error)
	ListProbeCandidates(context.Context) ([]repository.PGAccount, error)
	ListAccountGroupIDs(context.Context) (map[int64][]int64, error)
	PelicanVisibleGroups(context.Context) ([]int64, error)
}

// PelicanTarget 一个测试目标的来源与生成参数。
//
// APIKey 只在提交请求里出现：落库前会被剥掉，只在进程内用于发请求。
type PelicanTarget struct {
	GroupID int64  `json:"group_id"`
	Source  string `json:"source"`
	// AccountID 账号来源的本站账号 id。
	AccountID int64 `json:"account_id,omitempty"`
	// BaseURL 手填来源的网关地址。
	BaseURL string `json:"base_url,omitempty"`
	// APIKey 手填来源的密钥，绝不落库。
	APIKey   string `json:"api_key,omitempty"`
	Model    string `json:"model"`
	Protocol string `json:"protocol"`
	RetryOf  int64  `json:"retry_of,omitempty"`
}
type PelicanRequest struct {
	RequestID      string          `json:"request_id"`
	Prompt         string          `json:"prompt"`
	Targets        []PelicanTarget `json:"targets"`
	Concurrency    int             `json:"concurrency"`
	MaxTokens      int             `json:"max_tokens"`
	TimeoutSeconds int             `json:"timeout_seconds"`
	// Effort 思考强度，只对 Messages 协议生效；空表示不发（上游默认）。omitempty 让旧请求的 hash 不变。
	Effort string `json:"effort,omitempty"`
}

// PelicanGroupOption 作品归属分组：只决定 group_id / group_name 与可见范围。
type PelicanGroupOption struct {
	ID       int64    `json:"id"`
	Name     string   `json:"name"`
	Platform string   `json:"platform"`
	Models   []string `json:"models"`
}

// PelicanAccountOption 可选的本站账号，只回展示字段，密钥不出后端。
type PelicanAccountOption struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
	BaseURL  string `json:"base_url"`
	// GroupIDs 该账号归属的分组 id（含 composite 分组的跨平台归属）。管理端据此
	// 按归属分组过滤账号选择项；为空表示账号没挂任何启用中的分组。
	GroupIDs []int64 `json:"group_ids"`
}

type PelicanService struct {
	Repo    *repository.PelicanRepo
	pg      pelicanPG
	gateway *PelicanGateway
	mu      sync.Mutex
	jobs    map[int64]context.CancelFunc
	workers sync.WaitGroup
	closing bool
}

func NewPelicanService(repo *repository.PelicanRepo, pg pelicanPG) *PelicanService {
	return &PelicanService{Repo: repo, pg: pg, gateway: NewPelicanGateway(), jobs: map[int64]context.CancelFunc{}}
}
func (s *PelicanService) Close() {
	s.mu.Lock()
	s.closing = true
	for id, cancel := range s.jobs {
		cancel()
		ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		_ = s.Repo.Cancel(ctx, id)
		stop()
	}
	s.mu.Unlock()
	s.workers.Wait()
}

// Groups 列出可选的作品归属分组，并带上分组实际可用的模型作为输入提示。
func (s *PelicanService) Groups(ctx context.Context) ([]PelicanGroupOption, error) {
	groups, err := s.pg.ListGroups(ctx)
	if err != nil {
		return nil, ErrPelicanUnavailable
	}
	models, err := s.pg.ListGroupAvailableModels(ctx)
	if err != nil {
		return nil, ErrPelicanUnavailable
	}
	byGroup := map[int64][]string{}
	for _, m := range models {
		byGroup[m.GroupID] = m.Models
	}
	out := make([]PelicanGroupOption, 0, len(groups))
	for _, g := range groups {
		if g.Status != "active" {
			continue
		}
		list := byGroup[g.ID]
		if list == nil {
			list = []string{}
		}
		out = append(out, PelicanGroupOption{ID: g.ID, Name: g.Name, Platform: g.Platform, Models: list})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Accounts 列出可选的本站上游账号（anthropic / openai 的 api_key 账号）。
func (s *PelicanService) Accounts(ctx context.Context) ([]PelicanAccountOption, error) {
	accounts, err := s.pg.ListProbeCandidates(ctx)
	if err != nil {
		return nil, ErrPelicanUnavailable
	}
	groupIDs, err := s.pg.ListAccountGroupIDs(ctx)
	if err != nil {
		return nil, ErrPelicanUnavailable
	}
	out := make([]PelicanAccountOption, 0, len(accounts))
	for _, a := range accounts {
		if !pelicanPlatform(a.Platform) || strings.TrimSpace(a.APIKey) == "" {
			continue
		}
		base := strings.TrimRight(strings.TrimSpace(a.BaseURL), "/")
		if base == "" {
			continue
		}
		groups := groupIDs[a.ID]
		if groups == nil {
			groups = []int64{}
		}
		out = append(out, PelicanAccountOption{ID: a.ID, Name: a.Name, Platform: strings.ToLower(strings.TrimSpace(a.Platform)), BaseURL: base, GroupIDs: groups})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].ID < out[j].ID
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func pelicanPlatform(platform string) bool {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "anthropic", "openai":
		return true
	default:
		return false
	}
}

// normalizePelicanBaseURL 归一化手填地址：去掉具体生成路径后缀，禁止内嵌凭据与查询串。
func normalizePelicanBaseURL(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	u, err := url.Parse(v)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", ErrPelicanInvalid
	}
	out, err := modeldetect.NormalizeBaseURL(v)
	if err != nil {
		return "", ErrPelicanInvalid
	}
	for _, suffix := range []string{"/v1/messages/count_tokens", "/v1/messages", "/v1/responses", "/v1/chat/completions", "/messages", "/responses", "/chat/completions"} {
		if strings.HasSuffix(strings.ToLower(out), suffix) {
			out = strings.TrimRight(out[:len(out)-len(suffix)], "/")
			break
		}
	}
	if out == "" || len(out) > pelicanMaxBaseURLLen {
		return "", ErrPelicanInvalid
	}
	return out, nil
}

// pelicanProtocolPlatform 校验协议与账号平台是否匹配。
func pelicanProtocolPlatform(protocol, platform string) error {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "anthropic":
		if protocol != "messages" {
			return ErrPelicanInvalid
		}
	case "openai":
		if protocol == "messages" {
			return ErrPelicanInvalid
		}
	default:
		return errors.New("pelican.errors.accountUnavailable")
	}
	return nil
}

// defaultPelicanPrompt 全部目标都走 Messages（Claude）时用 Claude 题目，否则用通用题目。
func defaultPelicanPrompt(targets []PelicanTarget) string {
	if len(targets) == 0 {
		return DefaultPelicanPrompt
	}
	for _, t := range targets {
		if strings.TrimSpace(t.Protocol) != "messages" {
			return DefaultPelicanPrompt
		}
	}
	return DefaultPelicanClaudePrompt
}

func normalizePelicanRequest(r *PelicanRequest) error {
	if strings.TrimSpace(r.Prompt) == "" {
		r.Prompt = defaultPelicanPrompt(r.Targets)
	}
	r.Effort = strings.TrimSpace(r.Effort)
	if r.Effort != "" && !slices.Contains(modeldetect.EffortLevels, r.Effort) {
		return ErrPelicanInvalid
	}
	if r.Concurrency == 0 {
		r.Concurrency = 3
	}
	if r.MaxTokens == 0 {
		r.MaxTokens = 32000
	}
	if r.TimeoutSeconds == 0 {
		r.TimeoutSeconds = 900
	}
	if len(r.RequestID) < 8 || len(r.RequestID) > 128 || len(r.Targets) == 0 || len(r.Targets) > 100 || len(r.Prompt) > 65536 || r.Concurrency < 1 || r.Concurrency > 10 || r.MaxTokens < 1 || r.MaxTokens > 262144 || r.TimeoutSeconds < 1 || r.TimeoutSeconds > 3600 {
		return ErrPelicanInvalid
	}
	seen := map[string]bool{}
	for i := range r.Targets {
		t := &r.Targets[i]
		t.Model = strings.TrimSpace(t.Model)
		t.Protocol = strings.TrimSpace(t.Protocol)
		if t.GroupID <= 0 || t.Model == "" || len(t.Model) > 256 || (t.Protocol != "messages" && t.Protocol != "responses" && t.Protocol != "chat") {
			return ErrPelicanInvalid
		}
		switch t.Source {
		case PelicanSourceAccount:
			if t.AccountID <= 0 || t.BaseURL != "" || t.APIKey != "" {
				return ErrPelicanInvalid
			}
		case PelicanSourceManual:
			if t.AccountID != 0 || strings.TrimSpace(t.APIKey) == "" || len(t.APIKey) > pelicanMaxAPIKeyLen {
				return ErrPelicanInvalid
			}
			base, err := normalizePelicanBaseURL(t.BaseURL)
			if err != nil {
				return err
			}
			t.BaseURL = base
		default:
			return ErrPelicanInvalid
		}
		key := fmt.Sprintf("%d|%s|%s|%d|%s", t.GroupID, t.Model, t.Source, t.AccountID, t.BaseURL)
		if seen[key] {
			return ErrPelicanInvalid
		}
		seen[key] = true
	}
	return nil
}

// sanitizePelicanRequest 剥掉手填 Key，写入任务表前必须调用。
func sanitizePelicanRequest(r PelicanRequest) PelicanRequest {
	out := r
	out.Targets = make([]PelicanTarget, len(r.Targets))
	copy(out.Targets, r.Targets)
	for i := range out.Targets {
		out.Targets[i].APIKey = ""
	}
	return out
}

// pelicanCredential 一个目标实际发请求用的地址与密钥，只留在进程内。
type pelicanCredential struct {
	base string
	key  string
}

func pelicanTargetCredential(t PelicanTarget, accounts map[int64]repository.PGAccount) (pelicanCredential, error) {
	if t.Source == PelicanSourceManual {
		return pelicanCredential{base: t.BaseURL, key: t.APIKey}, nil
	}
	a, ok := accounts[t.AccountID]
	if !ok || strings.TrimSpace(a.APIKey) == "" {
		return pelicanCredential{}, errors.New("pelican.errors.accountUnavailable")
	}
	if err := pelicanProtocolPlatform(t.Protocol, a.Platform); err != nil {
		return pelicanCredential{}, err
	}
	base, err := normalizePelicanBaseURL(a.BaseURL)
	if err != nil {
		return pelicanCredential{}, errors.New("pelican.errors.accountUnavailable")
	}
	return pelicanCredential{base: base, key: a.APIKey}, nil
}

func (s *PelicanService) Start(ctx context.Context, r PelicanRequest) (*repository.PelicanBatch, error) {
	if err := normalizePelicanRequest(&r); err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(r)
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	// A retry of an accepted submission must work even if accounts/groups later changed.
	if existing, err := s.Repo.FindBatch(ctx, r.RequestID); err == nil {
		if existing.Hash != hash {
			return nil, repository.ErrPelicanConflict
		}
		return existing, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	groups, err := s.Groups(ctx)
	if err != nil {
		return nil, err
	}
	groupNames := map[int64]string{}
	for _, g := range groups {
		groupNames[g.ID] = g.Name
	}
	accounts := map[int64]repository.PGAccount{}
	needAccounts := false
	for _, t := range r.Targets {
		if t.Source == PelicanSourceAccount {
			needAccounts = true
			break
		}
	}
	if needAccounts {
		list, err := s.pg.ListProbeCandidates(ctx)
		if err != nil {
			return nil, ErrPelicanUnavailable
		}
		for _, a := range list {
			accounts[a.ID] = a
		}
	}
	results := make([]*repository.PelicanResult, 0, len(r.Targets))
	creds := make([]pelicanCredential, 0, len(r.Targets))
	for _, t := range r.Targets {
		name := groupNames[t.GroupID]
		if name == "" {
			return nil, ErrPelicanInvalid
		}
		cred, err := pelicanTargetCredential(t, accounts)
		if err != nil {
			return nil, err
		}
		if t.RetryOf != 0 {
			old, e := s.Repo.Get(ctx, t.RetryOf)
			if e != nil {
				return nil, e
			}
			if old.GroupID != t.GroupID || old.Model != t.Model || old.Status == "queued" || old.Status == "running" {
				return nil, ErrPelicanInvalid
			}
		}
		meta := repository.PelicanMetadata{GroupName: name, Protocol: t.Protocol, Source: t.Source, AccountID: t.AccountID, BaseURL: t.BaseURL, Prompt: r.Prompt, MaxTokens: r.MaxTokens, TimeoutSeconds: r.TimeoutSeconds, Effort: r.Effort, RetryOf: t.RetryOf}
		if t.Source == PelicanSourceAccount {
			// 账号名随记录快照：账号改名或删除后，管理端作品卡片仍能显示当时的名字。
			meta.AccountName = accounts[t.AccountID].Name
		}
		if t.Source == PelicanSourceManual {
			meta.KeyFP = keyidentity.Fingerprint(t.APIKey)
		}
		results = append(results, &repository.PelicanResult{GroupID: t.GroupID, Model: t.Model, Status: "queued", PelicanMetadata: meta})
		creds = append(creds, cred)
	}
	stored, err := json.Marshal(sanitizePelicanRequest(r))
	if err != nil {
		return nil, err
	}
	batch := &repository.PelicanBatch{RequestID: r.RequestID, Hash: hash, Request: stored}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return nil, errors.New("pelican.errors.unavailable")
	}
	created, err := s.Repo.Create(ctx, batch, results)
	if err != nil || !created {
		return batch, err
	}
	jobCtx, cancel := context.WithCancel(context.Background())
	s.jobs[batch.ID] = cancel
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		defer cancel()
		defer func() { s.mu.Lock(); delete(s.jobs, batch.ID); s.mu.Unlock() }()
		s.run(jobCtx, creds, r.Concurrency, results)
	}()
	return batch, nil
}

func (s *PelicanService) run(ctx context.Context, creds []pelicanCredential, concurrency int, results []*repository.PelicanResult) {
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	defer wg.Wait()
	for i, v := range results {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		wg.Add(1)
		go func(v *repository.PelicanResult, cred pelicanCredential) {
			defer wg.Done()
			defer func() { <-sem }()
			s.execute(ctx, cred, v)
		}(v, creds[i])
	}
}

// execute 用批次开始时解析好的凭据发请求：手填 Key 不落库，只能靠这份内存快照。
func (s *PelicanService) execute(parent context.Context, cred pelicanCredential, v *repository.PelicanResult) {
	if parent.Err() != nil {
		v.Status = "cancelled"
		finished := time.Now().UTC()
		v.FinishedAt = &finished
		s.persistResult(v)
		return
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(v.TimeoutSeconds)*time.Second)
	defer cancel()
	start := time.Now().UTC()
	v.StartedAt = &start
	v.Status = "running"
	if err := s.Repo.Update(ctx, v); err != nil {
		if !errors.Is(err, repository.ErrPelicanConflict) {
			v.Status = "failed"
			v.Error = "pelican.errors.failed"
			if parent.Err() != nil {
				v.Status = "cancelled"
				v.Error = ""
			}
			finished := time.Now().UTC()
			v.FinishedAt = &finished
			s.persistResult(v)
		}
		return
	}
	var err error
	if strings.TrimSpace(cred.base) == "" || strings.TrimSpace(cred.key) == "" {
		err = errors.New("pelican.errors.credentialsUnavailable")
	} else {
		v.Output, err = s.gateway.Generate(ctx, cred.base, cred.key, v)
	}
	end := time.Now().UTC()
	v.FinishedAt = &end
	v.DurationMs = end.Sub(start).Milliseconds()
	v.Status = "completed"
	if err != nil {
		v.Status = "failed"
		v.Error = err.Error()
	}
	if parent.Err() != nil {
		v.Status = "cancelled"
		v.Error = ""
	}
	v.HasDocument = v.Output != nil && v.Output.Document != ""
	// A transport success without a document is retained for diagnosis, not publishable.
	if v.Status == "completed" && !v.HasDocument {
		v.Status = "failed"
		v.Error = "pelican.errors.noDocument"
	}
	s.persistResult(v)
}

// pelicanPersistTimeout 单次落库超时：库在远端、回复正文可达数百 KB，5 秒不够用。
const pelicanPersistTimeout = 30 * time.Second

func (s *PelicanService) persistResult(v *repository.PelicanResult) {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), pelicanPersistTimeout)
		err = s.Repo.Update(ctx, v)
		cancel()
		if err == nil || errors.Is(err, repository.ErrPelicanConflict) {
			return
		}
		log.Printf("[pelican] persist result %d attempt %d failed: %v", v.ID, attempt+1, err)
		if attempt < 2 {
			time.Sleep(time.Duration(attempt+1) * time.Second)
		}
	}
	// 带正文写不进去（内容被库拒绝或过大）时退一步只记失败，不让记录一直停在「生成中」。
	fallback := *v
	fallback.Output = nil
	fallback.HasDocument = false
	if fallback.Status != "cancelled" {
		fallback.Status = "failed"
		fallback.Error = "pelican.errors.persistFailed"
	}
	ctx, cancel := context.WithTimeout(context.Background(), pelicanPersistTimeout)
	defer cancel()
	if e := s.Repo.Update(ctx, &fallback); e != nil && !errors.Is(e, repository.ErrPelicanConflict) {
		log.Printf("[pelican] cannot persist result %d (last error: %v); recovery will mark it interrupted on restart", v.ID, e)
	}
}
func (s *PelicanService) Cancel(ctx context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Stop chargeable work even if persistence is unavailable during cancellation.
	if cancel := s.jobs[id]; cancel != nil {
		cancel()
	}
	if _, err := s.Repo.Batch(ctx, id); err != nil {
		return err
	}
	if err := s.Repo.Cancel(ctx, id); err != nil {
		return err
	}
	return nil
}
func (s *PelicanService) VisibleGroups(ctx context.Context) ([]int64, error) {
	groups, err := s.pg.PelicanVisibleGroups(ctx)
	if err != nil {
		return nil, ErrPelicanUnavailable
	}
	return groups, nil
}
