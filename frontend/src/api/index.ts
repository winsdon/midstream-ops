import { http, unwrap } from './client'
import type {
  ApiResponse,
  BalanceHistoryItem,
  CostSyncState,
  CostSyncStatus,
  DashboardSummary,
  HealthEventItem,
  HealthStateItem,
  KeyCostRow,
  LocalGroupOption,
  OperatingCost,
  OperatingCostCategory,
  LoginResult,
  NotifyChannels,
  NotifyChannelsPayload,
  PaginatedData,
  PassiveRow,
  PricingPreviewRow,
  PricingRulePayload,
  ProbeResult,
  ProbeSummaryRow,
  Provider,
  ProviderAccount,
  RateActionItem,
  LocalGroupPricing,
  RateSnapshotItem,
  ScanItem,
  URLGroupItem,
  ProviderLink,
  ImportItem,
  SelfInfo,
  StatsGroupRow,
  StatsProviderRow,
  StrategySettings,
  StrategySettingsResult,
  TrendPoint
} from '@/types'

// ---- Auth ----
export const authApi = {
  login: (username: string, password: string) =>
    unwrap<LoginResult>(http.post<ApiResponse<LoginResult>>('/auth/login', { username, password })),
  me: () => unwrap<{ username: string }>(http.get<ApiResponse<{ username: string }>>('/auth/me'))
}

// ---- Dashboard ----
export interface TrendResult {
  days?: number
  start?: string
  end?: string
  points: TrendPoint[]
}

export const dashboardApi = {
  summary: (start?: string, end?: string) =>
    unwrap<DashboardSummary>(
      http.get<ApiResponse<DashboardSummary>>('/dashboard/summary', { params: { start, end } })
    ),
  trend: (days = 7) =>
    unwrap<TrendResult>(http.get<ApiResponse<TrendResult>>('/dashboard/trend', { params: { days } })),
  // 按日期区间取趋势，与 summary 共用口径
  trendRange: (start: string, end: string) =>
    unwrap<TrendResult>(http.get<ApiResponse<TrendResult>>('/dashboard/trend', { params: { start, end } }))
}

// ---- Providers ----
export interface ProviderPayload {
  name: string
  note?: string
  balance_type: string
  platform?: string
  auth_mode?: string
  base_url?: string
  login_email?: string
  login_password?: string | null
  access_token?: string | null
  refresh_token?: string | null
  upstream_user_id?: string
  low_balance_threshold?: number
  recharge_rate?: number
  probe_enabled?: boolean
  probe_model?: string | null
  ignore_balance_alert?: boolean
  /** 自营站：上游实扣不计入成本，改记运营成本。省略等同 false，会清除已有标记 */
  self_operated?: boolean
}

export interface TestConnectionPayload {
  platform: string
  auth_mode: string
  base_url: string
  email?: string
  password?: string
  access_token?: string
  user_id?: string
}

