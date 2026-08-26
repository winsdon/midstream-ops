/**
 * 授信台账列表里「余额告警」进度条的纯函数。
 *
 * 比例 = 线上余额 / 有效阈值。条宽封顶 100%，文字不封顶（420% 表示远高于安全线）。
 * 分档只跟后端跌破语义对齐：ratio < 1 告警，≥ 1 安全。不发明琥珀中间档。
 */

export type BalanceAlertTone = 'muted' | 'danger' | 'ok'

/** 未扫描或阈值无意义时返回 null，调用方据此显示 — 并走灰色条。 */
export function balanceAlertRatio(
  balance: number | null | undefined,
  threshold: number
): number | null {
  if (balance === null || balance === undefined || !Number.isFinite(balance)) return null
  if (!(threshold > 0) || !Number.isFinite(threshold)) return null
  return balance / threshold
}

/** 条宽百分比，0~100。null 当 0，避免未扫描时画出假的满条。 */
export function balanceAlertBarWidth(ratio: number | null): number {
  if (ratio === null || !Number.isFinite(ratio)) return 0
  return Math.min(Math.max(ratio * 100, 0), 100)
}

export function balanceAlertTone(ratio: number | null): BalanceAlertTone {
  if (ratio === null || !Number.isFinite(ratio)) return 'muted'
  if (ratio < 1) return 'danger'
  return 'ok'
}

/** 必须写完整字面量：Tailwind 扫描源码文本提取类名。 */
export function balanceAlertBarClass(tone: BalanceAlertTone): string {
  switch (tone) {
    case 'danger':
      return 'bg-red-500'
    case 'ok':
      return 'bg-emerald-500'
    default:
      return 'bg-gray-300 dark:bg-dark-600'
  }
}

export function balanceAlertTextClass(tone: BalanceAlertTone): string {
  switch (tone) {
    case 'danger':
      return 'font-semibold text-red-600 dark:text-red-400'
    case 'ok':
      return 'text-gray-500 dark:text-dark-400'
    default:
      return 'text-gray-400'
  }
}
