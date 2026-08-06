// 与后端对应的 API 类型定义。

export interface ApiResponse<T = unknown> {
  code: number
  message: string
  data?: T
}

export interface PaginatedData<T = unknown> {
  items: T[]
  total: number
  page: number
  page_size: number
  pages: number
}

export interface LoginResult {
  token: string
  expires_at: string
  username: string
}

export interface Provider {
  id: number
  name: string
  note: string
  balance_type: string
  platform: string
  auth_mode: string
  base_url: string
  login_email: string
  has_password: boolean
  has_access_token: boolean
  has_refresh_token: boolean
  upstream_user_id: string
  low_balance_threshold: number
  recharge_rate: number
  /** 凭据是否齐备。false 时不进采集队列，健康点显示「待配置凭据」而非采集异常 */
  credentials_ready: boolean
  probe_enabled: boolean
  probe_model?: string | null
  /** 站点级静音：不推余额告警（余额采集照常） */
  ignore_balance_alert: boolean
  /** 自营站：上游实扣是左手倒右手，不计入成本；真实支出改记运营成本 */
  self_operated: boolean
  last_balance?: number | null
  last_balance_at?: string | null
  last_balance_error?: string | null
  login_cooldown_until?: string | null
  account_count: number
  created_at: string
  sync_state?: ProviderSyncState | null
  // 上游站点指标（USD 原值，前端按 recharge_rate 折 CNY）
  today_cost?: number | null
  total_cost?: number | null
  today_reqs?: number | null
}

// ProviderSyncState 供应商采集健康（collector_state）。
export interface ProviderSyncState {
  last_run_at?: string | null
  last_success_at?: string | null
  last_error?: string | null
  consecutive_failures: number
  next_eligible_at?: string | null
}

// ---- 调价映射 ----

export interface SelfInfo {
  configured: boolean
  base_url: string
  login_email: string
}

// ---- 调价规则（多上游聚合）----

export interface PricingSourceRef {
  id?: number
  pricing_id?: number
  provider_id: number
  upstream_group: string
}

// 本站分组的调价规则：参考价按 price_source 聚合多个上游后加价
export interface LocalGroupPricing {
  id: number
  local_group_id: number
  local_group_name: string
  auto_enabled: boolean
  /** primary(指定主上游) | lowest | highest | average */
  price_source: string
  primary_provider_id?: number | null
  primary_group?: string | null
  /** fixed(参考价+markup) | percentage(参考价×(1+markup/100)) */
  markup_mode: string
  markup_value: number
  /** 跟随阈值(%)：上游变化 ≤ 阈值才自动跟随，超过则不动 */
  follow_threshold: number
  min_rate?: number | null
  max_rate?: number | null
  last_applied_rate?: number | null
  conflict: boolean
  created_at: string
  updated_at: string
  sources: PricingSourceRef[]
}

export interface PricingPreviewRow {
  pricing: LocalGroupPricing
  reference_rate?: number | null
  current_rate?: number | null
  target_rate?: number | null
  needs_apply: boolean
  source_rates: Record<string, number>
}

export interface PricingRulePayload {
  local_group_id: number
  local_group_name?: string
  auto_enabled?: boolean
  price_source?: string
  primary_provider_id?: number | null
  primary_group?: string | null
  markup_mode?: string
  markup_value?: number
  follow_threshold?: number
  min_rate?: number | null
  max_rate?: number | null
  sources: PricingSourceRef[]
}

export interface RateActionItem {
  id: number
  trigger_by: string
  old_rate?: number | null
  new_rate: number
  status: string
  error?: string | null
  created_at: string
}

export interface LocalGroupOption {
  id: number
  name: string
  rate: number
}

// ---- 系统设置 ----

export interface StrategySettings {
  refresh_enabled: boolean
  refresh_interval_seconds: number
  balance_alert_enabled: boolean
  default_balance_threshold: number
  balance_notify_channels: string[]
  balance_template: string
  rate_alert_enabled: boolean
  rate_notify_channels: string[]
  rate_template: string
  credit_alert_enabled: boolean
  credit_notify_channels: string[]
  credit_template: string
}

// GET /settings/strategy 的响应：策略本体 + 后端下发的默认模板与可用渠道
export interface StrategySettingsResult {
  strategy: StrategySettings
  default_balance_template: string
  default_rate_template: string
  default_credit_template: string
  available_channels: string[]
}

