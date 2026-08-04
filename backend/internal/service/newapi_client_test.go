package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newAPITestClient 指向 httptest 服务的客户端（短超时，避免用例卡住）。
func newAPITestClient() *NewAPIClient { return NewNewAPIClient(3 * time.Second) }

// TestNewAPILoginVersions 新旧两版登录响应的字段分流。
//
// 版本判据就是响应里有没有 access_token：新版（≥v1.0.0-rc.22，PR #6329）走
// JWT + new_api_refresh cookie，旧版只有会话 Cookie。两者必须落到不同字段上，
// 否则会话层无从判断该续期还是该重登。
func TestNewAPILoginVersions(t *testing.T) {
	cases := []struct {
		name     string
		handler  http.HandlerFunc
		wantJWT  string
		wantRefr string
		wantCk   string
		wantUser string
		wantQ    float64
	}{
		{
			name: "旧版只下发会话 Cookie",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.SetCookie(w, &http.Cookie{Name: "session", Value: "old-sess", Path: "/"})
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"success":true,"data":{"id":7,"quota":500000}}`))
			},
			wantCk:   "session=old-sess",
			wantUser: "7",
			wantQ:    500000,
		},
		{
			name: "新版下发 JWT 与 refresh cookie",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.SetCookie(w, &http.Cookie{Name: newapiRefreshCookie, Value: "r1", Path: "/api/user/auth"})
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"success":true,"data":{"access_token":"jwt1","access_expires_at":1900000000,"session":"s1","user":{"id":9,"quota":250000}}}`))
			},
			wantJWT:  "jwt1",
			wantRefr: "r1",
			wantUser: "9",
			wantQ:    250000,
		},
		{
			name: "新版同时下发会话 Cookie 时 refresh 单独摘出",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.SetCookie(w, &http.Cookie{Name: "session", Value: "s", Path: "/"})
				http.SetCookie(w, &http.Cookie{Name: newapiRefreshCookie, Value: "r2", Path: "/api/user/auth"})
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"success":true,"data":{"access_token":"jwt2","user":{"id":3}}}`))
			},
			wantJWT:  "jwt2",
			wantRefr: "r2",
			wantCk:   "session=s",
			wantUser: "3",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(c.handler)
			defer srv.Close()

			lr, err := newAPITestClient().Login(context.Background(), srv.URL, "u", "p")
			if err != nil {
				t.Fatalf("Login: %v", err)
			}
			if lr.AccessToken != c.wantJWT {
				t.Errorf("AccessToken = %q, want %q", lr.AccessToken, c.wantJWT)
			}
			if lr.RefreshToken != c.wantRefr {
				t.Errorf("RefreshToken = %q, want %q", lr.RefreshToken, c.wantRefr)
			}
			if lr.Cookie != c.wantCk {
				t.Errorf("Cookie = %q, want %q", lr.Cookie, c.wantCk)
			}
			if lr.UserID != c.wantUser {
				t.Errorf("UserID = %q, want %q", lr.UserID, c.wantUser)
			}
			if lr.Quota != c.wantQ {
				t.Errorf("Quota = %v, want %v", lr.Quota, c.wantQ)
			}
			// refresh 凭据绝不能混进普通请求的 Cookie 头（上游 Path 限定在 /api/user/auth）
			if strings.Contains(lr.Cookie, newapiRefreshCookie) {
				t.Errorf("refresh cookie 混进了会话 Cookie: %q", lr.Cookie)
			}
		})
	}
}

// TestNewAPILoginRejected 凭据被拒必须裹 ErrLoginRejected，会话层据此进入冷却阶梯。
func TestNewAPILoginRejected(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{
			name: "HTTP 401",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
			},
		},
		{
			name: "HTTP 200 + success:false",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"success":false,"message":"用户名或密码错误"}`))
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(c.handler)
			defer srv.Close()

			_, err := newAPITestClient().Login(context.Background(), srv.URL, "u", "p")
			if !IsLoginRejected(err) {
				t.Errorf("err = %v, 期望裹 ErrLoginRejected", err)
			}
		})
	}
}

