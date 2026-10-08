package handler

import (
	"database/sql"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"sub2api-account-monitor/internal/pkg/response"
	"sub2api-account-monitor/internal/repository"
	"sub2api-account-monitor/internal/service"
)

type PelicanHandler struct {
	svc    *service.PelicanService
	issuer *EmbedSessionIssuer
}

func NewPelicanHandler(svc *service.PelicanService) *PelicanHandler { return &PelicanHandler{svc: svc} }
func (h *PelicanHandler) SetIssuer(issuer *EmbedSessionIssuer)      { h.issuer = issuer }
func (h *PelicanHandler) CreateSession(c *gin.Context)              { h.issuer.Issue(c) }
func pelicanError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		response.NotFound(c, "pelican.errors.notFound")
	case errors.Is(err, repository.ErrPelicanConflict):
		response.Conflict(c, err.Error())
	case errors.Is(err, service.ErrPelicanUnavailable):
		response.ServiceUnavailable(c, err.Error())
	case errors.Is(err, service.ErrPelicanInvalid):
		response.BadRequest(c, err.Error())
	default:
		if strings.HasPrefix(err.Error(), "pelican.errors.") {
			response.BadRequest(c, err.Error())
		} else {
			response.InternalError(c, "pelican.errors.failed")
		}
	}
}
func pelicanID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "pelican.errors.invalid")
		return 0, false
	}
	return id, true
}
func (h *PelicanHandler) Config(c *gin.Context) {
	cfg, err := h.svc.Config(c.Request.Context())
	if err != nil {
		pelicanError(c, err)
		return
	}
	response.Success(c, cfg)
}
func (h *PelicanHandler) SaveConfig(c *gin.Context) {
	var cfg service.PelicanConfig
	if c.ShouldBindJSON(&cfg) != nil {
		pelicanError(c, service.ErrPelicanInvalid)
		return
	}
	if err := h.svc.SaveConfig(c.Request.Context(), cfg); err != nil {
		pelicanError(c, err)
		return
	}
	h.Config(c)
}
func (h *PelicanHandler) Groups(c *gin.Context) {
	groups, err := h.svc.Groups(c.Request.Context())
	if err != nil {
		pelicanError(c, err)
		return
	}
	response.Success(c, gin.H{"items": groups})
}
func (h *PelicanHandler) Start(c *gin.Context) {
	var req service.PelicanRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	if c.ShouldBindJSON(&req) != nil {
		pelicanError(c, service.ErrPelicanInvalid)
		return
	}
	b, err := h.svc.Start(c.Request.Context(), req)
	if err != nil {
		pelicanError(c, err)
		return
	}
	response.Success(c, b)
}
func (h *PelicanHandler) Batch(c *gin.Context) {
	id, ok := pelicanID(c)
	if !ok {
		return
	}
	b, err := h.svc.Repo.Batch(c.Request.Context(), id)
	if err != nil {
		pelicanError(c, err)
		return
	}
	items, _, err := h.svc.Repo.List(c.Request.Context(), repository.PelicanFilter{BatchID: id, PageSize: 100})
	if err != nil {
		pelicanError(c, err)
		return
	}
	response.Success(c, gin.H{"batch": b, "items": items})
}
func (h *PelicanHandler) Cancel(c *gin.Context) {
	id, ok := pelicanID(c)
	if !ok {
		return
	}
	if err := h.svc.Cancel(c.Request.Context(), id); err != nil {
		pelicanError(c, err)
		return
	}
	response.Success(c, nil)
}
func (h *PelicanHandler) Retry(c *gin.Context) {
	id, ok := pelicanID(c)
	if !ok {
		return
	}
	var req struct {
		RequestID string `json:"request_id"`
		KeyID     int64  `json:"key_id"`
	}
	if c.ShouldBindJSON(&req) != nil {
		pelicanError(c, service.ErrPelicanInvalid)
		return
	}
	old, err := h.svc.Repo.Get(c.Request.Context(), id)
	if err != nil {
		pelicanError(c, err)
		return
	}
	if req.KeyID == 0 {
		req.KeyID = old.KeyID
	}
	b, err := h.svc.Start(c.Request.Context(), service.PelicanRequest{RequestID: req.RequestID, Prompt: old.Prompt, MaxTokens: old.MaxTokens, TimeoutSeconds: old.TimeoutSeconds, Targets: []service.PelicanTarget{{GroupID: old.GroupID, KeyID: req.KeyID, Model: old.Model, Protocol: old.Protocol, RetryOf: id}}})
	if err != nil {
		pelicanError(c, err)
		return
	}
	response.Success(c, b)
}
func (h *PelicanHandler) Mutate(c *gin.Context) {
	action := c.Param("action")
	if action != "publish" && action != "unpublish" && action != "delete" {
		pelicanError(c, service.ErrPelicanInvalid)
		return
	}
	var req struct {
		IDs []int64 `json:"ids"`
	}
	if c.ShouldBindJSON(&req) != nil || len(req.IDs) == 0 || len(req.IDs) > 100 {
		pelicanError(c, service.ErrPelicanInvalid)
		return
	}
	for _, id := range req.IDs {
		if id <= 0 {
			pelicanError(c, service.ErrPelicanInvalid)
			return
		}
	}
	sort.Slice(req.IDs, func(i, j int) bool { return req.IDs[i] < req.IDs[j] })
	if err := h.svc.Repo.Mutate(c.Request.Context(), req.IDs, c.Param("action")); err != nil {
		pelicanError(c, err)
		return
	}
	response.Success(c, nil)
}
func (h *PelicanHandler) List(c *gin.Context)       { h.list(c, false) }
func (h *PelicanHandler) PublicList(c *gin.Context) { h.list(c, true) }
func (h *PelicanHandler) list(c *gin.Context, public bool) {
	page, size := response.ParsePagination(c)
	if size > 100 {
		size = 100
	}
	f := repository.PelicanFilter{Page: page, PageSize: size, Model: c.Query("model"), From: c.Query("from"), To: c.Query("to"), Public: public, Latest: public && c.Query("history") != "true"}
	for _, value := range []string{f.From, f.To} {
		if value != "" {
			if _, err := time.Parse(time.RFC3339, value); err != nil {
				pelicanError(c, service.ErrPelicanInvalid)
				return
			}
		}
	}
	if id := c.Query("group_id"); id != "" {
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil || n <= 0 {
			pelicanError(c, service.ErrPelicanInvalid)
			return
		}
		f.GroupID = n
	}
	if id := c.Query("batch_id"); id != "" && !public {
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil || n <= 0 {
			pelicanError(c, service.ErrPelicanInvalid)
			return
		}
		f.BatchID = n
	}
	if public {
		groups, err := h.svc.VisibleGroups(c.Request.Context())
		if err != nil {
			pelicanError(c, err)
			return
		}
		f.VisibleGroups = groups
		c.Header("Cache-Control", "no-store")
	}
	items, total, err := h.svc.Repo.List(c.Request.Context(), f)
	if err != nil {
		pelicanError(c, err)
		return
	}
	if public {
		out := make([]pelicanPublicDTO, 0, len(items))
		for _, v := range items {
			out = append(out, publicPelican(v))
		}
		response.Paginated(c, out, total, page, size)
	} else {
		response.Paginated(c, items, total, page, size)
	}
}

