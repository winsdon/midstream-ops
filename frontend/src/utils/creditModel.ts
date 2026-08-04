/**
 * KYC 表单的字段元数据与纯函数（不依赖 Vue，便于复用与单测）。
 *
 * 为什么用元数据数组而不是把 18 个字段写死在模板里：
 * 管理端（KycDialog）与客户自助嵌入页要渲染同一套表单，两份模板必然漂移 ——
 * 加一个字段就得记得改两处，忘了就是「客户填了但管理端看不到」。
 * 这里是唯一真相，KycForm.vue 用 v-for 渲染，两端共用。
 *
 * 【字段命名必须与后端 handler.kycDTO 的 JSON tag 完全一致】
 * 表单直接按 key 读写 KycFormData，拼错不会报错，只会静默丢字段。
 */

import type { KycFormData, KycStatus, KycSubjectType } from '@/types/credit'

/**
 * 可用 v-for 渲染的字段键名。
 *
 * 刻意排除 subject_type：它不是文本输入，而是驱动整张表可见性的开关，
 * 有自己的 segmented 控件。排除掉之后此类型的值恒为 string，
 * 表单赋值不必再做类型收窄。
 */
export type KycFieldKey = keyof Omit<KycFormData, 'subject_type'>

/** 输入控件形态。textarea 给地址这类长文本，date 给出生日期。 */
export type KycFieldInput = 'text' | 'textarea' | 'date'

export interface KycField {
  key: KycFieldKey
  /** i18n 键，实际文案在 credit.kyc.fields.* */
  labelKey: string
  input: KycFieldInput
  /**
   * 该字段属于哪种主体：
   * - 'both'       两种主体都要填（联系人、银行、国家）
   * - 'individual' 仅个人
   * - 'company'    仅公司
   *
   * 切换主体类型时另一侧的字段不清空 —— 填错类型改回来还在，
   * 提交时后端只校验当前类型的必填项，多余字段无害。
   */
  scope: 'both' | KycSubjectType
  /** 送审时必填。与后端 service/credit_kyc.go 的 requiredKYCFields 保持一致 */
  required?: boolean
}

export interface KycSection {
  /** i18n 键，文案在 credit.kyc.sections.* */
  titleKey: string
  /** 整段仅在该主体类型下显示；'both' 表示恒显示 */
  scope: 'both' | KycSubjectType
  fields: KycField[]
}

/**
 * 分组后的字段清单。
 *
 * required 标记必须与后端 requiredKYCFields() 逐项对齐 ——
 * 前端少标一个，用户点提交才被后端打回；多标一个，用户被拦在本地填不了草稿。
 */
