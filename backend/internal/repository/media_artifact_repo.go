package repository

import (
	"context"
	"strings"
)

// MediaArtifact 一份已转存到对象存储的产物。
//
// 【与 MediaTask.ResultURL 的区别】那一列存的是上游返回的 URL：xAI 自有 CDN
// 直链，国内不可达且会过期，只作为事后追溯的线索。这里的 URL 指向本站自己的
// 对象存储，不需要认证、不会过期，是前端真正拿来渲染的地址。
type MediaArtifact struct {
	ID        int64
	TaskID    int64
	Index     int
	URL       string
	ObjectKey string
	MimeType  string
	Bytes     int64
	CreatedAt string
}

// MediaArtifactRepo 产物记录存储。
type MediaArtifactRepo struct {
	db *DB
}

// NewMediaArtifactRepo 创建 MediaArtifactRepo。
func NewMediaArtifactRepo(s *Store) *MediaArtifactRepo {
	return &MediaArtifactRepo{db: s.DB()}
}

// Save 写入一条产物记录。
//
// 【必须幂等】转存重试（进程重启后补投、用户重复触发）会对同一 (task_id, idx)
// 再写一次。用 UPSERT 而非 INSERT，让重试覆盖旧值而不是撞唯一索引报错——
// 报错会让整次转存前功尽弃，而实际上对象已经传上去了。
func (r *MediaArtifactRepo) Save(ctx context.Context, a MediaArtifact) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO media_artifacts (task_id, idx, url, object_key, mime_type, bytes)
		VALUES (?,?,?,?,?,?)
		ON CONFLICT (task_id, idx) DO UPDATE SET
			url = EXCLUDED.url,
			object_key = EXCLUDED.object_key,
			mime_type = EXCLUDED.mime_type,
			bytes = EXCLUDED.bytes`,
		a.TaskID, a.Index, a.URL, a.ObjectKey, a.MimeType, a.Bytes)
	return err
}

// ListByTasks 批量查询多个任务的产物，返回 taskID → 产物列表（按 idx 升序）。
//
// 【为什么是批量而非单条】任务列表一次返回几十条记录，逐条查产物就是 N+1。
// 列表接口每 5 秒被前端轮询一次，N+1 在这里是真实的负载而不是洁癖。
//
// 【为什么手工拼占位符而不是 ANY($1)】走的是 database/sql + pgx stdlib，
// []int64 不是合法的 driver.Value，传进去会在运行时报「unsupported type」。
// 拼的是 `?` 的个数（值仍走参数绑定），由 rebind 壳统一换成 $N，不构成注入面。
func (r *MediaArtifactRepo) ListByTasks(ctx context.Context, taskIDs []int64) (map[int64][]MediaArtifact, error) {
	out := make(map[int64][]MediaArtifact)
	if len(taskIDs) == 0 {
		return out, nil
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(taskIDs)), ",")
	args := make([]any, len(taskIDs))
	for i, id := range taskIDs {
		args[i] = id
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT id, task_id, idx, url, object_key, mime_type, bytes, created_at
		FROM media_artifacts WHERE task_id IN (`+placeholders+`)
		ORDER BY task_id, idx`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var a MediaArtifact
		if err := rows.Scan(&a.ID, &a.TaskID, &a.Index, &a.URL,
			&a.ObjectKey, &a.MimeType, &a.Bytes, &a.CreatedAt); err != nil {
			return nil, err
		}
		out[a.TaskID] = append(out[a.TaskID], a)
	}
	return out, rows.Err()
}
