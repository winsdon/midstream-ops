-- 供应商 ↔ 本站账号 的显式关联表（归属的唯一真相）
--
-- 取代原先「账号名【】前缀即供应商」的动态匹配。前缀匹配的问题：
--   1. 账号改名即静默换归属，收益统计会无声漂移，系统无从告警；
--   2. 无法表达「名字里没有前缀但确实属于该站」；
--   3. 【】是一种个人命名习惯，不是通用规则，换个人用就失效。
-- 前缀规则本身保留，但降级为「猜建议名」（见 service/provider_name.go）。
--
-- account_id 是远端 PG accounts.id，无法建外键 —— providers 在本地 SQLite，
-- accounts 在远端只读 PG。这与 probe_results / upstream_key_costs / health_states
-- 等表的 account_id 处理一致，均为裸 INTEGER。悬垂 id（远端账号已删）在读取时
-- 按需过滤，不做级联清理：留着便于排查「当初关联的是哪个账号」。
--
-- account_name 冗余存储：用于列表展示与事后排查，且 PG 不可用时供应商列表
-- 仍能显示已关联的账号名（降级可用性）。它是关联时的快照而非真相，
-- 远端改名后不会自动更新，展示时以 PG 现值优先。
--
-- 【不用 CHECK 约束】与全库既有 12 个迁移保持一致，校验统一在 Go 侧做，
-- 这样非法输入能返回可读的 400 而不是驱动层抛出的不透明 500。

CREATE TABLE IF NOT EXISTS provider_accounts (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  provider_id  INTEGER NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
  account_id   INTEGER NOT NULL,
  account_name TEXT NOT NULL DEFAULT '',
  note         TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

-- 一个账号只能归属一个供应商：落进两个桶会让收益统计的合计翻倍。
-- 也是 ProviderIDOf 反查（探测热路径）的索引。
CREATE UNIQUE INDEX IF NOT EXISTS idx_provider_accounts_account
  ON provider_accounts(account_id);

-- 按供应商查子账号与计数
CREATE INDEX IF NOT EXISTS idx_provider_accounts_provider
  ON provider_accounts(provider_id);
