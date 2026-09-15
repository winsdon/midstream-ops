package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"sub2api-account-monitor/internal/config"
	"sub2api-account-monitor/internal/pkg/secretbox"
	"sub2api-account-monitor/internal/repository"
)

// newTestSyncService 建临时 monitor 测试库 + 一整套依赖的 ProviderSyncService。
// PG 未连接（Available()=false），故成本同步分支自动跳过，测试聚焦余额采集与汇总。
func newTestSyncService(t *testing.T) (*ProviderSyncService, *repository.ProviderRepo) {
	t.Helper()
	s := newTestStore(t)

	box := &secretbox.Box{}
	providerRepo := repository.NewProviderRepo(s, box)
	balanceRepo := repository.NewBalanceRepo(s)
	collectorRepo := repository.NewCollectorStateRepo(s)
	rateRepo := repository.NewRateRepo(s)
	pg := &repository.PG{} // 未连接：Available()=false

	cfg := &config.Config{}
	cfg.Balance.TimeoutSeconds = 5
	cfg.Cost.TimeoutSeconds = 5

	balanceSvc := NewBalanceService(providerRepo, balanceRepo, cfg)
	costSvc := NewCostSyncService(providerRepo, repository.NewUpstreamCostRepo(s), pg, cfg)
	rateSvc := NewRateService(rateRepo, pg)

	svc := NewProviderSyncService(providerRepo, collectorRepo, balanceSvc, costSvc, rateSvc, pg)
	return svc, providerRepo
}

// newUpstreamStub 伪装一个 sub2api 上游站点：登录返回固定余额，其余接口返回空成功。
// hits 记录登录次数，用于验证「冷却中的站点未被打」。
func newUpstreamStub(t *testing.T, hits *int64, loginStatus int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/auth/login":
			atomic.AddInt64(hits, 1)
			if loginStatus != http.StatusOK {
				w.WriteHeader(loginStatus)
				_, _ = w.Write([]byte(`{"code":1,"message":"密码错误"}`))
				return
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"access_token":"tok","expires_in":3600,"user":{"balance":12.5}}}`))
		default:
			// 仪表盘/分组等次要接口：返回空成功，不影响余额主流程
			_, _ = w.Write([]byte(`{"code":0,"data":{}}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// createProvider 建一个指向 stub 的上游站点。
func createProvider(t *testing.T, repo *repository.ProviderRepo, name, baseURL string) *repository.Provider {
	t.Helper()
	p, err := repo.Create(context.Background(), repository.CreateParams{
		Name:          name,
		BalanceType:   "sub2api",
		Platform:      "sub2api",
		AuthMode:      "password",
		BaseURL:       baseURL,
		LoginEmail:    "a@b.c",
		LoginPassword: "pw",
		RechargeRate:  1,
	})
	if err != nil {
		t.Fatalf("建供应商失败: %v", err)
	}
	return p
}

// TestSyncAllCountsSuccessAndFailure 全量刷新按站点成败分类汇总。
func TestSyncAllCountsSuccessAndFailure(t *testing.T) {
	svc, repo := newTestSyncService(t)

	var okHits, badHits int64
	okSrv := newUpstreamStub(t, &okHits, http.StatusOK)
	badSrv := newUpstreamStub(t, &badHits, http.StatusUnauthorized)

	createProvider(t, repo, "good-site", okSrv.URL)
	bad := createProvider(t, repo, "bad-site", badSrv.URL)

	result, err := svc.SyncAll(context.Background())
	if err != nil {
		t.Fatalf("SyncAll 出错: %v", err)
	}

	if result.Total != 2 {
		t.Errorf("Total = %d，期望 2", result.Total)
	}
	if result.Succeeded != 1 {
		t.Errorf("Succeeded = %d，期望 1", result.Succeeded)
	}
	if result.Failed != 1 {
		t.Errorf("Failed = %d，期望 1", result.Failed)
	}
	if len(result.Failures) != 1 {
		t.Fatalf("Failures 长度 = %d，期望 1", len(result.Failures))
	}
	if result.Failures[0].ProviderID != bad.ID {
		t.Errorf("失败站点 ID = %d，期望 %d", result.Failures[0].ProviderID, bad.ID)
	}
	if result.Failures[0].Name != "bad-site" {
		t.Errorf("失败站点名 = %q，期望 bad-site", result.Failures[0].Name)
	}
	if result.Failures[0].Error == "" {
		t.Error("失败站点缺少错误信息")
	}
}

