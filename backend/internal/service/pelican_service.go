package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"sub2api-account-monitor/internal/repository"
)

var ErrPelicanUnavailable = errors.New("plaza.errors.databaseUnavailable")
var ErrPelicanInvalid = errors.New("pelican.errors.invalid")

type pelicanPG interface {
	ListGroups(context.Context) ([]repository.PGGroup, error)
	ListUsers(context.Context) ([]repository.PGUser, error)
	ListPelicanKeys(context.Context, string) ([]repository.PGUserKey, error)
	PelicanVisibleGroups(context.Context) ([]int64, error)
}
type PelicanConfig struct {
	GatewayURL string `json:"gateway_url"`
	UserID     string `json:"user_id"`
}
type PelicanTarget struct {
	GroupID  int64  `json:"group_id"`
	KeyID    int64  `json:"key_id"`
	Model    string `json:"model"`
	Protocol string `json:"protocol"`
	RetryOf  int64  `json:"retry_of,omitempty"`
}
type PelicanRequest struct {
	RequestID      string          `json:"request_id"`
	Prompt         string          `json:"prompt"`
	Targets        []PelicanTarget `json:"targets"`
	Concurrency    int             `json:"concurrency"`
	MaxTokens      int             `json:"max_tokens"`
	TimeoutSeconds int             `json:"timeout_seconds"`
}
type PelicanKeyOption struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Masked string `json:"masked"`
}
type PelicanGroupOption struct {
	ID       int64              `json:"id"`
	Name     string             `json:"name"`
	Platform string             `json:"platform"`
	Models   []string           `json:"models"`
	Keys     []PelicanKeyOption `json:"keys"`
	Reason   string             `json:"reason,omitempty"`
}
type PelicanService struct {
	Repo     *repository.PelicanRepo
	settings *repository.SettingsRepo
	pg       pelicanPG
	gateway  *PelicanGateway
	mu       sync.Mutex
	jobs     map[int64]context.CancelFunc
	workers  sync.WaitGroup
	closing  bool
}

