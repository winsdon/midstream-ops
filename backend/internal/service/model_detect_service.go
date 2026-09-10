package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"sub2api-account-monitor/internal/repository"
	"sub2api-account-monitor/internal/service/modeldetect"
)

// 作业状态。
const (
	DetectJobRunning   = "running"
	DetectJobCompleted = "completed"
	DetectJobCancelled = "cancelled"
)

// detectJobTTL 作业快照在内存中的保留时长。结果本身已落库，
// 这里只是给前端轮询留出取回窗口，过期即清。
const detectJobTTL = 2 * time.Hour

// maxDetectTargets 单次作业的目标上限。检测会真实消耗上游额度，
// 一次勾几十个账号既慢又贵，宁可让用户分批跑。
const maxDetectTargets = 10

const (
	defaultDetectConcurrency = 3
	maxDetectConcurrency     = 10
)

// ErrDetectPGUnavailable 选了本站账号但线上库不可用。
var ErrDetectPGUnavailable = errors.New("线上数据库暂不可用，无法读取账号密钥")

var (
	ErrDetectJobNotFound = errors.New("作业不存在或已过期")
	ErrDetectJobBusy     = errors.New("检测尚未结束，请等待完成后再重试")
	ErrDetectRetryNone   = errors.New("没有可重试的请求失败项")
)

// DetectTargetInput 一个待检测目标：要么指向本站账号，要么是手填渠道。
type DetectTargetInput struct {
	AccountID *int64 `json:"account_id"`
	Name      string `json:"name"`
	BaseURL   string `json:"base_url"`
	APIKey    string `json:"api_key"`
}

// DetectRunRequest 发起检测的请求。
type DetectRunRequest struct {
	Targets      []DetectTargetInput `json:"targets"`
	Model        string              `json:"model"`
	AuthMode     string              `json:"auth_mode"`
	Checks       []string            `json:"checks"`
	ExtraHeaders map[string]string   `json:"extra_headers"`
	TimeoutMs    int                 `json:"timeout_ms"`
	// Concurrency 同时执行几项检测（跨目标共享）。0 / 缺省按 3；超出 1–10 夹紧，不 400。
	Concurrency int `json:"concurrency"`
}

// DetectRetryRequest 重试作业里请求失败的检测项。
//
// CheckID 为空时重试范围内所有请求失败项；指定时只重试该项（及其因前置失败而没发出请求的后续项）。
// 多目标时必须带 TargetIndex。
type DetectRetryRequest struct {
	TargetIndex *int   `json:"target_index"`
	CheckID     string `json:"check_id"`
}

// DetectJob 一次检测作业的进度与结果快照（可直接序列化给前端）。
type DetectJob struct {
	ID          string                   `json:"id"`
	Status      string                   `json:"status"`
	Checks      []string                 `json:"checks"`
	Total       int                      `json:"total"`
	Done        int                      `json:"done"`
	Current     string                   `json:"current"`
	Concurrency int                      `json:"concurrency"`
	Targets     []*modeldetect.TargetRun `json:"targets"`
	CreatedAt   time.Time                `json:"created_at"`
	UpdatedAt   time.Time                `json:"updated_at"`
}

// ModelDetectService 编排渠道检测：解析目标 → 按并发度执行 → 落库 → 供前端轮询。
type ModelDetectService struct {
	repo         *repository.ModelDetectionRepo
	pg           *repository.PG
	linkRepo     *repository.ProviderAccountRepo
	providerRepo *repository.ProviderRepo

	mu   sync.RWMutex
	jobs map[string]*detectJob
}

// detectJob 是作业的内部形态：快照 + 取消钩子。
type detectJob struct {
	mu      sync.RWMutex
	snap    DetectJob
	cancel  context.CancelFunc
	targets []modeldetect.Target // 含密钥，只留在进程内供重试，不进快照
}

