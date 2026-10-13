package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"sub2api-account-monitor/internal/repository"
	"sub2api-account-monitor/internal/service"
)

type pelicanHandlerPG struct {
	visible     []int64
	unavailable bool
}

func (p *pelicanHandlerPG) ListGroups(context.Context) ([]repository.PGGroup, error) {
	return []repository.PGGroup{{ID: 1, Name: "group", Platform: "anthropic", Status: "active"}}, nil
}
func (p *pelicanHandlerPG) ListGroupAvailableModels(context.Context) ([]repository.PGGroupModels, error) {
	return nil, nil
}
func (p *pelicanHandlerPG) ListProbeCandidates(context.Context) ([]repository.PGAccount, error) {
	return nil, nil
}
func (p *pelicanHandlerPG) ListAccountGroupIDs(context.Context) (map[int64][]int64, error) {
	return nil, nil
}
func (p *pelicanHandlerPG) PelicanVisibleGroups(context.Context) ([]int64, error) {
	if p.unavailable {
		return nil, errors.New("upstream offline")
	}
	return p.visible, nil
}

func TestPelicanPublicReadsFailClosed(t *testing.T) {
	h := NewPelicanHandler(service.NewPelicanService(nil, &pelicanHandlerPG{unavailable: true}))
	r := gin.New()
	r.GET("/results", h.PublicList)
	r.GET("/results/:id", h.PublicDetail)
	r.GET("/filters", h.Filters)
	for _, path := range []string{"/results", "/results/1", "/filters"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 503 {
			t.Fatalf("%s: status %d", path, w.Code)
		}
	}
}
func TestPelicanPublicDetailChecksPublicationAndCurrentGroup(t *testing.T) {
	store, cleanup, err := repository.NewTestStore()
	if errors.Is(err, repository.ErrNoTestDSN) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	repo := repository.NewPelicanRepo(store)
	pg := &pelicanHandlerPG{visible: []int64{1}}
	h := NewPelicanHandler(service.NewPelicanService(repo, pg))
	r := gin.New()
	r.GET("/results/:id", h.PublicDetail)
	batch := &repository.PelicanBatch{RequestID: "handler-test", Hash: "h", Request: json.RawMessage(`{}`)}
	v := &repository.PelicanResult{GroupID: 1, Model: "test"}
	if _, err := repo.Create(context.Background(), batch, []*repository.PelicanResult{v}); err != nil {
		t.Fatal(err)
	}
	v.Status = "completed"
	v.Output = &repository.PelicanOutput{Document: "<svg></svg>", Kind: "svg"}
	if err := repo.Update(context.Background(), v); err != nil {
		t.Fatal(err)
	}
	check := func(want int) {
		t.Helper()
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/results/"+strconv.FormatInt(v.ID, 10), nil))
		if w.Code != want {
			t.Fatalf("status=%d want=%d body=%s", w.Code, want, w.Body)
		}
	}
	check(404)
	if err := repo.Mutate(context.Background(), []int64{v.ID}, "publish"); err != nil {
		t.Fatal(err)
	}
	check(200)
	pg.visible = nil
	check(404)
	pg.visible = []int64{1}
	if err := repo.Mutate(context.Background(), []int64{v.ID}, "unpublish"); err != nil {
		t.Fatal(err)
	}
	check(404)
}

// 旧记录（分组 Key）不可重放；手填来源必须重新输入 Key。
func TestPelicanRetryCredentialRules(t *testing.T) {
	store, cleanup, err := repository.NewTestStore()
	if errors.Is(err, repository.ErrNoTestDSN) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	repo := repository.NewPelicanRepo(store)
	h := NewPelicanHandler(service.NewPelicanService(repo, &pelicanHandlerPG{visible: []int64{1}}))
	r := gin.New()
	r.POST("/results/:id/retry", h.Retry)
	ctx := context.Background()
	seed := func(requestID string, meta repository.PelicanMetadata) int64 {
		t.Helper()
		batch := &repository.PelicanBatch{RequestID: requestID, Hash: "h", Request: json.RawMessage(`{}`)}
		v := &repository.PelicanResult{GroupID: 1, Model: "test", Status: "queued", PelicanMetadata: meta}
		if _, err := repo.Create(ctx, batch, []*repository.PelicanResult{v}); err != nil {
			t.Fatal(err)
		}
		v.Status = "completed"
		v.Output = &repository.PelicanOutput{Document: "<svg></svg>", Kind: "svg"}
		if err := repo.Update(ctx, v); err != nil {
			t.Fatal(err)
		}
		return v.ID
	}
	post := func(id int64, body string) (int, string) {
		t.Helper()
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", "/results/"+strconv.FormatInt(id, 10)+"/retry", strings.NewReader(body)))
		return w.Code, w.Body.String()
	}
	legacy := seed("retry-legacy", repository.PelicanMetadata{GroupName: "group", Protocol: "messages", KeyID: 5, Prompt: "p"})
	if code, body := post(legacy, `{"request_id":"retry-legacy-2"}`); code != 400 || !strings.Contains(body, "legacyRetry") {
		t.Fatalf("legacy retry: %d %s", code, body)
	}
	manual := seed("retry-manual", repository.PelicanMetadata{GroupName: "group", Protocol: "messages", Source: service.PelicanSourceManual, BaseURL: "https://api.example.com", KeyFP: "abc", Prompt: "p"})
	if code, body := post(manual, `{"request_id":"retry-manual-2"}`); code != 400 || !strings.Contains(body, "keyRequired") {
		t.Fatalf("manual retry without key: %d %s", code, body)
	}
	account := seed("retry-account", repository.PelicanMetadata{GroupName: "group", Protocol: "messages", Source: service.PelicanSourceAccount, Prompt: "p"})
	if code, body := post(account, `{"request_id":"retry-account-2"}`); code != 400 || !strings.Contains(body, "accountUnavailable") {
		t.Fatalf("account retry without account id: %d %s", code, body)
	}
}