// TestNewAPILoginNoCredential 既无 JWT 也无 Cookie 视为失败（但不是凭据被拒，不该进冷却）。
func TestNewAPILoginNoCredential(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":1}}`))
	}))
	defer srv.Close()

	_, err := newAPITestClient().Login(context.Background(), srv.URL, "u", "p")
	if err == nil {
		t.Fatal("期望报错")
	}
	if IsLoginRejected(err) {
		t.Errorf("响应异常不应被计为登录被拒: %v", err)
	}
}

// TestRefreshSessionSuccess 续期请求的头部约定与轮换后凭据的回传。
func TestRefreshSessionSuccess(t *testing.T) {
	var gotCookie, gotOrigin, gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotCookie = r.Header.Get("Cookie")
		gotOrigin = r.Header.Get("Origin")
		http.SetCookie(w, &http.Cookie{Name: newapiRefreshCookie, Value: "r-new", Path: "/api/user/auth"})
		_, _ = w.Write([]byte(`{"success":true,"data":{"access_token":"jwt-new","access_expires_at":1900000000}}`))
	}))
	defer srv.Close()

	rr, err := newAPITestClient().RefreshSession(context.Background(), srv.URL, "r-old")
	if err != nil {
		t.Fatalf("RefreshSession: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/user/auth/refresh" {
		t.Errorf("请求 = %s %s, 期望 POST /api/user/auth/refresh", gotMethod, gotPath)
	}
	if gotCookie != newapiRefreshCookie+"=r-old" {
		t.Errorf("Cookie = %q, 期望只带 refresh 凭据", gotCookie)
	}
	// 上游开启 SessionCookieSecure 时校验 Origin，缺失即 403
	if gotOrigin != srv.URL {
		t.Errorf("Origin = %q, want %q", gotOrigin, srv.URL)
	}
	if rr.AccessToken != "jwt-new" || rr.RefreshToken != "r-new" {
		t.Errorf("续期结果 = %q/%q, want jwt-new/r-new", rr.AccessToken, rr.RefreshToken)
	}
	if rr.AccessExpiresAt.Unix() != 1900000000 {
		t.Errorf("AccessExpiresAt = %v", rr.AccessExpiresAt)
	}
}

// TestRefreshSessionKeepsOldToken 上游未轮换时沿用旧 refresh token，避免写空。
func TestRefreshSessionKeepsOldToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"access_token":"jwt"}}`))
	}))
	defer srv.Close()

	rr, err := newAPITestClient().RefreshSession(context.Background(), srv.URL, "r-old")
	if err != nil {
		t.Fatalf("RefreshSession: %v", err)
	}
	if rr.RefreshToken != "r-old" {
		t.Errorf("RefreshToken = %q, 期望沿用旧值", rr.RefreshToken)
	}
}

// TestRefreshSessionErrors 错误分类。
//
// 关键：续期失败绝不能裹 ErrLoginRejected——那会让 recordRejected 把站点
// 锁进 6 小时冷却，而实际只需降级重登一次。
func TestRefreshSessionErrors(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		wantErr error
	}{
		{
			name:    "404 → 上游无此端点，可降级重登",
			handler: func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) },
			wantErr: errRefreshUnsupported,
		},
		{
			name:    "401 → 凭据失效",
			handler: func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) },
			wantErr: errUnauthorized,
		},
		{
			name:    "403 → Origin 校验或会话被吊销",
			handler: func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) },
			wantErr: errUnauthorized,
		},
		{
			name: "success:false → 凭据失效",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"success":false,"message":"session revoked"}`))
			},
			wantErr: errUnauthorized,
		},
		{
			name: "缺 access_token → 凭据失效",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
			},
			wantErr: errUnauthorized,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(c.handler)
			defer srv.Close()

			_, err := newAPITestClient().RefreshSession(context.Background(), srv.URL, "r")
			if !errors.Is(err, c.wantErr) {
				t.Errorf("err = %v, 期望裹 %v", err, c.wantErr)
			}
			if IsLoginRejected(err) {
				t.Errorf("续期失败不应被计为登录被拒（会误触发 6h 冷却）: %v", err)
			}
		})
	}
}

