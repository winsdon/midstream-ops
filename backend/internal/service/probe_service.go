package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"sub2api-account-monitor/internal/config"
	"sub2api-account-monitor/internal/pkg/health"
	"sub2api-account-monitor/internal/repository"
)

// 平台官方端点（base_url 缺省时回退）。
var defaultEndpoints = map[string]string{
	"anthropic": "https://api.anthropic.com",
	"openai":    "https://api.openai.com",
	"gemini":    "https://generativelanguage.googleapis.com",
}

// dailyProbeBudget 每日探测预算（防打爆上游）。
const dailyProbeBudget = 1000

// ProbeService 主动探测上游 key 的 TTFT/总耗时/成功率。
// 探测结果喂六状态健康状态机（health_states），状态转移落 health_events。
type ProbeService struct {
	probeRepo    *repository.ProbeRepo
	providerRepo *repository.ProviderRepo
	linkRepo     *repository.ProviderAccountRepo
	healthRepo   *repository.HealthRepo
	pg           *repository.PG
	cfg          *config.Config
	httpClient   *http.Client
	timeout      time.Duration

	// OnStateChanged 健康状态转移钩子（suspended/恢复通知）；装配层注入。
	OnStateChanged func(accountName string, from, to health.State, detail string)
}

// NewProbeService 创建 ProbeService。
func NewProbeService(probeRepo *repository.ProbeRepo, providerRepo *repository.ProviderRepo,
	linkRepo *repository.ProviderAccountRepo, healthRepo *repository.HealthRepo,
	pg *repository.PG, cfg *config.Config) *ProbeService {
	timeout := time.Duration(cfg.Probe.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	transport := &http.Transport{
		TLSHandshakeTimeout:   20 * time.Second,
		ResponseHeaderTimeout: timeout, // 控制「等首字节」（TTFT 上限）
		IdleConnTimeout:       30 * time.Second,
	}
	// 流式探测需读完整段流（TTFT + 全量输出），整体超时放宽为 3 倍；
	// 「等首字节」由 ResponseHeaderTimeout 单独控制。
	return &ProbeService{
		probeRepo:    probeRepo,
		providerRepo: providerRepo,
		linkRepo:     linkRepo,
		healthRepo:   healthRepo,
		pg:           pg,
		cfg:          cfg,
		httpClient:   &http.Client{Timeout: timeout*3 + 10*time.Second, Transport: transport},
		timeout:      timeout,
	}
}

// HealthRepo 暴露健康存储（供 handler 查询）。
func (s *ProbeService) HealthRepo() *repository.HealthRepo { return s.healthRepo }

// LinkRepo 暴露供应商账号关联存储（供 handler 反查归属）。
func (s *ProbeService) LinkRepo() *repository.ProviderAccountRepo { return s.linkRepo }

// 可探测平台。
var probeablePlatforms = map[string]bool{"anthropic": true, "openai": true, "gemini": true}

// CanProbe 判断平台是否支持探测。
func CanProbe(platform string) bool { return probeablePlatforms[strings.ToLower(platform)] }

// Repo 暴露 probe 存储（供 handler 查询）。
func (s *ProbeService) Repo() *repository.ProbeRepo { return s.probeRepo }

// ProviderRepo 暴露 provider 存储（供 handler 查询）。
func (s *ProbeService) ProviderRepo() *repository.ProviderRepo { return s.providerRepo }

// ProbeOutcome 探测结果（供写库）。
type ProbeOutcome struct {
	StatusCode *int
	TTFTMs     *int64
	TotalMs    *int64
	Err        *string
}

// ProbeAccount 探测单个账号，返回结果（不写库）。
// apiKey 仅用于本次请求，绝不入库/日志。
func (s *ProbeService) ProbeAccount(ctx context.Context, acc repository.PGAccount, modelOverride *string) *ProbeOutcome {
	platform := strings.ToLower(acc.Platform)
	if !CanProbe(platform) {
		e := "平台 " + platform + " 不支持探测"
		return &ProbeOutcome{Err: &e}
	}
	if acc.APIKey == "" {
		e := "账号无 api_key"
		return &ProbeOutcome{Err: &e}
	}

	model := s.modelFor(platform, modelOverride)
	target, req := s.buildRequest(ctx, platform, acc, model)
	if req == nil {
		e := "无法构造探测请求"
		return &ProbeOutcome{Err: &e}
	}

	return s.executeStream(ctx, req, target)
}

// modelFor 解析探测模型：override > config 默认 > 兜底。
func (s *ProbeService) modelFor(platform string, override *string) string {
	if override != nil && *override != "" {
		return *override
	}
	if m, ok := s.cfg.Probe.DefaultModels[platform]; ok && m != "" {
		return m
	}
	switch platform {
	case "anthropic":
		return "claude-3-5-haiku-20241022"
	case "openai":
		return "gpt-4o-mini"
	case "gemini":
		return "gemini-2.5-flash"
	}
	return ""
}

// buildRequest 按平台构造最小流式请求，返回探测目标 base（用于展示，不含 key）。
func (s *ProbeService) buildRequest(ctx context.Context, platform string, acc repository.PGAccount, model string) (string, *http.Request) {
	base := acc.BaseURL
	if base == "" {
		base = defaultEndpoints[platform]
	}
	base = strings.TrimRight(base, "/")
	// base_url 可能已含 /v1 或 /v1beta 后缀，避免重复拼接
	hasVersionSuffix := strings.HasSuffix(base, "/v1") || strings.HasSuffix(base, "/v1beta")

	var url string
	var body []byte
	var headers map[string]string

	switch platform {
	case "anthropic":
		if hasVersionSuffix {
			url = base + "/messages"
		} else {
			url = base + "/v1/messages"
		}
		body, _ = json.Marshal(map[string]any{
			"model":      model,
			"max_tokens": 16,
			"stream":     true,
			"messages":   []map[string]string{{"role": "user", "content": "ping"}},
		})
		headers = map[string]string{
			"x-api-key":         acc.APIKey,
			"anthropic-version": "2023-06-01",
			"content-type":      "application/json",
		}
	case "openai":
		if hasVersionSuffix {
			url = base + "/chat/completions"
		} else {
			url = base + "/v1/chat/completions"
		}
		body, _ = json.Marshal(map[string]any{
			"model":      model,
			"stream":     true,
			"max_tokens": 16,
			"messages":   []map[string]string{{"role": "user", "content": "ping"}},
		})
		headers = map[string]string{
			"Authorization": "Bearer " + acc.APIKey,
			"content-type":  "application/json",
		}
	case "gemini":
		if hasVersionSuffix {
			url = base + "/models/" + model + ":streamGenerateContent?alt=sse"
		} else {
			url = base + "/v1beta/models/" + model + ":streamGenerateContent?alt=sse"
		}
		body, _ = json.Marshal(map[string]any{
			"contents": []map[string]any{
				{"role": "user", "parts": []map[string]string{{"text": "ping"}}},
			},
			"generationConfig": map[string]any{"maxOutputTokens": 16},
		})
		headers = map[string]string{
			"x-goog-api-key": acc.APIKey,
			"content-type":   "application/json",
		}
	default:
		return base, nil
	}

	bodyBytes := body
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return base, nil
	}
	// 支持重试重建 body
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(bodyBytes)), nil
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	// 探测目标同为供应商 sub2api 站点，前置 WAF 会拦截非浏览器 UA
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36")
	req.Header.Set("Accept", "text/event-stream")
	return base, req
}

