package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"sub2api-account-monitor/internal/pkg/secretbox"
	"sub2api-account-monitor/internal/repository"
)

// newapiStub 模拟一个 new-api 站点，按端点计数，用来断言「上游只被打了一次」。
//
// 登录端点一律返回旧版形态（只下发会话 Cookie），这样降级路径的结果一眼可辨：
// 拿到 Cookie 就说明走了密码重登，拿到 JWT 就说明走了续期。
type newapiStub struct {
	url         string
	refreshHits atomic.Int64
	loginHits   atomic.Int64
}

// newNewAPIStub 起一个桩站点。refreshStatus 传 200 表示续期成功，
// 传 404 / 401 分别模拟「上游无该端点（旧版）」与「refresh 凭据失效」。
//
// 该值在构造时固定、不可事后修改——否则测试 goroutine 的写与服务端 goroutine
// 的读之间没有 happens-before 边，-race 会报警。
func newNewAPIStub(t *testing.T, refreshStatus int) *newapiStub {
	t.Helper()
	s := &newapiStub{}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"quota_per_unit":500000}}`))
	})
	mux.HandleFunc("/api/user/login", func(w http.ResponseWriter, r *http.Request) {
		s.loginHits.Add(1)
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "stub", Path: "/"})
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":7,"quota":500000}}`))
	})
	mux.HandleFunc("/api/user/auth/refresh", func(w http.ResponseWriter, r *http.Request) {
		s.refreshHits.Add(1)
		if refreshStatus != http.StatusOK {
			w.WriteHeader(refreshStatus)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: newapiRefreshCookie, Value: "r-new", Path: "/api/user/auth"})
		_, _ = fmt.Fprintf(w, `{"success":true,"data":{"access_token":"jwt-new","access_expires_at":%d,"user":{"id":7}}}`,
			time.Now().Add(time.Hour).Unix())
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	s.url = srv.URL
	return s
}

// newTestProviderRepo 建一个临时库上的真实仓储。
// 用真库而非 mock：providerTokenManager 依赖具体类型 *repository.ProviderRepo，
// 且本组用例要验的正是「续期结果确实落了库」。凭据用零值 Box（明文直通）。
func newTestProviderRepo(t *testing.T) *repository.ProviderRepo {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := repository.NewSQLite(path)
	if err != nil {
		t.Fatalf("初始化 SQLite 失败: %v", err)
	}
	t.Cleanup(func() { _ = s.Close(); _ = os.Remove(path) })
	return repository.NewProviderRepo(s, &secretbox.Box{})
}

// newTestNewAPIProvider 建一个 new-api password 模式站点。
func newTestNewAPIProvider(t *testing.T, repo *repository.ProviderRepo, baseURL string) *repository.Provider {
	t.Helper()
	p, err := repo.Create(context.Background(), repository.CreateParams{
		Name:           "stub",
		BalanceType:    "sub2api",
		Platform:       "new-api",
		AuthMode:       "password",
		BaseURL:        baseURL,
		LoginEmail:     "u@example.com",
		LoginPassword:  "pw",
		UpstreamUserID: "7",
		RechargeRate:   1,
	})
	if err != nil {
		t.Fatalf("创建供应商失败: %v", err)
	}
	return p
}

// giveExpiredJWT 造出「新版站点、JWT 已过期、refresh token 尚在」的状态——
// 这正是 ensureNewAPI 该走续期分支的那个点。
func giveExpiredJWT(t *testing.T, repo *repository.ProviderRepo, id int64) {
	t.Helper()
	past := time.Now().Add(-time.Hour)
	if err := repo.UpdateTokenPair(context.Background(), id, "jwt-old", "r-old", &past); err != nil {
		t.Fatalf("写入过期 JWT 失败: %v", err)
	}
}

// mustGetProvider 重新读一份 provider。
// 每个并发调用方持有各自的副本，与生产一致（balance/cost/pricing 三处 401 重试点
// 各自从库里读出 provider 后独立调用会话层）。
func mustGetProvider(t *testing.T, repo *repository.ProviderRepo, id int64) *repository.Provider {
	t.Helper()
	p, err := repo.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("读取供应商失败: %v", err)
	}
	return p
}

