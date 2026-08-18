package service

import (
	"context"
	"net/http"
	"net/http/httptest"
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
