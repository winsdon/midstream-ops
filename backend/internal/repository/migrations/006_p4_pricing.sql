-- P4：调价映射（上游分组倍率 → 本站分组倍率联动）

-- 自己站点建模为特殊 provider（role='self'）：复用登录/token/加密设施，
-- 但不参与余额采集与探测调度。
ALTER TABLE providers ADD COLUMN role TEXT NOT NULL DEFAULT 'upstream'; -- upstream | self

-- 映射规则：目标倍率 = 上游倍率 × factor + offset，夹紧 [min_rate, max_rate]
CREATE TABLE IF NOT EXISTS rate_mappings (
  id                INTEGER PRIMARY KEY AUTOINCREMENT,
  provider_id       INTEGER NOT NULL,               -- 上游站（providers.id）
  upstream_group    TEXT NOT NULL,                  -- 上游分组名（rate_snapshots.entity_id, scope=upstream）
  local_group_id    INTEGER NOT NULL,               -- 本站分组 id（PG groups.id）
  local_group_name  TEXT NOT NULL DEFAULT '',       -- 冗余展示名
  auto_enabled      INTEGER NOT NULL DEFAULT 0,     -- 自动调价开关
  factor            REAL NOT NULL DEFAULT 1,
  offset            REAL NOT NULL DEFAULT 0,
  min_rate          REAL,                           -- NULL = 不设限
  max_rate          REAL,
  last_applied_rate REAL,                           -- 冲突检测基准：系统最后一次写入的值
  conflict          INTEGER NOT NULL DEFAULT 0,     -- 检测到人工改动，停止自动覆盖
  created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  UNIQUE (provider_id, upstream_group, local_group_id)
);

-- 调价审计：每次应用（自动或手动）留痕
CREATE TABLE IF NOT EXISTS rate_actions (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  mapping_id  INTEGER NOT NULL,
  trigger_by  TEXT NOT NULL,                        -- auto | manual
  old_rate    REAL,
  new_rate    REAL NOT NULL,
  status      TEXT NOT NULL,                        -- pending | applied | failed | skipped_conflict
  error       TEXT,
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_rate_actions_mapping ON rate_actions(mapping_id, created_at DESC);
