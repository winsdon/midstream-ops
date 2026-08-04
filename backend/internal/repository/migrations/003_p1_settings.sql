-- P1：系统设置（键值 JSON）+ 采集健康状态 + 供应商充值倍率/登录冷却

-- 系统设置：key 为设置域（strategy / notify），value 为 JSON
CREATE TABLE IF NOT EXISTS settings (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL,
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

-- 采集健康状态：每 (provider, task) 一行；provider_id=0 表示全局任务
CREATE TABLE IF NOT EXISTS collector_state (
  provider_id          INTEGER NOT NULL,
  task                 TEXT NOT NULL,          -- sync | probe | rate
  last_run_at          TEXT,
  last_success_at      TEXT,
  last_error           TEXT,                   -- 已截断的错误摘要
  consecutive_failures INTEGER NOT NULL DEFAULT 0,
  next_eligible_at     TEXT,                   -- 退避解禁时刻；NULL = 无限制
  PRIMARY KEY (provider_id, task)
);

-- 充值倍率：余额预警折合人民币用（balance × recharge_rate = CNY）
ALTER TABLE providers ADD COLUMN recharge_rate REAL NOT NULL DEFAULT 1;
-- 登录冷却：登录被拒（4xx）时按阶梯冷却，避免反复撞供应商 WAF/风控
ALTER TABLE providers ADD COLUMN login_failures INTEGER NOT NULL DEFAULT 0;
ALTER TABLE providers ADD COLUMN login_cooldown_until TEXT;
