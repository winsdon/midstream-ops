-- 管理员 refresh token（只存 SHA-256，明文只在登录/续期响应里出现一次）。
-- 管理员来自 config.yaml，没有 users 表，故不建 username 外键。
CREATE TABLE IF NOT EXISTS refresh_tokens (
  token_hash   TEXT PRIMARY KEY,
  username     TEXT NOT NULL,
  password_fp  TEXT NOT NULL,
  expires_at   TIMESTAMPTZ NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_expires ON refresh_tokens(expires_at);