export const providerApi = {
  list: (page = 1, pageSize = 100) =>
    unwrap<PaginatedData<Provider>>(
      http.get<ApiResponse<PaginatedData<Provider>>>('/providers', { params: { page, page_size: pageSize } })
    ),
  create: (payload: ProviderPayload) =>
    unwrap<Provider>(http.post<ApiResponse<Provider>>('/providers', payload)),
  update: (id: number, payload: ProviderPayload) =>
    unwrap<Provider>(http.put<ApiResponse<Provider>>(`/providers/${id}`, payload)),
  remove: (id: number) => unwrap<null>(http.delete<ApiResponse<null>>(`/providers/${id}`)),
  testConnection: (payload: TestConnectionPayload) =>
    unwrap<{ ok?: boolean; balance?: number | null; error?: string }>(
      http.post<ApiResponse<{ ok?: boolean; balance?: number | null; error?: string }>>(
        '/providers/test-connection',
        payload
      )
    ),
  scan: () =>
    unwrap<{ items: ScanItem[] }>(http.get<ApiResponse<{ items: ScanItem[] }>>('/providers/scan')),
  /** 按账号 base_url 归组（不依赖【】命名习惯的发现方式） */
  scanUrls: () =>
    unwrap<{ items: URLGroupItem[] }>(
      http.get<ApiResponse<{ items: URLGroupItem[] }>>('/providers/scan-urls')
    ),
  /** 批量建站并顺带写入账号关联；站点已存在时只跳过建站，关联照写 */
  import: (items: ImportItem[]) =>
    unwrap<{ created: string[]; skipped: string[]; linked: number }>(
      http.post<ApiResponse<{ created: string[]; skipped: string[]; linked: number }>>(
        '/providers/import',
        { items }
      )
    ),
  /** 该供应商已关联的账号（只读本地库，PG 挂了也能看） */
  links: (id: number) =>
    unwrap<{ items: ProviderLink[]; total: number }>(
      http.get<ApiResponse<{ items: ProviderLink[]; total: number }>>(`/providers/${id}/links`)
    ),
  /** 全量替换关联集合；勾中别的站已关联的账号会把它抢过来 */
  saveLinks: (id: number, accountIds: number[]) =>
    unwrap<{ linked: number }>(
      http.put<ApiResponse<{ linked: number }>>(`/providers/${id}/links`, { account_ids: accountIds })
    ),
  accounts: (id: number) =>
    unwrap<{ items: ProviderAccount[]; provider: string; total: number }>(
      http.get<ApiResponse<{ items: ProviderAccount[]; provider: string; total: number }>>(`/providers/${id}/accounts`)
    ),
  refreshBalance: (id: number) => unwrap<Provider>(http.post<ApiResponse<Provider>>(`/providers/${id}/balance/refresh`)),
  // 一键刷新全部上游站点；后端并发受限，登录冷却中的站点跳过。
  // 单独放宽超时：N 个站点分批采集，总耗时远超默认 120s，超时会让前端误报失败而后端仍在跑。
  refreshAll: () =>
    unwrap<RefreshAllResult>(
      http.post<ApiResponse<RefreshAllResult>>('/providers/balance/refresh-all', null, {
        timeout: 15 * 60 * 1000
      })
    ),
  manualBalance: (id: number, balance: number) =>
    unwrap<Provider>(http.put<ApiResponse<Provider>>(`/providers/${id}/balance`, { balance })),
  balanceHistory: (id: number, page = 1, pageSize = 50) =>
    unwrap<PaginatedData<BalanceHistoryItem>>(
      http.get<ApiResponse<PaginatedData<BalanceHistoryItem>>>(`/providers/${id}/balance/history`, {
        params: { page, page_size: pageSize }
      })
    ),
  // per-key 上游实扣明细（只读本地库，不打上游）
  keyCosts: (id: number, start?: string, end?: string) =>
    unwrap<KeyCostsResult>(
      http.get<ApiResponse<KeyCostsResult>>(`/providers/${id}/costs`, { params: { start, end } })
    ),
  // 手动触发成本同步；backfill=true 会回补历史，耗时较长
  syncCost: (id: number, backfill = false) =>
    unwrap<{ synced: boolean; backfill: boolean; sync_state: CostSyncState }>(
      http.post<ApiResponse<{ synced: boolean; backfill: boolean; sync_state: CostSyncState }>>(
        `/providers/${id}/costs/sync`,
        null,
        { params: { backfill: backfill ? 'true' : undefined } }
      )
    )
}

export interface KeyCostsResult {
  provider: string
  start: string
  end: string
  items: KeyCostRow[] | null
  total: number
  actual_cost: number
  official_cost: number
  sync_state: CostSyncState
}

export interface OperatingCostPayload {
  category: OperatingCostCategory
  amount: number
  /** YYYY-MM-DD，留空取今天（后端按配置时区判定） */
  occurred_on?: string
  note?: string
}

export interface OperatingCostsResult {
  items: OperatingCost[] | null
  /** 区间内合计（USD） */
  total: number
  start: string
  end: string
}

