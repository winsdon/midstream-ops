package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"sub2api-account-monitor/internal/config"
	"sub2api-account-monitor/internal/pkg/keyidentity"
	"sub2api-account-monitor/internal/pkg/secretbox"
	"sub2api-account-monitor/internal/repository"
)

func TestCostSyncNewAPIPersistsMappingTodayAndBackfill(t *testing.T) {
	var statCalls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota_per_unit":100000}}`))
		case "/api/token/":
			if r.Header.Get("Authorization") != "Bearer pat" || r.Header.Get("New-Api-User") != "7" {
				http.Error(w, "bad auth", http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"items":[{"id":42,"name":"kimi","group":"vip","status":1}],"total":1}}`))
		case "/api/token/42/key":
			if r.Method != http.MethodPost {
				http.Error(w, "method", http.StatusMethodNotAllowed)
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"key":"sk-kimi"}}`))
		case "/api/log/self/stat":
			statCalls.Add(1)
			if r.URL.Query().Get("type") != "2" || r.URL.Query().Get("token_name") != "kimi" || r.URL.Query().Get("group") != "vip" {
				http.Error(w, "bad filters", http.StatusBadRequest)
				return
			}
			if r.URL.Query().Get("start_timestamp") == "" || r.URL.Query().Get("end_timestamp") == "" {
				http.Error(w, "missing range", http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota":250000,"rpm":3}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	store := newTestStore(t)
	providerRepo := repository.NewProviderRepo(store, &secretbox.Box{})
	p, err := providerRepo.Create(context.Background(), repository.CreateParams{
		Name:           "tongba",
		BalanceType:    "sub2api",
		Platform:       "new-api",
		AuthMode:       "user_key",
		BaseURL:        srv.URL,
		AccessToken:    "pat",
		UpstreamUserID: "7",
	})
	if err != nil {
		t.Fatalf("Create provider: %v", err)
	}

	loc := time.FixedZone("CST", 8*60*60)
	svc := NewCostSyncService(providerRepo, repository.NewUpstreamCostRepo(store), nil, &config.Config{
		Location: loc,
		Cost:     config.CostConfig{TimeoutSeconds: 3},
	})
	fp := keyidentity.Fingerprint("sk-kimi")
	fingerprints := map[string]repository.AccountKeyFingerprint{
		fp: {AccountID: 146, AccountName: "【tongba】kimi ", Fingerprint: fp},
	}
	if err := svc.SyncOne(context.Background(), p, fingerprints, true); err != nil {
		t.Fatalf("SyncOne: %v", err)
	}

	var accountID int64
	var storedFingerprint string
	if err := store.DB().QueryRow(`SELECT account_id, key_fingerprint FROM upstream_key_map WHERE provider_id=? AND upstream_key_id=42`, p.ID).
		Scan(&accountID, &storedFingerprint); err != nil {
		t.Fatalf("mapping row: %v", err)
	}
	if accountID != 146 || storedFingerprint != fp {
		t.Fatalf("mapping account=%d fingerprint=%q", accountID, storedFingerprint)
	}

	var rows int
	var totalCost float64
	if err := store.DB().QueryRow(`SELECT COUNT(*), SUM(actual_cost) FROM upstream_key_costs WHERE provider_id=? AND upstream_key_id=42`, p.ID).
		Scan(&rows, &totalCost); err != nil {
		t.Fatalf("cost rows: %v", err)
	}
	if rows != backfillDays || totalCost != float64(backfillDays)*2.5 {
		t.Fatalf("rows=%d totalCost=%v, want %d/%v", rows, totalCost, backfillDays, float64(backfillDays)*2.5)
	}
	if statCalls.Load() != backfillDays+1 {
		t.Fatalf("stat calls=%d, want %d", statCalls.Load(), backfillDays+1)
	}

	var matched int64
	var backfilledAt *string
	if err := store.DB().QueryRow(`SELECT keys_matched, backfilled_at FROM cost_sync_state WHERE provider_id=?`, p.ID).
		Scan(&matched, &backfilledAt); err != nil {
		t.Fatalf("sync state: %v", err)
	}
	if matched != 1 || backfilledAt == nil || *backfilledAt == "" {
		t.Fatalf("matched=%d backfilled_at=%v", matched, backfilledAt)
	}
}

// TestCostSyncNewAPITokenKey429KeepsUsage 复现咩咩类故障：
// 揭 key 的 POST /api/token/{id}/key 被限流后，不得整轮放弃——用量仍要落库。
func TestCostSyncNewAPITokenKey429KeepsUsage(t *testing.T) {
	var keyPosts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota_per_unit":100000}}`))
		case r.URL.Path == "/api/token/":
			_, _ = w.Write([]byte(`{"success":true,"data":{"items":[
				{"id":1,"name":"alpha","group":"vip","status":1},
				{"id":2,"name":"beta","group":"vip","status":1}
			],"total":2}}`))
		case r.URL.Path == "/api/token/1/key":
			keyPosts.Add(1)
			_, _ = w.Write([]byte(`{"success":true,"data":{"key":"sk-alpha"}}`))
		case r.URL.Path == "/api/token/2/key":
			keyPosts.Add(1)
			w.WriteHeader(http.StatusTooManyRequests)
		case r.URL.Path == "/api/log/self/stat":
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota":100000,"rpm":1}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	store := newTestStore(t)
	providerRepo := repository.NewProviderRepo(store, &secretbox.Box{})
	costRepo := repository.NewUpstreamCostRepo(store)
	p, err := providerRepo.Create(context.Background(), repository.CreateParams{
		Name:           "咩咩",
		BalanceType:    "sub2api",
		Platform:       "new-api",
		AuthMode:       "user_key",
		BaseURL:        srv.URL,
		AccessToken:    "pat",
		UpstreamUserID: "7",
	})
	if err != nil {
		t.Fatalf("Create provider: %v", err)
	}

	loc := time.FixedZone("CST", 8*60*60)
	svc := NewCostSyncService(providerRepo, costRepo, nil, &config.Config{
		Location: loc,
		Cost:     config.CostConfig{TimeoutSeconds: 3},
	})
	fpAlpha := keyidentity.Fingerprint("sk-alpha")
	fingerprints := map[string]repository.AccountKeyFingerprint{
		fpAlpha: {AccountID: 11, AccountName: "【咩咩】alpha", Fingerprint: fpAlpha},
	}
	if err := svc.SyncOne(context.Background(), p, fingerprints, false); err != nil {
		t.Fatalf("SyncOne: %v", err)
	}

	var rows int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM upstream_key_costs WHERE provider_id=?`, p.ID).Scan(&rows); err != nil {
		t.Fatalf("cost rows: %v", err)
	}
	if rows != 2 {
		t.Fatalf("今日成本行数=%d，429 揭 key 失败后仍应写入全部 token 的用量", rows)
	}

	var storedFP string
	if err := store.DB().QueryRow(`SELECT key_fingerprint FROM upstream_key_map WHERE provider_id=? AND upstream_key_id=1`, p.ID).
		Scan(&storedFP); err != nil {
		t.Fatalf("mapping: %v", err)
	}
	if storedFP != fpAlpha {
		t.Fatalf("已揭开的 key 指纹应保留，得到 %q", storedFP)
	}

	var lastError *string
	var lastSynced *string
	if err := store.DB().QueryRow(`SELECT last_error, last_synced_at FROM cost_sync_state WHERE provider_id=?`, p.ID).
		Scan(&lastError, &lastSynced); err != nil {
		t.Fatalf("sync state: %v", err)
	}
	if lastError != nil && *lastError != "" {
		t.Fatalf("用量已成功时不应把揭 key 429 记成成本同步失败，得到 %q", *lastError)
	}
	if lastSynced == nil || *lastSynced == "" {
		t.Fatal("用量已成功时应写入 last_synced_at")
	}
	if keyPosts.Load() < 2 {
		t.Fatalf("应尝试揭两个 key，实际 POST /key %d 次", keyPosts.Load())
	}
}

// TestCostSyncNewAPISkipsStoredKeyReveal 已有指纹的 token 不再打 /key，避免每轮撞限流。
func TestCostSyncNewAPISkipsStoredKeyReveal(t *testing.T) {
	var key1Posts atomic.Int64
	var key2Posts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota_per_unit":100000}}`))
		case r.URL.Path == "/api/token/":
			_, _ = w.Write([]byte(`{"success":true,"data":{"items":[
				{"id":1,"name":"alpha","group":"vip","status":1},
				{"id":2,"name":"beta","group":"vip","status":1}
			],"total":2}}`))
		case r.URL.Path == "/api/token/1/key":
			key1Posts.Add(1)
			w.WriteHeader(http.StatusTooManyRequests)
		case r.URL.Path == "/api/token/2/key":
			key2Posts.Add(1)
			_, _ = w.Write([]byte(`{"success":true,"data":{"key":"sk-beta"}}`))
		case r.URL.Path == "/api/log/self/stat":
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota":100000,"rpm":1}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	store := newTestStore(t)
	providerRepo := repository.NewProviderRepo(store, &secretbox.Box{})
	costRepo := repository.NewUpstreamCostRepo(store)
	p, err := providerRepo.Create(context.Background(), repository.CreateParams{
		Name:           "咩咩",
		BalanceType:    "sub2api",
		Platform:       "new-api",
		AuthMode:       "user_key",
		BaseURL:        srv.URL,
		AccessToken:    "pat",
		UpstreamUserID: "7",
	})
	if err != nil {
		t.Fatalf("Create provider: %v", err)
	}

	fpAlpha := keyidentity.Fingerprint("sk-alpha")
	if err := costRepo.UpsertMappings(context.Background(), []repository.UpstreamKeyMapping{{
		ProviderID:     p.ID,
		UpstreamKeyID:  1,
		KeyName:        "alpha",
		KeyFingerprint: fpAlpha,
		Status:         "1",
		GroupName:      "vip",
	}}); err != nil {
		t.Fatalf("seed mapping: %v", err)
	}

	loc := time.FixedZone("CST", 8*60*60)
	svc := NewCostSyncService(providerRepo, costRepo, nil, &config.Config{
		Location: loc,
		Cost:     config.CostConfig{TimeoutSeconds: 3},
	})
	fpBeta := keyidentity.Fingerprint("sk-beta")
	fingerprints := map[string]repository.AccountKeyFingerprint{
		fpAlpha: {AccountID: 11, AccountName: "【咩咩】alpha", Fingerprint: fpAlpha},
		fpBeta:  {AccountID: 12, AccountName: "【咩咩】beta", Fingerprint: fpBeta},
	}
	if err := svc.SyncOne(context.Background(), p, fingerprints, false); err != nil {
		t.Fatalf("SyncOne: %v", err)
	}
	if key1Posts.Load() != 0 {
		t.Fatalf("已有指纹的 token 不应再 POST /key，实际 %d 次", key1Posts.Load())
	}
	if key2Posts.Load() != 1 {
		t.Fatalf("未揭过的 token 应 POST /key 一次，实际 %d", key2Posts.Load())
	}

	var storedFP string
	var accountID int64
	if err := store.DB().QueryRow(`SELECT key_fingerprint, account_id FROM upstream_key_map WHERE provider_id=? AND upstream_key_id=1`, p.ID).
		Scan(&storedFP, &accountID); err != nil {
		t.Fatalf("mapping: %v", err)
	}
	if storedFP != fpAlpha || accountID != 11 {
		t.Fatalf("已存指纹应保留并用于匹配，fingerprint=%q account=%d", storedFP, accountID)
	}
}