export interface NotifyChannels {
  dingtalk: { enabled: boolean; webhook: string; has_secret: boolean }
  feishu: { enabled: boolean; webhook: string; has_secret: boolean }
  telegram: { enabled: boolean; chat_id: string; has_bot_token: boolean }
}

// 保存请求：secret/bot_token 为 null 时后端保留原值
export interface NotifyChannelsPayload {
  dingtalk: { enabled: boolean; webhook: string; secret?: string | null }
  feishu: { enabled: boolean; webhook: string; secret?: string | null }
  telegram: { enabled: boolean; bot_token?: string | null; chat_id: string }
}

export interface ProviderAccount {
  id: number
  name: string
  platform: string
  type: string
  status: string
  schedulable: boolean
  rate_multiplier: number
  groups?: string[] | null
}

export interface ScanItem {
  // 后端字段名是 prefix（accounts.name 的【】前缀），导入时作为供应商名提交。
  // 前缀已不决定归属（那是 provider_accounts 表的职责），仅作批量建站的便利入口。
  prefix: string
  account_count: number
  exists: boolean
  /** 该前缀下的账号 id，导入时一并写入关联表 */
  account_ids: number[]
  /** 该前缀下账号连的站点地址（组内地址不唯一时任取其一），作为导入表单预填值 */
  base_url: string
  /** 该前缀下出现过的不同站点地址数；> 1 时须提示用户核对预填地址 */
  url_count: number
}

/** 按 base_url 归组里的单个账号。 */
export interface GroupedAccount {
  id: number
  name: string
  platform: string
  status: string
  /** 已关联到的供应商名（空 = 未关联）。勾选它等于把它从原供应商抢过来 */
  linked_to: string
}

/** 按账号 base_url 归组的结果（扫描弹窗「按站点地址」页签）。 */
export interface URLGroupItem {
  /** 规范化后的站点身份（去路径、去尾斜杠、host 小写） */
  base_url: string
  /** 组内首个原始 URL，便于人工核对 */
  sample_url: string
  account_count: number
  accounts: GroupedAccount[]
  /** 建议的供应商名：优先取【】前缀，无则回退 host。仅作预填，可改 */
  suggested_name: string
  /** 该 URL 已对应的供应商名（空 = 尚未建站） */
  existing_provider: string
}

/** 供应商已关联的账号。 */
export interface ProviderLink {
  account_id: number
  /** 关联时的账号名快照；远端改名后不会自动更新 */
  account_name: string
  note: string
}

/** 批量建站 + 关联的一项。 */
export interface ImportItem {
  name: string
  base_url?: string
  /** 余额获取方式；仅对新建站点生效，已存在的站点不改其采集方式 */
  balance_type?: string
  account_ids?: number[]
}

// CostSyncStatus 上游成本同步状态（数据新鲜度）。
// 后端按「最落后的供应商」取 last_synced_at，故它代表最保守的新鲜度。
export interface CostSyncStatus {
  last_synced_at?: string | null
  providers_total: number
  providers_ok: number
  keys_total: number
  keys_matched: number
  interval_minutes: number
  last_error: string
}

// cost = 上游实扣（倍率折后，真实付出）。
// cost_complete=false 表示有账号有流量但没匹配到上游 key，此时成本偏低、利润偏高。
export interface DashboardSummary {
  date: string
  start?: string
  end?: string
  revenue: number
  cost: number
  /** 自营站运营成本（买号/订阅/服务器），与上游实扣同为真实成本 */
  operating_cost: number
  profit: number
  requests: number
  provider_count: number
  account_count: number
  cost_complete: boolean
  accounts_missing: number
  cost_sync?: CostSyncStatus | null
  // 分组贡献：Top6 分组与 Top3 集中度（%）
  groups?: DashboardGroupRow[] | null
  group_total?: number
  group_concentration?: number
}

export interface DashboardGroupRow {
  group_id: number
  group_name: string
  revenue: number
  requests: number
}

export interface TrendPoint {
  day: string
  revenue: number
  cost: number
  official_cost: number
  /** 当天发生的自营站运营成本，全额计入发生日（不摊销），故单日可能出现尖刺 */
  operating_cost: number
  profit: number
  requests: number
  cost_complete: boolean
}

export interface StatsAccountRow {
  account_id: number
  account_name: string
  requests: number
  revenue: number
  cost: number
  profit: number
  cost_matched: boolean
}