/** 自营站运营成本：买号/订阅/服务器等站外支出。仅自营站可写入，否则后端返回 400。 */
export const operatingCostApi = {
  /** 区间缺省为本月至今 */
  list: (providerId: number, start?: string, end?: string) =>
    unwrap<OperatingCostsResult>(
      http.get<ApiResponse<OperatingCostsResult>>(`/providers/${providerId}/operating-costs`, {
        params: { start, end }
      })
    ),
  create: (providerId: number, payload: OperatingCostPayload) =>
    unwrap<OperatingCost>(
      http.post<ApiResponse<OperatingCost>>(`/providers/${providerId}/operating-costs`, payload)
    ),
  remove: (id: number) =>
    unwrap<{ deleted: number }>(http.delete<ApiResponse<{ deleted: number }>>(`/operating-costs/${id}`))
}

/** 全量刷新结果：total 不含被跳过的站点 */
export interface RefreshAllResult {
  total: number
  succeeded: number
  failed: number
  /** 因登录冷却未刷新的站点数 */
  skipped: number
  failures: RefreshAllFailure[] | null
}

export interface RefreshAllFailure {
  provider_id: number
  name: string
  error: string
}

// ---- Stats ----
export const statsApi = {
  byProvider: (start?: string, end?: string) =>
    unwrap<{ start: string; end: string; items: StatsProviderRow[]; cost_sync?: CostSyncStatus | null }>(
      http.get<ApiResponse<{ start: string; end: string; items: StatsProviderRow[]; cost_sync?: CostSyncStatus | null }>>(
        '/stats/providers',
        { params: { start, end } }
      )
    ),
  byGroup: (start?: string, end?: string) =>
    unwrap<{ start: string; end: string; items: StatsGroupRow[]; cost_sync?: CostSyncStatus | null }>(
      http.get<ApiResponse<{ start: string; end: string; items: StatsGroupRow[]; cost_sync?: CostSyncStatus | null }>>(
        '/stats/groups',
        { params: { start, end } }
      )
    )
}

// ---- Rates ----
export const rateApi = {
  history: (params: {
    scope?: string
    provider_id?: number
    entity_type?: string
    entity_id?: string
    changes_only?: boolean
    page?: number
    page_size?: number
  }) =>
    unwrap<PaginatedData<RateSnapshotItem>>(
      http.get<ApiResponse<PaginatedData<RateSnapshotItem>>>('/rates/history', {
        params: { ...params, changes_only: params.changes_only ? 'true' : undefined }
      })
    ),
  current: (params: { scope: string; provider_id?: number; include_deleted?: boolean }) =>
    unwrap<{ items: RateSnapshotItem[]; total: number }>(
      http.get<ApiResponse<{ items: RateSnapshotItem[]; total: number }>>('/rates/current', {
        params: { ...params, include_deleted: params.include_deleted ? 'true' : undefined }
      })
    )
}

// ---- Settings ----
export const settingsApi = {
  getStrategy: () =>
    unwrap<StrategySettingsResult>(http.get<ApiResponse<StrategySettingsResult>>('/settings/strategy')),
  saveStrategy: (payload: StrategySettings) =>
    unwrap<StrategySettings>(http.put<ApiResponse<StrategySettings>>('/settings/strategy', payload)),
  getNotify: () => unwrap<NotifyChannels>(http.get<ApiResponse<NotifyChannels>>('/settings/notify')),
  saveNotify: (payload: NotifyChannelsPayload) =>
    unwrap<{ saved: boolean }>(http.put<ApiResponse<{ saved: boolean }>>('/settings/notify', payload)),
  testNotify: (channel: string) =>
    unwrap<{ ok: boolean; error?: string }>(
      http.post<ApiResponse<{ ok: boolean; error?: string }>>('/settings/notify/test', { channel })
    )
}