// TestCostSyncNewAPIUsesListKeyWhenUnmasked 列表已带明文 key 时不必再 POST /key。
func TestCostSyncNewAPIUsesListKeyWhenUnmasked(t *testing.T) {
	var keyPosts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota_per_unit":100000}}`))
		case r.URL.Path == "/api/token/":
			_, _ = w.Write([]byte(`{"success":true,"data":{"items":[
				{"id":1,"name":"alpha","group":"vip","status":1,"key":"sk-listed-secret-key"}
			],"total":1}}`))
		case strings.HasSuffix(r.URL.Path, "/key"):
			keyPosts.Add(1)
			w.WriteHeader(http.StatusTooManyRequests)
		case r.URL.Path == "/api/log/self/stat":
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota":100000,"rpm":1}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	store := newTestStore(t)
	providerRepo := repository.NewProviderRepo(store, &secretbox.Box{})
	p, err := providerRepo.Create(context.Background(), repository.CreateParams{
		Name:           "咩咩",
		BalanceType:    "sub2api",
		Platform:       "new-api",
		AuthMode:       "user_key",
		BaseURL:        srv.URL,
		AccessToken:    "pat",
		UpstreamUserID: "7",
	})
	if err != nil {
		t.Fatalf("Create provider: %v", err)
	}
	svc := NewCostSyncService(providerRepo, repository.NewUpstreamCostRepo(store), nil, &config.Config{
		Location: time.FixedZone("CST", 8*60*60),
		Cost:     config.CostConfig{TimeoutSeconds: 3},
	})
	fp := keyidentity.Fingerprint("sk-listed-secret-key")
	fingerprints := map[string]repository.AccountKeyFingerprint{
		fp: {AccountID: 11, AccountName: "【咩咩】alpha", Fingerprint: fp},
	}
	if err := svc.SyncOne(context.Background(), p, fingerprints, false); err != nil {
		t.Fatalf("SyncOne: %v", err)
	}
	if keyPosts.Load() != 0 {
		t.Fatalf("列表已有明文 key 时不应 POST /key，实际 %d 次", keyPosts.Load())
	}
	var accountID int64
	if err := store.DB().QueryRow(`SELECT account_id FROM upstream_key_map WHERE provider_id=? AND upstream_key_id=1`, p.ID).
		Scan(&accountID); err != nil {
		t.Fatalf("mapping: %v", err)
	}
	if accountID != 11 {
		t.Fatalf("account=%d，期望用列表明文 key 匹配到 11", accountID)
	}
}