export interface StatsProviderRow {
  provider: string
  /** 自营站：成本取运营成本，前端显示「自营」标签而非「成本不完整」告警 */
  self_operated: boolean
  revenue: number
  cost: number
  /** 站点级运营成本，不摊到 accounts 明细，故子账号利润之和会大于本行利润 */
  operating_cost: number
  profit: number
  requests: number
  cost_complete: boolean
  accounts_missing: number
  accounts?: StatsAccountRow[] | null
}

// 分组成本为分摊值：上游按 key（≈账号）计一笔实扣，一个账号可服务多个分组，
// 故按各分组在该账号内的原始用量占比拆分。分摊不产生也不吞掉成本，
// 分组合计与「按供应商」维度的合计严格一致。字段集与 StatsProviderRow 同构。
export interface StatsGroupRow {
  group_id: number
  group_name: string
  rate_multiplier: number
  revenue: number
  cost: number
  /** 本维度恒为 0：运营成本是站点级固定成本，不摊到分组 */
  operating_cost: number
  profit: number
  requests: number
  cost_complete: boolean
  accounts_missing: number
  accounts?: StatsAccountRow[] | null
}

// ---- 自营站运营成本 ----

/** 运营成本类别（与后端 service 枚举一致） */
export type OperatingCostCategory = 'account' | 'subscription' | 'server' | 'other'

export interface OperatingCost {
  id: number
  provider_id: number
  category: OperatingCostCategory
  amount: number
  currency: string
  /** YYYY-MM-DD（发生日，全额计入当天，不摊销） */
  occurred_on: string
  note: string
  operator: string
  created_at: string
}

export interface KeyCostRow {
  upstream_key_id: number
  key_name: string
  account_id?: number | null
  account_name: string
  rate_multiplier?: number | null
  actual_cost: number
  official_cost: number
  matched: boolean
}

export interface CostSyncState {
  provider_id: number
  last_synced_at?: string | null
  last_error?: string | null
  keys_total: number
  keys_matched: number
  backfilled_at?: string | null
}

export interface RateHistoryItem {
  id: number
  entity_type: string
  entity_id: number
  entity_name: string
  old_rate: number
  new_rate: number
  observed_at: string
}

// RateSnapshotItem 变更驱动倍率快照行（/rates/current 与 /rates/history）。
export interface RateSnapshotItem {
  id: number
  scope: string
  provider_id: number
  entity_type: string
  entity_id: string
  entity_name: string
  rate: number
  /** 分组所属平台（anthropic|openai|gemini|...）；空串表示上游未提供，前端归「未分类」 */
  platform: string
  prev_rate?: number | null
  first_seen_at: string
  last_seen_at: string
  deleted: boolean
}

export interface PassiveRow {
  account_id: number
  account_name: string
  platform: string
  /** 归属供应商，0 = 未关联（后端按 provider_accounts 现值解析） */
  provider_id: number
  /** 归属供应商名，'' = 未关联，前端渲染成「未归属」 */
  provider_name: string
  requests: number
  duration_p50: number
  duration_p95: number
  first_token_p50: number
  first_token_p95: number
}

export interface ProbeResult {
  id: number
  provider_id?: number | null
  account_id: number
  account_name: string
  platform: string
  model: string
  base_url: string
  source: string
  success: boolean
  status_code?: number | null
  ttft_ms?: number | null
  total_ms?: number | null
  error?: string | null
  created_at: string
}

export interface ProbeSummaryRow {
  account_id: number
  account_name: string
  platform: string
  /** 归属供应商，0 = 未关联 */
  provider_id: number
  /** 归属供应商名，'' = 未关联 */
  provider_name: string
  total: number
  success_count: number
  success_rate: number
  avg_ttft_ms?: number | null
  avg_total_ms?: number | null
  last_success?: boolean | null
  last_at?: string | null
}

// ---- 分组健康 ----

export interface HealthStateItem {
  account_id: number
  account_name: string
  provider_id?: number | null
  /** 归属供应商名，'' = 未关联 */
  provider_name?: string
  state: string // healthy|degraded|suspended|observing|recovering|disabled
  consecutive_failures: number
  consecutive_successes: number
  weight_percent: number
  cooldown_until?: string | null
  last_probe_at?: string | null
}

export interface HealthEventItem {
  id: number
  from_state: string
  to_state: string
  reason: string
  detail?: string | null
  created_at: string
}

export interface BalanceHistoryItem {
  id: number
  balance?: number | null
  today_cost?: number | null
  today_requests?: number | null
  today_tokens?: number | null
  rpm?: number | null
  tpm?: number | null
  source: string
  error?: string | null
  created_at: string
}