// TestGetJSONAuthHeaders 三种认证形态各自该发哪些头。
func TestGetJSONAuthHeaders(t *testing.T) {
	cases := []struct {
		name       string
		auth       NewAPIAuth
		wantAuthz  string
		wantCookie string
		wantUser   string
	}{
		{
			name:       "旧版 Cookie 模式带 New-Api-User",
			auth:       NewAPIAuth{Cookie: "session=s", UserID: "7"},
			wantCookie: "session=s",
			wantUser:   "7",
		},
		{
			name:      "user_key 模式带 PAT 与 New-Api-User",
			auth:      NewAPIAuth{AccessToken: "pat", UserID: "7"},
			wantAuthz: "Bearer pat",
			wantUser:  "7",
		},
		{
			// 新版已移除该头，JWT 自带身份
			name:      "新版 JWT 模式不发 New-Api-User",
			auth:      NewAPIAuth{JWT: "jwt", UserID: "7"},
			wantAuthz: "Bearer jwt",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var authz, cookie, user string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				authz, cookie, user = r.Header.Get("Authorization"), r.Header.Get("Cookie"), r.Header.Get("New-Api-User")
				_, _ = w.Write([]byte(`{"success":true,"data":{"id":1}}`))
			}))
			defer srv.Close()

			var dst struct{ ID int64 }
			if err := newAPITestClient().getJSON(context.Background(), srv.URL+"/api/user/self", c.auth, "测试", &dst); err != nil {
				t.Fatalf("getJSON: %v", err)
			}
			if authz != c.wantAuthz {
				t.Errorf("Authorization = %q, want %q", authz, c.wantAuthz)
			}
			if cookie != c.wantCookie {
				t.Errorf("Cookie = %q, want %q", cookie, c.wantCookie)
			}
			if user != c.wantUser {
				t.Errorf("New-Api-User = %q, want %q", user, c.wantUser)
			}
		})
	}
}

func TestNewAPIListTokensPaginates(t *testing.T) {
	const total = 101
	var pages atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/token/" {
			t.Fatalf("path = %q, want /api/token/", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer pat" || r.Header.Get("New-Api-User") != "9" {
			t.Fatalf("unexpected auth headers: Authorization=%q New-Api-User=%q",
				r.Header.Get("Authorization"), r.Header.Get("New-Api-User"))
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("p"))
		if r.URL.Query().Get("page_size") != "100" {
			t.Fatalf("page_size = %q, want 100", r.URL.Query().Get("page_size"))
		}
		pages.Add(1)
		start := (page - 1) * 100
		end := start + 100
		if end > total {
			end = total
		}
		items := make([]string, 0, end-start)
		for i := start; i < end; i++ {
			items = append(items, fmt.Sprintf(`{"id":%d,"name":"token-%d","group":"vip","status":1}`, i+1, i+1))
		}
		_, _ = fmt.Fprintf(w, `{"success":true,"data":{"items":[%s],"total":%d}}`, strings.Join(items, ","), total)
	}))
	defer srv.Close()

	tokens, err := newAPITestClient().ListTokens(context.Background(), srv.URL, NewAPIAuth{AccessToken: "pat", UserID: "9"})
	if err != nil {
		t.Fatalf("ListTokens: %v", err)
	}
	if len(tokens) != total || tokens[100].ID != 101 || tokens[100].Name != "token-101" {
		t.Fatalf("tokens = %d, last = %+v", len(tokens), tokens[len(tokens)-1])
	}
	if pages.Load() != 2 {
		t.Fatalf("pages = %d, want 2", pages.Load())
	}
}

