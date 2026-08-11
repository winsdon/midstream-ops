package repository

import (
	"context"
	"database/sql"
	"errors"
)

// 任务状态。校验在 service 层做（建表刻意不加 CHECK），此处只提供常量避免手写字符串漂移。
const (
	MediaStatusPending   = "pending"
	MediaStatusSucceeded = "succeeded"
	MediaStatusFailed    = "failed"
)

// 任务类型。
const (
	MediaKindText2Image  = "t2i"
	MediaKindImage2Image = "i2i"
	MediaKindText2Video  = "t2v"
	MediaKindImage2Video = "i2v"
)

// MediaTask 一次生图 / 生视频任务。
//
// 图片任务同步完成，落库时已是终态；视频任务落 pending，由后续轮询推进。
type MediaTask struct {
	ID                int64
	Sub2apiUserID     string
	APIKeyID          int64
	KeyFingerprint    string
	GroupID           int64
	TaskKind          string
	Model             string
	Prompt            string
	ParamsJSON        string
	Status            string
	Progress          int
	UpstreamRequestID string
	ResultURL         string
	CostTicks         int64
	EstCostTicks      int64
	ErrorMessage      string
	ClientRequestID   string
	CreatedAt         string
	UpdatedAt         string
}

// MediaTaskParams 新建任务参数。
//
// 刻意不含 Status / Progress / CostTicks：新任务恒为 pending，
// 终态由 MarkSucceeded / MarkFailed 单一入口写入，避免调用方直接构造终态记录
// 而绕过「必须有上游响应才算完成」这一前提。
type MediaTaskParams struct {
	Sub2apiUserID   string
	APIKeyID        int64
	KeyFingerprint  string
	GroupID         int64
	TaskKind        string
	Model           string
	Prompt          string
	ParamsJSON      string
	EstCostTicks    int64
	ClientRequestID string
}

// MediaTaskRepo 生图 / 生视频任务存储。
type MediaTaskRepo struct {
	db *DB
}

// NewMediaTaskRepo 创建 MediaTaskRepo。
func NewMediaTaskRepo(s *Store) *MediaTaskRepo {
	return &MediaTaskRepo{db: s.DB()}
}

const mediaTaskCols = `id, sub2api_user_id, api_key_id, key_fingerprint, group_id,
	task_kind, model, prompt, params_json, status, progress,
	upstream_request_id, result_url, cost_ticks, est_cost_ticks,
	error_message, client_request_id, created_at, updated_at`

