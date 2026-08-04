-- P6：自动建号（资源层对接）
--
-- 记录「上游 key ↔ 本站账号」的对接关系。没有这张表就无法做补偿对账与取消对接。
-- mode=managed：本系统创建的资源，取消对接时可选择删除远端；
-- mode=existing：关联已有资源，取消对接只解除本地记录，绝不删远端。

CREATE TABLE IF NOT EXISTS upstream_connections (
  id                 INTEGER PRIMARY KEY AUTOINCREMENT,
  provider_id        INTEGER NOT NULL,              -- 上游站（providers.id）
  upstream_group     TEXT NOT NULL,                 -- 上游分组名
  upstream_group_id  INTEGER NOT NULL DEFAULT 0,    -- 上游分组数字 id（建 key 必需）
  upstream_key_id    INTEGER NOT NULL DEFAULT 0,
  upstream_key_name  TEXT NOT NULL DEFAULT '',
  local_account_id   INTEGER NOT NULL DEFAULT 0,    -- 本站账号 id
  local_account_name TEXT NOT NULL DEFAULT '',
  local_group_ids    TEXT NOT NULL DEFAULT '[]',    -- JSON 数字数组
  group_platform     TEXT NOT NULL DEFAULT '',      -- 自动推导：anthropic/openai/gemini/...
  mode               TEXT NOT NULL DEFAULT 'managed', -- managed | existing
  operation_id       TEXT NOT NULL DEFAULT '',      -- 幂等键，防手抖双击建两份
  status             TEXT NOT NULL DEFAULT 'pending', -- pending | active | failed
  error              TEXT,
  created_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

-- 同一上游分组只允许一条 active 连接（pending/failed 不占位，便于重试）
CREATE UNIQUE INDEX IF NOT EXISTS idx_upstream_conn_active
  ON upstream_connections(provider_id, upstream_group) WHERE status = 'active';
-- 幂等键唯一（空串不参与）
CREATE UNIQUE INDEX IF NOT EXISTS idx_upstream_conn_operation
  ON upstream_connections(operation_id) WHERE operation_id <> '';