// An explicit allowlist prevents accidental disclosure as internal result fields evolve.
type pelicanPublicDTO struct {
	ID          int64                     `json:"id"`
	GroupID     int64                     `json:"group_id"`
	GroupName   string                    `json:"group_name"`
	Model       string                    `json:"model"`
	TestedAt    time.Time                 `json:"tested_at"`
	PublishedAt *time.Time                `json:"published_at"`
	DurationMs  int64                     `json:"duration_ms"`
	Prompt      string                    `json:"prompt"`
	Protocol    string                    `json:"protocol"`
	MaxTokens   int                       `json:"max_tokens"`
	HasDocument bool                      `json:"has_document"`
	Output      *repository.PelicanOutput `json:"output,omitempty"`
}

func publicPelican(v *repository.PelicanResult) pelicanPublicDTO {
	out := pelicanPublicDTO{ID: v.ID, GroupID: v.GroupID, GroupName: v.GroupName, Model: v.Model, TestedAt: v.TestedAt, PublishedAt: v.PublishedAt, DurationMs: v.DurationMs, Prompt: v.Prompt, Protocol: v.Protocol, MaxTokens: v.MaxTokens, HasDocument: v.HasDocument}
	if v.Output != nil {
		out.Output = &repository.PelicanOutput{Document: v.Output.Document, Kind: v.Output.Kind}
	}
	return out
}
func (h *PelicanHandler) Detail(c *gin.Context)       { h.detail(c, false) }
func (h *PelicanHandler) PublicDetail(c *gin.Context) { h.detail(c, true) }
func (h *PelicanHandler) detail(c *gin.Context, public bool) {
	id, ok := pelicanID(c)
	if !ok {
		return
	}
	var groups []int64
	if public {
		var err error
		groups, err = h.svc.VisibleGroups(c.Request.Context())
		if err != nil {
			pelicanError(c, err)
			return
		}
		c.Header("Cache-Control", "no-store")
	}
	v, err := h.svc.Repo.Get(c.Request.Context(), id)
	if err != nil {
		pelicanError(c, err)
		return
	}
	if public {
		visible := false
		for _, g := range groups {
			if g == v.GroupID {
				visible = true
				break
			}
		}
		if !visible || v.PublishedAt == nil || v.Status != "completed" {
			pelicanError(c, sql.ErrNoRows)
			return
		}
		response.Success(c, publicPelican(v))
	} else {
		response.Success(c, v)
	}
}
func (h *PelicanHandler) Filters(c *gin.Context) {
	groups, err := h.svc.VisibleGroups(c.Request.Context())
	if err != nil {
		pelicanError(c, err)
		return
	}
	items, err := h.svc.Repo.Facets(c.Request.Context(), groups)
	if err != nil {
		pelicanError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, gin.H{"items": items})
}
