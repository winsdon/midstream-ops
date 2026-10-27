package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"sub2api-account-monitor/internal/config"
	"sub2api-account-monitor/internal/pkg/secretbox"
	"sub2api-account-monitor/internal/repository"
)

func TestCatchUpWindow(t *testing.T) {
	loc := time.FixedZone("CST", 8*60*60)
	now := time.Date(2026, 3, 2, 0, 5, 0, 0, loc) // 跨月：昨天是 2 月 28 日
	str := func(s string) *string { return &s }
	cases := []struct {
		name      string
		finalized *string
		want      int
	}{
		{"从未终结按满窗口", nil, backfillDays},
		{"空串同从未终结", str(""), backfillDays},
		{"脏数据按满窗口", str("bad"), backfillDays},
		{"已终结到昨天", str("2026-03-01"), 0},
		{"已终结到今天", str("2026-03-02"), 0},
		{"落后一天：补昨天连今天", str("2026-02-28"), 2},
		{"停机三天", str("2026-02-26"), 4},
		{"超过上限截断", str("2025-01-01"), backfillDays},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := catchUpWindow(c.finalized, now); got != c.want {
				t.Fatalf("catchUpWindow=%d, want %d", got, c.want)
			}
		})
	}
}

// TestCostSyncCatchUpAfterMidnight 已回补过的站，finalized_through 落后两天时：
// 本轮须重拉 (finalized, 昨天] 连同今天，并把 finalized_through 推进到昨天；
// 下一轮已终结到昨天，不再多打逐日查询。
func TestCostSyncCatchUpAfterMidnight(t *testing.T) {
	var statCalls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota_per_unit":100000}}`))
		case "/api/token/":
			_, _ = w.Write([]byte(`{"success":true,"data":{"items":[{"id":42,"name":"kimi","group":"vip","status":1,"key":"sk-kimi"}],"total":1}}`))
		case "/api/log/self/stat":
			statCalls.Add(1)
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota":250000,"rpm":3}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	store := newTestStore(t)
	ctx := context.Background()
	providerRepo := repository.NewProviderRepo(store, &secretbox.Box{})
	p, err := providerRepo.Create(ctx, repository.CreateParams{
		Name: "tongba", BalanceType: "sub2api", Platform: "new-api", AuthMode: "user_key",
		BaseURL: srv.URL, AccessToken: "pat", UpstreamUserID: "7",
	})
	if err != nil {
		t.Fatalf("Create provider: %v", err)
	}
	loc := time.FixedZone("CST", 8*60*60)
	costRepo := repository.NewUpstreamCostRepo(store)
	svc := NewCostSyncService(providerRepo, costRepo, nil, &config.Config{
		Location: loc, Cost: config.CostConfig{TimeoutSeconds: 3},
	})

	now := time.Now().In(loc)
	stale := now.AddDate(0, 0, -3).Format("2006-01-02")
	backfilled := now.Add(-72 * time.Hour)
	if err := costRepo.SaveSyncState(ctx, repository.CostSyncState{
		ProviderID: p.ID, BackfilledAt: &backfilled, FinalizedThrough: &stale,
	}); err != nil {
		t.Fatalf("SaveSyncState: %v", err)
	}

	if err := svc.SyncOne(ctx, p, nil, false); err != nil {
		t.Fatalf("SyncOne: %v", err)
	}
	// 今日 1 次 + 补齐窗口 3 天（前天、昨天、今天）
	if got := statCalls.Load(); got != 1+3 {
		t.Fatalf("stat calls=%d, want 4", got)
	}
	st, err := svc.SyncState(ctx, p.ID)
	if err != nil {
		t.Fatalf("SyncState: %v", err)
	}
	wantYesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	if st.FinalizedThrough == nil || *st.FinalizedThrough != wantYesterday {
		t.Fatalf("finalized_through=%v, want %s", st.FinalizedThrough, wantYesterday)
	}

	statCalls.Store(0)
	if err := svc.SyncOne(ctx, p, nil, false); err != nil {
		t.Fatalf("SyncOne #2: %v", err)
	}
	if got := statCalls.Load(); got != 1 {
		t.Fatalf("second round stat calls=%d, want 1 (today only)", got)
	}
}
