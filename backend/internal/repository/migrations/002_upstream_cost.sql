-- 上游供应商 per-key 逐日真实成本（口径：上游 actual_cost = 倍率折后实扣，即我们实际支付给供应商的金额）
-- 唯一键保证同步与历史回补幂等：同一天重复采集覆写而非追加。
CREATE TABLE IF NOT EXISTS upstream_key_costs (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  provider_id     INTEGER NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
  upstream_key_id INTEGER NOT NULL,          -- 上游站点 api_key.id
  key_name        TEXT NOT NULL DEFAULT '',  -- 上游 key 名称（展示用）
  account_id      INTEGER,                   -- 匹配到的本站 accounts.id（NULL=未匹配）
  usage_date      TEXT NOT NULL,             -- YYYY-MM-DD（本地时区）
  actual_cost     REAL NOT NULL DEFAULT 0,   -- 实扣（真实成本）
  official_cost   REAL NOT NULL DEFAULT 0,   -- 原始官价（对照用，逐日接口才有）
  requests        INTEGER NOT NULL DEFAULT 0,
  synced_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  UNIQUE (provider_id, upstream_key_id, usage_date)
);
CREATE INDEX IF NOT EXISTS idx_ukc_date        ON upstream_key_costs(usage_date);
CREATE INDEX IF NOT EXISTS idx_ukc_account     ON upstream_key_costs(account_id, usage_date);
CREATE INDEX IF NOT EXISTS idx_ukc_provider    ON upstream_key_costs(provider_id, usage_date);

-- 上游 key ↔ 本站账号映射（按 api_key 明文的 sha256 指纹匹配，不存明文）
CREATE TABLE IF NOT EXISTS upstream_key_map (
  provider_id     INTEGER NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
  upstream_key_id INTEGER NOT NULL,
  key_name        TEXT NOT NULL DEFAULT '',
  key_fingerprint TEXT NOT NULL DEFAULT '',  -- sha256(api_key) 十六进制
  account_id      INTEGER,                   -- 本站 accounts.id（NULL=未匹配上）
  account_name    TEXT NOT NULL DEFAULT '',
  rate_multiplier REAL,                      -- 上游分组倍率（展示用）
  status          TEXT NOT NULL DEFAULT '',
  updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  PRIMARY KEY (provider_id, upstream_key_id)
);
CREATE INDEX IF NOT EXISTS idx_ukm_account ON upstream_key_map(account_id);

-- 每个供应商的成本同步状态（前端展示「同步时间」）
CREATE TABLE IF NOT EXISTS cost_sync_state (
  provider_id   INTEGER PRIMARY KEY REFERENCES providers(id) ON DELETE CASCADE,
  last_synced_at TEXT,
  last_error     TEXT,
  keys_total     INTEGER NOT NULL DEFAULT 0,
  keys_matched   INTEGER NOT NULL DEFAULT 0,
  backfilled_at  TEXT,                       -- 逐日历史回补完成时间（NULL=未回补）
  updated_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
