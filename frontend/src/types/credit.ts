/**
 * 授信台账类型定义。字段与后端 handler.customerDTO / ledgerEntryDTO /
 * repository.CreditSummary 的 JSON tag 严格对齐。
 *
 * 金额一律 USD；时间一律 RFC3339 UTC 字符串，展示前经 fmtDateTime 转本地时区。
 *
 * 【本模块永不写上游】敞口来自本地台账（Σ垫付 − Σ回款），充值由人在 sub2api
 * 后台手动执行，前端没有也不应有任何触发上游写操作的入口。
 */

/** 分录方向：垫付增加敞口，回款减少敞口。 */
export type LedgerEntryType = 'advance' | 'repayment'

/** 客户状态。归档为软删除，不物理删除，台账记录保留。 */
export type CustomerStatus = 'active' | 'archived'

/** 告警档位闩锁：0 未触发 / 80 已达八成 / 100 已超额。 */
export type CreditAlertLevel = 0 | 80 | 100

export interface CreditCustomer {
  id: number
  sub2api_user_id: string
  display_name: string
  email: string
  /** 客户可见备注 */
  note: string
  /** 仅管理端可见，绝不进客户侧接口 */
  admin_note: string
  credit_limit: number
  /** 敞口 = Σ垫付 − Σ回款，可为负（表示客户预付） */
  outstanding: number
  /** 可用额度 = credit_limit − outstanding；未授信（limit ≤ 0）恒为 0 */
  available: number
  /** 敞口占额度的比例，取值 0~1 的小数而非百分数；未授信恒为 0 */
  usage_ratio: number
  status: CustomerStatus
  alert_level: CreditAlertLevel
  alert_at: string | null
  /** 最后一次记账时间，null 表示从未记过账 */
  last_entry_at: string | null
  created_at: string
  updated_at: string
}

export interface CreditLedgerEntry {
  id: number
  customer_id: number
  entry_type: LedgerEntryType
  /** 恒为正数，方向由 entry_type 决定 */
  amount: number
  currency: string
  /** 业务发生时间，可补录历史，与 created_at 不同 */
  occurred_at: string
  note: string
  external_ref: string
  operator: string
  /** 非 null 表示这是一笔冲正分录，指向被冲正的原分录 */
  reversed_of: number | null
  created_at: string
}

/** 授信总览（仅统计 active 客户）。 */
export interface CreditSummary {
  customer_count: number
  /** 已授信客户数（credit_limit > 0） */
  granted_count: number
  total_limit: number
  /** 敞口合计，即全部应收账 */
  total_outstanding: number
  over_limit_count: number
  /** 已达 80% 但未超额的客户数 */
  warning_count: number
}

/** 新建 / 编辑客户请求体。 */
export interface CustomerPayload {
  sub2api_user_id: string
  display_name: string
  email: string
  note: string
  admin_note: string
  credit_limit: number
  status: CustomerStatus
}

/**
 * 建档下拉的 sub2api 用户选项（读线上 users 表）。
 *
 * id 是字符串而非数字：customers.sub2api_user_id 是 TEXT 列，后端已在
 * DTO 层字符串化，前端直接提交即可，无需再转换。
 */
export interface Sub2apiUserOption {
  id: string
  email: string
  role: string
  /** 线上余额，仅作定授信额度时的参考 */
  balance: number
  status: string
  created_at: string
  /** 已建档：下拉里禁选，避免重复建档撞 UNIQUE */
  enrolled: boolean
}

/** 记一笔台账请求体。 */
export interface EntryPayload {
  entry_type: LedgerEntryType
  amount: number
  /** RFC3339；空字符串表示「现在」，由后端取当前时间 */
  occurred_at: string
  note: string
  external_ref: string
}

/* ---------- KYC ---------- */

/** KYC 主体类型。个人与公司的必填字段集合完全不同。 */
export type KycSubjectType = 'individual' | 'company'

/**
 * KYC 审核状态。
 *
 * draft → pending 由「提交送审」触发；pending → approved | rejected 由审核触发。
 * rejected 后允许重新编辑再提交，故 rejected → pending 亦合法。
 */
export type KycStatus = 'draft' | 'pending' | 'approved' | 'rejected'

/**
 * KYC 资料。
 *
 * 【含完整 PII】后端 _enc 列加密落库，此处一律是解密后的明文。
 * 除表单绑定外不要写进 localStorage、URL 或日志。
 */
export interface KycProfile {
  customer_id: number
  subject_type: KycSubjectType
  status: KycStatus
  country_region: string
  id_type: string

  /* 个人主体 */
  legal_name: string
  id_number: string
  birth_date: string
  address: string

  /* 公司主体 */
  company_name: string
  reg_number: string
  legal_rep: string
  reg_address: string
  tax_number: string

  /* 联系人（两种主体共用） */
  contact_name: string
  contact_phone: string
  contact_email: string

  /* 收付款信息 */
  bank_name: string
  bank_account: string
  bank_holder: string

  /* 审核轨迹。null 表示尚未发生过该动作 */
  submitted_at: string | null
  reviewed_at: string | null
  /** 内部审核人用户名，客户侧界面须裁剪掉 */
  reviewed_by: string
  review_note: string
  updated_at: string
}

/** KYC 表单可编辑字段（即 KycProfile 去掉状态与审核轨迹）。 */
export type KycFormData = Pick<
  KycProfile,
  | 'subject_type'
  | 'country_region'
  | 'id_type'
  | 'legal_name'
  | 'id_number'
  | 'birth_date'
  | 'address'
  | 'company_name'
  | 'reg_number'
  | 'legal_rep'
  | 'reg_address'
  | 'tax_number'
  | 'contact_name'
  | 'contact_phone'
  | 'contact_email'
  | 'bank_name'
  | 'bank_account'
  | 'bank_holder'
>

/**
 * 客户自助端看到的 KYC 档案，与后端 handler.embedKycDTO 对齐。
 *
 * 用 Omit 裁剪而非独立枚举字段：管理端 KycProfile 新增字段时，
 * 客户侧默认继承 —— 若那个字段是内部信息，TS 不会提醒。故此处的
 * 裁剪清单要与后端 embedKycDTO 一起改，两边都是显式的「不给客户看什么」。
 *
 * reviewed_by（内部审核人用户名）必须裁掉；review_note 保留 ——
 * 驳回理由不给客户看，客户无从修正。
 */
export type CustomerKycProfile = Omit<KycProfile, 'customer_id' | 'reviewed_by' | 'updated_at'>

/**
 * KYC 保存请求体。
 *
 * 不含 status —— 状态迁移只由 submit 开关与审核接口驱动，
 * 客户端无法把自己直接改成 approved。
 */
export interface KycPayload extends KycFormData {
  /** true = 提交送审（置 pending 并做必填校验）；false = 存草稿 */
  submit: boolean
}

/** 审核请求体。驳回时 note 必填，否则客户无从修正。 */
export interface KycReviewPayload {
  status: Extract<KycStatus, 'approved' | 'rejected'>
  note: string
}