func scanMediaTask(row interface{ Scan(...any) error }) (*MediaTask, error) {
	var t MediaTask
	err := row.Scan(&t.ID, &t.Sub2apiUserID, &t.APIKeyID, &t.KeyFingerprint, &t.GroupID,
		&t.TaskKind, &t.Model, &t.Prompt, &t.ParamsJSON, &t.Status, &t.Progress,
		&t.UpstreamRequestID, &t.ResultURL, &t.CostTicks, &t.EstCostTicks,
		&t.ErrorMessage, &t.ClientRequestID, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// Create 落一条 pending 任务。
//
// 必须先落库再打上游：视频提交成功即扣费，若先打上游再落库，落库失败就产生了
// 一笔查无实据的支出。反过来（先落库后失败）只是多一条 failed 记录，可对账。
//
// 幂等键冲突时返回既有任务与 true，让调用方直接复用而非报错——用户狂点按钮
// 或网络重试都会走到这里，报错反而会诱导用户再点一次。
func (r *MediaTaskRepo) Create(ctx context.Context, p MediaTaskParams) (*MediaTask, bool, error) {
	if existing, err := r.GetByClientRequestID(ctx, p.Sub2apiUserID, p.ClientRequestID); err == nil {
		return existing, true, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, false, err
	}

	var id int64
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO media_tasks (sub2api_user_id, api_key_id, key_fingerprint, group_id,
			task_kind, model, prompt, params_json, est_cost_ticks, client_request_id)
		VALUES (?,?,?,?,?,?,?,?,?,?) RETURNING id`,
		p.Sub2apiUserID, p.APIKeyID, p.KeyFingerprint, p.GroupID,
		p.TaskKind, p.Model, p.Prompt, p.ParamsJSON, p.EstCostTicks, p.ClientRequestID).Scan(&id)
	if err != nil {
		// UNIQUE 冲突：并发下两个请求同时通过了上面的存在性检查。
		// 再查一次即可拿到先落地的那条。
		if existing, getErr := r.GetByClientRequestID(ctx, p.Sub2apiUserID, p.ClientRequestID); getErr == nil {
			return existing, true, nil
		}
		return nil, false, err
	}
	t, err := r.GetByID(ctx, id)
	return t, false, err
}

// GetByID 按 ID 查询。调用方仍须校验归属，见 GetOwned。
func (r *MediaTaskRepo) GetByID(ctx context.Context, id int64) (*MediaTask, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+mediaTaskCols+` FROM media_tasks WHERE id = ?`, id)
	t, err := scanMediaTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

// GetOwned 按 ID + 用户查询，用于产物代理等需要归属校验的路径。
//
// 归属校验必须写进 SQL 而非查出来再比对：后者一旦有人漏写 if 就是越权读取
// 他人产物。查不到时统一返回 ErrNotFound，不区分「不存在」与「不属于你」，
// 避免通过响应差异枚举他人任务 ID。
func (r *MediaTaskRepo) GetOwned(ctx context.Context, id int64, userID string) (*MediaTask, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+mediaTaskCols+` FROM media_tasks WHERE id = ? AND sub2api_user_id = ?`, id, userID)
	t, err := scanMediaTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

// GetByClientRequestID 按幂等键查询。
func (r *MediaTaskRepo) GetByClientRequestID(ctx context.Context, userID, clientRequestID string) (*MediaTask, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+mediaTaskCols+` FROM media_tasks WHERE sub2api_user_id = ? AND client_request_id = ?`,
		userID, clientRequestID)
	t, err := scanMediaTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

// ListByUser 返回某用户最近的任务（时间倒序）。limit <= 0 时回退 50。
func (r *MediaTaskRepo) ListByUser(ctx context.Context, userID string, limit int) ([]MediaTask, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+mediaTaskCols+` FROM media_tasks WHERE sub2api_user_id = ?
		 ORDER BY created_at DESC, id DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MediaTask
	for rows.Next() {
		t, err := scanMediaTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// CountPendingVideos 统计某用户进行中的视频任务数（并发上限判定用）。
//
// 只数视频：图片是同步调用，落库时已是终态，不存在「进行中」。
func (r *MediaTaskRepo) CountPendingVideos(ctx context.Context, userID string) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM media_tasks
		WHERE sub2api_user_id = ? AND status = ? AND task_kind IN (?, ?)`,
		userID, MediaStatusPending, MediaKindText2Video, MediaKindImage2Video).Scan(&n)
	return n, err
}

// SetUpstreamRequestID 记录上游返回的 request_id（视频任务提交成功后立即调用）。
//
// 这一步必须尽快落库：request_id 是事后查询任务状态的唯一凭据，
// 丢了就等于花了钱却找不到产物。
func (r *MediaTaskRepo) SetUpstreamRequestID(ctx context.Context, id int64, requestID string) error {
	return r.exec(ctx, `UPDATE media_tasks SET upstream_request_id = ?,
		updated_at = ? WHERE id = ?`, requestID, nowUTC(), id)
}

// UpdateProgress 更新进行中任务的进度。
func (r *MediaTaskRepo) UpdateProgress(ctx context.Context, id int64, progress int) error {
	return r.exec(ctx, `UPDATE media_tasks SET progress = ?,
		updated_at = ? WHERE id = ?`, progress, nowUTC(), id)
}

// MarkSucceeded 标记任务成功。resultURL 对图片是上游直链，对视频留空（用 request_id 代理）。
func (r *MediaTaskRepo) MarkSucceeded(ctx context.Context, id int64, resultURL string, costTicks int64) error {
	return r.exec(ctx, `UPDATE media_tasks SET status = ?, progress = 100,
		result_url = ?, cost_ticks = ?,
		updated_at = ? WHERE id = ?`,
		MediaStatusSucceeded, resultURL, costTicks, nowUTC(), id)
}

// MarkFailed 标记任务失败。msg 必须已过 redactError 脱敏。
//
// 注意失败不代表没扣钱：视频任务在提交成功那一刻就已计费，
// 之后被内容审核拒绝仍然扣费。前端必须对这种失败明确提示。
func (r *MediaTaskRepo) MarkFailed(ctx context.Context, id int64, msg string) error {
	return r.exec(ctx, `UPDATE media_tasks SET status = ?, error_message = ?,
		updated_at = ? WHERE id = ?`,
		MediaStatusFailed, msg, nowUTC(), id)
}

// Cleanup 删除 before（YYYY-MM-DDTHH:MM:SSZ）之前创建的任务，返回删除行数。
func (r *MediaTaskRepo) Cleanup(ctx context.Context, before string) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM media_tasks WHERE created_at < ?`, before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// exec 执行更新并把「影响 0 行」翻译成 ErrNotFound，让调用方能回 404 而非静默成功。
func (r *MediaTaskRepo) exec(ctx context.Context, query string, args ...any) error {
	res, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
