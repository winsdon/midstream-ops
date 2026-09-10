/**
 * 被动统计色块：细桶补齐、显示格合并、80/50 分档。
 *
 * 后端按 window/60 发稀疏点；前端先补成 60 细桶，再折成 10–20 个方角块
 * （密度对齐 sub2api 渠道监控 90m/5min 那种矩阵，而不是 60 根细条）。
 */

import type { TimelinePoint } from '@/types'
import { slaPercent, type WindowMinutes } from '@/utils/stabilityModel'

export type { TimelinePoint }

export const TIMELINE_LENGTH = 60

/** 色块表列宽，表头与行共用。 */
export const HEATMAP_GRID =
  'grid-template-columns: minmax(10rem, 1.2fr) 4.5rem 4.5rem 5rem 5.5rem 4.5rem minmax(12rem, 2fr)'

/** 截图图例：健康 ≥80 / 需关注 50–79 / 异常 <50。 */
export const HEATMAP_HEALTH = 80
export const HEATMAP_WATCH = 50

export type CellTone = 'healthy' | 'watch' | 'bad' | 'empty'

export interface TimelineBar {
  tone: CellTone
  sla: number | null
  ok: number
  err: number
  t: string
  color: string
  duration_avg: number | null
  duration_p50: number | null
  duration_p90: number | null
  first_token_avg: number | null
  first_token_p50: number | null
  first_token_p90: number | null
  output_tokens: number
  duration_ms_sum: number
  input_tokens: number
  cache_read_tokens: number
}

/** 窗口 → 显示格数。必须整除 60。 */
export const DISPLAY_CELL_COUNT: Record<number, number> = {
  5: 10,
  30: 15,
  60: 12,
  360: 20,
  1440: 20
}

export function displayCellCount(minutes: WindowMinutes | number): number {
  return DISPLAY_CELL_COUNT[minutes] ?? 12
}

/** 窗口均分成 60 细桶。5m→5s，1h→1min，24h→24min。 */
export function timelineBucketMs(minutes: WindowMinutes | number): number {
  return (minutes * 60 * 1000) / TIMELINE_LENGTH
}

export function cellTone(sla: number | null | undefined): CellTone {
  if (sla == null || !Number.isFinite(sla)) return 'empty'
  if (sla >= HEATMAP_HEALTH) return 'healthy'
  if (sla >= HEATMAP_WATCH) return 'watch'
  return 'bad'
}

/** 行首圆点。必须写完整字面量供 Tailwind 扫描。 */
export const TONE_DOT: Record<CellTone, string> = {
  healthy: 'bg-emerald-500',
  watch: 'bg-amber-400',
  bad: 'bg-red-500',
  empty: 'bg-gray-300 dark:bg-dark-600'
}

const EMPTY_COLOR = 'rgb(229 231 235)'

export function cellColor(sla: number | null | undefined): string {
  if (sla == null || Number.isNaN(sla)) return EMPTY_COLOR
  return hslForPct(sla) ?? EMPTY_COLOR
}

function num(v?: number | null): number | null {
  return v == null || !Number.isFinite(v) ? null : v
}

function sumField(a?: number, b?: number): number {
  return (a ?? 0) + (b ?? 0)
}

function wavg(a: number | null, aw: number, b: number | null, bw: number): number | null {
  let n = 0
  let d = 0
  if (a != null && aw > 0) {
    n += a * aw
    d += aw
  }
  if (b != null && bw > 0) {
    n += b * bw
    d += bw
  }
  return d > 0 ? n / d : null
}

export function barFromPoint(p: TimelinePoint): Omit<TimelineBar, 't'> {
  const sla = slaPercent(p.ok, p.err)
  const tone = cellTone(sla)
  return {
    tone,
    sla,
    color: cellColor(sla),
    ok: p.ok,
    err: p.err,
    duration_avg: num(p.duration_avg),
    duration_p50: num(p.duration_p50),
    duration_p90: num(p.duration_p90),
    first_token_avg: num(p.first_token_avg),
    first_token_p50: num(p.first_token_p50),
    first_token_p90: num(p.first_token_p90),
    output_tokens: p.output_tokens ?? 0,
    duration_ms_sum: p.duration_ms_sum ?? 0,
    input_tokens: p.input_tokens ?? 0,
    cache_read_tokens: p.cache_read_tokens ?? 0
  }
}

export function barFromCounts(ok: number, err: number): Omit<TimelineBar, 't'> {
  return barFromPoint({ t: '', ok, err })
}