export const KYC_SECTIONS: readonly KycSection[] = [
  {
    titleKey: 'credit.kyc.sections.basic',
    scope: 'both',
    fields: [{ key: 'country_region', labelKey: 'credit.kyc.fields.countryRegion', input: 'text', scope: 'both', required: true }]
  },
  {
    titleKey: 'credit.kyc.sections.individual',
    scope: 'individual',
    fields: [
      { key: 'legal_name', labelKey: 'credit.kyc.fields.legalName', input: 'text', scope: 'individual', required: true },
      { key: 'id_type', labelKey: 'credit.kyc.fields.idType', input: 'text', scope: 'individual', required: true },
      { key: 'id_number', labelKey: 'credit.kyc.fields.idNumber', input: 'text', scope: 'individual', required: true },
      { key: 'birth_date', labelKey: 'credit.kyc.fields.birthDate', input: 'date', scope: 'individual' },
      { key: 'address', labelKey: 'credit.kyc.fields.address', input: 'textarea', scope: 'individual' }
    ]
  },
  {
    titleKey: 'credit.kyc.sections.company',
    scope: 'company',
    fields: [
      { key: 'company_name', labelKey: 'credit.kyc.fields.companyName', input: 'text', scope: 'company', required: true },
      { key: 'reg_number', labelKey: 'credit.kyc.fields.regNumber', input: 'text', scope: 'company', required: true },
      { key: 'legal_rep', labelKey: 'credit.kyc.fields.legalRep', input: 'text', scope: 'company', required: true },
      { key: 'reg_address', labelKey: 'credit.kyc.fields.regAddress', input: 'textarea', scope: 'company', required: true },
      { key: 'tax_number', labelKey: 'credit.kyc.fields.taxNumber', input: 'text', scope: 'company' }
    ]
  },
  {
    titleKey: 'credit.kyc.sections.contact',
    scope: 'both',
    fields: [
      { key: 'contact_name', labelKey: 'credit.kyc.fields.contactName', input: 'text', scope: 'both', required: true },
      { key: 'contact_phone', labelKey: 'credit.kyc.fields.contactPhone', input: 'text', scope: 'both', required: true },
      { key: 'contact_email', labelKey: 'credit.kyc.fields.contactEmail', input: 'text', scope: 'both', required: true }
    ]
  },
  {
    titleKey: 'credit.kyc.sections.bank',
    scope: 'both',
    fields: [
      { key: 'bank_name', labelKey: 'credit.kyc.fields.bankName', input: 'text', scope: 'both' },
      { key: 'bank_account', labelKey: 'credit.kyc.fields.bankAccount', input: 'text', scope: 'both' },
      { key: 'bank_holder', labelKey: 'credit.kyc.fields.bankHolder', input: 'text', scope: 'both' }
    ]
  }
]

/** 按主体类型筛出要显示的分段（个人不显示公司段，反之亦然）。 */
export function visibleSections(subjectType: KycSubjectType): KycSection[] {
  return KYC_SECTIONS.filter((s) => s.scope === 'both' || s.scope === subjectType)
}

/** 空白表单。新建与「换个客户」都从这里起手，避免残留上一个客户的 PII。 */
export function emptyKycForm(): KycFormData {
  return {
    subject_type: 'individual',
    country_region: '',
    id_type: '',
    legal_name: '',
    id_number: '',
    birth_date: '',
    address: '',
    company_name: '',
    reg_number: '',
    legal_rep: '',
    reg_address: '',
    tax_number: '',
    contact_name: '',
    contact_phone: '',
    contact_email: '',
    bank_name: '',
    bank_account: '',
    bank_holder: ''
  }
}

/**
 * 从服务端档案抽出表单字段（丢掉状态与审核轨迹，那些不可编辑）。
 *
 * 入参声明为 KycFormData 而非 KycProfile：本函数只读表单字段，
 * 管理端的 KycProfile 与客户侧的 CustomerKycProfile 都是它的超集，
 * 收窄到实际依赖的最小集合即可两端共用（ISP）。
 */
export function toKycForm(profile: KycFormData): KycFormData {
  const form = emptyKycForm()
  for (const key of Object.keys(form) as (keyof KycFormData)[]) {
    // 后端字段恒为字符串；缺省时保留空串而非 undefined（undefined 会让 input 变非受控）
    const value = profile[key]
    if (typeof value === 'string' && value !== '') {
      Object.assign(form, { [key]: value })
    }
  }
  return form
}

/**
 * 送审前的本地必填校验，返回缺失字段的 labelKey 列表。
 *
 * 与后端校验重复是刻意的：本地即时反馈避免一次往返，后端才是权威。
 * 两处必须同步 —— 后端 service/credit_kyc.go 的 requiredKYCFields 是准绳。
 */
export function missingRequired(form: KycFormData): string[] {
  return visibleSections(form.subject_type)
    .flatMap((s) => s.fields)
    .filter((f) => f.required && !form[f.key].trim())
    .map((f) => f.labelKey)
}

/** 审核状态 → badge 样式类。用完整字面量，Tailwind 扫的是源码文本。 */
export function kycStatusClass(status: KycStatus): string {
  switch (status) {
    case 'approved':
      return 'badge-success'
    case 'pending':
      return 'badge-warning'
    case 'rejected':
      return 'badge-danger'
    default:
      return 'badge-gray'
  }
}
