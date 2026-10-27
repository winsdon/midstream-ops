package service

import (
	"context"
	"encoding/json"
	"fmt"
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

type provisionFixture struct {
	svc                                      *ProvisionService
	store                                    *repository.Store
	providerID                               int64
	creates, accounts, deletes, localDeletes atomic.Int64
	deleteFailures                           atomic.Int64
	mu                                       sync.Mutex
	name                                     string
}

type provisionOptions struct {
	accountFailure, keyFailure, createTimeout, uncertain, sub2api bool
}

func newProvisionFixture(t *testing.T, opt provisionOptions) *provisionFixture {
	t.Helper()
	f := &provisionFixture{store: newTestStore(t)}
	repo := repository.NewProviderRepo(f.store, &secretbox.Box{})
	conns := repository.NewConnectionRepo(f.store)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/user/self/groups":
			fmt.Fprint(w, `{"success":true,"data":{"vip":{"ratio":1}}}`)
		case "/api/v1/groups/available":
			fmt.Fprint(w, `{"code":0,"data":[{"id":9,"name":"vip","platform":"openai","rate_multiplier":1}]}`)
		case "/api/v1/groups/rates":
			fmt.Fprint(w, `{"code":0,"data":[]}`)
		case "/api/token/":
			if r.Method == "POST" {
				f.creates.Add(1)
				var payload struct {
					Name  string `json:"name"`
					Group string `json:"group"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				rows, err := conns.List(context.Background(), true)
				found := false
				for _, row := range rows {
					if row.UpstreamKeyName == payload.Name && row.Status == repository.ConnStatusPending {
						found = true
					}
				}
				if err != nil || !found {
					t.Error("intent name not durable before creation")
				}
				if payload.Group != "vip" {
					t.Error("wrong group")
				}
				if !opt.uncertain {
					f.mu.Lock()
					f.name = payload.Name
					f.mu.Unlock()
				}
				if opt.createTimeout || opt.uncertain {
					time.Sleep(150 * time.Millisecond)
				}
				fmt.Fprint(w, `{"success":true}`)
			} else {
				f.mu.Lock()
				name := f.name
				f.mu.Unlock()
				items := []NewAPIToken{}
				if name != "" {
					items = append(items, NewAPIToken{ID: 42, Name: name, Group: "vip", Key: "masked********"})
				}
				json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"items": items, "total": len(items)}})
			}
		case "/api/token/42/key":
			rows, _ := conns.List(context.Background(), true)
			found := false
			for _, row := range rows {
				if row.UpstreamKeyID == 42 {
					found = true
				}
			}
			if !found {
				t.Error("token id not durable before reading key")
			}
			if opt.keyFailure {
				http.Error(w, "sensitive-secret", 500)
				return
			}
			fmt.Fprint(w, `{"success":true,"data":{"key":"abcdefghijklmnopqrstuvwxyz"}}`)
		case "/api/token/42", "/api/v1/keys/42":
			if r.Method != "DELETE" {
				t.Error("wrong deletion method")
			}
			if opt.sub2api != strings.HasPrefix(r.URL.Path, "/api/v1/") {
				t.Error("wrong platform deletion route")
			}
			f.deletes.Add(1)
			if f.deleteFailures.Load() > 0 {
				f.deleteFailures.Add(-1)
				http.Error(w, "sensitive-secret", 500)
				return
			}
			f.mu.Lock()
			f.name = ""
			f.mu.Unlock()
			fmt.Fprint(w, `{"success":true,"code":0}`)
		case "/api/v1/keys":
			f.creates.Add(1)
			fmt.Fprint(w, `{"code":0,"data":{"id":42,"key":"sk-abcdefghijklmnopqrstuvwxyz"}}`)
		case "/api/v1/admin/groups":
			fmt.Fprint(w, `{"code":0,"data":{"items":[{"id":1,"platform":"openai"},{"id":2,"platform":"anthropic"},{"id":3,"platform":"gemini"}]}}`)
		case "/api/v1/admin/accounts":
			f.accounts.Add(1)
			var payload struct {
				Platform    string         `json:"platform"`
				GroupIDs    []int64        `json:"group_ids"`
				Credentials map[string]any `json:"credentials"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			if len(payload.GroupIDs) != 1 || map[string]int64{"openai": 1, "anthropic": 2, "gemini": 3}[payload.Platform] != payload.GroupIDs[0] {
				t.Error("wrong account protocol/group")
			}
			if payload.Credentials["api_key"] != "sk-abcdefghijklmnopqrstuvwxyz" {
				t.Error("wrong key")
			}
			if opt.accountFailure {
				http.Error(w, "sensitive-secret", 400)
				return
			}
			fmt.Fprintf(w, `{"code":0,"data":{"id":%d}}`, 100+f.accounts.Load())
		case "/api/v1/admin/accounts/101":
			f.localDeletes.Add(1)
			fmt.Fprint(w, `{"code":0}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	platform, auth := "new-api", "user_key"
	if opt.sub2api {
		platform, auth = "sub2api", "token"
	}
	up, err := repo.Create(context.Background(), repository.CreateParams{Name: "up", Platform: platform, AuthMode: auth, BaseURL: srv.URL, AccessToken: "pat", UpstreamUserID: "7"})
	if err != nil {
		t.Fatal(err)
	}
	f.providerID = up.ID
	pw := "pw"
	self, err := repo.UpsertSelf(context.Background(), srv.URL, "admin", &pw)
	if err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().Add(time.Hour)
	if err := repo.UpdateTokenPair(context.Background(), self.ID, "admin-token", "", &expiry); err != nil {
		t.Fatal(err)
	}
	bal := NewBalanceService(repo, nil, &config.Config{})
	if opt.createTimeout || opt.uncertain {
		bal.newapiClient.httpClient.Timeout = 50 * time.Millisecond
	}
	f.svc = NewProvisionService(repo, conns, repository.NewProviderAccountRepo(f.store), nil, bal)
	return f
}

func (f *provisionFixture) req(platform string) ConnectRequest {
	return ConnectRequest{ProviderID: f.providerID, UpstreamGroup: "vip", Platform: platform, LocalGroupIDs: []int64{map[string]int64{"openai": 1, "anthropic": 2, "gemini": 3}[platform]}, OperationID: "op-" + platform}
}

func TestProvisionNewAPIProtocolsAndIdempotency(t *testing.T) {
	f := newProvisionFixture(t, provisionOptions{})
	for _, platform := range []string{"openai", "anthropic", "gemini"} {
		req := f.req(platform)
		conn, err := f.svc.Connect(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if conn.Status != "active" || conn.GroupPlatform != platform || conn.UpstreamGroupID != 0 || conn.UpstreamKeyID != 42 {
			t.Fatalf("bad connection: %+v", conn)
		}
		again, err := f.svc.Connect(context.Background(), req)
		if err != nil || again.ID != conn.ID {
			t.Fatalf("idempotency: %v", err)
		}
		raw, _ := json.Marshal(conn)
		if strings.Contains(string(raw), "abcdefghijklmnopqrstuvwxyz") {
			t.Fatal("secret in connection")
		}
	}
	if f.creates.Load() != 3 || f.accounts.Load() != 3 {
		t.Fatal("expected exactly three resources")
	}
	links, err := f.svc.linkRepo.ListByProvider(context.Background(), f.providerID)
	if err != nil || len(links) != 3 {
		t.Fatalf("missing provider links: %v", err)
	}
}

func TestProvisionValidationBeforeWrites(t *testing.T) {
	for _, kind := range []string{"protocol", "upstream", "local", "mismatch"} {
		t.Run(kind, func(t *testing.T) {
			f := newProvisionFixture(t, provisionOptions{})
			req := f.req("openai")
			switch kind {
			case "protocol":
				req.Platform = ""
			case "upstream":
				req.UpstreamGroup = "missing"
			case "local":
				req.LocalGroupIDs = []int64{999}
			case "mismatch":
				req.LocalGroupIDs = []int64{2}
			}
			if _, err := f.svc.Connect(context.Background(), req); err == nil {
				t.Fatal("expected validation error")
			}
			if f.creates.Load() != 0 || f.accounts.Load() != 0 {
				t.Fatal("wrote before validation")
			}
		})
	}
}

func TestProvisionCompensationAndFailedReplay(t *testing.T) {
	for _, kind := range []string{"account", "key", "delete"} {
		t.Run(kind, func(t *testing.T) {
			f := newProvisionFixture(t, provisionOptions{accountFailure: kind != "key", keyFailure: kind == "key"})
			if kind == "delete" {
				f.deleteFailures.Store(1)
			}
			req := f.req("openai")
			if _, err := f.svc.Connect(context.Background(), req); err == nil || strings.Contains(err.Error(), "sensitive-secret") {
				t.Fatalf("unsafe/missing error: %v", err)
			}
			conn, err := f.svc.connRepo.GetByOperationID(context.Background(), req.OperationID)
			if err != nil || conn.Status != "failed" || conn.UpstreamKeyName == "" {
				t.Fatalf("missing failure trace: %v", err)
			}
			if kind == "delete" && conn.UpstreamKeyID != 42 {
				t.Fatal("lost cleanup id")
			}
			if kind != "delete" && conn.UpstreamKeyID != 0 {
				t.Fatal("deleted key still marked live")
			}
			if _, err := f.svc.Connect(context.Background(), req); err == nil {
				t.Fatal("failed replay returned success")
			}
			if f.creates.Load() != 1 || f.deletes.Load() != 1 {
				t.Fatal("duplicate create or missing compensation")
			}
			if kind == "delete" {
				if err := f.svc.Disconnect(context.Background(), conn.ID, true); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestProvisionTimeoutDoesNotRecreate(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(fmt.Sprint(uncertain), func(t *testing.T) {
			f := newProvisionFixture(t, provisionOptions{createTimeout: true, uncertain: uncertain})
			req := f.req("openai")
			_, err := f.svc.Connect(context.Background(), req)
			if (err != nil) != uncertain {
				t.Fatalf("unexpected outcome: %v", err)
			}
			_, err = f.svc.Connect(context.Background(), req)
			if (err != nil) != uncertain {
				t.Fatalf("unexpected replay: %v", err)
			}
			if f.creates.Load() != 1 {
				t.Fatal("POST retried")
			}
			if uncertain {
				conn, _ := f.svc.connRepo.GetByOperationID(context.Background(), req.OperationID)
				if conn.Status != "pending" || conn.UpstreamKeyName == "" || f.accounts.Load() != 0 {
					t.Fatal("lost pending intent")
				}
				if err := f.svc.Disconnect(context.Background(), conn.ID, true); err == nil {
					t.Fatal("discarded unresolved intent")
				}
			}
		})
	}
}

func TestProvisionDisconnectRoutesAndRetry(t *testing.T) {
	for _, sub2api := range []bool{false, true} {
		t.Run(fmt.Sprint(sub2api), func(t *testing.T) {
			f := newProvisionFixture(t, provisionOptions{sub2api: sub2api})
			req := f.req("openai")
			if sub2api {
				req.Platform = ""
			}
			conn, err := f.svc.Connect(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			f.deleteFailures.Store(1)
			if err := f.svc.Disconnect(context.Background(), conn.ID, true); err == nil {
				t.Fatal("delete failure ignored")
			}
			saved, err := f.svc.connRepo.GetByID(context.Background(), conn.ID)
			if err != nil || saved.UpstreamKeyID != 42 || saved.LocalAccountID != 0 {
				t.Fatalf("lost retry state: %v", err)
			}
			if err := f.svc.Disconnect(context.Background(), conn.ID, true); err != nil {
				t.Fatal(err)
			}
			if f.localDeletes.Load() != 1 || f.deletes.Load() != 2 {
				t.Fatal("wrong retry counts")
			}
		})
	}
}

func TestProvisionKeyStateWriteFailureStopsAccount(t *testing.T) {
	f := newProvisionFixture(t, provisionOptions{})
	_, err := f.store.DB().Exec(`CREATE FUNCTION fail_key_update() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.upstream_key_id > 0 THEN RAISE EXCEPTION 'test write failure'; END IF; RETURN NEW; END $$`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.store.DB().Exec(`CREATE TRIGGER fail_key_update BEFORE UPDATE ON upstream_connections FOR EACH ROW EXECUTE FUNCTION fail_key_update()`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Connect(context.Background(), f.req("openai")); err == nil {
		t.Fatal("ignored persistence failure")
	}
	if f.accounts.Load() != 0 || f.deletes.Load() != 1 {
		t.Fatal("account created after failed key persistence")
	}
}
