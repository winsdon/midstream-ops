-- 模型检测（渠道指纹）结果留痕。
--
-- 只存脱敏后的报告：api_key 明文既不入库也不进 report，目标同一性靠 target_fp
-- （base_url + key 的哈希前缀）串起来，这样手填的临时目标也能对比历史漂移。
-- 号池会轮换账号，同一把 key 两次结果可能不同，历史对照是判稳的必要手段。
CREATE TABLE IF NOT EXISTS model_detections (
  id                 BIGSERIAL PRIMARY KEY,
  account_id         BIGINT,
  account_name       TEXT        NOT NULL DEFAULT '',
  provider_id        BIGINT,
  target_fp          TEXT        NOT NULL,
  target_name        TEXT        NOT NULL DEFAULT '',
  base_url           TEXT        NOT NULL,
  model              TEXT        NOT NULL,
  label              TEXT        NOT NULL,
  confidence         TEXT        NOT NULL,
  authenticity_score INT         NOT NULL DEFAULT 0,
  authenticity_grade TEXT        NOT NULL DEFAULT '',
  scores             JSONB       NOT NULL DEFAULT '{}'::jsonb,
  report             JSONB       NOT NULL DEFAULT '{}'::jsonb,
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_model_detections_target ON model_detections(target_fp, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_model_detections_account ON model_detections(account_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_model_detections_created ON model_detections(created_at DESC);
