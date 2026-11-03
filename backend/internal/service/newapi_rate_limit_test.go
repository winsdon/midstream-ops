package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTokenUsageLimitSharedAcrossClientsAndBatches(t *testing.T) {
	starts := make(chan time.Time, 4)
	var active, peak atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		starts <- time.Now()
		time.Sleep(10 * time.Millisecond)
		_, _ = w.Write([]byte(`{"success":true,"data":{"quota":500000}}`))
	}))
	defer srv.Close()
	c1, c2 := newAPITestClient(), newAPITestClient()
	c1.SetTokenUsageLimit(srv.URL, 1, 60)
	errCh := make(chan error, 2)
	for _, c := range []*NewAPIClient{c1, c2} {
		go func(c *NewAPIClient) {
			_, err := c.GetTokensUsage(context.Background(), srv.URL, NewAPIAuth{}, []NewAPIToken{{ID: 1}, {ID: 2}}, time.Now(), time.Now(), 500000)
			errCh <- err
		}(c)
	}
	for range 2 {
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
	}
	var previous time.Time
	for range 4 {
		started := <-starts
		if !previous.IsZero() && started.Sub(previous) < 45*time.Millisecond {
			t.Fatalf("requests were not spaced: %s", started.Sub(previous))
		}
		previous = started
	}
	if peak.Load() != 1 {
		t.Fatalf("shared concurrency = %d, want 1", peak.Load())
	}
}

func TestTokenUsage429StopsBatchAndHonorsCooldownWithoutInterval(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	c := newAPITestClient()
	c.SetTokenUsageLimit(srv.URL, 1, 0)
	tokens := make([]NewAPIToken, 20)
	for range 2 {
		_, err := c.GetTokensUsage(context.Background(), srv.URL, NewAPIAuth{}, tokens, time.Now(), time.Now(), 500000)
		if !errors.Is(err, ErrRateLimited) {
			t.Fatalf("want rate limited, got %v", err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("429 should stop queued and subsequent requests, got %d", calls.Load())
	}
}

func TestTokenUsageLimiterCancellationAndReconfiguration(t *testing.T) {
	c := newAPITestClient()
	const key = "test://limiter-reconfiguration"
	c.SetTokenUsageLimit(key, 1, 0)
	l := tokenUsageLimiterFor(key)
	release, err := l.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c.SetTokenUsageLimit(key, 2, 0)
	release2, err := l.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c.SetTokenUsageLimit(key, 1, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := l.acquire(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked acquire = %v", err)
	}
	release()
	release2()
}

// logQueryIntervalServer 模拟按用户限制日志查询间隔的站点：距上次放行不足 gap 即
// 429 + Retry-After: 1（ant 的 LOG_QUERY_RATE_LIMITED）。
func logQueryIntervalServer(gap time.Duration, calls, limited *atomic.Int64) *httptest.Server {
	var mu sync.Mutex
	var last time.Time
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		mu.Lock()
		allowed := last.IsZero() || time.Since(last) >= gap
		if allowed {
			last = time.Now()
		}
		mu.Unlock()
		if !allowed {
			limited.Add(1)
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"code":"LOG_QUERY_RATE_LIMITED","success":false}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"quota":500000}}`))
	}))
}

func TestTokenUsageRetriesTransient429(t *testing.T) {
	var calls, limited atomic.Int64
	srv := logQueryIntervalServer(800*time.Millisecond, &calls, &limited)
	defer srv.Close()
	c := newAPITestClient()
	c.SetTokenUsageLimit(srv.URL, 1, 0)
	tokens := []NewAPIToken{{ID: 1}, {ID: 2}, {ID: 3}, {ID: 4}, {ID: 5}, {ID: 6}}
	out, err := c.GetTokensUsage(context.Background(), srv.URL, NewAPIAuth{}, tokens, time.Now(), time.Now(), 500000)
	if err != nil {
		t.Fatalf("transient 429 should be retried, got %v (calls=%d limited=%d)", err, calls.Load(), limited.Load())
	}
	if len(out) != len(tokens) {
		t.Fatalf("got %d usages, want %d", len(out), len(tokens))
	}
	if limited.Load() == 0 {
		t.Fatal("test server never rate limited; scenario not exercised")
	}
	// 第 2 个请求撞墙一次后应学到间隔，其余请求不再被拒
	if limited.Load() != 1 {
		t.Fatalf("limiter should learn the interval after the first 429, got %d 429s", limited.Load())
	}
}

func TestTodayStatSharesLimiterWithTokenUsage(t *testing.T) {
	var calls, limited atomic.Int64
	srv := logQueryIntervalServer(800*time.Millisecond, &calls, &limited)
	defer srv.Close()
	c := newAPITestClient()
	c.SetTokenUsageLimit(srv.URL, 1, 900)
	if _, _, err := c.GetTodayStat(context.Background(), srv.URL, NewAPIAuth{}, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetTokensUsage(context.Background(), srv.URL, NewAPIAuth{}, []NewAPIToken{{ID: 1}}, time.Now(), time.Now(), 500000); err != nil {
		t.Fatal(err)
	}
	if limited.Load() != 0 {
		t.Fatalf("configured interval should space balance and cost queries, got %d 429s", limited.Load())
	}
}
