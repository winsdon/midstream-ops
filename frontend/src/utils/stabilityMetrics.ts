/**
 * 被动统计卡片的派生指标与展示格式（纯函数，不依赖 Vue）。
 *
 * 健康分是账号流量综合分，不是探测状态机，也不是运维看板那套含 CPU/DB 的分。
 */

import type { PassiveRow } from '@/types'
import { slaPercent } from '@/utils/stabilityModel'

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
    requests: row.requests,
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
  const weightOf = (r: PassiveRow) => r.requests
  let requests = 0
  let success = 0
  let error = 0
  const platforms: string[] = []
  const providers: string[] = []
  const groups: string[] = []
  for (const r of rows) {
    requests += r.requests
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
    requests,
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

/**
 * 账号健康分 0–100。
 *
 * 对常见 3–8s 首字、≤1% 错误率不扣分（参考图 P50 5s / 错误率 0.38% 应是满分）。
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
  return clamp(Math.round(100 - penalty), 0, 100)
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

function clamp(v: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, v))
}

/** ≥90 绿，<70 琥珀，中间中性。必须写完整字面量供 Tailwind 扫描。 */
export function healthScoreClass(score: number): string {
  if (score >= 90) return 'font-semibold text-emerald-600 dark:text-emerald-400'
  if (score < 70) return 'font-semibold text-amber-600 dark:text-amber-400'
  return 'font-semibold text-gray-700 dark:text-dark-300'
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