// executeStream 执行流式请求，测 TTFT 与总耗时（网络错误重试 1 次）。
func (s *ProbeService) executeStream(ctx context.Context, req *http.Request, target string) *ProbeOutcome {
	out := &ProbeOutcome{}
	start := time.Now()

	var resp *http.Response
	var err error
	const maxAttempts = 3 // 与余额采集一致：WAF/TLS 间歇性失败，重试换连接常能成功
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				e := ctx.Err().Error()
				out.Err = &e
				return out
			case <-time.After(800 * time.Millisecond):
			}
			// 重试需重建请求（body 已读），复制全部 header（含鉴权）
			if req.GetBody != nil {
				b, _ := req.GetBody()
				nreq, _ := http.NewRequestWithContext(ctx, req.Method, req.URL.String(), b)
				nreq.Header = req.Header.Clone()
				req = nreq
			}
		}
		resp, err = s.httpClient.Do(req)
		if err == nil {
			break
		}
	}
	if err != nil {
		total := time.Since(start).Milliseconds()
		out.TotalMs = &total
		e := redactError("请求失败: "+err.Error(), req)
		out.Err = &e
		return out
	}
	defer func() { _ = resp.Body.Close() }()

	sc := resp.StatusCode
	out.StatusCode = &sc

	// 非 2xx：读错误体，记录失败
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		total := time.Since(start).Milliseconds()
		out.TotalMs = &total
		e := redactError(fmt.Sprintf("HTTP %d: %s", resp.StatusCode, truncate(string(body), 300)), req)
		out.Err = &e
		return out
	}

	// 流式读取：首条非空 SSE 行 = TTFT
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)
	gotFirst := false
	for scanner.Scan() {
		line := scanner.Text()
		if !gotFirst && strings.TrimSpace(line) != "" && !strings.HasPrefix(line, ":") {
			ttft := time.Since(start).Milliseconds()
			out.TTFTMs = &ttft
			gotFirst = true
		}
		// 读完整个流
	}
	total := time.Since(start).Milliseconds()
	out.TotalMs = &total

	if err := scanner.Err(); err != nil {
		e := redactError("读取流失败: "+err.Error(), req)
		out.Err = &e
		return out
	}
	if !gotFirst {
		e := "未收到任何数据块"
		out.Err = &e
		return out
	}
	_ = target
	return out
}

