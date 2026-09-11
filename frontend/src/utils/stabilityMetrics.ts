/**
 * 被动统计卡片的派生指标与展示格式（纯函数，不依赖 Vue）。
 *
 * 健康分是账号流量综合分，不是探测状态机，也不是运维看板那套含 CPU/DB 的分。
 */

import type { PassiveRow, TimelinePoint } from '@/types'
import { accountHealthScore, slaPercent } from '@/utils/stabilityModel'
import { cellEndIso, mergeTimelines, type TimelineBar } from '@/utils/stabilityTimeline'
import { sortRows, type SortOrder } from '@/utils/tableSort'

export { accountHealthScore }

export type HeatmapSortKey = 'sla' | 'requests' | 'firstToken' | 'tps' | 'cache'

export const HEATMAP_SORT_KEYS: readonly HeatmapSortKey[] = [
  'sla',
  'requests',
  'firstToken',
  'tps',
  'cache'
]

export interface HeatmapSort {
  key: HeatmapSortKey
  order: SortOrder
}

/** 成功率/缓存默认升序（差的在前）；请求数、首字默认降序（量大/更慢的在前）。 */
const SORT_START_DESC: ReadonlySet<HeatmapSortKey> = new Set(['requests', 'firstToken'])

export const DEFAULT_HEATMAP_SORT: HeatmapSort = { key: 'requests', order: 'desc' }

export function nextHeatmapSort(current: HeatmapSort, clicked: HeatmapSortKey): HeatmapSort {
  if (current.key === clicked) {
    return { key: clicked, order: current.order === 'asc' ? 'desc' : 'asc' }
  }
  return { key: clicked, order: SORT_START_DESC.has(clicked) ? 'desc' : 'asc' }
}

function heatmapSortValue(m: StatusCardModel, key: HeatmapSortKey): number | null {
  switch (key) {
    case 'sla':
      return m.sla
    case 'requests':
      return m.requests
    case 'firstToken':
      return m.firstTokenP50
    case 'tps':
      return m.tokensPerSecond
    case 'cache':
      return m.cacheRate
  }
}

export function sortHeatmapModels(
  rows: readonly StatusCardModel[],
  key: HeatmapSortKey,
  order: SortOrder
): StatusCardModel[] {
  return sortRows(rows, (r) => heatmapSortValue(r, key), order)
}

/** 色块行（外层块与弹窗子行共用）。 */
export interface StatusCardModel {
  key: string
  title: string
  subtitle: string
  platform: string
  accountCount?: number
  sla: number | null
  firstTokenP50: number | null
  durationP50: number | null
  tokensPerSecond: number | null
  cacheRate: number | null
  requests: number
  successCount: number
  errorCount: number
  timeline: TimelinePoint[]
}

export interface PageKpis {
  sla: number | null
  firstTokenP50: number | null
  tokensPerSecond: number | null
  cacheRate: number | null
  requests: number
  rpm: number | null
}

export function cardFromRow(row: PassiveRow, subtitle: string): StatusCardModel {
  return {
    key: String(row.account_id),
    title: row.account_name,
    subtitle,
    platform: row.platform,
    sla: row.sla,
    firstTokenP50: row.first_token_p50,
    durationP50: row.duration_p50,
    tokensPerSecond: row.tokens_per_second,
    cacheRate: row.cache_rate,
    requests: row.requests,
    successCount: row.success_count,
    errorCount: row.error_count,
    timeline: [...(row.timeline ?? [])]
  }
}

export function cardFromSection(rows: readonly PassiveRow[], title: string): StatusCardModel {
  const agg = aggregatePassiveRows(rows, title)
  return {
    key: title,
    title,
    subtitle: '',
    platform: agg.platform,
    accountCount: agg.accountCount,
    sla: agg.sla,
    firstTokenP50: agg.first_token_p50,
    durationP50: agg.duration_p50,
    tokensPerSecond: agg.tokens_per_second,
    cacheRate: agg.cache_rate,
    requests: agg.requests,
    successCount: agg.success_count,
    errorCount: agg.error_count,
    timeline: mergeTimelines(rows.map((r) => r.timeline ?? []))
  }
}

