-- P2：多平台支持（sub2api / new-api）+ 多认证模式 + 凭据加密准备

-- 平台与认证模式
ALTER TABLE providers ADD COLUMN platform TEXT NOT NULL DEFAULT 'sub2api';   -- sub2api | new-api
ALTER TABLE providers ADD COLUMN auth_mode TEXT NOT NULL DEFAULT 'password'; -- password | token | user_key

-- token 模式（sub2api）：直填 Access/Refresh Token
ALTER TABLE providers ADD COLUMN refresh_token TEXT NOT NULL DEFAULT '';

-- new-api 会话与换算
ALTER TABLE providers ADD COLUMN session_cookie TEXT NOT NULL DEFAULT '';    -- password 登录产物
ALTER TABLE providers ADD COLUMN upstream_user_id TEXT NOT NULL DEFAULT ''; -- New-Api-User 头
ALTER TABLE providers ADD COLUMN quota_per_unit REAL NOT NULL DEFAULT 500000; -- quota → USD 换算
