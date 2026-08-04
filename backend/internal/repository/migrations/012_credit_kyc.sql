-- 授信额度 + KYC 台账
--
-- 业务语义：运营方替客户「垫付」——客户尚未付款，运营方先在 sub2api 后台给他充值，
-- 事后再收款。授信额度封顶的是「未回款的垫付余额（应收账）」，是循环授信，
-- 不是「本月用了多少钱」的周期用量：
--
--     已用额度 = Σ垫付 − Σ回款
--     可用额度 = 授信额度 − 已用额度
--
-- 因此已用额度完全由本地台账算出，不依赖线上 PG 的 usage_logs 按用户聚合
-- （那张表根本没有 user_id 列）。
--
-- 【本模块永不写上游】系统只记账、审批、监控，充值动作由人在 sub2api 后台手动执行。
-- 不要试图「顺手」调用 sub2api 的充值接口：sub2api 开启了会话绑定
-- （JWT 的 bnd claim = sha256(客户端IP + UA)），服务端直连的 IP/UA 与浏览器必然不同 →
-- 校验失败，且 sub2api 在指纹不匹配时会撤销该用户整个会话家族，把人从浏览器踢下线。
--
-- 【不用 CHECK 约束】全库既有迁移无一使用 CHECK，此处不引入新惯例。
-- 枚举与金额校验统一在 Go 侧 credit_service.go 的单一入口做，
-- 这样非法输入能返回可读的 400，而不是驱动层抛出的不透明 500。

-- 客户：一个客户 = 一个 sub2api user_id
CREATE TABLE IF NOT EXISTS customers (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  sub2api_user_id TEXT NOT NULL UNIQUE,       -- 客户唯一口径
  display_name    TEXT NOT NULL DEFAULT '',   -- 明文：上游本就明文，且是唯一可搜索的键
  email           TEXT NOT NULL DEFAULT '',   -- 同上
  note            TEXT NOT NULL DEFAULT '',   -- 客户可见备注
  admin_note      TEXT NOT NULL DEFAULT '',   -- 仅管理端，永不进客户侧 DTO
  credit_limit    REAL NOT NULL DEFAULT 0,    -- ≤0 表示未授信，不参与告警
  outstanding     REAL NOT NULL DEFAULT 0,    -- 冗余缓存；credit_ledger 才是唯一真相
  status          TEXT NOT NULL DEFAULT 'active', -- active | archived
  alert_level     INTEGER NOT NULL DEFAULT 0, -- 告警闩锁档位：0 | 80 | 100
  alert_at        TEXT,                       -- 上次升档告警时刻
  last_entry_at   TEXT,                       -- 最后一次记账时刻（列表高亮「距今 N 天」用）
  created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_customers_status ON customers(status);

-- 授信台账：只追加，不改不删
--
-- amount 恒为正数，方向由 entry_type 决定（advance 增加敞口，repayment 减少敞口）。
-- 记错了走「冲正」：写一笔反向分录并用 reversed_of 指向原分录，保留完整审计轨迹。
CREATE TABLE IF NOT EXISTS credit_ledger (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  customer_id  INTEGER NOT NULL,
  entry_type   TEXT NOT NULL,              -- advance（垫付）| repayment（回款）
  amount       REAL NOT NULL,              -- 恒为正
  currency     TEXT NOT NULL DEFAULT 'USD',
  occurred_at  TEXT NOT NULL,              -- 业务时间，可补录历史
  note         TEXT NOT NULL DEFAULT '',
  external_ref TEXT NOT NULL DEFAULT '',   -- 对账键，明文（需可搜索）
  operator     TEXT NOT NULL DEFAULT '',   -- 记账人
  reversed_of  INTEGER,                    -- 冲正指向原分录 id
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_ledger_customer ON credit_ledger(customer_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_ledger_occurred ON credit_ledger(occurred_at DESC);

-- KYC 资料（与 customers 1:1）
--
-- 【_enc 后缀列绝不可进 WHERE / ORDER BY / UNIQUE / INDEX】
-- secretbox.Seal 每次用随机 nonce，同一明文两次加密结果不同，相等比较必然失败。
-- 后缀命名让下面这条自检可执行：
--     rg '_enc' backend/ | rg -i 'where|order by|index'
-- 该命令应当无输出。需要搜索的字段（email、external_ref）一律留明文。
--
-- 读取一律用 secretbox.Open() 而非 MustOpen()：MustOpen 解密失败时静默返回空串，
-- 对可重新配置的凭据尚可接受，对身份证号这类不可再生的 PII 不可接受。
CREATE TABLE IF NOT EXISTS customer_kyc (
  customer_id      INTEGER PRIMARY KEY REFERENCES customers(id) ON DELETE CASCADE,
  subject_type     TEXT NOT NULL DEFAULT 'individual', -- individual | company
  status           TEXT NOT NULL DEFAULT 'draft',      -- draft | pending | approved | rejected
  country_region   TEXT NOT NULL DEFAULT '',
  id_type          TEXT NOT NULL DEFAULT '',

  -- 个人主体
  legal_name_enc   TEXT NOT NULL DEFAULT '',
  id_number_enc    TEXT NOT NULL DEFAULT '',
  birth_date_enc   TEXT NOT NULL DEFAULT '',
  address_enc      TEXT NOT NULL DEFAULT '',

  -- 公司主体
  company_name_enc TEXT NOT NULL DEFAULT '',
  reg_number_enc   TEXT NOT NULL DEFAULT '',
  legal_rep_enc    TEXT NOT NULL DEFAULT '',
  reg_address_enc  TEXT NOT NULL DEFAULT '',
  tax_number_enc   TEXT NOT NULL DEFAULT '',

  -- 联系人（两种主体共用）
  contact_name_enc  TEXT NOT NULL DEFAULT '',
  contact_phone_enc TEXT NOT NULL DEFAULT '',
  contact_email_enc TEXT NOT NULL DEFAULT '',

  -- 收付款信息
  bank_name_enc    TEXT NOT NULL DEFAULT '',
  bank_account_enc TEXT NOT NULL DEFAULT '',
  bank_holder_enc  TEXT NOT NULL DEFAULT '',

  -- 审核轨迹（明文）
  submitted_at     TEXT,
  reviewed_at      TEXT,
  reviewed_by      TEXT NOT NULL DEFAULT '',
  review_note      TEXT NOT NULL DEFAULT '',
  created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_kyc_status ON customer_kyc(status);