// NewModelDetectService 创建服务。
func NewModelDetectService(repo *repository.ModelDetectionRepo, pg *repository.PG,
	linkRepo *repository.ProviderAccountRepo, providerRepo *repository.ProviderRepo) *ModelDetectService {
	return &ModelDetectService{repo: repo, pg: pg, linkRepo: linkRepo,
		providerRepo: providerRepo, jobs: map[string]*detectJob{}}
}

// Repo 暴露存储（供 handler 查历史）。
func (s *ModelDetectService) Repo() *repository.ModelDetectionRepo { return s.repo }

// DetectAccount 可检测的本站账号（不含密钥）。
type DetectAccount struct {
	AccountID    int64    `json:"account_id"`
	AccountName  string   `json:"account_name"`
	Platform     string   `json:"platform"`
	BaseURL      string   `json:"base_url"`
	ProviderID   int64    `json:"provider_id"`
	ProviderName string   `json:"provider_name"`
	ProbeModel   string   `json:"probe_model"`
	Groups       []string `json:"groups"`
}

// ListAccounts 列出可检测的 anthropic 账号。
//
// 只返回展示字段：密钥永远不出后端，前端提交时只带 account_id。
func (s *ModelDetectService) ListAccounts(ctx context.Context) ([]DetectAccount, error) {
	if s.pg == nil || !s.pg.Available() {
		return nil, ErrDetectPGUnavailable
	}
	accounts, err := s.pg.ListProbeCandidates(ctx)
	if err != nil {
		return nil, err
	}
	linkMap, _ := s.linkRepo.AccountToProvider(ctx)
	nameByID, _ := s.providerRepo.NameByID(ctx)
	groupsByAccount, _ := s.pg.AccountGroups(ctx)

	out := make([]DetectAccount, 0, len(accounts))
	for _, a := range accounts {
		if !strings.EqualFold(a.Platform, "anthropic") || a.APIKey == "" {
			continue
		}
		groups := groupsByAccount[a.ID]
		if groups == nil {
			groups = []string{}
		}
		item := DetectAccount{
			AccountID: a.ID, AccountName: a.Name, Platform: strings.ToLower(a.Platform),
			BaseURL: strings.TrimRight(a.BaseURL, "/"), Groups: groups,
		}
		if pid := linkMap[a.ID]; pid > 0 {
			item.ProviderID = pid
			item.ProviderName = nameByID[pid]
			if p, err := s.providerRepo.GetByID(ctx, pid); err == nil && p.ProbeModel != nil {
				item.ProbeModel = *p.ProbeModel
			}
		}
		out = append(out, item)
	}
	return out, nil
}

// Start 解析目标并异步执行检测，返回作业 id。
//
// 目标解析在返回前完成：地址不合法、账号查不到这类错误应当立刻反馈，
// 而不是让用户轮询半天才看到一行「目标无效」。
func (s *ModelDetectService) Start(ctx context.Context, req DetectRunRequest) (string, error) {
	if len(req.Targets) == 0 {
		return "", errors.New("至少选择一个检测目标")
	}
	if len(req.Targets) > maxDetectTargets {
		return "", fmt.Errorf("单次最多检测 %d 个目标", maxDetectTargets)
	}

	targets, err := s.resolveTargets(ctx, req)
	if err != nil {
		return "", err
	}
	checks := modeldetect.ResolveCheckIDs(req.Checks)

	jobID, err := newJobID()
	if err != nil {
		return "", err
	}
	runCtx, cancel := context.WithCancel(context.Background())
	now := time.Now()
	concurrency := clampDetectConcurrency(req.Concurrency)
	job := &detectJob{
		snap: DetectJob{
			ID: jobID, Status: DetectJobRunning, Checks: checks,
			Total: len(targets) * len(checks), Concurrency: concurrency,
			CreatedAt: now, UpdatedAt: now,
		},
		cancel:  cancel,
		targets: targets,
	}
	for _, t := range targets {
		job.snap.Targets = append(job.snap.Targets, &modeldetect.TargetRun{
			Name: t.Name, BaseURL: t.BaseURL, Model: t.Model, AuthMode: t.AuthMode,
			AccountID: t.AccountID, ProviderID: t.ProviderID,
		})
	}

	s.mu.Lock()
	s.jobs[jobID] = job
	s.mu.Unlock()
	s.evictExpired()

	go s.run(runCtx, job, targets, checks)
	return jobID, nil
}