func NewPelicanService(repo *repository.PelicanRepo, settings *repository.SettingsRepo, pg pelicanPG) *PelicanService {
	return &PelicanService{Repo: repo, settings: settings, pg: pg, gateway: NewPelicanGateway(), jobs: map[int64]context.CancelFunc{}}
}
func (s *PelicanService) Close() {
	s.mu.Lock()
	s.closing = true
	for id, cancel := range s.jobs {
		cancel()
		ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		_ = s.Repo.Cancel(ctx, id)
		stop()
	}
	s.mu.Unlock()
	s.workers.Wait()
}
func (s *PelicanService) Config(ctx context.Context) (PelicanConfig, error) {
	var c PelicanConfig
	raw, err := s.settings.Get(ctx, "pelican")
	if err != nil {
		return c, err
	}
	if raw != "" {
		err = json.Unmarshal([]byte(raw), &c)
	}
	return c, err
}
func (s *PelicanService) SaveConfig(ctx context.Context, c PelicanConfig) error {
	c.GatewayURL = strings.TrimRight(strings.TrimSpace(c.GatewayURL), "/")
	c.UserID = strings.TrimSpace(c.UserID)
	u, err := url.Parse(c.GatewayURL)
	id, e := strconv.ParseInt(c.UserID, 10, 64)
	if err != nil || e != nil || id <= 0 || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return ErrPelicanInvalid
	}
	for _, suffix := range []string{"/messages", "/responses", "/chat/completions"} {
		if strings.HasSuffix(u.Path, suffix) {
			return ErrPelicanInvalid
		}
	}
	users, err := s.pg.ListUsers(ctx)
	if err != nil {
		return ErrPelicanUnavailable
	}
	found := false
	for _, user := range users {
		if user.ID == id && user.Status == "active" {
			found = true
			break
		}
	}
	if !found {
		return ErrPelicanInvalid
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return s.settings.Set(ctx, "pelican", string(raw))
}
func (s *PelicanService) Groups(ctx context.Context) ([]PelicanGroupOption, error) {
	cfg, err := s.Config(ctx)
	if err != nil {
		return nil, err
	}
	groups, err := s.pg.ListGroups(ctx)
	if err != nil {
		return nil, ErrPelicanUnavailable
	}
	keys := []repository.PGUserKey{}
	if cfg.UserID != "" {
		keys, err = s.pg.ListPelicanKeys(ctx, cfg.UserID)
		if err != nil {
			return nil, ErrPelicanUnavailable
		}
	}
	out := make([]PelicanGroupOption, 0)
	for _, g := range groups {
		if g.Status != "active" {
			continue
		}
		v := PelicanGroupOption{ID: g.ID, Name: g.Name, Platform: g.Platform, Models: []string{}, Keys: []PelicanKeyOption{}}
		models := map[string]bool{}
		for _, k := range keys {
			if k.GroupID == g.ID {
				mask := "••••"
				if len(k.Key) > 8 {
					mask += k.Key[len(k.Key)-4:]
				}
				v.Keys = append(v.Keys, PelicanKeyOption{ID: k.ID, Name: k.Name, Masked: mask})
				for _, m := range k.Models {
					models[m] = true
				}
			}
		}
		for m := range models {
			v.Models = append(v.Models, m)
		}
		sort.Strings(v.Models)
		if g.Platform != "anthropic" && g.Platform != "openai" && g.Platform != "composite" {
			v.Reason = "pelican.unsupported"
		} else if len(v.Keys) == 0 {
			v.Reason = "pelican.noKey"
		}
		out = append(out, v)
	}
	return out, nil
}
func normalizePelicanRequest(r *PelicanRequest) error {
	if strings.TrimSpace(r.Prompt) == "" {
		r.Prompt = DefaultPelicanPrompt
	}
	if r.Concurrency == 0 {
		r.Concurrency = 3
	}
	if r.MaxTokens == 0 {
		r.MaxTokens = 32000
	}
	if r.TimeoutSeconds == 0 {
		r.TimeoutSeconds = 900
	}
	if len(r.RequestID) < 8 || len(r.RequestID) > 128 || len(r.Targets) == 0 || len(r.Targets) > 100 || len(r.Prompt) > 65536 || r.Concurrency < 1 || r.Concurrency > 10 || r.MaxTokens < 1 || r.MaxTokens > 262144 || r.TimeoutSeconds < 1 || r.TimeoutSeconds > 3600 {
		return ErrPelicanInvalid
	}
	seen := map[string]bool{}
	for i := range r.Targets {
		t := &r.Targets[i]
		t.Model = strings.TrimSpace(t.Model)
		key := fmt.Sprintf("%d/%s", t.GroupID, t.Model)
		if t.GroupID <= 0 || t.KeyID <= 0 || t.Model == "" || len(t.Model) > 256 || seen[key] || (t.Protocol != "messages" && t.Protocol != "responses" && t.Protocol != "chat") {
			return ErrPelicanInvalid
		}
		seen[key] = true
	}
	return nil
}
func pelicanKey(keys []repository.PGUserKey, t PelicanTarget) (repository.PGUserKey, error) {
	for _, k := range keys {
		if k.ID == t.KeyID && k.GroupID == t.GroupID {
			if (k.Platform == "anthropic" && t.Protocol != "messages") || (k.Platform == "openai" && t.Protocol == "messages") || (k.Platform != "anthropic" && k.Platform != "openai" && k.Platform != "composite") {
				return repository.PGUserKey{}, ErrPelicanInvalid
			}
			return k, nil
		}
	}
	return repository.PGUserKey{}, errors.New("pelican.errors.keyUnavailable")
}
func (s *PelicanService) Start(ctx context.Context, r PelicanRequest) (*repository.PelicanBatch, error) {
	if err := normalizePelicanRequest(&r); err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(r)
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	// A retry of an accepted submission must work even if keys/config later changed.
	if existing, err := s.Repo.FindBatch(ctx, r.RequestID); err == nil {
		if existing.Hash != hash {
			return nil, repository.ErrPelicanConflict
		}
		return existing, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	cfg, err := s.Config(ctx)
	if err != nil {
		return nil, err
	}
	if cfg.UserID == "" || cfg.GatewayURL == "" {
		return nil, errors.New("pelican.errors.notConfigured")
	}
	keys, err := s.pg.ListPelicanKeys(ctx, cfg.UserID)
	if err != nil {
		return nil, ErrPelicanUnavailable
	}
	results := make([]*repository.PelicanResult, 0, len(r.Targets))
	for _, t := range r.Targets {
		k, err := pelicanKey(keys, t)
		if err != nil {
			return nil, err
		}
		if t.RetryOf != 0 {
			old, e := s.Repo.Get(ctx, t.RetryOf)
			if e != nil {
				return nil, e
			}
			if old.GroupID != t.GroupID || old.Model != t.Model || old.Status == "queued" || old.Status == "running" {
				return nil, ErrPelicanInvalid
			}
		}
		results = append(results, &repository.PelicanResult{GroupID: t.GroupID, Model: t.Model, Status: "queued", PelicanMetadata: repository.PelicanMetadata{GroupName: k.GroupName, Protocol: t.Protocol, KeyID: t.KeyID, RetryOf: t.RetryOf, Prompt: r.Prompt, MaxTokens: r.MaxTokens, TimeoutSeconds: r.TimeoutSeconds}})
	}
	stored, _ := json.Marshal(struct {
		PelicanRequest
		Config PelicanConfig `json:"config"`
	}{r, cfg})
	batch := &repository.PelicanBatch{RequestID: r.RequestID, Hash: hash, Request: stored}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return nil, errors.New("pelican.errors.unavailable")
	}
	created, err := s.Repo.Create(ctx, batch, results)
	if err != nil || !created {
		return batch, err
	}
	jobCtx, cancel := context.WithCancel(context.Background())
	s.jobs[batch.ID] = cancel
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		defer cancel()
		defer func() { s.mu.Lock(); delete(s.jobs, batch.ID); s.mu.Unlock() }()
		s.run(jobCtx, cfg, r.Concurrency, results)
	}()
	return batch, nil
}
func (s *PelicanService) run(ctx context.Context, cfg PelicanConfig, concurrency int, results []*repository.PelicanResult) {
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	defer wg.Wait()
	for _, v := range results {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		wg.Add(1)
		go func(v *repository.PelicanResult) { defer wg.Done(); defer func() { <-sem }(); s.execute(ctx, cfg, v) }(v)
	}
}
func (s *PelicanService) execute(parent context.Context, cfg PelicanConfig, v *repository.PelicanResult) {
	if parent.Err() != nil {
		v.Status = "cancelled"
		finished := time.Now().UTC()
		v.FinishedAt = &finished
		s.persistResult(v)
		return
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(v.TimeoutSeconds)*time.Second)
	defer cancel()
	start := time.Now().UTC()
	v.StartedAt = &start
	v.Status = "running"
	if err := s.Repo.Update(ctx, v); err != nil {
		if !errors.Is(err, repository.ErrPelicanConflict) {
			v.Status = "failed"
			v.Error = "pelican.errors.failed"
			if parent.Err() != nil {
				v.Status = "cancelled"
				v.Error = ""
			}
			finished := time.Now().UTC()
			v.FinishedAt = &finished
			s.persistResult(v)
		}
		return
	}
	keys, err := s.pg.ListPelicanKeys(ctx, cfg.UserID)
	if err == nil {
		var key repository.PGUserKey
		key, err = pelicanKey(keys, PelicanTarget{GroupID: v.GroupID, KeyID: v.KeyID, Protocol: v.Protocol})
		if err == nil {
			v.Output, err = s.gateway.Generate(ctx, cfg.GatewayURL, key.Key, v)
		}
	} else {
		err = ErrPelicanUnavailable
	}
	end := time.Now().UTC()
	v.FinishedAt = &end
	v.DurationMs = end.Sub(start).Milliseconds()
	v.Status = "completed"
	if err != nil {
		v.Status = "failed"
		v.Error = err.Error()
	}
	if parent.Err() != nil {
		v.Status = "cancelled"
		v.Error = ""
	}
	v.HasDocument = v.Output != nil && v.Output.Document != ""
	// A transport success without a document is retained for diagnosis, not publishable.
	if v.Status == "completed" && !v.HasDocument {
		v.Status = "failed"
		v.Error = "pelican.errors.noDocument"
	}
	s.persistResult(v)
}
func (s *PelicanService) persistResult(v *repository.PelicanResult) {
	for attempt := 0; attempt < 3; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := s.Repo.Update(ctx, v)
		cancel()
		if err == nil || errors.Is(err, repository.ErrPelicanConflict) {
			return
		}
		if attempt < 2 {
			time.Sleep(time.Duration(attempt+1) * time.Second)
		}
	}
	log.Printf("[pelican] cannot persist result %d; recovery will mark it interrupted on restart", v.ID)
}
func (s *PelicanService) Cancel(ctx context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Stop chargeable work even if persistence is unavailable during cancellation.
	if cancel := s.jobs[id]; cancel != nil {
		cancel()
	}
	if _, err := s.Repo.Batch(ctx, id); err != nil {
		return err
	}
	if err := s.Repo.Cancel(ctx, id); err != nil {
		return err
	}
	return nil
}
func (s *PelicanService) VisibleGroups(ctx context.Context) ([]int64, error) {
	groups, err := s.pg.PelicanVisibleGroups(ctx)
	if err != nil {
		return nil, ErrPelicanUnavailable
	}
	return groups, nil
}
