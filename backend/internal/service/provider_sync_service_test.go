package service

import (
	"context"
	"net/http"
	"net/http/httptest"
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