// resolveTargets 把请求里的目标解析成可执行的 Target（含密钥）。
func (s *ModelDetectService) resolveTargets(ctx context.Context, req DetectRunRequest) ([]modeldetect.Target, error) {
	var accounts []repository.PGAccount
	needAccounts := false
	for _, t := range req.Targets {
		if t.AccountID != nil {
			needAccounts = true
			break
		}
	}
	if needAccounts {
		if s.pg == nil || !s.pg.Available() {
			return nil, ErrDetectPGUnavailable
		}
		list, err := s.pg.ListProbeCandidates(ctx)
		if err != nil {
			return nil, err
		}
		accounts = list
	}

	timeout := time.Duration(req.TimeoutMs) * time.Millisecond
	out := make([]modeldetect.Target, 0, len(req.Targets))
	for i, in := range req.Targets {
		target := modeldetect.Target{
			Name: in.Name, BaseURL: in.BaseURL, APIKey: in.APIKey,
			Model: req.Model, AuthMode: req.AuthMode,
			ExtraHeaders: req.ExtraHeaders, Timeout: timeout,
		}
		if in.AccountID != nil {
			acc, ok := findAccountByID(accounts, *in.AccountID)
			if !ok {
				return nil, fmt.Errorf("第 %d 个目标：账号 %d 不存在或不可检测", i+1, *in.AccountID)
			}
			target.AccountID = &acc.ID
			target.APIKey = acc.APIKey
			if target.Name == "" {
				target.Name = acc.Name
			}
			if target.BaseURL == "" {
				target.BaseURL = acc.BaseURL
			}
			// 未指定模型时，跟随该账号所属供应商的探测模型
			if target.Model == "" {
				if pid, err := s.linkRepo.ProviderIDOf(ctx, acc.ID); err == nil && pid > 0 {
					target.ProviderID = &pid
					if p, err := s.providerRepo.GetByID(ctx, pid); err == nil && p.ProbeModel != nil {
						target.Model = *p.ProbeModel
					}
				}
			} else if pid, err := s.linkRepo.ProviderIDOf(ctx, acc.ID); err == nil && pid > 0 {
				target.ProviderID = &pid
			}
		}
		normalized, err := target.Normalize()
		if err != nil {
			label := target.Name
			if label == "" {
				label = fmt.Sprintf("第 %d 个目标", i+1)
			}
			return nil, fmt.Errorf("%s：%w", label, err)
		}
		out = append(out, normalized)
	}
	return out, nil
}

func findAccountByID(list []repository.PGAccount, id int64) (repository.PGAccount, bool) {
	for _, a := range list {
		if a.ID == id {
			return a, true
		}
	}
	return repository.PGAccount{}, false
}

// run 所有目标同时入队，检测项竞争同一把闸门。无依赖的项并行，
// ping-again / 签名篡改等有 Requires 的项仍等前置完成。
func (s *ModelDetectService) run(ctx context.Context, job *detectJob,
	targets []modeldetect.Target, checks []string) {
	gate := modeldetect.NewGate(job.concurrency())
	var wg sync.WaitGroup
	for i, target := range targets {
		wg.Add(1)
		go func(i int, target modeldetect.Target) {
			defer wg.Done()
			if ctx.Err() != nil {
				return
			}
			result := modeldetect.Run(ctx, target, checks, gate, func(res *modeldetect.CheckResult) {
				job.onCheckProgress(i, res)
			})
			job.setTargetResult(i, result)
			s.persist(context.Background(), target, result)
		}(i, target)
	}
	wg.Wait()

	status := DetectJobCompleted
	if ctx.Err() != nil {
		status = DetectJobCancelled
	}
	job.setStatus(status)
}

