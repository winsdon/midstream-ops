-- 本地 monitor 库初始 schema（PostgreSQL）。
--
-- 本文件由 15 个 SQLite 迁移压缩而来，是它们叠加后的终态。压缩的理由：
-- 005/008 里的一次性数据迁移在新库上扫 0 行（源表 001/006 建、005/008 末尾 DROP），
-- 9 处 ALTER TABLE providers ADD COLUMN 的净效果就是把列直接写进 CREATE TABLE。
-- 保留演进过程只是把同一终态用 15 步走完。
--
-- 类型映射：
--   INTEGER PRIMARY KEY AUTOINCREMENT -> BIGSERIAL
--   INTEGER                           -> BIGINT（不用 INTEGER：cost_ticks 以 1e-10 USD
--                                       为单位，32 位必然溢出且是静默截断）
--   REAL                              -> DOUBLE PRECISION
--   TEXT（RFC3339 时刻）               -> TIMESTAMPTZ
--   TEXT（YYYY-MM-DD 日历日）          -> DATE
--   INTEGER（0/1）                     -> BOOLEAN
--   DEFAULT (strftime(...))           -> DEFAULT now()
--
-- 沿用 SQLite 时代的两条约定：
--   1. 无 CHECK 约束 —— 校验放 Go 侧，以便返回可读的 400 而非数据库错误。
--   2. 跨库引用不建外键 —— account_id / local_group_id 等指向上游 sub2api 库，
--      悬垂 id 是有意保留的，便于排查。

-- ============================================================ 供应商

