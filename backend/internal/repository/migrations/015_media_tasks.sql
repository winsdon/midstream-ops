-- 生图 / 生视频任务台账
--
-- 【为什么必须落库】视频费用在任务提交成功那一刻就已计入，即便上游内容审核
-- 拒绝（轮询时返回 400）也不退还。任务记录因此是「钱花在哪」的凭证，不能只
-- 挂在 30 分钟的内存嵌入会话上——用户刷新 iframe、进程重启，已付费的任务都
-- 不能消失。这与广场页（纯只读展示，重启即失效可接受）是完全不同的性质。
--
-- 【只存元数据不存产物】图片存 xAI CDN 直链、视频存 upstream_request_id，
-- 查看时由后端实时代理。代价是图片直链过期后产物不可再取（xAI 未公布 TTL）；
-- 换来的是零存储运维、零磁盘配额管理。将来若要留存产物，加一列 local_path
-- 即可，现有记录不受影响——空值天然表示「走实时代理」。
--
-- 【绝不存明文 key】只存 keyidentity.Fingerprint(key) 与 sub2api 的 api_key_id。
-- 明文 key 只在「后端 ↔ 网关」这一段的内存里存在，不进库、不进日志、不进响应。
-- 指纹用于「这个任务是哪把 key 打的」的事后归因，且用户轮换 key 后旧任务
-- 仍可比对（指纹算法对 sk- 前缀不敏感，见 pkg/keyidentity）。
--
-- 【client_request_id 是幂等键】前端每次提交生成一个随机 ID，配合下方 UNIQUE
-- 索引兜住重复提交（用户狂点按钮、网络层重试）。范式取自 provision_service 的
-- operation_id：先落 pending 记录再打上游，即便补偿失败也能事后对账。
-- 对「提交即扣费且不退款」的视频任务，这不是优化，是必需品。
--
-- 【为什么 prompt 不加密】prompt 是用户刚在本页输入的创作描述、非 PII，且要在
-- 任务列表里展示与检索。KYC 的身份证号加密是因为它是不可再生的强 PII，泄露
-- 后果不可逆；prompt 不适用同一标准。代价是运维可见用户 prompt，该权衡记在
-- docs/DESIGN_NOTES.md。
--
-- 【params_json 存参数快照而非拆列】size / quality / resolution / duration / n
-- 分属不同任务类型，拆成独立列会得到一张大半为空的稀疏表，且每加一种生成
-- 能力就要改表结构。快照 JSON 只用于展示与复现，从不参与 WHERE / 聚合，
-- 因此不需要可查询性。
--
-- 【不用 CHECK 约束】与全库既有 14 个迁移保持一致：枚举与范围校验统一在 Go 侧
-- service 的单一入口做，这样非法输入返回可读的 400，而不是驱动层的不透明 500。

CREATE TABLE IF NOT EXISTS media_tasks (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  sub2api_user_id     TEXT    NOT NULL,               -- 来自嵌入会话，绝不取自请求体
  api_key_id          INTEGER NOT NULL DEFAULT 0,     -- sub2api api_keys.id
  key_fingerprint     TEXT    NOT NULL DEFAULT '',    -- keyidentity.Fingerprint(key)
  group_id            INTEGER NOT NULL DEFAULT 0,
  task_kind           TEXT    NOT NULL,               -- t2i | i2i | t2v | i2v
  model               TEXT    NOT NULL,
  prompt              TEXT    NOT NULL DEFAULT '',
  params_json         TEXT    NOT NULL DEFAULT '{}',  -- 参数快照，仅展示与复现
  status              TEXT    NOT NULL DEFAULT 'pending', -- pending | succeeded | failed
  progress            INTEGER NOT NULL DEFAULT 0,     -- 0-100，仅视频任务有意义
  upstream_request_id TEXT    NOT NULL DEFAULT '',    -- 视频任务的 request_id
  result_url          TEXT    NOT NULL DEFAULT '',    -- 图片任务的上游直链
  cost_ticks          INTEGER NOT NULL DEFAULT 0,     -- 上游实扣，1 tick = 1e-10 USD
  est_cost_ticks      INTEGER NOT NULL DEFAULT 0,     -- 提交前预估，与实扣对照
  error_message       TEXT    NOT NULL DEFAULT '',    -- 已过 redactError 脱敏
  client_request_id   TEXT    NOT NULL,               -- 幂等键
  created_at          TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at          TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

-- 幂等键按用户隔离：不同用户各自生成的随机 ID 理论上可能相撞，
-- 全局唯一会让后来者被误判为重复提交而拿到别人的任务。
CREATE UNIQUE INDEX IF NOT EXISTS idx_media_tasks_idem
  ON media_tasks(sub2api_user_id, client_request_id);

-- 任务列表的唯一查询路径：按用户 + 时间倒序。
CREATE INDEX IF NOT EXISTS idx_media_tasks_user_created
  ON media_tasks(sub2api_user_id, created_at DESC);

-- 部分索引：待刷新的视频任务通常只占极小比例，全表索引是浪费。
CREATE INDEX IF NOT EXISTS idx_media_tasks_pending
  ON media_tasks(status) WHERE status = 'pending';
