-- 授信客户线上余额缓存与提前告警闩锁。
-- 只读扫描 users.balance，不写入游、不进入台账敞口。
ALTER TABLE customers ADD COLUMN IF NOT EXISTS low_balance_threshold DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE customers ADD COLUMN IF NOT EXISTS user_balance DOUBLE PRECISION;
ALTER TABLE customers ADD COLUMN IF NOT EXISTS user_balance_at TIMESTAMPTZ;
ALTER TABLE customers ADD COLUMN IF NOT EXISTS user_balance_alert_level BIGINT NOT NULL DEFAULT 0;
ALTER TABLE customers ADD COLUMN IF NOT EXISTS user_balance_alert_at TIMESTAMPTZ;