export function pageKpis(rows: readonly PassiveRow[], minutes: number): PageKpis {
  const agg = aggregatePassiveRows(rows, '')
  return {
    sla: agg.sla,
    firstTokenP50: agg.first_token_p50,
    tokensPerSecond: agg.tokens_per_second,
    cacheRate: agg.cache_rate,
    requests: agg.requests,
    rpm: rpm(agg.requests, minutes)
  }
}

/** 被动详情弹窗：账号行与供应商/分组块共用。 */
export interface PassiveDetail {
  title: string
  platform: string
  provider_name: string
  groups: string[]
  /** 块级才有：该面板下的账号数 */
  accountCount?: number
  requests: number
  success_count: number
  error_count: number
  sla: number | null
  duration_avg: number | null
  duration_p50: number | null
  duration_p90: number | null
  first_token_avg: number | null
  first_token_p50: number | null
  first_token_p90: number | null
  tokens_per_second: number | null
  cache_rate: number | null
}

/** 按权重平均；缺值与非正权重跳过。无有效样本返回 null。 */
export function weightedAvg(
  items: readonly { weight: number; value: number | null | undefined }[]
): number | null {
  let num = 0
  let den = 0
  for (const it of items) {
    if (it.value == null || !Number.isFinite(it.value) || !Number.isFinite(it.weight) || it.weight <= 0) {
      continue
    }
    num += it.value * it.weight
    den += it.weight
  }
  return den > 0 ? num / den : null
}

function uniqueStrings(values: readonly string[]): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const raw of values) {
    const v = raw.trim()
    if (!v || seen.has(v)) continue
    seen.add(v)
    out.push(v)
  }
  return out
}

export function passiveDetailFromRow(row: PassiveRow): PassiveDetail {
  return {
    title: row.account_name,
    platform: row.platform,
    provider_name: row.provider_name,
    groups: [...(row.groups ?? [])],
    requests: row.success_count + row.error_count,
    success_count: row.success_count,
    error_count: row.error_count,
    sla: row.sla,
    duration_avg: row.duration_avg,
    duration_p50: row.duration_p50,
    duration_p90: row.duration_p90,
    first_token_avg: row.first_token_avg,
    first_token_p50: row.first_token_p50,
    first_token_p90: row.first_token_p90,
    tokens_per_second: row.tokens_per_second,
    cache_rate: row.cache_rate
  }
}

/**
 * 把一块里的账号行合成详情。延迟/吞吐/缓存按请求次数加权平均
 * （前端没有原始样本，无法重算真分位数）。SLA 用成功/失败求和。
 */
export function aggregatePassiveRows(rows: readonly PassiveRow[], title: string): PassiveDetail {
  const weightOf = (r: PassiveRow) => r.success_count
  let success = 0
  let error = 0
  const platforms: string[] = []
  const providers: string[] = []
  const groups: string[] = []
  for (const r of rows) {
    success += r.success_count
    error += r.error_count
    platforms.push(r.platform)
    providers.push(r.provider_name)
    for (const g of r.groups ?? []) groups.push(g)
  }
  const w = (pick: (r: PassiveRow) => number | null) =>
    weightedAvg(rows.map((r) => ({ weight: weightOf(r), value: pick(r) })))
  return {
    title,
    platform: uniqueStrings(platforms).join(' · '),
    provider_name: uniqueStrings(providers).join(' · '),
    groups: uniqueStrings(groups),
    accountCount: rows.length,
    requests: success + error,
    success_count: success,
    error_count: error,
    sla: slaPercent(success, error),
    duration_avg: w((r) => r.duration_avg),
    duration_p50: w((r) => r.duration_p50),
    duration_p90: w((r) => r.duration_p90),
    first_token_avg: w((r) => r.first_token_avg),
    first_token_p50: w((r) => r.first_token_p50),
    first_token_p90: w((r) => r.first_token_p90),
    tokens_per_second: w((r) => r.tokens_per_second),
    cache_rate: w((r) => r.cache_rate)
  }
}

