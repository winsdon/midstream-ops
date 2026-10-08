package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrPelicanConflict = errors.New("pelican.errors.conflict")

type PelicanOutput struct {
	Request  json.RawMessage `json:"request,omitempty"`
	Text     string          `json:"text,omitempty"`
	Document string          `json:"document,omitempty"`
	Kind     string          `json:"kind,omitempty"`
	Usage    json.RawMessage `json:"usage,omitempty"`
}
type PelicanMetadata struct {
	GroupName      string     `json:"group_name"`
	Protocol       string     `json:"protocol"`
	KeyID          int64      `json:"key_id"`
	RetryOf        int64      `json:"retry_of,omitempty"`
	Prompt         string     `json:"prompt"`
	MaxTokens      int        `json:"max_tokens"`
	TimeoutSeconds int        `json:"timeout_seconds"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
	DurationMs     int64      `json:"duration_ms"`
	Error          string     `json:"error,omitempty"`
	HasDocument    bool       `json:"has_document"`
}
type PelicanResult struct {
	ID          int64      `json:"id"`
	BatchID     int64      `json:"batch_id"`
	GroupID     int64      `json:"group_id"`
	Model       string     `json:"model"`
	Status      string     `json:"status"`
	PublishedAt *time.Time `json:"published_at"`
	TestedAt    time.Time  `json:"tested_at"`
	PelicanMetadata
	Output *PelicanOutput `json:"output,omitempty"`
}
type PelicanBatch struct {
	ID        int64           `json:"id"`
	RequestID string          `json:"request_id"`
	Hash      string          `json:"-"`
	Request   json.RawMessage `json:"request"`
	CreatedAt time.Time       `json:"created_at"`
}
type PelicanFilter struct {
	GroupID, BatchID int64
	Model, From, To  string
	Page, PageSize   int
	Public, Latest   bool
	VisibleGroups    []int64
}
type PelicanRepo struct{ db *DB }

func NewPelicanRepo(s *Store) *PelicanRepo { return &PelicanRepo{db: s.DB()} }

type PelicanFacet struct {
	GroupID   int64  `json:"group_id"`
	GroupName string `json:"group_name"`
	Model     string `json:"model"`
}

func (r *PelicanRepo) Facets(ctx context.Context, groups []int64) ([]PelicanFacet, error) {
	where, args := pelicanWhere(PelicanFilter{Public: true, VisibleGroups: groups})
	rows, err := r.db.QueryContext(ctx, `SELECT DISTINCT ON(group_id,model) group_id,metadata->>'group_name',model FROM pelican_results WHERE `+where+` ORDER BY group_id,model,tested_at DESC,id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]PelicanFacet, 0)
	for rows.Next() {
		var v PelicanFacet
		if err := rows.Scan(&v.GroupID, &v.GroupName, &v.Model); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r *PelicanRepo) FindBatch(ctx context.Context, requestID string) (*PelicanBatch, error) {
	b := &PelicanBatch{}
	err := r.db.QueryRowContext(ctx, `SELECT id,request_id,request_hash,request,created_at FROM pelican_batches WHERE request_id=?`, requestID).Scan(&b.ID, &b.RequestID, &b.Hash, &b.Request, &b.CreatedAt)
	return b, err
}
func (r *PelicanRepo) Batch(ctx context.Context, id int64) (*PelicanBatch, error) {
	b := &PelicanBatch{}
	err := r.db.QueryRowContext(ctx, `SELECT id,request_id,request_hash,request,created_at FROM pelican_batches WHERE id=?`, id).Scan(&b.ID, &b.RequestID, &b.Hash, &b.Request, &b.CreatedAt)
	return b, err
}

// Create commits the batch and every target before any chargeable request is made.
func (r *PelicanRepo) Create(ctx context.Context, b *PelicanBatch, results []*PelicanResult) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, `INSERT INTO pelican_batches(request_id,request_hash,request) VALUES(?,?,?) ON CONFLICT(request_id) DO NOTHING RETURNING id,created_at`, b.RequestID, b.Hash, string(b.Request)).Scan(&b.ID, &b.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		existing, e := r.FindBatch(ctx, b.RequestID)
		if e != nil {
			return false, e
		}
		if existing.Hash != b.Hash {
			return false, ErrPelicanConflict
		}
		*b = *existing
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, v := range results {
		data, e := json.Marshal(v.PelicanMetadata)
		if e != nil {
			return false, e
		}
		v.BatchID = b.ID
		if e = tx.QueryRowContext(ctx, `INSERT INTO pelican_results(batch_id,group_id,model,metadata) VALUES(?,?,?,?) RETURNING id,tested_at`, b.ID, v.GroupID, v.Model, string(data)).Scan(&v.ID, &v.TestedAt); e != nil {
			return false, e
		}
	}
	return true, tx.Commit()
}
func scanPelican(s interface{ Scan(...any) error }, detail bool) (*PelicanResult, error) {
	v := &PelicanResult{}
	var meta, output []byte
	dest := []any{&v.ID, &v.BatchID, &v.GroupID, &v.Model, &v.Status, &v.PublishedAt, &v.TestedAt, &meta}
	if detail {
		dest = append(dest, &output)
	}
	if err := s.Scan(dest...); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(meta, &v.PelicanMetadata); err != nil {
		return nil, err
	}
	if detail {
		v.Output = &PelicanOutput{}
		if err := json.Unmarshal(output, v.Output); err != nil {
			return nil, err
		}
	}
	return v, nil
}

