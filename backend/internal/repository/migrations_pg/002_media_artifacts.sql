-- 生图 / 生视频产物转存
--
-- 【为什么单独一张表而不是给 media_tasks 加列】一个图片任务最多产出 4 张图
-- （n 上限为 4）。塞进任务行会逼出 result_url_2 / _3 / _4 这种反模式，
-- 而且将来改上限就要改表结构。
--
-- 【为什么带 ON DELETE CASCADE】用户删任务时产物记录随之消失。R2 上的对象
-- 不同步删——让「删记录」这个高频操作依赖一次跨境网络调用是不划算的，
-- 对象的清理交给桶的生命周期规则。

CREATE TABLE IF NOT EXISTS media_artifacts (
  id          BIGSERIAL PRIMARY KEY,
  task_id     BIGINT NOT NULL REFERENCES media_tasks(id) ON DELETE CASCADE,
  idx         INT    NOT NULL DEFAULT 0,        -- 同任务内的序号，决定展示顺序
  url         TEXT   NOT NULL,                  -- 对象存储的公开 URL
  object_key  TEXT   NOT NULL DEFAULT '',       -- 便于将来按前缀批量清理
  mime_type   TEXT   NOT NULL DEFAULT '',
  bytes       BIGINT NOT NULL DEFAULT 0,        -- BIGINT：视频可超 2GB
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 幂等：转存重试时按 (task_id, idx) 覆盖而不是堆出重复行
CREATE UNIQUE INDEX IF NOT EXISTS idx_media_artifacts_task_idx
  ON media_artifacts(task_id, idx);

-- 转存状态。
--   ''        未涉及转存（R2 未启用，或任务尚未成功）
--   'pending' 转存中（视频异步转存期间）
--   'stored'  已转存，前端可直接用 media_artifacts 里的 URL
--   'failed'  转存失败，前端回退到 inline / 代理路径
--
-- 【为什么需要 pending 而不是「有没有产物行」二态】视频转存是异步的，
-- 进程重启时需要能找出「已成功但还没转存完」的任务重新投递。
ALTER TABLE media_tasks ADD COLUMN IF NOT EXISTS storage_status TEXT NOT NULL DEFAULT '';

-- 重启后补扫用：只关心在途的那几条，用部分索引而非全表索引
CREATE INDEX IF NOT EXISTS idx_media_tasks_storage_pending
  ON media_tasks(storage_status) WHERE storage_status = 'pending';