// TestSyncAllSkipsCooldown 冷却中的站点被跳过，且完全未打上游。
// 这是与单站点手动刷新的关键差异：全量刷新不绕过冷却。
func TestSyncAllSkipsCooldown(t *testing.T) {
	svc, repo := newTestSyncService(t)

	var okHits, cooledHits int64
	okSrv := newUpstreamStub(t, &okHits, http.StatusOK)
	cooledSrv := newUpstreamStub(t, &cooledHits, http.StatusOK)

	createProvider(t, repo, "normal", okSrv.URL)
	cooled := createProvider(t, repo, "cooling", cooledSrv.URL)

	ctx := context.Background()
	if err := repo.SetLoginCooldown(ctx, cooled.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("设置冷却失败: %v", err)
	}

	result, err := svc.SyncAll(ctx)
	if err != nil {
		t.Fatalf("SyncAll 出错: %v", err)
	}

	if result.Skipped != 1 {
		t.Errorf("Skipped = %d，期望 1", result.Skipped)
	}
	if result.Total != 1 {
		t.Errorf("Total = %d，期望 1（跳过的不计入）", result.Total)
	}
	if result.Succeeded != 1 {
		t.Errorf("Succeeded = %d，期望 1", result.Succeeded)
	}
	if n := atomic.LoadInt64(&cooledHits); n != 0 {
		t.Errorf("冷却中的站点被请求了 %d 次，期望 0", n)
	}
	if n := atomic.LoadInt64(&okHits); n != 1 {
		t.Errorf("正常站点被请求了 %d 次，期望 1", n)
	}
}

// TestSyncAllRespectsConcurrencyLimit 并发数不超过 syncAllConcurrency。
func TestSyncAllRespectsConcurrencyLimit(t *testing.T) {
	svc, repo := newTestSyncService(t)

	var mu sync.Mutex
	inFlight, peak := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/auth/login" {
			mu.Lock()
			inFlight++
			if inFlight > peak {
				peak = inFlight
			}
			mu.Unlock()

			time.Sleep(30 * time.Millisecond) // 拉长窗口，让并发真正重叠

			mu.Lock()
			inFlight--
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/auth/login" {
			_, _ = w.Write([]byte(`{"code":0,"data":{"access_token":"tok","expires_in":3600,"user":{"balance":1}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"code":0,"data":{}}`))
	}))
	t.Cleanup(srv.Close)

	const total = 12
	for i := 0; i < total; i++ {
		createProvider(t, repo, "site-"+string(rune('a'+i)), srv.URL)
	}

	result, err := svc.SyncAll(context.Background())
	if err != nil {
		t.Fatalf("SyncAll 出错: %v", err)
	}
	if result.Total != total {
		t.Errorf("Total = %d，期望 %d", result.Total, total)
	}

	mu.Lock()
	defer mu.Unlock()
	if peak > syncAllConcurrency {
		t.Errorf("并发峰值 = %d，超过上限 %d", peak, syncAllConcurrency)
	}
	if peak < 2 {
		t.Errorf("并发峰值 = %d，说明退化成串行了", peak)
	}
}

