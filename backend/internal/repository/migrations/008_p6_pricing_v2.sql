-- P6：调价模型升级 —— 一对一映射 → 多上游聚合
--
-- 旧模型：一条 rate_mappings = 一个上游分组 → 一个本站分组（factor/offset）
-- 新模型：以「本站分组」为主体，可参考多个上游数据源，
--         参考价按 指定主上游/最低/最高/平均 聚合后再加价。

-- 主表：一个本站分组一条规则
CREATE TABLE IF NOT EXISTS local_group_pricing (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  local_group_id      INTEGER NOT NULL UNIQUE,      -- 本站分组 id（以 admin API 口径为准）
  local_group_name    TEXT NOT NULL DEFAULT '',
  auto_enabled        INTEGER NOT NULL DEFAULT 0,
  -- 参考价来源：primary(指定主上游) | lowest | highest | average
  price_source        TEXT NOT NULL DEFAULT 'primary',
  primary_provider_id INTEGER,                      -- price_source='primary' 时必填
  primary_group       TEXT,
  -- 加价方式：fixed(参考价+markup) | percentage(参考价×(1+markup/100))
  markup_mode         TEXT NOT NULL DEFAULT 'percentage',
  markup_value        REAL NOT NULL DEFAULT 10,
  -- 跟随阈值(%)：上游变化幅度 ≤ 阈值才自动跟随；超过则不动，留给人工确认
  follow_threshold    REAL NOT NULL DEFAULT 10,
  min_rate            REAL,
  max_rate            REAL,
  last_applied_rate   REAL,                         -- 冲突检测基准：系统最后一次写入的值
  conflict            INTEGER NOT NULL DEFAULT 0,
  created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

-- 子表：该规则参考哪些上游分组
CREATE TABLE IF NOT EXISTS pricing_sources (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  pricing_id     INTEGER NOT NULL,
  provider_id    INTEGER NOT NULL,
  upstream_group TEXT NOT NULL,
  UNIQUE (pricing_id, provider_id, upstream_group)
);
CREATE INDEX IF NOT EXISTS idx_pricing_sources_pricing ON pricing_sources(pricing_id);
CREATE INDEX IF NOT EXISTS idx_pricing_sources_upstream ON pricing_sources(provider_id, upstream_group);

-- 迁移旧数据：每条 rate_mappings → 一条 pricing（primary 来源 + fixed 加价）+ 一条 source
-- 旧公式 target = up × factor + offset；新模型 fixed 模式为 target = ref + markup。
-- factor=1 时两者等价（markup=offset）；factor≠1 时无法无损表达，
-- 按「以当前上游倍率为基准的等效加价」换算，并在下一次预览时由用户确认。
INSERT INTO local_group_pricing
  (local_group_id, local_group_name, auto_enabled, price_source, primary_provider_id, primary_group,
   markup_mode, markup_value, follow_threshold, min_rate, max_rate, last_applied_rate, conflict, created_at, updated_at)
SELECT
  m.local_group_id,
  m.local_group_name,
  m.auto_enabled,
  'primary',
  m.provider_id,
  m.upstream_group,
  CASE WHEN m.factor = 1 THEN 'fixed' ELSE 'percentage' END,
  CASE WHEN m.factor = 1 THEN m.offset ELSE (m.factor - 1) * 100 END,
  10,
  m.min_rate,
  m.max_rate,
  m.last_applied_rate,
  m.conflict,
  m.created_at,
  m.updated_at
FROM rate_mappings m
WHERE NOT EXISTS (SELECT 1 FROM local_group_pricing p WHERE p.local_group_id = m.local_group_id);

INSERT OR IGNORE INTO pricing_sources (pricing_id, provider_id, upstream_group)
SELECT p.id, m.provider_id, m.upstream_group
FROM rate_mappings m
JOIN local_group_pricing p ON p.local_group_id = m.local_group_id;

-- 审计表改挂 pricing_id（保留历史行，mapping_id 语义转为 pricing_id）
ALTER TABLE rate_actions RENAME TO rate_actions_old;
CREATE TABLE IF NOT EXISTS rate_actions (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  pricing_id  INTEGER NOT NULL,
  trigger_by  TEXT NOT NULL,                        -- auto | manual
  old_rate    REAL,
  new_rate    REAL NOT NULL,
  status      TEXT NOT NULL,                        -- pending | applied | failed | skipped_conflict
  error       TEXT,
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_rate_actions_pricing ON rate_actions(pricing_id, created_at DESC);

-- 旧审计按 mapping → local_group 找到新 pricing_id
INSERT INTO rate_actions (pricing_id, trigger_by, old_rate, new_rate, status, error, created_at)
SELECT p.id, a.trigger_by, a.old_rate, a.new_rate, a.status, a.error, a.created_at
FROM rate_actions_old a
JOIN rate_mappings m ON m.id = a.mapping_id
JOIN local_group_pricing p ON p.local_group_id = m.local_group_id;

DROP TABLE rate_actions_old;
DROP TABLE rate_mappings;