func TestUniqueAccountFingerprintsMarksDuplicatesAmbiguous(t *testing.T) {
	index := uniqueAccountFingerprints([]repository.AccountKeyFingerprint{
		{AccountID: 57, AccountName: "【walk】kiro 高缓 0.055", Fingerprint: "same"},
		{AccountID: 159, AccountName: "【walk】ccmax 备用", Fingerprint: "same"},
		{AccountID: 146, AccountName: "【tongba】kimi", Fingerprint: "unique"},
	})
	if got := index["same"].AccountID; got != 0 {
		t.Fatalf("duplicate fingerprint resolved to account %d", got)
	}
	if got := index["unique"].AccountID; got != 146 {
		t.Fatalf("unique fingerprint resolved to account %d", got)
	}
}

func TestMatchCostAccountFallsBackOnlyWhenNameIsUnique(t *testing.T) {
	fingerprints := map[string]repository.AccountKeyFingerprint{
		"one": {AccountID: 1, AccountName: "【tongba】kimi", Fingerprint: "one"},
		"two": {AccountID: 2, AccountName: "【other】same", Fingerprint: "two"},
		"tri": {AccountID: 3, AccountName: "【other】same", Fingerprint: "tri"},
	}
	if acc, ok := matchCostAccount("", "kimi", "", fingerprints); !ok || acc.AccountID != 1 {
		t.Fatalf("unique name fallback = %+v/%v", acc, ok)
	}
	if acc, ok := matchCostAccount("", "same", "", fingerprints); ok {
		t.Fatalf("ambiguous name fallback unexpectedly matched %+v", acc)
	}
	if acc, ok := matchCostAccount("same", "kiro 高缓 0.055", "", map[string]repository.AccountKeyFingerprint{
		"same": {Fingerprint: "same"},
	}); ok {
		t.Fatalf("ambiguous fingerprint unexpectedly fell back to %+v", acc)
	}
}