func TestNewAPIGetTokenKeyUsesAuthenticatedPOST(t *testing.T) {
	var gotMethod, gotPath, gotAuthorization string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotAuthorization = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"success":true,"data":{"key":"sk-full-secret"}}`))
	}))
	defer srv.Close()

	key, err := newAPITestClient().GetTokenKey(context.Background(), srv.URL, NewAPIAuth{JWT: "jwt"}, 42)
	if err != nil {
		t.Fatalf("GetTokenKey: %v", err)
	}
	if key != "sk-full-secret" {
		t.Fatalf("key = %q", key)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/token/42/key" || gotAuthorization != "Bearer jwt" {
		t.Fatalf("request = %s %s Authorization=%q", gotMethod, gotPath, gotAuthorization)
	}
}

func TestNewAPIGetTokenUsageFiltersAndConvertsQuota(t *testing.T) {
	start := time.Date(2026, 7, 30, 0, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	end := start.Add(24 * time.Hour)
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		q := r.URL.Query()
		if r.URL.Path != "/api/log/self/stat" || q.Get("type") != "2" {
			t.Fatalf("unexpected stat request: %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		if q.Get("start_timestamp") != strconv.FormatInt(start.Unix(), 10) || q.Get("end_timestamp") != strconv.FormatInt(end.Unix(), 10) {
			t.Fatalf("unexpected date range: %s - %s", q.Get("start_timestamp"), q.Get("end_timestamp"))
		}
		switch q.Get("token_name") {
		case "prod key":
			if q.Get("group") != "vip/group" {
				t.Fatalf("group = %q", q.Get("group"))
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota":250000,"rpm":17}}`))
		case "zero":
			if _, ok := q["group"]; ok {
				t.Fatalf("empty group must be omitted")
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota":0}}`))
		default:
			t.Fatalf("token_name = %q", q.Get("token_name"))
		}
	}))
	defer srv.Close()

	usage, err := newAPITestClient().GetTokenUsage(context.Background(), srv.URL, NewAPIAuth{Cookie: "session=s", UserID: "7"}, "prod key", "vip/group", start, end, 100000)
	if err != nil {
		t.Fatalf("GetTokenUsage: %v", err)
	}
	if usage.ActualCost != 2.5 || usage.Requests != 17 {
		t.Fatalf("usage = %+v, want cost=2.5 requests=17", usage)
	}
	zero, err := newAPITestClient().GetTokenUsage(context.Background(), srv.URL, NewAPIAuth{}, "zero", "", start, end, 0)
	if err != nil {
		t.Fatalf("zero GetTokenUsage: %v", err)
	}
	if zero.ActualCost != 0 || calls.Load() != 2 {
		t.Fatalf("zero usage = %+v, calls = %d", zero, calls.Load())
	}
}

func TestNewAPIGetTokensUsageCapsConcurrencyAndReturnsFailures(t *testing.T) {
	var active, maxActive atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			old := maxActive.Load()
			if current <= old || maxActive.CompareAndSwap(old, current) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		if r.URL.Query().Get("token_name") == "bad" {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"quota":500000}}`))
	}))
	defer srv.Close()

	tokens := make([]NewAPIToken, 10)
	for i := range tokens {
		tokens[i] = NewAPIToken{ID: int64(i + 1), Name: fmt.Sprintf("token-%d", i+1)}
	}
	start := time.Unix(1000, 0)
	usage, err := newAPITestClient().GetTokensUsage(context.Background(), srv.URL, NewAPIAuth{}, tokens, start, start.Add(time.Hour), 500000)
	if err != nil {
		t.Fatalf("GetTokensUsage: %v", err)
	}
	if len(usage) != len(tokens) || maxActive.Load() > 4 {
		t.Fatalf("usage=%d max concurrency=%d", len(usage), maxActive.Load())
	}

	_, err = newAPITestClient().GetTokensUsage(context.Background(), srv.URL, NewAPIAuth{}, []NewAPIToken{{ID: 1, Name: "bad"}}, start, start.Add(time.Hour), 500000)
	if err == nil {
		t.Fatal("upstream failure should be returned")
	}
}

func TestOriginOf(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://api.example.com", "https://api.example.com"},
		{"https://api.example.com/", "https://api.example.com"},
		{"https://api.example.com/sub/path", "https://api.example.com"},
		{"http://127.0.0.1:3000/x", "http://127.0.0.1:3000"},
	}
	for _, c := range cases {
		if got := originOf(c.in); got != c.want {
			t.Errorf("originOf(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
