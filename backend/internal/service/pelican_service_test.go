package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"sub2api-account-monitor/internal/repository"
)

type pelicanTestPG struct {
	accounts []repository.PGAccount
	groups   map[int64][]int64
	fail     bool
}

func (p *pelicanTestPG) ListGroups(context.Context) ([]repository.PGGroup, error) {
	return []repository.PGGroup{{ID: 1, Name: "group", Platform: "anthropic", Status: "active"}}, nil
}
func (p *pelicanTestPG) ListGroupAvailableModels(context.Context) ([]repository.PGGroupModels, error) {
	return []repository.PGGroupModels{{GroupID: 1, GroupName: "group", Platform: "anthropic", Models: []string{"claude-opus-5"}}}, nil
}
func (p *pelicanTestPG) ListPelicanAccounts(context.Context) ([]repository.PGPelicanAccount, error) {
	out := make([]repository.PGPelicanAccount, 0, len(p.accounts))
	for _, a := range p.accounts {
		out = append(out, repository.PGPelicanAccount{PGAccount: a})
	}
	return out, nil
}
func (p *pelicanTestPG) ListAccountGroupIDs(context.Context) (map[int64][]int64, error) {
	if p.fail {
		return nil, errors.New("offline")
	}
	return p.groups, nil
}
func (p *pelicanTestPG) PelicanVisibleGroups(context.Context) ([]int64, error) {
	if p.fail {
		return nil, errors.New("offline")
	}
	return []int64{1}, nil
}

func pelicanTestServer(t *testing.T, respond func(w http.ResponseWriter)) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		respond(w)
	}))
	t.Cleanup(server.Close)
	return server, &calls
}

func TestPelicanVisibilityFailsClosed(t *testing.T) {
	s := NewPelicanService(nil, &pelicanTestPG{fail: true})
	if _, err := s.VisibleGroups(context.Background()); !errors.Is(err, ErrPelicanUnavailable) {
		t.Fatalf("want unavailable, got %v", err)
	}
}

func TestPelicanRequestValidation(t *testing.T) {
	manual := PelicanTarget{GroupID: 1, Source: PelicanSourceManual, BaseURL: "https://api.example.com/v1/messages", APIKey: "secret", Model: "test", Protocol: "messages"}
	r := PelicanRequest{RequestID: "request-123", Targets: []PelicanTarget{manual, manual}}
	if normalizePelicanRequest(&r) == nil {
		t.Fatal("accepted duplicate targets")
	}
	r.Targets = r.Targets[:1]
	r.Prompt = "  original\n"
	if err := normalizePelicanRequest(&r); err != nil || r.Prompt != "  original\n" || r.MaxTokens != 32000 || r.TimeoutSeconds != 900 {
		t.Fatalf("bad defaults %+v %v", r, err)
	}
	// The generation path is stripped so the gateway can rebuild it per protocol.
	if got := r.Targets[0].BaseURL; got != "https://api.example.com" {
		t.Fatalf("base url not normalized: %s", got)
	}
	for _, bad := range []PelicanTarget{
		{GroupID: 1, Source: PelicanSourceManual, BaseURL: "https://api.example.com", Model: "m", Protocol: "messages"},
		{GroupID: 1, Source: PelicanSourceManual, AccountID: 2, BaseURL: "https://api.example.com", APIKey: "k", Model: "m", Protocol: "messages"},
		{GroupID: 1, Source: PelicanSourceManual, BaseURL: "https://api.example.com?x=1", APIKey: "k", Model: "m", Protocol: "messages"},
		{GroupID: 1, Source: PelicanSourceManual, BaseURL: "https://user:pass@api.example.com", APIKey: "k", Model: "m", Protocol: "messages"},
		{GroupID: 1, Source: PelicanSourceAccount, BaseURL: "https://api.example.com", Model: "m", Protocol: "messages"},
		{GroupID: 1, Source: "", AccountID: 2, Model: "m", Protocol: "messages"},
		{GroupID: 1, Source: PelicanSourceAccount, AccountID: 2, Model: "m", Protocol: "grpc"},
	} {
		req := PelicanRequest{RequestID: "request-123", Targets: []PelicanTarget{bad}}
		if normalizePelicanRequest(&req) == nil {
			t.Fatalf("accepted invalid target %+v", bad)
		}
	}
}

