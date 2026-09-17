CREATE TABLE IF NOT EXISTS model_detection_baselines (
  id BIGSERIAL PRIMARY KEY,
  owner TEXT NOT NULL,
  account_id BIGINT,
  target_fp TEXT NOT NULL,
  target_name TEXT NOT NULL DEFAULT '',
  base_url TEXT NOT NULL,
  model TEXT NOT NULL,
  template_version TEXT NOT NULL DEFAULT 'ccmax-v1',
  status TEXT NOT NULL,
  quality_ok BOOLEAN NOT NULL DEFAULT FALSE,
  input_tokens INT NOT NULL DEFAULT 0,
  output_tokens INT NOT NULL DEFAULT 0,
  thinking_tokens INT NOT NULL DEFAULT 0,
  thinking_chars INT NOT NULL DEFAULT 0,
  ttft_ms BIGINT NOT NULL DEFAULT 0,
  duration_ms BIGINT NOT NULL DEFAULT 0,
  response_summary TEXT NOT NULL DEFAULT '',
  report JSONB NOT NULL DEFAULT '{}'::jsonb,
  error TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_model_detection_baselines_owner ON model_detection_baselines(owner);
