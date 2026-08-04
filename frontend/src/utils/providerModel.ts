/**
 * 上游站点列表的搜索、筛选、排序纯函数（便于单测，不依赖 Vue）。
 *
 * 后端 List 全量返回且不解析任何 query 参数，数据本就全在前端，
 * 故筛选排序一律本地完成，零后端改动。
 */

import type { Provider } from '@/types'
import { compareValuesWithOrder, type SortOrder } from '@/utils/tableSort'

export type ProviderSortKey = 'todayCostDesc' | 'balanceDesc' | 'balanceAsc' | 'name'

/** 站点状态：已连接 / 异常 / 待首次采集 / 未纳入监控。 */
export type ProviderStatus = 'connected' | 'error' | 'pending' | 'unmonitored'

/**
 * 余额阈值与后端预警共用 CNY 口径：站点覆盖优先，否则使用系统默认阈值。
 * 余额快照是 USD 原值，须先按充值倍率折算。
 */
export function isLowBalance(p: Provider, defaultBalanceThreshold = 0): boolean {
  const threshold = p.low_balance_threshold > 0
    ? p.low_balance_threshold
    : defaultBalanceThreshold
  const rechargeRate = p.recharge_rate > 0 ? p.recharge_rate : 1

  return (
    Number.isFinite(threshold) &&
    threshold > 0 &&
    p.last_balance !== null &&
    p.last_balance !== undefined &&
    Number.isFinite(p.last_balance) &&
    p.last_balance * rechargeRate <= threshold
  )
}

/**
 * 状态派生：本文件是全站唯一的状态定义来源。
 * ProviderCard 的徽标、列表视图的健康点、状态筛选三者都基于它，
 * 避免同一份规则在三处各写一遍而漂移。
 */
export function providerStatus(p: Provider): ProviderStatus {
  if (p.balance_type !== 'sub2api') return 'unmonitored'
  if (p.login_cooldown_until || (p.sync_state?.consecutive_failures ?? 0) > 0) return 'error'
  if (!p.sync_state?.last_success_at) return 'pending'
  return 'connected'
}

/**
 * 排序用今日消费。
 * 该值来自余额快照 metrics，未纳入监控的站点恒为空——返回 null 让它们排末尾，
 * 而不是当 0 处理挤进有消费的站点之间。
 */
function sortTodayCost(p: Provider): number | null {
  return p.today_cost ?? null
}

/** 排序用余额，缺值同样返回 null 排末尾。 */
function sortBalance(p: Provider): number | null {
  return p.last_balance ?? null
}

/**
 * 不监控（balance_type='none'）的站点恒排最后，与排序键无关。
 *
 * 与「缺值恒末尾」是两件不同的事：缺值是「这个字段没数据」，不监控是
 * 「这个站点整体不参与运营关注」—— 后者即使余额字段有值（manual 手填过）
 * 也该沉底，故不能靠 compareValuesWithOrder 的空值规则蹭到，必须显式前置。
 *
 * 判据是 balance_type === 'none' 而非 providerStatus() === 'unmonitored'：
 * 后者把 manual（人工维护余额，仍在关注范围内）也算进去了。
 */
function noneLastCompare(a: Provider, b: Provider): number {
  const an = a.balance_type === 'none' ? 1 : 0
  const bn = b.balance_type === 'none' ? 1 : 0
  return an - bn
}

/** 按 key 返回新数组（不修改入参）。缺值恒排末尾，同值回退按名称保证顺序稳定。 */
export function sortProviders(list: Provider[], key: ProviderSortKey): Provider[] {
  const sorted = [...list]
  if (key === 'name') {
    sorted.sort((a, b) => noneLastCompare(a, b) || a.name.localeCompare(b.name))
    return sorted
  }
  const pick = key === 'todayCostDesc' ? sortTodayCost : sortBalance
  const order: SortOrder = key === 'balanceAsc' ? 'asc' : 'desc'
  sorted.sort((a, b) => {
    // 不监控恒沉底，优先于排序键本身。与缺值规则同理不受方向影响 ——
    // 否则切成升序时不监控的站点会冒到顶部。
    const n = noneLastCompare(a, b)
    if (n !== 0) return n
    // 缺值恒末尾、有值部分按方向翻转，两者的方向处理由 compareValuesWithOrder
    // 内部隔开；同值回退按名称保证顺序稳定
    return compareValuesWithOrder(pick(a), pick(b), order) || a.name.localeCompare(b.name)
  })
  return sorted
}

/** 按站点名与地址做大小写不敏感的子串搜索。 */
export function searchProviders(list: Provider[], query: string): Provider[] {
  const q = query.trim().toLowerCase()
  if (!q) return list
  return list.filter(
    (p) => p.name.toLowerCase().includes(q) || (p.base_url || '').toLowerCase().includes(q)
  )
}

/** 按平台 / 状态 / 余额类型过滤（null 表示不限）。 */
export function filterProviders(
  list: Provider[],
  platform: string | null,
  status: ProviderStatus | null,
  balanceType: string | null
): Provider[] {
  let out = list
  if (platform !== null) {
    out = out.filter((p) => (p.platform || 'sub2api') === platform)
  }
  if (status !== null) {
    out = out.filter((p) => providerStatus(p) === status)
  }
  if (balanceType !== null) {
    out = out.filter((p) => p.balance_type === balanceType)
  }
  return out
}

/** 带计数的筛选项（计数随搜索结果联动）。 */
export interface FilterOption<T extends string> {
  value: T
  count: number
}

/** 按取值分桶计数，桶名升序。 */
function countBy<T extends string>(list: Provider[], pick: (p: Provider) => T): FilterOption<T>[] {
  const map = new Map<T, number>()
  for (const p of list) {
    const key = pick(p)
    map.set(key, (map.get(key) ?? 0) + 1)
  }
  return [...map.entries()]
    .map(([value, count]) => ({ value, count }))
    .sort((a, b) => a.value.localeCompare(b.value))
}

export function platformOptions(list: Provider[]): FilterOption<string>[] {
  return countBy(list, (p) => p.platform || 'sub2api')
}

export function statusOptions(list: Provider[]): FilterOption<ProviderStatus>[] {
  return countBy(list, providerStatus)
}

export function balanceTypeOptions(list: Provider[]): FilterOption<string>[] {
  return countBy(list, (p) => p.balance_type)
}