// Retry 重跑作业里请求失败的检测项。协议失败（非法参数被接受等）不重试。
func (s *ModelDetectService) Retry(jobID string, req DetectRetryRequest) (int, error) {
	s.mu.RLock()
	job, ok := s.jobs[jobID]
	s.mu.RUnlock()
	if !ok {
		return 0, ErrDetectJobNotFound
	}

	plan, err := job.planRetry(req)
	if err != nil {
		return 0, err
	}
	runCtx, cancel := context.WithCancel(context.Background())
	if !job.beginRetry(cancel) {
		cancel()
		return 0, ErrDetectJobBusy
	}
	go s.runRetry(runCtx, job, plan)
	return plan.count(), nil
}

type retryPlan struct {
	items []retryItem
}

type retryItem struct {
	index  int
	target modeldetect.Target
	checks []string
}

func (p retryPlan) count() int {
	n := 0
	for _, it := range p.items {
		n += len(it.checks)
	}
	return n
}

func (j *detectJob) planRetry(req DetectRetryRequest) (retryPlan, error) {
	j.mu.RLock()
	defer j.mu.RUnlock()
	if j.snap.Status == DetectJobRunning {
		return retryPlan{}, ErrDetectJobBusy
	}
	n := len(j.snap.Targets)
	if n == 0 || len(j.targets) != n {
		return retryPlan{}, ErrDetectRetryNone
	}

	var indexes []int
	if req.TargetIndex != nil {
		i := *req.TargetIndex
		if i < 0 || i >= n {
			return retryPlan{}, fmt.Errorf("目标序号 %d 超出范围", i)
		}
		indexes = []int{i}
	} else if req.CheckID != "" && n > 1 {
		return retryPlan{}, errors.New("多个目标时请指定 target_index")
	} else {
		for i := 0; i < n; i++ {
			indexes = append(indexes, i)
		}
	}

	var plan retryPlan
	for _, i := range indexes {
		run := j.snap.Targets[i]
		seed, err := retrySeed(run, req.CheckID)
		if err != nil {
			return retryPlan{}, err
		}
		ids := modeldetect.ExpandRetryIDs(seed, run.Checks, j.snap.Checks)
		if len(ids) == 0 {
			continue
		}
		plan.items = append(plan.items, retryItem{index: i, target: j.targets[i], checks: ids})
	}
	if plan.count() == 0 {
		return retryPlan{}, ErrDetectRetryNone
	}
	return plan, nil
}

func retrySeed(run *modeldetect.TargetRun, checkID string) ([]string, error) {
	if run == nil {
		return nil, ErrDetectRetryNone
	}
	if checkID != "" {
		c := findJobCheck(run, checkID)
		if !modeldetect.RequestFailed(c) {
			title := checkID
			if c != nil && c.Title != "" {
				title = c.Title
			}
			return nil, fmt.Errorf("%s 不是请求失败，无法重试", title)
		}
		return []string{checkID}, nil
	}
	var seed []string
	for _, c := range run.Checks {
		if modeldetect.RequestFailed(c) {
			seed = append(seed, c.ID)
		}
	}
	return seed, nil
}

func (j *detectJob) beginRetry(cancel context.CancelFunc) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.snap.Status == DetectJobRunning {
		return false
	}
	j.cancel = cancel
	j.snap.Status = DetectJobRunning
	j.snap.Current = "准备重试"
	j.snap.UpdatedAt = time.Now()
	return true
}

func (s *ModelDetectService) runRetry(ctx context.Context, job *detectJob, plan retryPlan) {
	gate := modeldetect.NewGate(job.concurrency())
	var wg sync.WaitGroup
	for _, item := range plan.items {
		wg.Add(1)
		go func(item retryItem) {
			defer wg.Done()
			if ctx.Err() != nil {
				return
			}
			prev, _ := job.targetSnapshot(item.index)
			result := modeldetect.RetryChecks(ctx, item.target, item.checks, prev, gate, func(res *modeldetect.CheckResult) {
				job.onCheckProgress(item.index, res)
			})
			job.setTargetResult(item.index, result)
			s.persist(context.Background(), item.target, result)
		}(item)
	}
	wg.Wait()

	status := DetectJobCompleted
	if ctx.Err() != nil {
		status = DetectJobCancelled
	}
	job.setStatus(status)
}

