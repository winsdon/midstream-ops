/**
 * 被动统计卡片的派生指标与展示格式（纯函数，不依赖 Vue）。
 *
 * 健康分是账号流量综合分，不是探测状态机，也不是运维看板那套含 CPU/DB 的分。
 */

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