const pelicanColumns = `id,batch_id,group_id,model,status,published_at,tested_at,metadata`

func (r *PelicanRepo) Get(ctx context.Context, id int64) (*PelicanResult, error) {
	return scanPelican(r.db.QueryRowContext(ctx, `SELECT `+pelicanColumns+`,output FROM pelican_results WHERE id=?`, id), true)
}
func pelicanWhere(f PelicanFilter) (string, []any) {
	where := []string{"1=1"}
	args := []any{}
	if f.Public {
		where = append(where, "published_at IS NOT NULL", "status='completed'")
		if len(f.VisibleGroups) == 0 {
			where = append(where, "FALSE")
		} else {
			marks := make([]string, len(f.VisibleGroups))
			for i, id := range f.VisibleGroups {
				marks[i] = "?"
				args = append(args, id)
			}
			where = append(where, "group_id IN ("+strings.Join(marks, ",")+")")
		}
	}
	if f.GroupID > 0 {
		where = append(where, "group_id=?")
		args = append(args, f.GroupID)
	}
	if f.BatchID > 0 {
		where = append(where, "batch_id=?")
		args = append(args, f.BatchID)
	}
	if f.Model != "" {
		where = append(where, "model=?")
		args = append(args, f.Model)
	}
	if f.From != "" {
		where = append(where, "tested_at>=?::timestamptz")
		args = append(args, f.From)
	}
	if f.To != "" {
		where = append(where, "tested_at<?::timestamptz")
		args = append(args, f.To)
	}
	return strings.Join(where, " AND "), args
}
func (r *PelicanRepo) List(ctx context.Context, f PelicanFilter) ([]*PelicanResult, int64, error) {
	where, args := pelicanWhere(f)
	source := `SELECT ` + pelicanColumns + `,ROW_NUMBER() OVER(PARTITION BY group_id,model ORDER BY tested_at DESC,id DESC) AS rn FROM pelican_results WHERE ` + where
	tail := ""
	if f.Latest {
		tail = " WHERE rn=1"
	}
	var total int64
	if err := r.db.QueryRowContext(ctx, `WITH filtered AS (`+source+`) SELECT COUNT(*) FROM filtered`+tail, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 || f.PageSize > 100 {
		f.PageSize = 20
	}
	args = append(args, f.PageSize, (f.Page-1)*f.PageSize)
	rows, err := r.db.QueryContext(ctx, `WITH filtered AS (`+source+`) SELECT `+pelicanColumns+` FROM filtered`+tail+` ORDER BY tested_at DESC,id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]*PelicanResult, 0)
	for rows.Next() {
		v, e := scanPelican(rows, false)
		if e != nil {
			return nil, 0, e
		}
		out = append(out, v)
	}
	return out, total, rows.Err()
}

// Update cannot resurrect a cancelled/interrupted result.
func (r *PelicanRepo) Update(ctx context.Context, v *PelicanResult) error {
	meta, err := json.Marshal(v.PelicanMetadata)
	if err != nil {
		return err
	}
	output, err := json.Marshal(v.Output)
	if err != nil {
		return err
	}
	if v.Output == nil {
		output = []byte(`{}`)
	}
	res, err := r.db.ExecContext(ctx, `UPDATE pelican_results SET status=?,metadata=?,output=?,tested_at=COALESCE(?::timestamptz,tested_at) WHERE id=? AND status IN ('queued','running')`, v.Status, string(meta), string(output), v.StartedAt, v.ID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return ErrPelicanConflict
	}
	return err
}
func (r *PelicanRepo) Cancel(ctx context.Context, batchID int64) error {
	_, err := r.db.ExecContext(ctx, `UPDATE pelican_results SET status='cancelled', metadata=metadata || jsonb_build_object('finished_at',now()) WHERE batch_id=? AND status IN ('queued','running')`, batchID)
	return err
}
func (r *PelicanRepo) Recover(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, `UPDATE pelican_results SET status='interrupted', metadata=metadata || jsonb_build_object('finished_at',now()) WHERE status IN ('queued','running')`)
	return err
}

// Mutate is all-or-nothing, including validation of every selected row.
func (r *PelicanRepo) Mutate(ctx context.Context, ids []int64, action string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		var status string
		var published *time.Time
		var doc string
		if err = tx.QueryRowContext(ctx, `SELECT status,published_at,COALESCE(output->>'document','') FROM pelican_results WHERE id=? FOR UPDATE`, id).Scan(&status, &published, &doc); err != nil {
			return err
		}
		switch action {
		case "publish":
			if status != "completed" || doc == "" {
				return ErrPelicanConflict
			}
			_, err = tx.ExecContext(ctx, `UPDATE pelican_results SET published_at=COALESCE(published_at,now()) WHERE id=?`, id)
		case "unpublish":
			_, err = tx.ExecContext(ctx, `UPDATE pelican_results SET published_at=NULL WHERE id=?`, id)
		case "delete":
			if published != nil || status == "queued" || status == "running" {
				return ErrPelicanConflict
			}
			_, err = tx.ExecContext(ctx, `DELETE FROM pelican_results WHERE id=?`, id)
		default:
			return fmt.Errorf("invalid action")
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
