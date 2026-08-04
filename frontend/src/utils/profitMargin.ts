/**
 * 利润率计算与分档（纯函数，便于单测与跨组件复用）。
 *
 * 与 latencyBand 一样是展示决策，但分档阈值来自业务水位而非视觉手感 ——
 * 改阈值前先确认中转业务的实际毛利区间。
 */

/**
 * 利润率（百分点）。收益为 0 时返回 null —— 数学上未定义，不是 0%。
 *
 * 守卫用 `revenue > 0` 而非 `!revenue`：负收益会让符号翻转
 * （-50 / -100 = +50%，亏损显示成盈利）。当前收益是 actual_cost 求和不会为负，
 * 属潜在而非现存缺陷，但收口成公共函数时不该把洞带过去。
 *
 * 返回百分点而非 0~1 比值，与 fmtPct 的入参约定一致。
 */
export function profitMargin(revenue?: number | null, profit?: number | null): number | null {
  if (revenue === null || revenue === undefined || !Number.isFinite(revenue) || revenue <= 0) return null
  if (profit === null || profit === undefined || !Number.isFinite(profit)) return null
  return (profit / revenue) * 100
}

export type MarginBand = 'loss' | 'thin' | 'ok' | 'good' | 'unknown'

/**
 * 分档阈值（百分点）：<0 亏损 / 0-10 薄利 / 10-30 正常 / >30 良好。
 *
 * 薄利档单独拎出来是这个分档的意义所在 —— 8% 和 33% 同样是「盈利」，
 * 但前者只要上游涨一次价就转亏，而利润列的正负着色区分不了这两者。
 */
const MARGIN_BANDS = [0, 10, 30] as const

/** 分档。缺值一律 unknown，绝不落 loss 给出虚假的亏损信号。 */
export function marginBand(pct?: number | null): MarginBand {
  if (pct === null || pct === undefined || !Number.isFinite(pct)) return 'unknown'
  const [zero, thin, good] = MARGIN_BANDS
  if (pct < zero) return 'loss'
  if (pct < thin) return 'thin'
  if (pct < good) return 'ok'
  return 'good'
}

/** 供 tooltip 说明分档区间（百分点）。 */
export function marginBandThresholds(): readonly number[] {
  return MARGIN_BANDS
}
