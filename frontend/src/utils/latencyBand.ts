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
 * 首字延迟阈值（ms）：<10s 优 / 10-30s 良 / 30-60s 警 / ≥60s 差。
 *
 * 对齐 sub2api 用量页 latencyHealth.ts（10s / 30s / 60s）。旧档（2s / 6s / 12s）
 * 会把现网常态（P50 3-8s）打成黄，颜色常亮即退化为噪音。
 */
const TTFT_BANDS = [10000, 30000, 60000] as const

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
