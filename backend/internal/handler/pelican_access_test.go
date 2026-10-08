package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"sub2api-account-monitor/internal/repository"
	"sub2api-account-monitor/internal/service"
)

type pelicanHandlerPG struct {
	visible     []int64
	unavailable bool
}

func (p *pelicanHandlerPG) ListGroups(context.Context) ([]repository.PGGroup, error) { return nil, nil }
func (p *pelicanHandlerPG) ListUsers(context.Context) ([]repository.PGUser, error)   { return nil, nil }
func (p *pelicanHandlerPG) ListPelicanKeys(context.Context, string) ([]repository.PGUserKey, error) {
	return nil, nil
}
func (p *pelicanHandlerPG) PelicanVisibleGroups(context.Context) ([]int64, error) {
	if p.unavailable {
		return nil, errors.New("upstream offline")
	}
	return p.visible, nil
}

func TestPelicanPublicReadsFailClosed(t *testing.T) {
	h := NewPelicanHandler(service.NewPelicanService(nil, nil, &pelicanHandlerPG{unavailable: true}))
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
	h := NewPelicanHandler(service.NewPelicanService(repo, nil, pg))
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