func (j *detectJob) targetSnapshot(idx int) (*modeldetect.TargetRun, bool) {
	j.mu.RLock()
	defer j.mu.RUnlock()
	if idx < 0 || idx >= len(j.snap.Targets) || j.snap.Targets[idx] == nil {
		return nil, false
	}
	cp := *j.snap.Targets[idx]
	cp.Checks = append([]*modeldetect.CheckResult(nil), j.snap.Targets[idx].Checks...)
	return &cp, true
}

// clampDetectConcurrency 把页面传来的并发度夹进 [1, 10]，0 / 缺省视为 3。
func clampDetectConcurrency(n int) int {
	if n == 0 {
		return defaultDetectConcurrency
	}
	if n < 1 {
		return 1
	}
	if n > maxDetectConcurrency {
		return maxDetectConcurrency
	}
	return n
}

// persist 把脱敏后的报告落库。写失败只记日志：结果已经在内存里，
// 让用户看不到本轮结论比丢一条历史更糟。
func (s *ModelDetectService) persist(ctx context.Context, target modeldetect.Target, run *modeldetect.TargetRun) {
	if s.repo == nil || run == nil || run.Verdict == nil {
		return
	}
	scores, _ := json.Marshal(run.Verdict.Scores)
	report, err := json.Marshal(run)
	if err != nil {
		log.Printf("[detect] 序列化报告失败: %v", err)
		return
	}
	rec := &repository.ModelDetection{
		AccountID:         target.AccountID,
		AccountName:       target.Name,
		ProviderID:        target.ProviderID,
		TargetFP:          TargetFingerprint(target.BaseURL, target.APIKey),
		TargetName:        target.Name,
		BaseURL:           target.BaseURL,
		Model:             target.Model,
		Label:             run.Verdict.Label,
		Confidence:        run.Verdict.Confidence,
		AuthenticityScore: run.Verdict.Authenticity.Score,
		AuthenticityGrade: run.Verdict.Authenticity.Grade,
		Scores:            scores,
		Report:            report,
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := s.repo.Insert(timeoutCtx, rec); err != nil {
		log.Printf("[detect] 写入检测历史失败: %v", err)
	}
}

// TargetFingerprint 目标同一性指纹：base_url + key 的哈希前缀。
// 存指纹而非密钥，既能把同一条渠道的多次检测串成历史，又不留下可用的凭据。
func TargetFingerprint(baseURL, apiKey string) string {
	sum := sha256.Sum256([]byte(strings.TrimRight(baseURL, "/") + "|" + apiKey))
	return hex.EncodeToString(sum[:])[:16]
}

// Get 取作业快照。
func (s *ModelDetectService) Get(jobID string) (*DetectJob, bool) {
	s.mu.RLock()
	job, ok := s.jobs[jobID]
	s.mu.RUnlock()
	if !ok {
		return nil, false
	}
	return job.snapshot(), true
}

// Cancel 取消作业。已发出的请求上游仍会执行完，本地不再继续新的检测项。
func (s *ModelDetectService) Cancel(jobID string) bool {
	s.mu.RLock()
	job, ok := s.jobs[jobID]
	s.mu.RUnlock()
	if !ok {
		return false
	}
	job.cancel()
	return true
}

// evictExpired 清理过期作业快照。
func (s *ModelDetectService) evictExpired() {
	cutoff := time.Now().Add(-detectJobTTL)
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, job := range s.jobs {
		job.mu.RLock()
		stale := job.snap.UpdatedAt.Before(cutoff)
		job.mu.RUnlock()
		if stale {
			delete(s.jobs, id)
		}
	}
}

// Cleanup 按保留天数清理检测历史（调度器调用）。
func (s *ModelDetectService) Cleanup(ctx context.Context, retentionDays int) {
	if s.repo == nil || retentionDays <= 0 {
		return
	}
	before := time.Now().AddDate(0, 0, -retentionDays)
	if n, err := s.repo.DeleteOlderThan(ctx, before); err == nil && n > 0 {
		log.Printf("[cleanup] model_detections 删除 %d 行", n)
	}
	s.evictExpired()
}

// ---- detectJob 的并发安全存取 ----

func (j *detectJob) onCheckProgress(targetIdx int, res *modeldetect.CheckResult) {
	if res == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if targetIdx < len(j.snap.Targets) && j.snap.Targets[targetIdx] != nil {
		upsertJobCheck(j.snap.Targets[targetIdx], res)
	}
	j.snap.Done = countFinished(j.snap.Targets)
	j.snap.Current = runningSummary(j.snap.Targets, j.snap.Concurrency)
	j.snap.UpdatedAt = time.Now()
}

// countFinished 统计已结束的检测项，用于进度显示。
//
// 跨项审计不计入：它不是用户勾选的项，也不发请求，Total 里没有它的份额。
func countFinished(targets []*modeldetect.TargetRun) int {
	n := 0
	for _, t := range targets {
		if t == nil {
			continue
		}
		for _, c := range t.Checks {
			if c == nil || c.ID == modeldetect.AuditCheckID {
				continue
			}
			if c.Status != modeldetect.StatusRunning {
				n++
			}
		}
	}
	return n
}

func findJobCheck(run *modeldetect.TargetRun, id string) *modeldetect.CheckResult {
	if run == nil {
		return nil
	}
	for _, c := range run.Checks {
		if c != nil && c.ID == id {
			return c
		}
	}
	return nil
}

func upsertJobCheck(run *modeldetect.TargetRun, res *modeldetect.CheckResult) {
	for i, c := range run.Checks {
		if c != nil && c.ID == res.ID {
			run.Checks[i] = res
			return
		}
	}
	run.Checks = append(run.Checks, res)
}

func runningSummary(targets []*modeldetect.TargetRun, concurrency int) string {
	var parts []string
	for _, t := range targets {
		if t == nil {
			continue
		}
		for _, c := range t.Checks {
			if c != nil && c.Status == modeldetect.StatusRunning {
				parts = append(parts, t.Name+" · "+c.Title)
			}
		}
	}
	if len(parts) == 0 {
		return ""
	}
	if concurrency > 1 {
		return fmt.Sprintf("%d 路并发 · %s", concurrency, strings.Join(parts, " ｜ "))
	}
	return strings.Join(parts, " ｜ ")
}

func (j *detectJob) concurrency() int {
	j.mu.RLock()
	defer j.mu.RUnlock()
	n := j.snap.Concurrency
	if n < 1 {
		return 1
	}
	return n
}

func (j *detectJob) setTargetResult(idx int, run *modeldetect.TargetRun) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if idx < len(j.snap.Targets) {
		j.snap.Targets[idx] = run
	}
	j.snap.Done = countFinished(j.snap.Targets)
	j.snap.UpdatedAt = time.Now()
}

func (j *detectJob) setStatus(status string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.snap.Status = status
	j.snap.Current = ""
	j.snap.UpdatedAt = time.Now()
}

// snapshot 深拷贝一层：目标切片会被并发写入，直接把内部切片交出去会数据竞争。
func (j *detectJob) snapshot() *DetectJob {
	j.mu.RLock()
	defer j.mu.RUnlock()
	out := j.snap
	out.Targets = make([]*modeldetect.TargetRun, len(j.snap.Targets))
	for i, t := range j.snap.Targets {
		if t == nil {
			continue
		}
		cp := *t
		cp.Checks = append([]*modeldetect.CheckResult(nil), t.Checks...)
		out.Targets[i] = &cp
	}
	out.Checks = append([]string(nil), j.snap.Checks...)
	return &out
}

func newJobID() (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成作业 id 失败: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
