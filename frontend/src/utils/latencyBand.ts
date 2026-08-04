/**
 * 延迟分档（纯函数，便于单测与跨组件复用）。
 *
 * 与 CreditUsageBar 的阈值不同，这里的阈值没有需要对齐的服务端告警逻辑 ——
 * 它纯粹是展示决策，改阈值不牵动后端。
 */

export type LatencyBand = 'fast' | 'ok' | 'slow' | 'bad' | 'unknown'

/** 首字 与 总耗时 用两套阈值，见下方常量注释。 */
export type LatencyKind = 'ttft' | 'total'

/**
 * 首字延迟阈值（ms）：<2s 优 / 2-6s 良 / 6-12s 警 / >12s 差。
 *
 * 取宽松档而非严格档：这一列是常驻盯盘视野的，若正常波动也频繁变黄，
 * 颜色就退化成背景噪音，真正劣化时反而看不见。
 *
 * 阈值由现网样本反推而非凭感觉：旧档（1.5/3/6s）下随机十条真实请求有八条
 * 落在告警色，等于整列常亮。现档下同一批样本只剩两条琥珀 —— 正是最慢的那两条。
 */
const TTFT_BANDS = [2000, 6000, 12000] as const

/**
 * 总耗时阈值（ms）：整段响应天然比首字长一个量级 ——
 * 长回复跑几十秒是正常的（总耗时 = 首字 + 输出时长，而输出时长由回复长度决定，
 * 不由上游快慢决定），套 TTFT 阈值会让这一列齐刷刷变红，颜色失去区分力。
 *
 * 60s 这条线对齐常见客户端请求超时：越过它就不是「慢」而是「失败」了。
 */
const TOTAL_BANDS = [10000, 30000, 60000] as const

/**
 * 分档。null / undefined / 非有限数 / 负数一律 unknown ——
 * 「没有样本」不是「很快」，绝不能落到 fast 档给出虚假的绿色。
 */
export function latencyBand(ms?: number | null, kind: LatencyKind = 'ttft'): LatencyBand {
  if (ms === null || ms === undefined || !Number.isFinite(ms) || ms < 0) return 'unknown'
  const [good, warn, bad] = kind === 'total' ? TOTAL_BANDS : TTFT_BANDS
  if (ms < good) return 'fast'
  if (ms < warn) return 'ok'
  if (ms < bad) return 'slow'
  return 'bad'
}

/** 供 tooltip 说明当前档位的阈值区间（ms），便于用户理解颜色依据。见 LatencyCell。 */
export function latencyBandThresholds(kind: LatencyKind = 'ttft'): readonly number[] {
  return kind === 'total' ? TOTAL_BANDS : TTFT_BANDS
}