func TestGroupAccountsByKeysUsesFingerprintNotName(t *testing.T) {
	fp := keyidentity.Fingerprint("sk-walk-kiro")
	fingerprints := map[string]repository.AccountKeyFingerprint{
		fp: {AccountID: 57, AccountName: "【walk】kiro 高缓 0.055", Fingerprint: fp},
	}
	keys := []ProviderAPIKey{
		{
			ID:   1,
			Name: "some-random-key-label",
			Key:  "sk-walk-kiro",
			Group: &struct {
				Name           string  `json:"name"`
				RateMultiplier float64 `json:"rate_multiplier"`
			}{Name: "Kiro - 中缓", RateMultiplier: 0.06},
		},
		{
			ID:   2,
			Name: "unmatched",
			Key:  "sk-other",
			Group: &struct {
				Name           string  `json:"name"`
				RateMultiplier float64 `json:"rate_multiplier"`
			}{Name: "Claude Max", RateMultiplier: 0.85},
		},
	}

	got := groupAccountsByKeys(keys, fingerprints)
	hits := got["Kiro - 中缓"]
	if len(hits) != 1 || hits[0].AccountID != 57 {
		t.Fatalf("应按指纹归到 Kiro - 中缓，实际 %#v", got)
	}
	if _, ok := got["Claude Max"]; ok {
		t.Fatal("未匹配上的 key 不该出现在任何分组")
	}
}