// redactError 脱敏错误信息中的 api_key。
func redactError(msg string, req *http.Request) string {
	for _, h := range []string{"x-api-key", "Authorization", "x-goog-api-key"} {
		if v := req.Header.Get(h); v != "" {
			msg = strings.ReplaceAll(msg, v, "***")
			msg = strings.ReplaceAll(msg, strings.TrimPrefix(v, "Bearer "), "***")
		}
	}
	return truncate(msg, 500)
}

// RunProbeForAccount 探测单个账号并写库（手动/调度共用），随后喂健康状态机。
func (s *ProbeService) RunProbeForAccount(ctx context.Context, acc repository.PGAccount, modelOverride *string, source string) (*repository.ProbeResult, error) {
	platform := strings.ToLower(acc.Platform)
	model := s.modelFor(platform, modelOverride)
	outcome := s.ProbeAccount(ctx, acc, modelOverride)

	// 关联供应商（读关联表，唯一真相）。未关联时留 NULL —— 与改造前
	// 「账号名没有前缀」的行为一致，探测记录照常入库，只是查不到归属。
	var providerID *int64
	if pid, err := s.linkRepo.ProviderIDOf(ctx, acc.ID); err == nil && pid > 0 {
		providerID = &pid
	}

	base := acc.BaseURL
	if base == "" {
		base = defaultEndpoints[platform]
	}

	pr := &repository.ProbeResult{
		ProviderID:  providerID,
		AccountID:   acc.ID,
		AccountName: acc.Name,
		Platform:    platform,
		Model:       model,
		BaseURL:     strings.TrimRight(base, "/"),
		Source:      source,
		Success:     outcome.Err == nil,
		StatusCode:  outcome.StatusCode,
		TTFTMs:      outcome.TTFTMs,
		TotalMs:     outcome.TotalMs,
		Error:       outcome.Err,
	}
	if err := s.probeRepo.Insert(ctx, pr); err != nil {
		return nil, err
	}
	s.feedHealth(ctx, acc, providerID, pr)
	return pr, nil
}

