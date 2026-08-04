-- P3：变更驱动倍率快照（替换 rate_state + rate_change_history 两表）
--
-- 每行代表「一次真实的倍率状态」：first_seen_at 为该倍率首次出现（=变化时刻），
-- last_seen_at 为最后一次确认存在。涨跌幅由相邻行推导（LAG），无需单独历史表。
-- scope 区分本站（local，从 PG 轮询）与上游站点（upstream，随 provider sync 拉取）。

CREATE TABLE IF NOT EXISTS rate_snapshots (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  scope         TEXT NOT NULL DEFAULT 'local',   -- local | upstream
  provider_id   INTEGER NOT NULL DEFAULT 0,      -- upstream 时指向 providers.id
  entity_type   TEXT NOT NULL,                   -- group | account
  entity_id     TEXT NOT NULL,                   -- local 用数字 id；upstream 用分组名
  name          TEXT NOT NULL,
  rate          REAL NOT NULL,
  first_seen_at TEXT NOT NULL,
  last_seen_at  TEXT NOT NULL,
  deleted       INTEGER NOT NULL DEFAULT 0       -- 上游已消失（重新出现则插新行复活）
);
CREATE INDEX IF NOT EXISTS idx_rate_snap_entity ON rate_snapshots(scope, provider_id, entity_type, entity_id, first_seen_at DESC);
CREATE INDEX IF NOT EXISTS idx_rate_snap_time   ON rate_snapshots(first_seen_at);

-- 旧数据迁移：
-- 1) rate_change_history 每条变化 → 一行快照（该倍率区间 [observed_at, 下一次变化)）
INSERT INTO rate_snapshots (scope, provider_id, entity_type, entity_id, name, rate, first_seen_at, last_seen_at, deleted)
SELECT 'local', 0, h.entity_type, CAST(h.entity_id AS TEXT), h.entity_name, h.new_rate, h.observed_at, h.observed_at, 0
FROM rate_change_history h;

-- 2) rate_state 当前基线 → 当前行（若与最后一次变化同倍率则合并为延长 last_seen_at）
INSERT INTO rate_snapshots (scope, provider_id, entity_type, entity_id, name, rate, first_seen_at, last_seen_at, deleted)
SELECT 'local', 0, s.entity_type, CAST(s.entity_id AS TEXT), s.entity_name, s.rate,
       COALESCE(
         (SELECT MAX(h.observed_at) FROM rate_change_history h
          WHERE h.entity_type = s.entity_type AND h.entity_id = s.entity_id AND h.new_rate = s.rate),
         s.updated_at),
       s.updated_at, 0
FROM rate_state s
WHERE NOT EXISTS (
  SELECT 1 FROM rate_snapshots r
  WHERE r.scope='local' AND r.entity_type = s.entity_type AND r.entity_id = CAST(s.entity_id AS TEXT)
    AND r.rate = s.rate
    AND r.first_seen_at = COALESCE(
      (SELECT MAX(h.observed_at) FROM rate_change_history h
       WHERE h.entity_type = s.entity_type AND h.entity_id = s.entity_id AND h.new_rate = s.rate),
      s.updated_at)
);
-- 基线行的 last_seen_at 延长到 rate_state.updated_at
UPDATE rate_snapshots SET last_seen_at = (
  SELECT s.updated_at FROM rate_state s
  WHERE s.entity_type = rate_snapshots.entity_type AND CAST(s.entity_id AS TEXT) = rate_snapshots.entity_id
    AND s.rate = rate_snapshots.rate
)
WHERE scope='local' AND EXISTS (
  SELECT 1 FROM rate_state s
  WHERE s.entity_type = rate_snapshots.entity_type AND CAST(s.entity_id AS TEXT) = rate_snapshots.entity_id
    AND s.rate = rate_snapshots.rate
);

-- 3) 删除旧表
DROP TABLE IF EXISTS rate_state;
DROP TABLE IF EXISTS rate_change_history;