// TestEnsureNewAPIRefreshIsSingleFlight 并发续期必须单飞。
//
// 上游的 refresh token 每次调用都会轮换，旧值在 30s 重放窗口内被再次使用会触发
// 重放检测并吊销整个会话——并发刷新等于自己把自己踢下线。故 10 个 goroutine
// 同时要会话时，上游的 /api/user/auth/refresh 只允许被打一次，其余走双重检查
// 命中库里的新令牌。
func TestEnsureNewAPIRefreshIsSingleFlight(t *testing.T) {
	stub := newNewAPIStub(t, http.StatusOK)
	repo := newTestProviderRepo(t)
	p := newTestNewAPIProvider(t, repo, stub.url)
	giveExpiredJWT(t, repo, p.ID)

	m := newTokenManager(repo, nil, newAPITestClient())

	const n = 10
	sessions := make([]*providerSession, n)
	errs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int, pp *repository.Provider) {
			defer wg.Done()
			<-start // 尽量让 10 个 goroutine 同时冲进来
			sessions[i], errs[i] = m.ensureNewAPI(context.Background(), pp)
		}(i, mustGetProvider(t, repo, p.ID))
	}
	close(start)
	wg.Wait()

	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: ensureNewAPI: %v", i, errs[i])
		}
		if sessions[i].NewAPI.JWT != "jwt-new" {
			t.Errorf("goroutine %d 拿到 JWT %q，期望续期后的 jwt-new", i, sessions[i].NewAPI.JWT)
		}
	}
	if got := stub.refreshHits.Load(); got != 1 {
		t.Errorf("续期端点被打了 %d 次，期望 1 次（单飞失效会触发上游重放检测）", got)
	}
	if got := stub.loginHits.Load(); got != 0 {
		t.Errorf("登录端点被打了 %d 次，期望 0 次（有 refresh token 就不该撞登录接口）", got)
	}

	// 轮换后的凭据必须落库，否则下次仍拿旧值去刷 → 直接触发重放检测
	fresh := mustGetProvider(t, repo, p.ID)
	if fresh.AccessToken != "jwt-new" || fresh.RefreshToken != "r-new" {
		t.Errorf("落库凭据 = %q/%q，期望 jwt-new/r-new", fresh.AccessToken, fresh.RefreshToken)
	}
}

// TestEnsureNewAPIUsesValidJWT JWT 未过期时直接用，不打任何上游端点。
func TestEnsureNewAPIUsesValidJWT(t *testing.T) {
	stub := newNewAPIStub(t, http.StatusOK)
	repo := newTestProviderRepo(t)
	p := newTestNewAPIProvider(t, repo, stub.url)
	future := time.Now().Add(time.Hour)
	if err := repo.UpdateTokenPair(context.Background(), p.ID, "jwt-live", "r", &future); err != nil {
		t.Fatalf("UpdateTokenPair: %v", err)
	}

	m := newTokenManager(repo, nil, newAPITestClient())
	sess, err := m.ensureNewAPI(context.Background(), mustGetProvider(t, repo, p.ID))
	if err != nil {
		t.Fatalf("ensureNewAPI: %v", err)
	}
	if sess.NewAPI.JWT != "jwt-live" {
		t.Errorf("JWT = %q, want jwt-live", sess.NewAPI.JWT)
	}
	if stub.refreshHits.Load() != 0 || stub.loginHits.Load() != 0 {
		t.Errorf("有效期内不该打上游：refresh=%d login=%d", stub.refreshHits.Load(), stub.loginHits.Load())
	}
}

// TestEnsureNewAPILegacyCookieSkipsRefresh 旧版站点回归保护。
//
// refresh_token 只可能由新版登录成功写入，故它为空即「确认是旧版」：
// 必须原样走 Cookie 路径，一次续期请求都不能发（否则每轮采集白打一次 404）。
func TestEnsureNewAPILegacyCookieSkipsRefresh(t *testing.T) {
	stub := newNewAPIStub(t, http.StatusNotFound)
	repo := newTestProviderRepo(t)
	p := newTestNewAPIProvider(t, repo, stub.url)
	if err := repo.UpdateSession(context.Background(), p.ID, "session=old", "7", 500000); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}

	m := newTokenManager(repo, nil, newAPITestClient())
	sess, err := m.ensureNewAPI(context.Background(), mustGetProvider(t, repo, p.ID))
	if err != nil {
		t.Fatalf("ensureNewAPI: %v", err)
	}
	if sess.NewAPI.Cookie != "session=old" || sess.NewAPI.JWT != "" {
		t.Errorf("会话 = cookie %q / jwt %q，期望只有旧版 Cookie", sess.NewAPI.Cookie, sess.NewAPI.JWT)
	}
	if stub.refreshHits.Load() != 0 || stub.loginHits.Load() != 0 {
		t.Errorf("旧版站点不该打上游：refresh=%d login=%d", stub.refreshHits.Load(), stub.loginHits.Load())
	}
}