// feedHealth 把探测结果喂进健康状态机，转移落库并触发通知钩子。
func (s *ProbeService) feedHealth(ctx context.Context, acc repository.PGAccount, providerID *int64, pr *repository.ProbeResult) {
	cur, err := s.healthRepo.Get(ctx, acc.ID)
	if err != nil {
		log.Printf("[health] 读取账号 %d 状态失败: %v", acc.ID, err)
		return
	}

	statusCode := 0
	if pr.StatusCode != nil {
		statusCode = *pr.StatusCode
	}
	now := time.Now()
	next, tr := health.Step(health.Config{}, cur.Snapshot(), health.ProbeResult{
		Success:    pr.Success,
		StatusCode: statusCode,
		At:         now,
	})

	st := repository.HealthState{
		AccountID:            acc.ID,
		AccountName:          acc.Name,
		ProviderID:           providerID,
		State:                next.State,
		ConsecutiveFailures:  next.ConsecutiveFailures,
		ConsecutiveSuccesses: next.ConsecutiveSuccesses,
		WeightPercent:        next.WeightPercent,
		CooldownUntil:        next.CooldownUntil,
		ObservingUntil:       next.ObservingUntil,
		LastProbeAt:          &now,
	}
	if err := s.healthRepo.Upsert(ctx, st); err != nil {
		log.Printf("[health] 写回账号 %d 状态失败: %v", acc.ID, err)
		return
	}

	if tr.Changed {
		var detail *string
		if pr.Error != nil {
			d := truncate(*pr.Error, 500)
			detail = &d
		}
		_ = s.healthRepo.InsertEvent(ctx, repository.HealthEvent{
			AccountID: acc.ID,
			FromState: string(tr.From),
			ToState:   string(tr.To),
			Reason:    string(tr.Reason),
			Detail:    detail,
		})
		log.Printf("[health] 账号 %s 状态: %s -> %s (%s)", acc.Name, tr.From, tr.To, tr.Reason)
		if s.OnStateChanged != nil {
			d := ""
			if detail != nil {
				d = *detail
			}
			s.OnStateChanged(acc.Name, tr.From, tr.To, d)
		}
	}
}

// probeDue 判断账号是否到达探测时机（失败退避 + suspended 冷却期跳过）。
func (s *ProbeService) probeDue(ctx context.Context, accountID int64, now time.Time) bool {
	st, err := s.healthRepo.Get(ctx, accountID)
	if err != nil {
		return true // 读不到状态时不阻塞探测
	}
	if st.State == health.StateDisabled {
		return false
	}
	// suspended 冷却期内跳过（冷却结束后的探测承担「复活确认」职责）
	if st.State == health.StateSuspended && st.CooldownUntil != nil && now.Before(*st.CooldownUntil) {
		return false
	}
	// 失败退避：基础间隔之外按连续失败叠加
	if st.LastProbeAt != nil {
		if backoff := health.ProbeBackoff(st.ConsecutiveFailures); backoff > 0 {
			if now.Before(st.LastProbeAt.Add(backoff)) {
				return false
			}
		}
	}
	return true
}

// RunProbesForProvider 探测供应商关联的所有可探测账号（cron 调用，带并发控制）。
func (s *ProbeService) RunProbesForProvider(ctx context.Context, provider *repository.Provider) {
	if !s.pg.Available() {
		return
	}
	accs, err := s.pg.ListProbeCandidates(ctx)
	if err != nil {
		return
	}
	// 该供应商关联的账号集合（关联表为唯一真相）
	links, err := s.linkRepo.ListByProvider(ctx, provider.ID)
	if err != nil {
		return
	}
	want := make(map[int64]bool, len(links))
	for _, l := range links {
		want[l.AccountID] = true
	}
	conc := s.cfg.Probe.Concurrency
	if conc <= 0 {
		conc = 2
	}
	sem := make(chan struct{}, conc)
	now := time.Now()
	day := now.In(s.cfg.Location).Format("2006-01-02")
	var pending []repository.PGAccount
	for _, a := range accs {
		if want[a.ID] {
			if !CanProbe(a.Platform) {
				continue
			}
			// 退避/冷却/禁用检查
			if !s.probeDue(ctx, a.ID, now) {
				continue
			}
			// 每日预算：原子扣减，超限即停止本轮全部探测
			ok, err := s.healthRepo.TryConsumeProbeBudget(ctx, day, dailyProbeBudget)
			if err != nil || !ok {
				if !ok {
					log.Printf("[probe] 当日探测预算（%d）已用尽，跳过剩余账号", dailyProbeBudget)
				}
				break
			}
			pending = append(pending, a)
		}
	}
	done := make(chan struct{}, len(pending))
	for _, a := range pending {
		sem <- struct{}{}
		go func(acc repository.PGAccount) {
			defer func() { <-sem; done <- struct{}{} }()
			_, _ = s.RunProbeForAccount(ctx, acc, provider.ProbeModel, "schedule")
		}(a)
	}
	for range pending {
		<-done
	}
}

// RunAllScheduled 探测所有 probe_enabled 供应商（cron）。
func (s *ProbeService) RunAllScheduled(ctx context.Context) {
	providers, err := s.providerRepo.ListProbeEnabled(ctx)
	if err != nil {
		return
	}
	for _, p := range providers {
		s.RunProbesForProvider(ctx, p)
	}
}