CREATE TABLE IF NOT EXISTS providers (
  id                    BIGSERIAL PRIMARY KEY,
  name                  TEXT NOT NULL UNIQUE,         -- 前缀名（不含【】），如 walk
  note                  TEXT NOT NULL DEFAULT '',
  balance_type          TEXT NOT NULL DEFAULT 'none', -- sub2api | manual | none
  base_url              TEXT NOT NULL DEFAULT '',
  login_email           TEXT NOT NULL DEFAULT '',
  login_password        TEXT NOT NULL DEFAULT '',     -- secretbox 密文
  access_token          TEXT NOT NULL DEFAULT '',     -- secretbox 密文
  token_expires_at      TIMESTAMPTZ,
  low_balance_threshold DOUBLE PRECISION NOT NULL DEFAULT 0,
  probe_enabled         BOOLEAN NOT NULL DEFAULT false,
  probe_model           TEXT,
  last_balance          DOUBLE PRECISION,
  last_balance_at       TIMESTAMPTZ,
  last_balance_error    TEXT,
  recharge_rate         DOUBLE PRECISION NOT NULL DEFAULT 1,
  login_failures        BIGINT NOT NULL DEFAULT 0,
  login_cooldown_until  TIMESTAMPTZ,
  platform              TEXT NOT NULL DEFAULT 'sub2api',
  auth_mode             TEXT NOT NULL DEFAULT 'password',
  refresh_token         TEXT NOT NULL DEFAULT '',     -- secretbox 密文
  session_cookie        TEXT NOT NULL DEFAULT '',     -- secretbox 密文
  upstream_user_id      TEXT NOT NULL DEFAULT '',
  quota_per_unit        DOUBLE PRECISION NOT NULL DEFAULT 500000,
  role                  TEXT NOT NULL DEFAULT 'upstream', -- upstream | self
  ignore_balance_alert  BOOLEAN NOT NULL DEFAULT false,
  self_operated         BOOLEAN NOT NULL DEFAULT false,
  created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ============================================================ 余额与成本

CREATE TABLE IF NOT EXISTS balance_snapshots (
  id          BIGSERIAL PRIMARY KEY,
  provider_id BIGINT NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
  balance     DOUBLE PRECISION,
  currency    TEXT NOT NULL DEFAULT 'USD',
  source      TEXT NOT NULL DEFAULT 'auto',  -- auto | manual
  metrics     TEXT,                          -- JSON：dashboard/stats 摘要
  error       TEXT,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_balance_snap_provider_time ON balance_snapshots(provider_id, created_at);

-- 上游 per-key 逐日成本。成本口径 = 上游实扣 actual_cost，official_cost 仅作官价对照。
CREATE TABLE IF NOT EXISTS upstream_key_costs (
  id              BIGSERIAL PRIMARY KEY,
  provider_id     BIGINT NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
  upstream_key_id BIGINT NOT NULL,           -- 供应商站的 api_key.id
  key_name        TEXT NOT NULL DEFAULT '',
  account_id      BIGINT,                    -- 匹配到的本站 accounts.id（NULL=未匹配）
  usage_date      DATE NOT NULL,             -- 日历日，刻意不存时刻
  actual_cost     DOUBLE PRECISION NOT NULL DEFAULT 0,
  official_cost   DOUBLE PRECISION NOT NULL DEFAULT 0,
  requests        BIGINT NOT NULL DEFAULT 0,
  synced_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (provider_id, upstream_key_id, usage_date)
);
CREATE INDEX IF NOT EXISTS idx_ukc_account  ON upstream_key_costs(account_id, usage_date);
CREATE INDEX IF NOT EXISTS idx_ukc_date     ON upstream_key_costs(usage_date);
CREATE INDEX IF NOT EXISTS idx_ukc_provider ON upstream_key_costs(provider_id, usage_date);

CREATE TABLE IF NOT EXISTS upstream_key_map (
  provider_id     BIGINT NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
  upstream_key_id BIGINT NOT NULL,
  key_name        TEXT NOT NULL DEFAULT '',
  key_fingerprint TEXT NOT NULL DEFAULT '',  -- sha256(api_key) 十六进制
  account_id      BIGINT,
  account_name    TEXT NOT NULL DEFAULT '',
  rate_multiplier DOUBLE PRECISION,
  status          TEXT NOT NULL DEFAULT '',
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (provider_id, upstream_key_id)
);
CREATE INDEX IF NOT EXISTS idx_ukm_account ON upstream_key_map(account_id);

CREATE TABLE IF NOT EXISTS cost_sync_state (
  provider_id    BIGINT PRIMARY KEY REFERENCES providers(id) ON DELETE CASCADE,
  last_synced_at TIMESTAMPTZ,
  last_error     TEXT,
  keys_total     BIGINT NOT NULL DEFAULT 0,
  keys_matched   BIGINT NOT NULL DEFAULT 0,
  backfilled_at  TIMESTAMPTZ,                -- 首次历史回补完成时间（NULL=未回补）
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 自营站运营成本（买号/订阅/服务器）。自营站上游实扣不计入成本，改由本表记账。
CREATE TABLE IF NOT EXISTS provider_operating_costs (
  id          BIGSERIAL PRIMARY KEY,
  provider_id BIGINT NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
  category    TEXT NOT NULL DEFAULT 'other',  -- account | subscription | server | other
  amount      DOUBLE PRECISION NOT NULL DEFAULT 0,
  currency    TEXT NOT NULL DEFAULT 'USD',
  occurred_on DATE NOT NULL,                  -- 日历日，不是时刻
  note        TEXT NOT NULL DEFAULT '',
  operator    TEXT NOT NULL DEFAULT '',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_poc_date     ON provider_operating_costs(occurred_on);
CREATE INDEX IF NOT EXISTS idx_poc_provider ON provider_operating_costs(provider_id, occurred_on);

-- ============================================================ 采集与探测

CREATE TABLE IF NOT EXISTS collector_state (
  provider_id          BIGINT NOT NULL,
  task                 TEXT NOT NULL,          -- sync | probe | rate
  last_run_at          TIMESTAMPTZ,
  last_success_at      TIMESTAMPTZ,
  last_error           TEXT,                   -- 已截断的错误摘要
  consecutive_failures BIGINT NOT NULL DEFAULT 0,
  next_eligible_at     TIMESTAMPTZ,            -- 退避解禁时刻，NULL = 不限制
  PRIMARY KEY (provider_id, task)
);

CREATE TABLE IF NOT EXISTS probe_results (
  id           BIGSERIAL PRIMARY KEY,
  provider_id  BIGINT,
  account_id   BIGINT NOT NULL,
  account_name TEXT NOT NULL,
  platform     TEXT NOT NULL,
  model        TEXT NOT NULL,
  base_url     TEXT NOT NULL DEFAULT '',
  source       TEXT NOT NULL DEFAULT 'schedule', -- schedule | manual
  success      BOOLEAN NOT NULL,
  status_code  BIGINT,
  ttft_ms      BIGINT,
  total_ms     BIGINT,
  error        TEXT,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_probe_account_time  ON probe_results(account_id, created_at);
CREATE INDEX IF NOT EXISTS idx_probe_provider_time ON probe_results(provider_id, created_at);
CREATE INDEX IF NOT EXISTS idx_probe_time          ON probe_results(created_at);

CREATE TABLE IF NOT EXISTS probe_budget (
  day  DATE PRIMARY KEY,
  used BIGINT NOT NULL DEFAULT 0
);

-- ============================================================ 健康状态机

CREATE TABLE IF NOT EXISTS health_states (
  account_id            BIGINT PRIMARY KEY,           -- 上游 accounts.id
  account_name          TEXT NOT NULL DEFAULT '',
  provider_id           BIGINT,
  state                 TEXT NOT NULL DEFAULT 'healthy', -- healthy|degraded|suspended|observing|recovering|disabled
  consecutive_failures  BIGINT NOT NULL DEFAULT 0,
  consecutive_successes BIGINT NOT NULL DEFAULT 0,
  weight_percent        BIGINT NOT NULL DEFAULT 100,  -- 降权百分比（degraded/recovering 用）
  cooldown_until        TIMESTAMPTZ,                  -- suspended 冷却截止
  observing_until       TIMESTAMPTZ,                  -- observing 观察窗截止
  last_probe_at         TIMESTAMPTZ,
  updated_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_health_provider ON health_states(provider_id);

CREATE TABLE IF NOT EXISTS health_events (
  id         BIGSERIAL PRIMARY KEY,
  account_id BIGINT NOT NULL,
  from_state TEXT NOT NULL,
  to_state   TEXT NOT NULL,
  reason     TEXT NOT NULL DEFAULT '',   -- hard_failure | soft_failure | probe_success | cooldown_expired | ...
  detail     TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_health_events_account ON health_events(account_id, created_at DESC);

-- ============================================================ 倍率与调价

-- 变更驱动：每行 = 一次真实的倍率状态。倍率没变只延长 last_seen_at，不插新行。
CREATE TABLE IF NOT EXISTS rate_snapshots (
  id            BIGSERIAL PRIMARY KEY,
  scope         TEXT NOT NULL DEFAULT 'local',  -- local | upstream
  provider_id   BIGINT NOT NULL DEFAULT 0,      -- upstream 时指向 providers.id
  entity_type   TEXT NOT NULL,                  -- group | account
  entity_id     TEXT NOT NULL,                  -- local 存数字 id，upstream 存分组名
  name          TEXT NOT NULL,
  rate          DOUBLE PRECISION NOT NULL,
  platform      TEXT NOT NULL DEFAULT '',
  first_seen_at TIMESTAMPTZ NOT NULL,
  last_seen_at  TIMESTAMPTZ NOT NULL,
  deleted       BOOLEAN NOT NULL DEFAULT false
);
CREATE INDEX IF NOT EXISTS idx_rate_snap_entity ON rate_snapshots(scope, provider_id, entity_type, entity_id, first_seen_at DESC);
CREATE INDEX IF NOT EXISTS idx_rate_snap_time   ON rate_snapshots(first_seen_at);

CREATE TABLE IF NOT EXISTS local_group_pricing (
  id                  BIGSERIAL PRIMARY KEY,
  local_group_id      BIGINT NOT NULL UNIQUE,       -- 本站分组 id（以 admin API 口径为准）
  local_group_name    TEXT NOT NULL DEFAULT '',
  auto_enabled        BOOLEAN NOT NULL DEFAULT false,
  price_source        TEXT NOT NULL DEFAULT 'primary', -- primary | lowest | highest | average
  primary_provider_id BIGINT,
  primary_group       TEXT,
  markup_mode         TEXT NOT NULL DEFAULT 'percentage', -- fixed | percentage
  markup_value        DOUBLE PRECISION NOT NULL DEFAULT 10,
  follow_threshold    DOUBLE PRECISION NOT NULL DEFAULT 10, -- 跟随阈值(%)
  min_rate            DOUBLE PRECISION,
  max_rate            DOUBLE PRECISION,
  last_applied_rate   DOUBLE PRECISION,             -- 冲突检测基准：系统最后一次写入的值
  conflict            BOOLEAN NOT NULL DEFAULT false,
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS pricing_sources (
  id             BIGSERIAL PRIMARY KEY,
  pricing_id     BIGINT NOT NULL,
  provider_id    BIGINT NOT NULL,
  upstream_group TEXT NOT NULL,
  UNIQUE (pricing_id, provider_id, upstream_group)
);
CREATE INDEX IF NOT EXISTS idx_pricing_sources_pricing  ON pricing_sources(pricing_id);
CREATE INDEX IF NOT EXISTS idx_pricing_sources_upstream ON pricing_sources(provider_id, upstream_group);

CREATE TABLE IF NOT EXISTS rate_actions (
  id         BIGSERIAL PRIMARY KEY,
  pricing_id BIGINT NOT NULL,
  trigger_by TEXT NOT NULL,              -- auto | manual
  old_rate   DOUBLE PRECISION,
  new_rate   DOUBLE PRECISION NOT NULL,
  status     TEXT NOT NULL,              -- pending | applied | failed | skipped_conflict
  error      TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_rate_actions_pricing ON rate_actions(pricing_id, created_at DESC);

-- ============================================================ 供应商关联与自动建号

-- 供应商 ↔ 本站账号的显式关联（归属唯一真相，取代账号名【】前缀匹配）。
CREATE TABLE IF NOT EXISTS provider_accounts (
  id           BIGSERIAL PRIMARY KEY,
  provider_id  BIGINT NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
  account_id   BIGINT NOT NULL,
  account_name TEXT NOT NULL DEFAULT '',
  note         TEXT NOT NULL DEFAULT '',
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_provider_accounts_account  ON provider_accounts(account_id);
CREATE INDEX        IF NOT EXISTS idx_provider_accounts_provider ON provider_accounts(provider_id);

CREATE TABLE IF NOT EXISTS upstream_connections (
  id                 BIGSERIAL PRIMARY KEY,
  provider_id        BIGINT NOT NULL,               -- 上游站（providers.id）
  upstream_group     TEXT NOT NULL,
  upstream_group_id  BIGINT NOT NULL DEFAULT 0,
  upstream_key_id    BIGINT NOT NULL DEFAULT 0,
  upstream_key_name  TEXT NOT NULL DEFAULT '',
  local_account_id   BIGINT NOT NULL DEFAULT 0,     -- 本站账号 id
  local_account_name TEXT NOT NULL DEFAULT '',
  local_group_ids    TEXT NOT NULL DEFAULT '[]',    -- JSON 数字数组
  group_platform     TEXT NOT NULL DEFAULT '',      -- anthropic/openai/gemini/...
  mode               TEXT NOT NULL DEFAULT 'managed', -- managed | existing
  operation_id       TEXT NOT NULL DEFAULT '',      -- 幂等键，防手动双击重复建号
  status             TEXT NOT NULL DEFAULT 'pending', -- pending | active | failed
  error              TEXT,
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_upstream_conn_active
  ON upstream_connections(provider_id, upstream_group) WHERE status = 'active';
CREATE UNIQUE INDEX IF NOT EXISTS idx_upstream_conn_operation
  ON upstream_connections(operation_id) WHERE operation_id <> '';

-- ============================================================ 授信台账与 KYC

CREATE TABLE IF NOT EXISTS customers (
  id              BIGSERIAL PRIMARY KEY,
  sub2api_user_id TEXT NOT NULL UNIQUE,        -- 客户唯一口径
  display_name    TEXT NOT NULL DEFAULT '',    -- 冗余，供列表检索
  email           TEXT NOT NULL DEFAULT '',
  note            TEXT NOT NULL DEFAULT '',    -- 客户可见备注
  admin_note      TEXT NOT NULL DEFAULT '',    -- 内部备注，不进客户端 DTO
  credit_limit    DOUBLE PRECISION NOT NULL DEFAULT 0, -- 0 表示未授信，不触发告警
  outstanding     DOUBLE PRECISION NOT NULL DEFAULT 0, -- 余额缓存；credit_ledger 才是唯一真相
  status          TEXT NOT NULL DEFAULT 'active', -- active | archived
  alert_level     BIGINT NOT NULL DEFAULT 0,   -- 告警档位：0 | 80 | 100
  alert_at        TIMESTAMPTZ,
  last_entry_at   TIMESTAMPTZ,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_customers_status ON customers(status);

-- 只追加，不改不删。冲正靠 reversed_of 指向原记录。
CREATE TABLE IF NOT EXISTS credit_ledger (
  id           BIGSERIAL PRIMARY KEY,
  customer_id  BIGINT NOT NULL,
  entry_type   TEXT NOT NULL,              -- advance（垫付）| repayment（回款）
  amount       DOUBLE PRECISION NOT NULL,
  currency     TEXT NOT NULL DEFAULT 'USD',
  occurred_at  TIMESTAMPTZ NOT NULL,       -- 业务时间，可补录历史
  note         TEXT NOT NULL DEFAULT '',
  external_ref TEXT NOT NULL DEFAULT '',
  operator     TEXT NOT NULL DEFAULT '',
  reversed_of  BIGINT,                     -- 冲正指向原记录 id
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_ledger_customer ON credit_ledger(customer_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_ledger_occurred ON credit_ledger(occurred_at DESC);

-- _enc 后缀列为 secretbox 密文。未配置 MONITOR_CREDENTIALS_KEY 时明文直通。
CREATE TABLE IF NOT EXISTS customer_kyc (
  customer_id       BIGINT PRIMARY KEY REFERENCES customers(id) ON DELETE CASCADE,
  subject_type      TEXT NOT NULL DEFAULT 'individual', -- individual | company
  status            TEXT NOT NULL DEFAULT 'draft',      -- draft | pending | approved | rejected
  country_region    TEXT NOT NULL DEFAULT '',
  id_type           TEXT NOT NULL DEFAULT '',

  -- 个人主体
  legal_name_enc    TEXT NOT NULL DEFAULT '',
  id_number_enc     TEXT NOT NULL DEFAULT '',
  birth_date_enc    TEXT NOT NULL DEFAULT '',
  address_enc       TEXT NOT NULL DEFAULT '',

  -- 公司主体
  company_name_enc  TEXT NOT NULL DEFAULT '',
  reg_number_enc    TEXT NOT NULL DEFAULT '',
  legal_rep_enc     TEXT NOT NULL DEFAULT '',
  reg_address_enc   TEXT NOT NULL DEFAULT '',
  tax_number_enc    TEXT NOT NULL DEFAULT '',

  -- 联系人（两种主体共用）
  contact_name_enc  TEXT NOT NULL DEFAULT '',
  contact_phone_enc TEXT NOT NULL DEFAULT '',
  contact_email_enc TEXT NOT NULL DEFAULT '',

  -- 收款信息
  bank_name_enc     TEXT NOT NULL DEFAULT '',
  bank_account_enc  TEXT NOT NULL DEFAULT '',
  bank_holder_enc   TEXT NOT NULL DEFAULT '',

  -- 审核轨迹（不加密）
  submitted_at      TIMESTAMPTZ,
  reviewed_at       TIMESTAMPTZ,
  reviewed_by       TEXT NOT NULL DEFAULT '',
  review_note       TEXT NOT NULL DEFAULT '',
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_kyc_status ON customer_kyc(status);

-- ============================================================ 生图 / 生视频

CREATE TABLE IF NOT EXISTS media_tasks (
  id                  BIGSERIAL PRIMARY KEY,
  sub2api_user_id     TEXT   NOT NULL,               -- 来自嵌入会话，不可由请求指定
  api_key_id          BIGINT NOT NULL DEFAULT 0,     -- sub2api api_keys.id
  key_fingerprint     TEXT   NOT NULL DEFAULT '',    -- keyidentity.Fingerprint(key)
  group_id            BIGINT NOT NULL DEFAULT 0,
  task_kind           TEXT   NOT NULL,               -- t2i | i2i | t2v | i2v
  model               TEXT   NOT NULL,
  prompt              TEXT   NOT NULL DEFAULT '',
  params_json         TEXT   NOT NULL DEFAULT '{}',
  status              TEXT   NOT NULL DEFAULT 'pending', -- pending | succeeded | failed
  progress            BIGINT NOT NULL DEFAULT 0,     -- 0-100
  upstream_request_id TEXT   NOT NULL DEFAULT '',
  result_url          TEXT   NOT NULL DEFAULT '',
  -- BIGINT 不是 INTEGER：1 tick = 1e-10 USD，32 位溢出是必然且静默的。
  cost_ticks          BIGINT NOT NULL DEFAULT 0,     -- 上游实扣
  est_cost_ticks      BIGINT NOT NULL DEFAULT 0,     -- 提交前预估
  error_message       TEXT   NOT NULL DEFAULT '',    -- 已过 redactError
  client_request_id   TEXT   NOT NULL,               -- 幂等键
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_media_tasks_idem
  ON media_tasks(sub2api_user_id, client_request_id);
CREATE INDEX IF NOT EXISTS idx_media_tasks_pending
  ON media_tasks(status) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_media_tasks_user_created
  ON media_tasks(sub2api_user_id, created_at DESC);

-- ============================================================ 系统设置

CREATE TABLE IF NOT EXISTS settings (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL,               -- JSON 字符串
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
