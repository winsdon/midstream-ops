package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sub2api-account-monitor/internal/repository"
	"sync/atomic"
	"testing"
	"time"
)

type pelicanTestPG struct {
	keys []repository.PGUserKey
	fail bool
}

func (p *pelicanTestPG) ListGroups(context.Context) ([]repository.PGGroup, error) {
	return []repository.PGGroup{{ID: 1, Name: "group", Platform: "anthropic", Status: "active"}}, nil
}
func (p *pelicanTestPG) ListUsers(context.Context) ([]repository.PGUser, error) {
	return []repository.PGUser{{ID: 1, Status: "active"}}, nil
}
func (p *pelicanTestPG) ListPelicanKeys(context.Context, string) ([]repository.PGUserKey, error) {
	return p.keys, nil
}
func (p *pelicanTestPG) PelicanVisibleGroups(context.Context) ([]int64, error) {
	if p.fail {
		return nil, errors.New("offline")
	}
	return []int64{1}, nil
}

func TestPelicanVisibilityFailsClosed(t *testing.T) {
	s := NewPelicanService(nil, nil, &pelicanTestPG{fail: true})
	if _, err := s.VisibleGroups(context.Background()); !errors.Is(err, ErrPelicanUnavailable) {
		t.Fatalf("want unavailable, got %v", err)
	}
}
func TestPelicanRequestsRejectDuplicateTargets(t *testing.T) {
	target := PelicanTarget{GroupID: 1, KeyID: 1, Model: "test", Protocol: "messages"}
	r := PelicanRequest{RequestID: "request-123", Targets: []PelicanTarget{target, target}}
	if normalizePelicanRequest(&r) == nil {
		t.Fatal("accepted duplicate group/model")
	}
	r.Targets = r.Targets[:1]
	r.Prompt = "  original\n"
	if err := normalizePelicanRequest(&r); err != nil || r.Prompt != "  original\n" || r.MaxTokens != 32000 || r.TimeoutSeconds != 900 {
		t.Fatalf("bad defaults %+v %v", r, err)
	}
}
func TestPelicanServiceChargesOnceAndKeepsRetryHistory(t *testing.T) {
	store, cleanup, err := repository.NewTestStore()
	if errors.Is(err, repository.ErrNoTestDSN) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"content":[{"type":"text","text":"<svg></svg>"}],"stop_reason":"end_turn"}`)
	}))
	defer server.Close()
	repo := repository.NewPelicanRepo(store)
	s := NewPelicanService(repo, repository.NewSettingsRepo(store), &pelicanTestPG{keys: []repository.PGUserKey{{ID: 2, GroupID: 1, GroupName: "group", Platform: "anthropic", Key: "test-key"}}})
	defer s.Close()
	ctx := context.Background()
	if err := s.SaveConfig(ctx, PelicanConfig{UserID: "1", GatewayURL: server.URL}); err != nil {
		t.Fatal(err)
	}
	req := PelicanRequest{RequestID: "unique-request", Targets: []PelicanTarget{{GroupID: 1, KeyID: 2, Model: "test", Protocol: "messages"}}}
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
	req.RequestID = "retry-request"
	req.Targets[0].RetryOf = rows[0].ID
	third, err := s.Start(ctx, req)
	if err != nil || third.ID == first.ID {
		t.Fatalf("retry %v", err)
	}
	s.workers.Wait()
	old, err := repo.Get(ctx, rows[0].ID)
	if err != nil || old.Status != "completed" || calls.Load() != 2 {
		t.Fatalf("overwrote history %v", err)
	}
	retry, _, err := repo.List(ctx, repository.PelicanFilter{BatchID: third.ID})
	if err != nil || len(retry) != 1 || retry[0].RetryOf != old.ID {
		t.Fatal("missing retry link")
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
	s := NewPelicanService(repo, repository.NewSettingsRepo(store), &pelicanTestPG{keys: []repository.PGUserKey{{ID: 2, GroupID: 1, Platform: "anthropic", Key: "key"}}})
	defer s.Close()
	ctx := context.Background()
	if err := s.SaveConfig(ctx, PelicanConfig{UserID: "1", GatewayURL: server.URL}); err != nil {
		t.Fatal(err)
	}
	batch, err := s.Start(ctx, PelicanRequest{RequestID: "cancel-batch", Concurrency: 1, Targets: []PelicanTarget{{GroupID: 1, KeyID: 2, Model: "a", Protocol: "messages"}, {GroupID: 1, KeyID: 2, Model: "b", Protocol: "messages"}}})
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