// ---- Pricing（调价规则）----
export const pricingApi = {
  getSelf: () => unwrap<SelfInfo>(http.get<ApiResponse<SelfInfo>>('/pricing/self')),
  saveSelf: (base_url: string, email: string, password?: string | null) =>
    unwrap<{ ok: boolean; error?: string }>(
      http.put<ApiResponse<{ ok: boolean; error?: string }>>('/pricing/self', { base_url, email, password })
    ),
  localGroups: () =>
    unwrap<{ items: LocalGroupOption[] }>(http.get<ApiResponse<{ items: LocalGroupOption[] }>>('/pricing/local-groups')),
  // 全部调价规则 + 应用预览
  rules: () =>
    unwrap<{ items: PricingPreviewRow[]; total: number }>(
      http.get<ApiResponse<{ items: PricingPreviewRow[]; total: number }>>('/pricing/rules')
    ),
  // 按本站分组 upsert 规则
  saveRule: (payload: PricingRulePayload) =>
    unwrap<LocalGroupPricing>(http.post<ApiResponse<LocalGroupPricing>>('/pricing/rules', payload)),
  deleteRule: (id: number) => unwrap<null>(http.delete<ApiResponse<null>>(`/pricing/rules/${id}`)),
  applyRule: (id: number, force = false) =>
    unwrap<{ ok: boolean; error?: string }>(
      http.post<ApiResponse<{ ok: boolean; error?: string }>>(`/pricing/rules/${id}/apply`, { force })
    ),
  resolveConflict: (id: number) =>
    unwrap<{ ok: boolean }>(http.post<ApiResponse<{ ok: boolean }>>(`/pricing/rules/${id}/resolve-conflict`)),
  actions: (id: number, limit = 50) =>
    unwrap<{ items: RateActionItem[]; total: number }>(
      http.get<ApiResponse<{ items: RateActionItem[]; total: number }>>(`/pricing/rules/${id}/actions`, {
        params: { limit }
      })
    ),
  // 已对接的上游分组集合（key = "providerID:group"）
  mapped: () => unwrap<{ keys: string[] }>(http.get<ApiResponse<{ keys: string[] }>>('/pricing/mapped'))
}

// ---- Stability ----
// 窗口参数统一用 minutes：稳定性页档位下探到 5 分钟，整数小时表达不了。
export const stabilityApi = {
  passive: (minutes = 1440) =>
    unwrap<{ minutes: number; items: PassiveRow[]; note?: string }>(
      http.get<ApiResponse<{ minutes: number; items: PassiveRow[]; note?: string }>>('/stability/passive', { params: { minutes } })
    ),
  probes: (params: { account_id?: number; page?: number; page_size?: number }) =>
    unwrap<PaginatedData<ProbeResult>>(http.get<ApiResponse<PaginatedData<ProbeResult>>>('/stability/probes', { params })),
  probeSummary: (minutes = 1440) =>
    unwrap<{ minutes: number; items: ProbeSummaryRow[] }>(
      http.get<ApiResponse<{ minutes: number; items: ProbeSummaryRow[] }>>('/stability/probes/summary', { params: { minutes } })
    ),
  probeTrend: (accountId: number, minutes = 1440) =>
    unwrap<{ account_id: number; minutes: number; items: ProbeResult[] }>(
      http.get<ApiResponse<{ account_id: number; minutes: number; items: ProbeResult[] }>>('/stability/probes/trend', {
        params: { account_id: accountId, minutes }
      })
    ),
  runProbe: (payload: { account_id?: number; provider_id?: number }) =>
    unwrap<ProbeResult | { queued: boolean; provider: string }>(
      http.post<ApiResponse<ProbeResult | { queued: boolean; provider: string }>>('/stability/probe/run', payload)
    ),
  healthStates: () =>
    unwrap<{ items: HealthStateItem[]; total: number; budget_used: number }>(
      http.get<ApiResponse<{ items: HealthStateItem[]; total: number; budget_used: number }>>('/stability/health')
    ),
  healthEvents: (accountId: number, limit = 50) =>
    unwrap<{ items: HealthEventItem[]; total: number }>(
      http.get<ApiResponse<{ items: HealthEventItem[]; total: number }>>('/stability/health/events', {
        params: { account_id: accountId, limit }
      })
    ),
  setHealthDisabled: (accountId: number, disabled: boolean) =>
    unwrap<{ account_id: number; disabled: boolean }>(
      http.put<ApiResponse<{ account_id: number; disabled: boolean }>>(`/stability/health/${accountId}/disabled`, {
        disabled
      })
    )
}
