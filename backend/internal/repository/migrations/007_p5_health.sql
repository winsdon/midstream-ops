-- P5：分组健康（六状态状态机 + 探测预算）

-- 账号健康状态：probe 结果喂状态机，转移落库
CREATE TABLE IF NOT EXISTS health_states (
  account_id           INTEGER PRIMARY KEY,          -- PG accounts.id
  account_name         TEXT NOT NULL DEFAULT '',
  provider_id          INTEGER,                      -- 关联供应商（可空）
  state                TEXT NOT NULL DEFAULT 'healthy', -- healthy|degraded|suspended|observing|recovering|disabled
  consecutive_failures INTEGER NOT NULL DEFAULT 0,
  consecutive_successes INTEGER NOT NULL DEFAULT 0,
  weight_percent       INTEGER NOT NULL DEFAULT 100, -- 降权百分比（degraded/recovering 阶梯）
  cooldown_until       TEXT,                         -- suspended 冷却截止
  observing_until      TEXT,                         -- observing 观察窗截止
  last_probe_at        TEXT,
  updated_at           TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_health_provider ON health_states(provider_id);

-- 状态迁移事件（时间线展示）
CREATE TABLE IF NOT EXISTS health_events (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  account_id  INTEGER NOT NULL,
  from_state  TEXT NOT NULL,
  to_state    TEXT NOT NULL,
  reason      TEXT NOT NULL DEFAULT '',   -- hard_failure | soft_failure | probe_success | cooldown_expired | ...
  detail      TEXT,                        -- 已脱敏错误摘要
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_health_events_account ON health_events(account_id, created_at DESC);

-- 每日探测预算（防打爆上游；日界按本地时区日期字符串）
CREATE TABLE IF NOT EXISTS probe_budget (
  day  TEXT PRIMARY KEY,   -- YYYY-MM-DD
  used INTEGER NOT NULL DEFAULT 0
);