// TestRefreshNewAPIFallsBackToLogin 续期失败降级密码重登，且不进冷却阶梯。
//
// 两条关键断言：
//   - 降级后拿到的是旧版 Cookie（说明确实重登了，而不是把坏令牌原样返回）；
//   - login_failures 保持 0。续期失败不是「登录被拒」，若误计入阶梯，
//     站点会被锁进 6 小时冷却，余额与成本采集全部停摆。
func TestRefreshNewAPIFallsBackToLogin(t *testing.T) {
	cases := []struct {
		name   string
		status int
	}{
		{name: "404：上游无该端点（降级回旧版）", status: http.StatusNotFound},
		{name: "401：refresh 凭据失效或会话被吊销", status: http.StatusUnauthorized},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stub := newNewAPIStub(t, c.status)
			repo := newTestProviderRepo(t)
			p := newTestNewAPIProvider(t, repo, stub.url)
			giveExpiredJWT(t, repo, p.ID)

			m := newTokenManager(repo, nil, newAPITestClient())
			sess, err := m.ensureNewAPI(context.Background(), mustGetProvider(t, repo, p.ID))
			if err != nil {
				t.Fatalf("ensureNewAPI: %v", err)
			}
			if sess.NewAPI.Cookie != "session=stub" || sess.NewAPI.JWT != "" {
				t.Errorf("会话 = cookie %q / jwt %q，期望降级为旧版 Cookie", sess.NewAPI.Cookie, sess.NewAPI.JWT)
			}
			if got := stub.refreshHits.Load(); got != 1 {
				t.Errorf("续期端点被打了 %d 次，期望 1 次", got)
			}
			if got := stub.loginHits.Load(); got != 1 {
				t.Errorf("登录端点被打了 %d 次，期望 1 次（降级重登）", got)
			}

			fresh := mustGetProvider(t, repo, p.ID)
			if fresh.AccessToken != "" || fresh.RefreshToken != "" {
				t.Errorf("失效的新版凭据未清干净：%q/%q", fresh.AccessToken, fresh.RefreshToken)
			}
			if fresh.LoginFailures != 0 || fresh.LoginCooldownUntil != nil {
				t.Errorf("续期失败被误计为登录被拒：failures=%d cooldown=%v",
					fresh.LoginFailures, fresh.LoginCooldownUntil)
			}
		})
	}
}

// TestEnsureNewAPIUserKey user_key 模式无会话概念，恒用静态 PAT，缺字段即报错。
func TestEnsureNewAPIUserKey(t *testing.T) {
	stub := newNewAPIStub(t, http.StatusOK)
	repo := newTestProviderRepo(t)
	p := newTestNewAPIProvider(t, repo, stub.url)
	m := newTokenManager(repo, nil, newAPITestClient())

	p.AuthMode = "user_key"
	p.AccessToken = "pat"
	sess, err := m.ensureNewAPI(context.Background(), p)
	if err != nil {
		t.Fatalf("ensureNewAPI: %v", err)
	}
	if sess.NewAPI.AccessToken != "pat" || sess.NewAPI.JWT != "" {
		t.Errorf("会话 = pat %q / jwt %q，期望只有静态令牌", sess.NewAPI.AccessToken, sess.NewAPI.JWT)
	}

	p.AccessToken = ""
	if _, err := m.ensureNewAPI(context.Background(), p); err == nil {
		t.Error("缺少系统访问令牌时应报错")
	}
	if stub.refreshHits.Load() != 0 || stub.loginHits.Load() != 0 {
		t.Errorf("user_key 模式不该打上游：refresh=%d login=%d", stub.refreshHits.Load(), stub.loginHits.Load())
	}
}