export function errorRatePercent(success: number, errorCount: number): number | null {
  if (!Number.isFinite(success) || !Number.isFinite(errorCount)) return null
  const total = success + errorCount
  if (total <= 0) return null
  return (errorCount / total) * 100
}

export function rpm(requests: number, minutes: number): number | null {
  if (!Number.isFinite(requests) || !Number.isFinite(minutes) || minutes <= 0) return null
  return requests / minutes
}

/** ≥90 绿，70–89 琥珀，<70 红。必须写完整字面量供 Tailwind 扫描。 */
export function healthScoreClass(score: number): string {
  if (score >= 90) return 'font-semibold text-emerald-600 dark:text-emerald-400'
  if (score >= 70) return 'font-semibold text-amber-600 dark:text-amber-400'
  return 'font-semibold text-red-600 dark:text-red-400'
}

export function formatRpm(v?: number | null): string {
  if (v == null || !Number.isFinite(v) || v < 0) return '-'
  if (Number.isInteger(v)) return String(v)
  return v.toFixed(1)
}

export function formatTokenRate(v?: number | null): string {
  if (v == null || !Number.isFinite(v) || v <= 0) return '-'
  if (v >= 1_000_000) return trimFloat(v / 1_000_000) + 'M'
  if (v >= 1000) return trimFloat(v / 1000) + 'K'
  return String(Math.round(v))
}

function trimFloat(v: number): string {
  return v.toFixed(1).replace(/\.0$/, '')
}

export interface CellTip {
  empty: boolean
  start: string
  end: string
  health: number
  sla: number | null
  errorRate: number | null
  firstTokenAvg: number | null
  firstTokenP50: number | null
  firstTokenP90: number | null
  durationAvg: number | null
  durationP50: number | null
  durationP90: number | null
  tps: number | null
  cache: number | null
  rpm: number | null
}

function tpsOf(outputTokens: number, durationMsSum: number): number | null {
  if (!Number.isFinite(outputTokens) || !Number.isFinite(durationMsSum) || durationMsSum <= 0) return null
  return outputTokens / (durationMsSum / 1000)
}

function cacheOf(cacheRead: number, input: number): number | null {
  const total = cacheRead + input
  if (!Number.isFinite(total) || total <= 0) return null
  return (cacheRead / total) * 100
}

export function cellTip(
  bar: TimelineBar,
  opts: { windowMinutes: number; cellMinutes: number }
): CellTip {
  const empty = bar.ok + bar.err <= 0
  const errPct = errorRatePercent(bar.ok, bar.err)
  return {
    empty,
    start: bar.t,
    end: cellEndIso(bar.t, opts.windowMinutes),
    health: empty
      ? 0
      : accountHealthScore({ sla: bar.sla, ttftP50: bar.first_token_p50, errorRate: errPct }),
    sla: bar.sla,
    errorRate: errPct,
    firstTokenAvg: bar.first_token_avg,
    firstTokenP50: bar.first_token_p50,
    firstTokenP90: bar.first_token_p90,
    durationAvg: bar.duration_avg,
    durationP50: bar.duration_p50,
    durationP90: bar.duration_p90,
    tps: tpsOf(bar.output_tokens, bar.duration_ms_sum),
    cache: cacheOf(bar.cache_read_tokens, bar.input_tokens),
    rpm: rpm(bar.ok + bar.err, opts.cellMinutes)
  }
}

export function formatLatencyTriple(parts: {
  avg?: number | null
  p50?: number | null
  p90?: number | null
}): string {
  return `AVG ${fmtLatency(parts.avg)} · P50 ${fmtLatency(parts.p50)} · P90 ${fmtLatency(parts.p90)}`
}

function fmtLatency(ms?: number | null): string {
  if (ms == null || !Number.isFinite(ms) || ms < 0) return '-'
  if (ms >= 1000) return (ms / 1000).toFixed(1) + 's'
  return Math.round(ms) + 'ms'
}
