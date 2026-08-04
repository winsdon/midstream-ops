-- 自营站标记 + 运营成本台账
--
-- 自营站 = 用户自己经营的上游站点。其上游实扣（upstream_key_costs.actual_cost）
-- 是左手倒右手，不是真实支出，故从所有成本聚合中剔除。真实支出改由本表手工录入：
-- 买账号、订阅费、服务器等花在站外的钱。
--
-- 【剔除在查询时判定，历史数据不动】CostByAccount / CostByDay / CostByProvider
-- JOIN providers 后用 CASE WHEN self_operated=1 THEN 0 置零。这样标记一打开
-- 立即对所有历史区间生效，取消标记也立即回滚，无需回刷数据。
--
-- 【为什么置 0 而不是 WHERE 过滤掉行】过滤会让账号从聚合结果 map 里消失，
-- StatsService 随即把它判成 CostMatched=false，触发前端「成本不完整、利润被
-- 高估 ⚠」告警。自营站成本为 0 是有意为之，不是数据缺失，绝不能误报。
--
-- 【为什么不复用 role 列】role='self' 是 __self__ 单例（本站调价连接），
-- 且 List / ListCollectable / ListProbeEnabled 均 WHERE role='upstream'，
-- 加第三个 role 值会让自营站从供应商列表里直接消失。
--
-- 【occurred_on 是日历日，不是时刻】与 upstream_key_costs.usage_date 同口径
-- （YYYY-MM-DD，本地时区），两者才能落在同一时间轴上相加。刻意不存时刻：
-- 「7月3日买了个号」本就没有精确到秒的意义，存时刻反而引入时区换算风险。
--
-- 【不做跨期摊销】买一个月的号全额计入购买当天。摊销需要起止日 + 按日展开，
-- 复杂度远高于收益 —— 月维度总账不受影响，日维度尖刺可由使用者自行解读。
--
-- 【运营成本不摊到分组】买号 / 服务器不属于任何分组，按用量强行分摊是虚假精度；
-- 且自营站当期零用量却有运营成本时，没有任何分组能承载它，成本会凭空消失。
-- 代价是「分组合计 ≡ 供应商合计」不再成立，前端已加口径说明。
--
-- 【不用 CHECK 约束】与全库既有 13 个迁移保持一致，枚举与金额校验统一在 Go 侧
-- service 的单一入口做，这样非法输入能返回可读的 400 而不是驱动层的不透明 500。

ALTER TABLE providers ADD COLUMN self_operated INTEGER NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS provider_operating_costs (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  provider_id INTEGER NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
  category    TEXT NOT NULL DEFAULT 'other',  -- account | subscription | server | other
  amount      REAL NOT NULL DEFAULT 0,        -- 恒为正，归一到分
  currency    TEXT NOT NULL DEFAULT 'USD',    -- 与上游实扣同币种，才能直接相加
  occurred_on TEXT NOT NULL,                  -- YYYY-MM-DD（本地时区日历日）
  note        TEXT NOT NULL DEFAULT '',
  operator    TEXT NOT NULL DEFAULT '',       -- 记账人
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

-- 按站点查明细（弹窗）与按站点聚合（统计）
CREATE INDEX IF NOT EXISTS idx_poc_provider ON provider_operating_costs(provider_id, occurred_on);
-- 按日聚合（趋势图）
CREATE INDEX IF NOT EXISTS idx_poc_date     ON provider_operating_costs(occurred_on);