// 空提示词一律用水墨鹈鹕题，不再按协议切换。
func TestPelicanDefaultPrompt(t *testing.T) {
	claude := PelicanTarget{GroupID: 1, Source: PelicanSourceAccount, AccountID: 2, Model: "claude-opus-5-5", Protocol: "messages"}
	gpt := PelicanTarget{GroupID: 1, Source: PelicanSourceAccount, AccountID: 3, Model: "gpt-5.1", Protocol: "responses"}
	for _, targets := range [][]PelicanTarget{{claude}, {gpt}, {claude, gpt}} {
		r := PelicanRequest{RequestID: "request-123", Targets: targets}
		if err := normalizePelicanRequest(&r); err != nil || r.Prompt != DefaultPelicanPrompt {
			t.Fatalf("prompt=%q err=%v", r.Prompt, err)
		}
	}
	if !strings.Contains(DefaultPelicanPrompt, "水墨") || !strings.Contains(DefaultPelicanPrompt, "鹈鹕") {
		t.Fatalf("default prompt must be the ink-wash pelican: %q", DefaultPelicanPrompt)
	}
}

// 不勾糖果题时请求体与旧版逐字节一致，已提交批次的幂等 hash 不会因为加了字段而变。
func TestPelicanCandyKeepsRequestHash(t *testing.T) {
	raw, _ := json.Marshal(PelicanRequest{RequestID: "request-123"})
	if strings.Contains(string(raw), "candy") {
		t.Fatalf("candy must be omitted when off: %s", raw)
	}
	raw, _ = json.Marshal(PelicanRequest{RequestID: "request-123", Candy: true})
	if !strings.Contains(string(raw), `"candy":true`) {
		t.Fatalf("candy flag missing: %s", raw)
	}
}

// 账号状态与 sub2api 账号列表同一优先级：限流中 > 过载 > 错误 > 临时不可调度 > 停用 > 不可调度 > 正常。
func TestPelicanAccountState(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	later, earlier := now.Add(time.Hour), now.Add(-time.Hour)
	account := func(status string, schedulable bool, mutate func(*repository.PGPelicanAccount)) repository.PGPelicanAccount {
		a := repository.PGPelicanAccount{PGAccount: repository.PGAccount{Status: status, Schedulable: schedulable}}
		if mutate != nil {
			mutate(&a)
		}
		return a
	}
	cases := []struct {
		name  string
		in    repository.PGPelicanAccount
		want  string
		until bool
	}{
		{"active", account("active", true, nil), PelicanAccountActive, false},
		{"paused", account("active", false, nil), PelicanAccountUnschedulable, false},
		{"inactive", account("inactive", true, nil), PelicanAccountInactive, false},
		{"disabled", account("disabled", true, nil), PelicanAccountInactive, false},
		{"error", account("error", false, nil), PelicanAccountError, false},
		{"rate limited beats error", account("error", true, func(a *repository.PGPelicanAccount) { a.RateLimitResetAt = &later }), PelicanAccountRateLimited, true},
		{"overloaded", account("active", true, func(a *repository.PGPelicanAccount) { a.OverloadUntil = &later }), PelicanAccountOverloaded, true},
		{"temp unschedulable", account("active", true, func(a *repository.PGPelicanAccount) { a.TempUnschedulableUntil = &later }), PelicanAccountTempUnschedulable, true},
		{"error beats temp unschedulable", account("error", true, func(a *repository.PGPelicanAccount) { a.TempUnschedulableUntil = &later }), PelicanAccountError, false},
		{"expired limits ignored", account("active", true, func(a *repository.PGPelicanAccount) {
			a.RateLimitResetAt, a.OverloadUntil, a.TempUnschedulableUntil = &earlier, &earlier, &earlier
		}), PelicanAccountActive, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, until := pelicanAccountState(tc.in, now)
			if got != tc.want || (until != nil) != tc.until {
				t.Fatalf("state=%s until=%v want %s until=%v", got, until, tc.want, tc.until)
			}
		})
	}
}

