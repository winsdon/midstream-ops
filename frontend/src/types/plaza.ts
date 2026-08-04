/**
 * 模型广场类型定义。字段与后端 service.Plaza* 结构体的 JSON tag 严格对齐。
 * 价格单位为 USD/token（per_request 模式下是 USD/次），展示时按需缩放。
 */

export type BillingMode = 'token' | 'per_request' | 'image'

export interface PlazaGroup {
  id: number
  name: string
  platform: string
  rate_multiplier: number
  subscription_type: string
  is_exclusive: boolean
}

export interface PlazaPrice {
  input: number | null
  output: number | null
  cache_write: number | null
  cache_read: number | null
  image_output: number | null
  per_request: number | null
  has_intervals: boolean
}

export interface PlazaInterval {
  min_tokens: number
  max_tokens: number | null
  tier_label?: string
  input: number | null
  output: number | null
  cache_write: number | null
  cache_read: number | null
  per_request: number | null
}

/** 模型的一个来源（渠道 × 平台），详情弹窗展示明细。 */
export interface PlazaSource {
  channel_name: string
  channel_desc: string
  platform: string
  billing_mode: BillingMode
  price: PlazaPrice
  intervals: PlazaInterval[] | null
  groups: PlazaGroup[] | null
}

/** 近 N 小时真实流量指标。 */
export interface PlazaMetric {
  request_count: number
  avg_duration_ms: number
  tokens_per_second: number
  /** 成功率百分比（0-100）；null 表示窗口内无请求。 */
  success_rate: number | null
}

/** 主动探测汇总（仅覆盖开启探测的账号，可能缺失）。 */
export interface PlazaProbe {
  total: number
  success_count: number
  avg_ttft_ms: number | null
  avg_total_ms: number | null
  last_success: boolean | null
}

/**
 * 价格来源：
 * - channel  完全来自本站渠道自定义定价
 * - official 完全来自内嵌的 LiteLLM 官方价表
 * - mixed    渠道定价 + 官方价表补齐缺失字段
 * - unknown  两处都没有价格
 */
export type PriceSource = 'channel' | 'official' | 'mixed' | 'unknown'

export interface PlazaModel {
  name: string
  platforms: string[] | null
  groups: PlazaGroup[] | null
  billing_mode: BillingMode
  price: PlazaPrice
  /** 多来源有效价不一致 → 卡面价格加「低至」前缀。 */
  multi_price: boolean
  price_source: PriceSource
  /** 官方标准价（不含本站倍率）；详情页据此 × 分组倍率算分组定价。null 表示价表无此模型。 */
  official_price: PlazaPrice | null
  description: string
  sources: PlazaSource[] | null
  metric: PlazaMetric | null
  probe: PlazaProbe | null
}

export interface PlazaData {
  models: PlazaModel[] | null
  metric_hours: number
  updated_at: string
}
