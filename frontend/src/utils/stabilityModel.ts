/**
 * 稳定性页的窗口档位、筛选、选项计数与行级评级纯函数（不依赖 Vue，便于单测）。
 *
 * 沿用 providerModel.ts 的哲学：后端全量返回，筛选一律本地完成 ——
 * 点 pill 切筛选是即时交互，走后端意味着每次点击都要等一次
 * percentile_cont 重算，而数据本就已经在前端了。
 */

import type { FilterOption } from '@/utils/providerModel'
import { latencyBand } from '@/utils/latencyBand'
import { sortRows, type SortOrder } from '@/utils/tableSort'

/** 时间窗口档位（分钟）。实时盯盘口径，对齐运维监控的 5m/30m/1h/6h/24h。 */
export type WindowMinutes = 5 | 30 | 60 | 360 | 1440
export const WINDOW_OPTIONS: readonly WindowMinutes[] = [5, 30, 60, 360, 1440]
/** 默认窗口：近 30 分钟。短到能看见正在发生的事，长到被动流量够样本。 */
export const DEFAULT_WINDOW_MINUTES: WindowMinutes = 30

/**
 * Select 不能用 '' 同时表示「全部」和「未归属/未分组」桶。
 * 工具条把这两个哨兵映射回 filterRows 的 null / ''。
 */
export const SELECT_ALL = '__all__'
export const SELECT_EMPTY = '__empty__'

export function selectValue(filter: string | null): string {
  if (filter === null) return SELECT_ALL
  if (filter === '') return SELECT_EMPTY
  return filter
}

export function parseSelectValue(v: string | number | boolean | null): string | null {
  if (v === null || v === SELECT_ALL || v === '') return null
  if (v === SELECT_EMPTY) return ''
  return String(v)
}

/**
 * 探测调度间隔（分钟），与 config.yaml 的 probe.interval_minutes 默认值一致。
 *
 * 仅用于「短窗口可能无样本」的提示文案。用户改了配置这里不会跟着变，
 * 但提示本身的目的是解释「为什么表是空的」，量级对了就够，
 * 不值得为此多加一个配置下发接口。
 */
export const PROBE_INTERVAL_MINUTES = 15

/**
 * 健康状态的排序档位：按「越糟越大」排，升序即把最健康的排前面。
 *
 * 不排状态字符串：字母序下 degraded < disabled < healthy 毫无意义，
 * 而用户点这列想看的是「哪些账号有问题」。
 */
export const HEALTH_RANK: Record<string, number> = {
  healthy: 0,
  recovering: 1,
  observing: 2,
  degraded: 3,
  suspended: 4,
  disabled: 5
}

/**
 * 无记录（从未被探测）视作 healthy —— 必须与 healthBadge 的兜底一致，
 * 否则徽标显示「正常」而排序/筛选把它归到别处，同一行自相矛盾。
 */
export function healthRank(state?: string): number {
  return HEALTH_RANK[state ?? 'healthy'] ?? 0
}

/** 无记录归入 healthy 桶，是 healthRank 兜底规则的筛选侧对应物。 */
export function normalizeHealth(state?: string): string {
  return state ?? 'healthy'
}

/**
 * 成功率阈值（%）：>=95 正常 / >=80 警戒 / 更低为差。
 *
 * 与 latencyBand 的阈值不同，这两个数字有两个消费方（评级点与文字着色），
 * 故必须收在一处 —— 同一行里不该出现「成功率文字是琥珀色但评级点是绿的」。
 */
export const RATE_BANDS = [95, 80] as const
/** 被动 SLA 着色 / 评级：比主动探测松一档，小样本里一次失败不该整行变黄。 */
export const PASSIVE_RATE_BANDS = [90, 70] as const

export type RateBand = 'good' | 'warn' | 'bad' | 'unknown'

/**
 * 成功率 / SLA 着色。必须写完整字面量：Tailwind 扫描源码文本提取类名。
 * 缺值灰色而非弃权 —— 列上「没数字」和评级点「该维度弃权」是两件事。
 */
export const RATE_TONE: Record<RateBand, string> = {
  good: 'font-semibold text-emerald-600',
  warn: 'font-semibold text-amber-600',
  bad: 'font-semibold text-red-600',
  unknown: 'text-gray-400'
}

export function rateClass(v?: number | null): string {
  return RATE_TONE[rateBand(v)]
}

export function passiveRateClass(v?: number | null): string {
  return RATE_TONE[rateBand(v, PASSIVE_RATE_BANDS)]
}

/**
 * 流量 SLA（0–100）。分母 = 成功 + SLA 口径失败；两边都 0 返回 null。
 * 与运维监控 / 广场模型指标同一公式，只是这里输出百分数而非 0–1。
 */
export function slaPercent(success: number, errorCount: number): number | null {
  if (!Number.isFinite(success) || !Number.isFinite(errorCount)) return null
  const total = success + errorCount
  if (total <= 0) return null
  return (success / total) * 100
}

/**
 * 账号健康分 0–100。对常见 3–8s 首字、≤1% 错误率不扣分。
 * 色块与悬停数字共用这一分，避免「评分 60 却是绿块」。
 */