func TestPelicanEffortValidation(t *testing.T) {
	target := PelicanTarget{GroupID: 1, Source: PelicanSourceAccount, AccountID: 2, Model: "claude-opus-5-5", Protocol: "messages"}
	for _, ok := range []string{"", "low", "medium", "high", " xhigh ", "max"} {
		r := PelicanRequest{RequestID: "request-123", Effort: ok, Targets: []PelicanTarget{target}}
		if err := normalizePelicanRequest(&r); err != nil {
			t.Fatalf("rejected effort %q: %v", ok, err)
		}
		if r.Effort != strings.TrimSpace(ok) {
			t.Fatalf("effort not trimmed: %q", r.Effort)
		}
	}
	for _, bad := range []string{"HIGH", "minimal", "ultra"} {
		r := PelicanRequest{RequestID: "request-123", Effort: bad, Targets: []PelicanTarget{target}}
		if normalizePelicanRequest(&r) == nil {
			t.Fatalf("accepted effort %q", bad)
		}
	}
	// 不指定思考强度时请求序列化不带 effort：旧待确认提交重放时 hash 不变。
	raw, _ := json.Marshal(PelicanRequest{RequestID: "request-123"})
	if strings.Contains(string(raw), "effort") {
		t.Fatalf("empty effort must be omitted: %s", raw)
	}
}

func TestPelicanGroupsAndAccountsHideSecrets(t *testing.T) {
	s := NewPelicanService(nil, &pelicanTestPG{
		accounts: []repository.PGAccount{
			{ID: 2, Name: "claude", Platform: "anthropic", BaseURL: "https://api.example.com/", APIKey: "sk-secret"},
			{ID: 3, Name: "no-key", Platform: "openai", BaseURL: "https://api.example.com", APIKey: ""},
			{ID: 4, Name: "gemini", Platform: "gemini", BaseURL: "https://api.example.com", APIKey: "sk-secret"},
		},
		groups: map[int64][]int64{2: {1, 7}},
	})
	accounts, err := s.Accounts(context.Background())
	if err != nil || len(accounts) != 1 || accounts[0].ID != 2 || accounts[0].BaseURL != "https://api.example.com" {
		t.Fatalf("accounts %+v %v", accounts, err)
	}
	// 分组归属随账号一起下发，管理端才能按归属分组过滤选择项。
	if len(accounts[0].GroupIDs) != 2 || accounts[0].GroupIDs[0] != 1 || accounts[0].GroupIDs[1] != 7 {
		t.Fatalf("group ids %+v", accounts[0].GroupIDs)
	}
	raw, _ := json.Marshal(accounts)
	if strings.Contains(string(raw), "sk-secret") || strings.Contains(string(raw), "api_key") {
		t.Fatalf("account list leaked key material: %s", raw)
	}
	groups, err := s.Groups(context.Background())
	if err != nil || len(groups) != 1 || groups[0].Name != "group" || len(groups[0].Models) != 1 {
		t.Fatalf("groups %+v %v", groups, err)
	}
	// 账号没挂任何分组时回空数组，前端拿到的是 [] 而不是 null。
	plain := NewPelicanService(nil, &pelicanTestPG{accounts: []repository.PGAccount{{ID: 5, Name: "solo", Platform: "anthropic", BaseURL: "https://api.example.com", APIKey: "k"}}})
	plainAccounts, err := plain.Accounts(context.Background())
	if err != nil || len(plainAccounts) != 1 {
		t.Fatalf("accounts %+v %v", plainAccounts, err)
	}
	if raw, _ := json.Marshal(plainAccounts); !strings.Contains(string(raw), `"group_ids":[]`) {
		t.Fatalf("group_ids must serialize as an array: %s", raw)
	}
	// 分组归属查询失败必须关门，不能悄悄下发不过滤的账号列表。
	if _, err := NewPelicanService(nil, &pelicanTestPG{fail: true}).Accounts(context.Background()); !errors.Is(err, ErrPelicanUnavailable) {
		t.Fatalf("want unavailable, got %v", err)
	}
}

