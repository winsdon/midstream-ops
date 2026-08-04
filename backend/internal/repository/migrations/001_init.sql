-- 供应商表
CREATE TABLE IF NOT EXISTS providers (
  id                    INTEGER PRIMARY KEY AUTOINCREMENT,
  name                  TEXT NOT NULL UNIQUE,        -- 前缀名（不含【】），如 walk
  note                  TEXT NOT NULL DEFAULT '',
  balance_type          TEXT NOT NULL DEFAULT 'none',-- sub2api | manual | none
  base_url              TEXT NOT NULL DEFAULT '',
  login_email           TEXT NOT NULL DEFAULT '',
  login_password        TEXT NOT NULL DEFAULT '',
  access_token          TEXT NOT NULL DEFAULT '',
  token_expires_at      TEXT,
  low_balance_threshold REAL NOT NULL DEFAULT 0,
  probe_enabled         INTEGER NOT NULL DEFAULT 0,
  probe_model           TEXT,
  last_balance          REAL,
  last_balance_at       TEXT,
  last_balance_error    TEXT,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

-- 余额快照
CREATE TABLE IF NOT EXISTS balance_snapshots (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  provider_id INTEGER NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
  balance     REAL,
  currency    TEXT NOT NULL DEFAULT 'USD',
  source      TEXT NOT NULL DEFAULT 'auto',  -- auto | manual
  metrics     TEXT,                          -- JSON：dashboard/stats 摘要
  error       TEXT,
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_balance_snap_provider_time ON balance_snapshots(provider_id, created_at);

-- 当前倍率状态（diff 基准）
CREATE TABLE IF NOT EXISTS rate_state (
  entity_type TEXT NOT NULL,               -- group | account
  entity_id   INTEGER NOT NULL,
  entity_name TEXT NOT NULL,
  rate        REAL NOT NULL,
  updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  PRIMARY KEY (entity_type, entity_id)
);

-- 倍率变化历史
CREATE TABLE IF NOT EXISTS rate_change_history (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  entity_type TEXT NOT NULL,
  entity_id   INTEGER NOT NULL,
  entity_name TEXT NOT NULL,
  old_rate    REAL NOT NULL,
  new_rate    REAL NOT NULL,
  observed_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_rate_hist_entity ON rate_change_history(entity_type, entity_id, observed_at);
CREATE INDEX IF NOT EXISTS idx_rate_hist_time   ON rate_change_history(observed_at);

-- 探测结果
CREATE TABLE IF NOT EXISTS probe_results (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  provider_id  INTEGER,
  account_id   INTEGER NOT NULL,
  account_name TEXT NOT NULL,
  platform     TEXT NOT NULL,
  model        TEXT NOT NULL,
  base_url     TEXT NOT NULL DEFAULT '',
  source       TEXT NOT NULL DEFAULT 'schedule', -- schedule | manual
  success      INTEGER NOT NULL,
  status_code  INTEGER,
  ttft_ms      INTEGER,
  total_ms     INTEGER,
  error        TEXT,
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_probe_account_time  ON probe_results(account_id, created_at);
CREATE INDEX IF NOT EXISTS idx_probe_provider_time ON probe_results(provider_id, created_at);
CREATE INDEX IF NOT EXISTS idx_probe_time          ON probe_results(created_at);