export function accountHealthScore(input: {
  sla?: number | null
  ttftP50?: number | null
  errorRate?: number | null
}): number {
  const err =
    input.errorRate != null && Number.isFinite(input.errorRate)
      ? input.errorRate
      : input.sla != null && Number.isFinite(input.sla)
        ? 100 - input.sla
        : 0
  const penalty = errorPenalty(err) + ttftPenalty(input.ttftP50)
  return Math.min(100, Math.max(0, Math.round(100 - penalty)))
}

function errorPenalty(errorRate: number): number {
  if (!Number.isFinite(errorRate) || errorRate <= 1) return 0
  if (errorRate <= 10) return ((errorRate - 1) / 9) * 40
  return 50
}

function ttftPenalty(ttftP50?: number | null): number {
  if (ttftP50 == null || !Number.isFinite(ttftP50) || ttftP50 < 0) return 0
  if (ttftP50 < 10_000) return 0
  if (ttftP50 <= 30_000) return ((ttftP50 - 10_000) / 20_000) * 30
  return 40
}

/**
 * 成功率分档。缺值返 unknown —— 「这个数落在哪档」与「缺值意味着什么」
 * 是两件事，后者由调用方决定（见 rateToGrade 与 rateClass，
 * 二者对缺值的处理刻意不同）。
 */
export function rateBand(
  v?: number | null,
  bands: readonly [number, number] = RATE_BANDS
): RateBand {
  if (v === null || v === undefined || !Number.isFinite(v)) return 'unknown'
  const [good, warn] = bands
  if (v >= good) return 'good'
  if (v >= warn) return 'warn'
  return 'bad'
}

/** 两张表结构不同，抽出筛选真正依赖的最小字段集。 */
export interface FilterableRow {
  account_id: number
  /** '' = 未归属 */
  provider_name: string
  /** 本站分组名。缺省或空数组 = 未分组 */
  groups?: readonly string[]
}

/** 账号 id → 健康状态的查表函数。由调用方注入，纯函数不必知道状态存在 Vue ref 里。 */
export type HealthLookup = (accountId: number) => string | undefined

function rowGroups(r: FilterableRow): readonly string[] {
  return r.groups ?? []
}

function inGroup(r: FilterableRow, group: string): boolean {
  const gs = rowGroups(r)
  return group === '' ? gs.length === 0 : gs.includes(group)
}

/**
 * 按归属供应商 / 健康状态 / 分组过滤（null 表示不限）。
 *
 * provider / group 传 '' 即筛「未归属」「未分组」桶 —— 故必须用 null 而非 '' 表示不限。
 * 一个账号可属于多个分组：命中任一即保留。
 */
export function filterRows<T extends FilterableRow>(
  rows: readonly T[],
  provider: string | null,
  health: string | null,
  healthOf: HealthLookup,
  group: string | null = null
): T[] {
  let out = [...rows]
  if (provider !== null) {
    out = out.filter((r) => r.provider_name === provider)
  }
  if (health !== null) {
    out = out.filter((r) => normalizeHealth(healthOf(r.account_id)) === health)
  }
  if (group !== null) {
    out = out.filter((r) => inGroup(r, group))
  }
  return out
}

/** 按取值分桶计数，桶名升序（与 providerModel.countBy 同构）。 */
function countBy<T>(rows: readonly T[], pick: (row: T) => string): FilterOption<string>[] {
  const map = new Map<string, number>()
  for (const r of rows) {
    const key = pick(r)
    map.set(key, (map.get(key) ?? 0) + 1)
  }
  return [...map.entries()]
    .map(([value, count]) => ({ value, count }))
    .sort((a, b) => a.value.localeCompare(b.value))
}

/** 归属供应商选项。'' 桶代表未归属，排序时因空串最小自然落在首位。 */
export function providerOptions<T extends FilterableRow>(rows: readonly T[]): FilterOption<string>[] {
  return countBy(rows, (r) => r.provider_name)
}

/**
 * 搜索依赖的最小字段集。刻意不 extends FilterableRow —— account_id 与搜索无关，
 * 沿用 creditModel 的做法收窄到实际依赖的字段（ISP）。
 */
export interface SearchableRow {
  account_name: string
  platform: string
  provider_name: string
  groups?: readonly string[]
}

/**
 * 按账号名 / 平台 / 归属供应商 / 分组模糊搜索（大小写不敏感）。
 *
 * 刻意不匹配 account_id，与 linkModel.matchAccount 不同：那里匹配 id 是因为
 * 关联弹窗会显示 id 且账号名常是不可读的哈希；稳定性表两者都不显示，
 * 匹配 id 只会让输入「3」命中 3/13/23/30 这一堆无关账号。
 */
export function searchStabilityRows<T extends SearchableRow>(rows: readonly T[], query: string): T[] {
  const q = query.trim().toLowerCase()
  if (!q) return [...rows]
  return rows.filter(
    (r) =>
      r.account_name.toLowerCase().includes(q) ||
      r.platform.toLowerCase().includes(q) ||
      r.provider_name.toLowerCase().includes(q) ||
      (r.groups ?? []).some((g) => g.toLowerCase().includes(q))
  )
}

