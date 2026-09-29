-- 智商测试（鹈鹕 SVG 动画 / 糖果题）留痕。
--
-- 与 model_detections 分表：智商题只看答题，不出渠道分类与真实性评分，那几列对它没有意义，
-- 混进去还会污染真伪历史（以及按账号取最近一次判定）。
-- 同样只存脱敏报告：api_key 明文不入库，目标同一性靠 target_fp 串联。
-- results 是每道题的 {id,title,status,summary}，列表页直接用；report 含作品与回复全文，只在详情里取。
CREATE TABLE IF NOT EXISTS model_iq_runs (
  id           BIGSERIAL PRIMARY KEY,
  account_id   BIGINT,
  account_name TEXT        NOT NULL DEFAULT '',
  provider_id  BIGINT,
  target_fp    TEXT        NOT NULL,
  target_name  TEXT        NOT NULL DEFAULT '',
  base_url     TEXT        NOT NULL,
  model        TEXT        NOT NULL,
  passed       INT         NOT NULL DEFAULT 0,
  total        INT         NOT NULL DEFAULT 0,
  results      JSONB       NOT NULL DEFAULT '[]'::jsonb,
  report       JSONB       NOT NULL DEFAULT '{}'::jsonb,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_model_iq_runs_target ON model_iq_runs(target_fp, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_model_iq_runs_created ON model_iq_runs(created_at DESC);