func TestPelicanManualTargetKeepsKeyOutOfStorage(t *testing.T) {
	store, cleanup, err := repository.NewTestStore()
	if errors.Is(err, repository.ErrNoTestDSN) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	server, calls := pelicanTestServer(t, func(w http.ResponseWriter) {
		fmt.Fprint(w, `{"content":[{"type":"text","text":"<svg></svg>"}],"stop_reason":"end_turn"}`)
	})
	repo := repository.NewPelicanRepo(store)
	s := NewPelicanService(repo, &pelicanTestPG{})
	defer s.Close()
	ctx := context.Background()
	req := PelicanRequest{RequestID: "manual-request", Targets: []PelicanTarget{{GroupID: 1, Source: PelicanSourceManual, BaseURL: server.URL, APIKey: "sk-manual", Model: "test", Protocol: "messages"}}}
	first, err := s.Start(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Start(ctx, req)
	if err != nil || first.ID != second.ID {
		t.Fatalf("duplicate submit: %v", err)
	}
	s.workers.Wait()
	rows, _, err := repo.List(ctx, repository.PelicanFilter{BatchID: first.ID})
	if err != nil || len(rows) != 1 || rows[0].Status != "completed" || calls.Load() != 1 {
		t.Fatalf("rows=%+v calls=%d err=%v", rows, calls.Load(), err)
	}
	row := rows[0]
	if row.Source != PelicanSourceManual || row.KeyFP == "" || row.AccountID != 0 {
		t.Fatalf("metadata %+v", row.PelicanMetadata)
	}
	if strings.Contains(row.KeyFP, "sk-manual") {
		t.Fatal("fingerprint contains the plaintext key")
	}
	stored, err := repo.Batch(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored.Request), "sk-manual") {
		t.Fatalf("task table kept the plaintext key: %s", stored.Request)
	}
	rawMeta, _ := json.Marshal(row.PelicanMetadata)
	if strings.Contains(string(rawMeta), "sk-manual") {
		t.Fatalf("result metadata kept the plaintext key: %s", rawMeta)
	}
	req.RequestID = "manual-retry"
	req.Targets[0].RetryOf = row.ID
	third, err := s.Start(ctx, req)
	if err != nil || third.ID == first.ID {
		t.Fatalf("retry %v", err)
	}
	s.workers.Wait()
	old, err := repo.Get(ctx, row.ID)
	if err != nil || old.Status != "completed" || calls.Load() != 2 {
		t.Fatalf("overwrote history %v", err)
	}
	retry, _, err := repo.List(ctx, repository.PelicanFilter{BatchID: third.ID})
	if err != nil || len(retry) != 1 || retry[0].RetryOf != old.ID {
		t.Fatal("missing retry link")
	}
}