/** 分组选项。空串桶 = 未分组；多分组账号在每个组各计一次。 */
export function groupOptions<T extends FilterableRow>(rows: readonly T[]): FilterOption<string>[] {
  const exploded: { g: string }[] = []
  for (const r of rows) {
    const gs = rowGroups(r)
    if (gs.length === 0) exploded.push({ g: '' })
    else for (const g of gs) exploded.push({ g })
  }
  return countBy(exploded, (x) => x.g)
}

/** 健康状态选项，按「越糟越靠后」排而非字母序，与表格排序方向一致。 */
export function healthOptions<T extends FilterableRow>(
  rows: readonly T[],
  healthOf: HealthLookup
): FilterOption<string>[] {
  return countBy(rows, (r) => normalizeHealth(healthOf(r.account_id))).sort(
    (a, b) => healthRank(a.value) - healthRank(b.value)
  )
}

/** 行级综合评级。 */
export type RowGrade = 'good' | 'warn' | 'bad' | 'unknown'

/**
 * 评级的严重度序。unknown 排在 good 之后 warn 之前：
 * 「没数据」比「正常」可疑，但比「确定有问题」轻。
 */
export const GRADE_RANK: Record<RowGrade, number> = {
  good: 0,
  unknown: 1,
  warn: 2,
  bad: 3
}

/** 延迟档 → 评级。ok 与 fast 都算正常，只有跨过警戒线才点亮。 */
function latencyToGrade(ms?: number | null): RowGrade {
  switch (latencyBand(ms, 'ttft')) {
    case 'fast':
    case 'ok':
      return 'good'
    case 'slow':
      return 'warn'
    case 'bad':
      return 'bad'
    default:
      return 'unknown'
  }
}

/**
 * 成功率 → 评级。分档本身走 rateBand（与文字着色共用一份阈值）。
 *
 * unknown 返回 good（该维度弃权）而非 unknown：被动表结构性没有成功率
 * （线上 usage_logs 只记成功请求）。若返 unknown，被动表每行至少 unknown、
 * 评级点全灰 —— 那就等于没有这个功能。
 */
function rateToGrade(v?: number | null, bands: readonly [number, number] = RATE_BANDS): RowGrade {
  const band = rateBand(v, bands)
  return band === 'unknown' ? 'good' : band
}

/**
 * 健康状态 → 评级。
 *
 * disabled 归 unknown 而非 bad：人工停用不是「坏」，是「不在观测范围内」，
 * 标红会让运维误以为出了故障。
 */
function healthToGrade(state?: string): RowGrade {
  switch (normalizeHealth(state)) {
    case 'healthy':
    case 'recovering':
      return 'good'
    case 'observing':
    case 'degraded':
      return 'warn'
    case 'suspended':
      return 'bad'
    case 'disabled':
      return 'unknown'
    default:
      return 'good'
  }
}

/**
 * 行级总评 = 三个维度里最差的那个。
 *
 * 取最差而非加权平均：这个点的用途是「这一行该不该看」，任一维度出问题就该点亮。
 * 平均会把「成功率 100% 但首字 8 秒」摊成一个中间色，
 * 恰好掩盖了唯一需要报告的事实。
 */
/** 被动卡片块内排序：差的在前，再 SLA 升序，再首字 P50 降序。空值沉底。 */
export function sortPassiveRows<T extends { sla: number | null; first_token_p50?: number | null }>(
  rows: readonly T[],
  gradeOf: (r: T) => RowGrade
): T[] {
  return [...rows].sort((a, b) => {
    const g = GRADE_RANK[gradeOf(b)] - GRADE_RANK[gradeOf(a)]
    if (g) return g
    const sa = a.sla
    const sb = b.sla
    if (sa == null && sb != null) return 1
    if (sb == null && sa != null) return -1
    if (sa != null && sb != null && sa !== sb) return sa - sb
    return (b.first_token_p50 ?? -1) - (a.first_token_p50 ?? -1)
  })
}

/** 被动卡片按成功率 / 请求次数排序；空值沉底，不修改入参。 */
export function sortStabilityRows<T extends { sla: number | null; requests: number }>(
  rows: readonly T[],
  key: 'sla' | 'requests',
  order: SortOrder
): T[] {
  return sortRows(rows, (r) => (key === 'sla' ? r.sla : r.requests), order)
}

export function rowGrade(input: {
  ttftMs?: number | null
  /** 被动表传流量 SLA；缺样本时为 null/undefined，该维度弃权 */
  successRate?: number | null
  healthState?: string
  /** 缺省走主动探测 95/80；被动传 PASSIVE_RATE_BANDS */
  rateBands?: readonly [number, number]
}): RowGrade {
  const grades: RowGrade[] = [
    latencyToGrade(input.ttftMs),
    rateToGrade(input.successRate, input.rateBands),
    healthToGrade(input.healthState)
  ]
  return grades.reduce((worst, g) => (GRADE_RANK[g] > GRADE_RANK[worst] ? g : worst), 'good')
}