// newNewAPICostTimeoutStub 伪装 new-api 站：登录和余额成功，单个 token 用量接口失败。
// 这是咩咩类故障的最小复现：/log/self/stat?type=2 超时/5xx，站点本身是通的。
func newNewAPICostTimeoutStub(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/user/login":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "s", Path: "/"})
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":1,"quota":500000}}`))
		case r.URL.Path == "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota_per_unit":500000}}`))
		case r.URL.Path == "/api/user/self":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":1,"username":"m","quota":500000,"used_quota":0}}`))
		case r.URL.Path == "/api/user/self/groups":
			_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
		case r.URL.Path == "/api/log/self/stat":
			if r.URL.Query().Get("type") == "2" {
				w.WriteHeader(http.StatusGatewayTimeout)
				_, _ = w.Write([]byte(`timeout`))
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota":0,"rpm":0}}`))
		case strings.HasSuffix(r.URL.Path, "/key"):
			_, _ = w.Write([]byte(`{"success":true,"data":{"key":"sk-heavy"}}`))
		case strings.HasPrefix(r.URL.Path, "/api/token/"):
			_, _ = w.Write([]byte(`{"success":true,"data":{"items":[{"id":1902,"name":"heavy","group":"Grok heavy","status":1}],"total":1}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"success":false,"message":"not found"}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestSyncOneCostFailureDoesNotRecordSiteFailure 余额成功、单个 token 用量失败时：
// 站点采集记成功（不打红、不退避），成本错误留在 cost_sync_state。
func TestSyncOneCostFailureDoesNotRecordSiteFailure(t *testing.T) {
	store := newTestStore(t)
	box := &secretbox.Box{}
	providerRepo := repository.NewProviderRepo(store, box)
	balanceRepo := repository.NewBalanceRepo(store)
	collectorRepo := repository.NewCollectorStateRepo(store)
	rateRepo := repository.NewRateRepo(store)
	costRepo := repository.NewUpstreamCostRepo(store)
	pg := &repository.PG{}
	pg.SetAvailableForTest(true)

	cfg := &config.Config{Location: time.UTC}
	cfg.Balance.TimeoutSeconds = 5
	cfg.Cost.TimeoutSeconds = 5

	svc := NewProviderSyncService(
		providerRepo, collectorRepo,
		NewBalanceService(providerRepo, balanceRepo, cfg),
		NewCostSyncService(providerRepo, costRepo, pg, cfg),
		NewRateService(rateRepo, pg),
		pg,
	)

	srv := newNewAPICostTimeoutStub(t)
	p, err := providerRepo.Create(context.Background(), repository.CreateParams{
		Name:          "咩咩",
		BalanceType:   "sub2api",
		Platform:      "new-api",
		AuthMode:      "password",
		BaseURL:       srv.URL,
		LoginEmail:    "a@b.c",
		LoginPassword: "pw",
		RechargeRate:  1,
	})
	if err != nil {
		t.Fatalf("建供应商失败: %v", err)
	}

	ctx := context.Background()
	if _, err := collectorRepo.RecordFailure(ctx, p.ID, taskSync, "旧的成本超时", nil); err != nil {
		t.Fatalf("预置失败记录: %v", err)
	}

	outcome, err := svc.SyncOne(ctx, p.ID, false, false)
	if err != nil {
		t.Fatalf("SyncOne 返回错误: %v", err)
	}
	if outcome == nil || outcome.Err != nil {
		t.Fatalf("余额已成功时 outcome.Err 应为 nil，得到 %v", outcome)
	}
	if outcome.Snapshot == nil || outcome.Snapshot.Balance == nil {
		t.Fatal("余额快照应写入")
	}

	st, err := collectorRepo.Get(ctx, p.ID, taskSync)
	if err != nil {
		t.Fatalf("读 collector_state: %v", err)
	}
	if st.ConsecutiveFailures != 0 {
		t.Errorf("consecutive_failures = %d，期望 0（成本失败不应打红站点）", st.ConsecutiveFailures)
	}
	if st.LastSuccessAt == nil {
		t.Error("last_success_at 应被写入")
	}
	if st.NextEligibleAt != nil {
		t.Errorf("不应写入退避解禁时刻，得到 %v", st.NextEligibleAt)
	}

	costStates, err := costRepo.SyncStates(ctx)
	if err != nil {
		t.Fatalf("读 cost_sync_state: %v", err)
	}
	cs := costStates[p.ID]
	if cs.LastError == nil || *cs.LastError == "" {
		t.Fatal("成本错误应留在 cost_sync_state.last_error")
	}
}

// TestSyncAllEmpty 无可采集站点时返回空结果而非报错。
func TestSyncAllEmpty(t *testing.T) {
	svc, _ := newTestSyncService(t)

	result, err := svc.SyncAll(context.Background())
	if err != nil {
		t.Fatalf("SyncAll 出错: %v", err)
	}
	if result.Total != 0 || result.Succeeded != 0 || result.Failed != 0 {
		t.Errorf("空结果期望全 0，实得 %+v", result)
	}
	if result.Failures == nil {
		// JSON 序列化后前端拿到 null 也能处理，但显式空切片更友好
		t.Log("Failures 为 nil（序列化为 null）")
	}
}
