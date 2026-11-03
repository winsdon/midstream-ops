-- 每个供应商的 new-api 成本统计限流覆盖。
-- 0 表示沿用系统默认值，避免影响已有供应商。
ALTER TABLE providers
  ADD COLUMN IF NOT EXISTS cost_max_concurrency BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS cost_interval_ms BIGINT NOT NULL DEFAULT 0;
