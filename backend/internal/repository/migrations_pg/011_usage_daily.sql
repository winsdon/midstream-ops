-- ============================================================ 收入日结汇总
-- sub2api 的 usage_logs 只保留约 30 天，统计若一直实时聚合明细，过期后历史就归零。
-- 每天结算前一天落到本表（永久保留，不参与 cleanup）；未结算的日子（通常只有今天）
-- 仍实时查 usage_logs，两段由 StatsService 按 usage_rollup_state 的分界拼接。
--
-- 粒度 = 日 × 用户 × 分组 × 账号：按账号 / 分组×账号 / 用户×分组×账号 / 按日 / 活跃用户
-- 这几种读口径都能从这一张表上卷出来。名称与倍率是结算时的快照。
CREATE TABLE IF NOT EXISTS usage_daily (
  usage_date    DATE   NOT NULL,                       -- 站点时区（cfg.Location）
  user_id       BIGINT NOT NULL DEFAULT 0,             -- 0 = 无用户
  group_id      BIGINT NOT NULL DEFAULT 0,             -- 0 = 无分组
  account_id    BIGINT NOT NULL,
  user_name     TEXT   NOT NULL DEFAULT '',
  group_name    TEXT   NOT NULL DEFAULT '',
  group_rate    DOUBLE PRECISION NOT NULL DEFAULT 1,
  account_name  TEXT   NOT NULL DEFAULT '',
  requests      BIGINT NOT NULL,
  revenue       DOUBLE PRECISION NOT NULL,             -- Σ actual_cost（用户实扣 = 收入）
  cost_weight   DOUBLE PRECISION NOT NULL,             -- Σ total_cost 裸值，成本分摊权重
  official_cost DOUBLE PRECISION NOT NULL,             -- Σ total_cost × account_rate_multiplier（官价对照）
  settled_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (usage_date, user_id, group_id, account_id)
);
CREATE INDEX IF NOT EXISTS idx_usage_daily_account ON usage_daily(account_id, usage_date);

-- 单行表：已结算到哪一天（含）。NULL/无行 = 从未结算，读路径全部走实时。
CREATE TABLE IF NOT EXISTS usage_rollup_state (
  id              SMALLINT PRIMARY KEY,
  settled_through DATE,
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 上游成本「已终结到哪一天」：跨零点与停机期间的缺口按此用逐日接口补齐。
ALTER TABLE cost_sync_state ADD COLUMN IF NOT EXISTS finalized_through DATE;

-- ============================================================ 下线调价映射
DROP TABLE IF EXISTS rate_actions;
DROP TABLE IF EXISTS pricing_sources;
DROP TABLE IF EXISTS local_group_pricing;