func TestPelicanAccountTargetUsesAccountCredential(t *testing.T) {
	store, cleanup, err := repository.NewTestStore()
	if errors.Is(err, repository.ErrNoTestDSN) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	var auth string
	var sent map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("x-api-key")
		_ = json.NewDecoder(r.Body).Decode(&sent)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"content":[{"type":"text","text":"<svg></svg>"}],"stop_reason":"end_turn"}`)
	}))
	defer server.Close()
	repo := repository.NewPelicanRepo(store)
	pg := &pelicanTestPG{accounts: []repository.PGAccount{{ID: 7, Name: "acct", Platform: "anthropic", BaseURL: server.URL, APIKey: "sk-account"}}}
	s := NewPelicanService(repo, pg)
	defer s.Close()
	ctx := context.Background()
	batch, err := s.Start(ctx, PelicanRequest{RequestID: "account-request", Effort: "high", Targets: []PelicanTarget{{GroupID: 1, Source: PelicanSourceAccount, AccountID: 7, Model: "claude-opus-5-5", Protocol: "messages"}}})
	if err != nil {
		t.Fatal(err)
	}
	s.workers.Wait()
	rows, _, err := repo.List(ctx, repository.PelicanFilter{BatchID: batch.ID})
	if err != nil || len(rows) != 1 || rows[0].Status != "completed" {
		t.Fatalf("rows %+v err %v", rows, err)
	}
	if rows[0].Source != PelicanSourceAccount || rows[0].AccountID != 7 || rows[0].KeyFP != "" {
		t.Fatalf("metadata %+v", rows[0].PelicanMetadata)
	}
	// 账号名随记录留痕：管理端作品卡片直接展示，不必再查账号表。
	if rows[0].AccountName != "acct" {
		t.Fatalf("account name not snapshotted: %+v", rows[0].PelicanMetadata)
	}
	if auth != "sk-account" {
		t.Fatalf("account key not used: %q", auth)
	}
	// 思考强度随记录留痕，并按 output_config.effort 发给上游。
	if rows[0].Effort != "high" {
		t.Fatalf("effort not recorded: %+v", rows[0].PelicanMetadata)
	}
	if cfg, _ := sent["output_config"].(map[string]any); cfg["effort"] != "high" {
		t.Fatalf("effort not sent: %v", sent)
	}
	if _, err := s.Start(ctx, PelicanRequest{RequestID: "account-bad-proto", Targets: []PelicanTarget{{GroupID: 1, Source: PelicanSourceAccount, AccountID: 7, Model: "test", Protocol: "chat"}}}); err == nil {
		t.Fatal("anthropic account accepted chat completions")
	}
	if _, err := s.Start(ctx, PelicanRequest{RequestID: "account-missing", Targets: []PelicanTarget{{GroupID: 1, Source: PelicanSourceAccount, AccountID: 99, Model: "test", Protocol: "messages"}}}); err == nil || err.Error() != "pelican.errors.accountUnavailable" {
		t.Fatalf("missing account: %v", err)
	}
}

func TestPelicanServiceRejectsUnknownGroup(t *testing.T) {
	store, cleanup, err := repository.NewTestStore()
	if errors.Is(err, repository.ErrNoTestDSN) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	repo := repository.NewPelicanRepo(store)
	s := NewPelicanService(repo, &pelicanTestPG{})
	defer s.Close()
	_, err = s.Start(context.Background(), PelicanRequest{RequestID: "unknown-group", Targets: []PelicanTarget{{GroupID: 9, Source: PelicanSourceManual, BaseURL: "https://api.example.com", APIKey: "k", Model: "test", Protocol: "messages"}}})
	if err == nil {
		t.Fatal("accepted a target on an unknown group")
	}
}

func TestPelicanServiceCancelQueuedAndRunning(t *testing.T) {
	store, cleanup, err := repository.NewTestStore()
	if errors.Is(err, repository.ErrNoTestDSN) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		started <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	repo := repository.NewPelicanRepo(store)
	s := NewPelicanService(repo, &pelicanTestPG{})
	defer s.Close()
	ctx := context.Background()
	batch, err := s.Start(ctx, PelicanRequest{RequestID: "cancel-batch", Concurrency: 1, Targets: []PelicanTarget{
		{GroupID: 1, Source: PelicanSourceManual, BaseURL: server.URL, APIKey: "k", Model: "a", Protocol: "messages"},
		{GroupID: 1, Source: PelicanSourceManual, BaseURL: server.URL, APIKey: "k", Model: "b", Protocol: "messages"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not start")
	}
	if err := s.Cancel(ctx, batch.ID); err != nil {
		t.Fatal(err)
	}
	s.workers.Wait()
	rows, _, err := repo.List(ctx, repository.PelicanFilter{BatchID: batch.ID})
	if err != nil || len(rows) != 2 {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Status != "cancelled" {
			t.Fatalf("state %s", row.Status)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("queued request charged after cancel")
	}
}

// 勾选糖果题后每个目标并行发两次请求：作品带交付指令，糖果题不带；
// 糖果题判分进 metadata、全文进 output，作品失败不影响糖果题结论，记录状态只看作品。
func TestPelicanCandyRunsAlongsideArtwork(t *testing.T) {
	store, cleanup, err := repository.NewTestStore()
	if errors.Is(err, repository.ErrNoTestDSN) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	var artworkCalls, candyCalls atomic.Int32
	artworkFails := atomic.Bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if body["system"] == nil {
			candyCalls.Add(1)
			fmt.Fprint(w, `{"content":[{"type":"text","text":"推理……\n最终答案：21"}],"stop_reason":"end_turn"}`)
			return
		}
		artworkCalls.Add(1)
		if artworkFails.Load() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		fmt.Fprint(w, `{"content":[{"type":"text","text":"<svg></svg>"}],"stop_reason":"end_turn"}`)
	}))
	defer server.Close()
	repo := repository.NewPelicanRepo(store)
	s := NewPelicanService(repo, &pelicanTestPG{accounts: []repository.PGAccount{{ID: 7, Name: "acct", Platform: "anthropic", BaseURL: server.URL, APIKey: "sk-account"}}})
	defer s.Close()
	ctx := context.Background()
	target := PelicanTarget{GroupID: 1, Source: PelicanSourceAccount, AccountID: 7, Model: "claude-opus-5-5", Protocol: "messages"}

	batch, err := s.Start(ctx, PelicanRequest{RequestID: "candy-request", Candy: true, Targets: []PelicanTarget{target}})
	if err != nil {
		t.Fatal(err)
	}
	s.workers.Wait()
	rows, _, err := repo.List(ctx, repository.PelicanFilter{BatchID: batch.ID})
	if err != nil || len(rows) != 1 || rows[0].Status != "completed" {
		t.Fatalf("rows %+v err %v", rows, err)
	}
	if c := rows[0].Candy; c == nil || c.Status != CandyCorrect || c.Answer != "21" || c.Expected != "21" {
		t.Fatalf("candy not graded on the list row: %+v", rows[0].Candy)
	}
	detail, err := repo.Get(ctx, rows[0].ID)
	if err != nil || !strings.Contains(detail.Output.CandyText, "最终答案：21") || detail.Output.Document == "" {
		t.Fatalf("detail output %+v err %v", detail.Output, err)
	}
	if artworkCalls.Load() != 1 || candyCalls.Load() != 1 {
		t.Fatalf("artwork=%d candy=%d", artworkCalls.Load(), candyCalls.Load())
	}

	artworkFails.Store(true)
	failed, err := s.Start(ctx, PelicanRequest{RequestID: "candy-artwork-fails", Candy: true, Targets: []PelicanTarget{target}})
	if err != nil {
		t.Fatal(err)
	}
	s.workers.Wait()
	rows, _, err = repo.List(ctx, repository.PelicanFilter{BatchID: failed.ID})
	if err != nil || len(rows) != 1 || rows[0].Status != "failed" || rows[0].Candy == nil || rows[0].Candy.Status != CandyCorrect {
		t.Fatalf("artwork failure must not hide the candy result: %+v err %v", rows, err)
	}

	plain, err := s.Start(ctx, PelicanRequest{RequestID: "no-candy", Targets: []PelicanTarget{target}})
	if err != nil {
		t.Fatal(err)
	}
	s.workers.Wait()
	rows, _, err = repo.List(ctx, repository.PelicanFilter{BatchID: plain.ID})
	if err != nil || len(rows) != 1 || rows[0].Candy != nil || candyCalls.Load() != 2 {
		t.Fatalf("candy must not run when off: %+v calls=%d err %v", rows, candyCalls.Load(), err)
	}
}