func TestGroupAccountsByKeysDedupesSameAccount(t *testing.T) {
	fp := keyidentity.Fingerprint("sk-shared")
	fingerprints := map[string]repository.AccountKeyFingerprint{
		fp: {AccountID: 9, AccountName: "acc", Fingerprint: fp},
	}
	g := &struct {
		Name           string  `json:"name"`
		RateMultiplier float64 `json:"rate_multiplier"`
	}{Name: "default"}
	keys := []ProviderAPIKey{
		{ID: 1, Name: "a", Key: "sk-shared", Group: g},
		{ID: 2, Name: "b", Key: "sk-shared", Group: g},
	}
	got := groupAccountsByKeys(keys, fingerprints)
	if len(got["default"]) != 1 {
		t.Fatalf("同一账号在同一分组只应出现一次，实际 %#v", got["default"])
	}
}

func TestSub2APIFingerprintMatchRegression(t *testing.T) {
	fp := keyidentity.Fingerprint("sub2-key")
	accounts := map[string]repository.AccountKeyFingerprint{
		fp: {AccountID: 88, AccountName: "sub2 account", Fingerprint: fp},
	}
	acc, ok := matchCostAccount(fp, "unrelated key name", "", accounts)
	if !ok || acc.AccountID != 88 {
		t.Fatalf("sub2api fingerprint regression: %+v/%v", acc, ok)
	}
}

func TestResolveUpstreamKeyRate(t *testing.T) {
	group := func(name string, rate float64) *struct {
		Name           string  `json:"name"`
		RateMultiplier float64 `json:"rate_multiplier"`
	} {
		return &struct {
			Name           string  `json:"name"`
			RateMultiplier float64 `json:"rate_multiplier"`
		}{Name: name, RateMultiplier: rate}
	}

	tests := []struct {
		name      string
		key       ProviderAPIKey
		overrides map[string]float64
		want      *float64
	}{
		{
			name: "只有分组倍率",
			key:  ProviderAPIKey{Group: group("Claude Max", 0.85)},
			want: floatPtr(0.85),
		},
		{
			name: "key 顶层倍率优先于分组默认",
			key:  ProviderAPIKey{RateMultiplier: 0.06, Group: group("Kiro", 1)},
			want: floatPtr(0.06),
		},
		{
			name:      "专属覆盖优先于分组默认",
			key:       ProviderAPIKey{Group: group("Kiro", 1)},
			overrides: map[string]float64{"Kiro": 0.06},
			want:      floatPtr(0.06),
		},
		{
			name: "无分组无顶层",
			key:  ProviderAPIKey{},
			want: nil,
		},
		{
			name:      "new-api 仅有分组名时用覆盖",
			key:       ProviderAPIKey{Group: group("vip", 0)},
			overrides: map[string]float64{"vip": 1.5},
			want:      floatPtr(1.5),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveUpstreamKeyRate(tt.key, tt.overrides)
			if tt.want == nil {
				if got != nil {
					t.Fatalf("got %v, want nil", *got)
				}
				return
			}
			if got == nil {
				t.Fatalf("got nil, want %v", *tt.want)
			}
			if *got != *tt.want {
				t.Fatalf("got %v, want %v", *got, *tt.want)
			}
		})
	}
}

func floatPtr(v float64) *float64 { return &v }
