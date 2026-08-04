/**
 * 稳定性页的窗口档位、筛选、选项计数与行级评级纯函数（不依赖 Vue，便于单测）。
 *
 * 沿用 providerModel.ts 的哲学：后端全量返回，筛选一律本地完成 ——
 * 点 pill 切筛选是即时交互，走后端意味着每次点击都要等一次
 * percentile_cont 重算，而数据本就已经在前端了。
 */

import type { FilterOption } from '@/utils/providerModel'
import { latencyBand } from '@/utils/latencyBand'

/** 时间窗口档位（分钟）。实时盯盘口径，不是复盘口径。 */
export type WindowMinutes = 5 | 30 | 60 | 300 | 1440
export const WINDOW_OPTIONS: readonly WindowMinutes[] = [5, 30, 60, 300, 1440]

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

export type RateBand = 'good' | 'warn' | 'bad' | 'unknown'

/**
 * 成功率分档。缺值返 unknown —— 「这个数落在哪档」与「缺值意味着什么」
 * 是两件事，后者由调用方决定（见 rateToGrade 与 ActiveTable 的 rateClass，
 * 二者对缺值的处理刻意不同）。
 */
export function rateBand(v?: number | null): RateBand {
  if (v === null || v === undefined || !Number.isFinite(v)) return 'unknown'
  const [good, warn] = RATE_BANDS
  if (v >= good) return 'good'
  if (v >= warn) return 'warn'
  return 'bad'
}

/** 两张表结构不同，抽出筛选真正依赖的最小字段集。 */
export interface FilterableRow {
  account_id: number
  /** '' = 未归属 */
  provider_name: string
}

/** 账号 id → 健康状态的查表函数。由调用方注入，纯函数不必知道状态存在 Vue ref 里。 */
export type HealthLookup = (accountId: number) => string | undefined

/**
 * 按归属供应商 / 健康状态过滤（null 表示不限）。
 *
 * provider 传 '' 即筛「未归属」桶 —— 故必须用 null 而非 '' 表示不限。
 */
export function filterRows<T extends FilterableRow>(
  rows: readonly T[],
  provider: string | null,
  health: string | null,
  healthOf: HealthLookup
): T[] {
  let out = [...rows]
  if (provider !== null) {
    out = out.filter((r) => r.provider_name === provider)
  }
  if (health !== null) {
    out = out.filter((r) => normalizeHealth(healthOf(r.account_id)) === health)
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
}

/**
 * 按账号名 / 平台 / 归属供应商模糊搜索（大小写不敏感）。
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
      r.provider_name.toLowerCase().includes(q)
  )
}

/**
 * 抖动比可信所需的最小样本量。
 *
 * 低于此数时 percentile_cont(0.95) 基本就是最大值本身 —— 三次请求里
 * 偶然一次慢就会算出「6.2×」，既吓人又不含信息。宁可显示 '-'。
 */
export const JITTER_MIN_SAMPLES = 20

/**
 * 抖动比 = P95 / P50，衡量延迟的离散程度而非快慢。
 *
 * 这是被动表唯一回答「稳不稳」的指标：P50 800ms/P95 900ms 与
 * P50 800ms/P95 9000ms 在延迟列上完全一样，但只有后者是问题。
 *
 * 样本不足或 P50 为 0（无有效样本）时返回 null，由 compareValuesWithOrder 沉底。
 * P95 >= P50 对同一组分位数恒成立，故比值恒 >= 1，无需反向守卫。
 */
export function jitterRatio(p50?: number | null, p95?: number | null, samples?: number): number | null {
  if (samples === undefined || samples < JITTER_MIN_SAMPLES) return null
  if (p50 === null || p50 === undefined || !Number.isFinite(p50) || p50 <= 0) return null
  if (p95 === null || p95 === undefined || !Number.isFinite(p95)) return null
  return p95 / p50
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
function rateToGrade(v?: number | null): RowGrade {
  const band = rateBand(v)
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
export function rowGrade(input: {
  ttftMs?: number | null
  /** 被动表恒为 undefined —— 该维度弃权，见 rateToGrade */
  successRate?: number | null
  healthState?: string
}): RowGrade {
  const grades: RowGrade[] = [
    latencyToGrade(input.ttftMs),
    rateToGrade(input.successRate),
    healthToGrade(input.healthState)
  ]
  return grades.reduce((worst, g) => (GRADE_RANK[g] > GRADE_RANK[worst] ? g : worst), 'good')
}