function mergeBars(a: Omit<TimelineBar, 't'>, b: Omit<TimelineBar, 't'>): Omit<TimelineBar, 't'> {
  const ok = a.ok + b.ok
  const err = a.err + b.err
  const sla = slaPercent(ok, err)
  return {
    ok,
    err,
    sla,
    tone: cellTone(sla),
    color: cellColor(sla),
    duration_avg: wavg(a.duration_avg, a.ok, b.duration_avg, b.ok),
    duration_p50: wavg(a.duration_p50, a.ok, b.duration_p50, b.ok),
    duration_p90: wavg(a.duration_p90, a.ok, b.duration_p90, b.ok),
    first_token_avg: wavg(a.first_token_avg, a.ok, b.first_token_avg, b.ok),
    first_token_p50: wavg(a.first_token_p50, a.ok, b.first_token_p50, b.ok),
    first_token_p90: wavg(a.first_token_p90, a.ok, b.first_token_p90, b.ok),
    output_tokens: sumField(a.output_tokens, b.output_tokens),
    duration_ms_sum: sumField(a.duration_ms_sum, b.duration_ms_sum),
    input_tokens: sumField(a.input_tokens, b.input_tokens),
    cache_read_tokens: sumField(a.cache_read_tokens, b.cache_read_tokens)
  }
}

/**
 * 把稀疏点补成 60 细桶。generatedAt 是窗口右端（不含）。
 */
export function padTimeline(
  points: readonly TimelinePoint[],
  minutes: WindowMinutes | number,
  generatedAt: string
): TimelineBar[] {
  const bucketMs = timelineBucketMs(minutes)
  const end = Date.parse(generatedAt)
  const origin = Number.isFinite(end) ? end - minutes * 60 * 1000 : 0
  const byIndex = new Map<number, TimelinePoint>()
  for (const p of points) {
    const ts = Date.parse(p.t)
    if (!Number.isFinite(ts)) continue
    const idx = Math.round((ts - origin) / bucketMs)
    if (idx < 0 || idx >= TIMELINE_LENGTH) continue
    byIndex.set(idx, p)
  }
  const bars: TimelineBar[] = []
  for (let i = 0; i < TIMELINE_LENGTH; i++) {
    const p = byIndex.get(i)
    const bar = p ? barFromPoint(p) : barFromCounts(0, 0)
    bars.push({ ...bar, t: new Date(origin + i * bucketMs).toISOString() })
  }
  return bars
}

/** 细桶折成显示格：同格 ok/err 相加。 */
export function displayCells(
  points: readonly TimelinePoint[],
  minutes: WindowMinutes | number,
  generatedAt: string
): TimelineBar[] {
  const fine = padTimeline(points, minutes, generatedAt)
  const count = displayCellCount(minutes)
  const per = TIMELINE_LENGTH / count
  if (!Number.isInteger(per) || per <= 0) return fine
  const cells: TimelineBar[] = []
  for (let i = 0; i < count; i++) {
    const start = i * per
    let acc: Omit<TimelineBar, 't'> = { ...barFromCounts(0, 0) }
    for (let j = 0; j < per; j++) {
      const { t: _t, ...rest } = fine[start + j]
      acc = mergeBars(acc, rest)
    }
    cells.push({ ...acc, t: fine[start].t })
  }
  return cells
}

/** 同时间桶统计相加。返回新数组，按时间升序。 */
export function mergeTimelines(list: readonly (readonly TimelinePoint[])[]): TimelinePoint[] {
  const map = new Map<string, TimelinePoint>()
  for (const points of list) {
    for (const p of points) {
      const cur = map.get(p.t)
      if (!cur) {
        map.set(p.t, { ...p })
        continue
      }
      const merged = mergeBars(barFromPoint(cur), barFromPoint(p))
      map.set(p.t, {
        t: p.t,
        ok: merged.ok,
        err: merged.err,
        duration_avg: merged.duration_avg,
        duration_p50: merged.duration_p50,
        duration_p90: merged.duration_p90,
        first_token_avg: merged.first_token_avg,
        first_token_p50: merged.first_token_p50,
        first_token_p90: merged.first_token_p90,
        output_tokens: merged.output_tokens,
        duration_ms_sum: merged.duration_ms_sum,
        input_tokens: merged.input_tokens,
        cache_read_tokens: merged.cache_read_tokens
      })
    }
  }
  return [...map.values()].sort((a, b) => Date.parse(a.t) - Date.parse(b.t))
}

/** 行首实时点：最右非空显示格。 */
export function liveToneFromCells(cells: readonly TimelineBar[]): CellTone {
  for (let i = cells.length - 1; i >= 0; i--) {
    if (cells[i].tone !== 'empty') return cells[i].tone
  }
  return 'empty'
}

/** 可用率红→黄→绿。与 sub2api hslForPct 同一公式。 */
export function hslForPct(pct: number | null | undefined): string | undefined {
  if (pct === null || pct === undefined || Number.isNaN(pct)) return undefined
  const clamped = Math.max(0, Math.min(100, pct))
  return `hsl(${clamped * 1.2} 72% 42%)`
}

export function cellEndIso(startIso: string, minutes: WindowMinutes | number): string {
  const start = Date.parse(startIso)
  if (!Number.isFinite(start)) return startIso
  const width = (minutes * 60 * 1000) / displayCellCount(minutes)
  return new Date(start + width).toISOString()
}
