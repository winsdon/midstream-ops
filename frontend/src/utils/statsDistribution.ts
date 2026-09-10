export interface DistItem {
  label: string
  value: number
}

export interface DistSlice {
  label: string
  value: number
  /** 占正值合计的百分比，0–100 */
  share: number
}

const DEFAULT_MAX_SLICES = 8

/**
 * 把分类金额收成环形图切片。
 *
 * 环形图只能表达正份额：零值和负值直接丢掉，不进「其他」。
 * 切片过多会糊成色块，超出 maxSlices 的尾部并入一个「其他」桶。
 * 不改动入参数组。
 */
export function buildDistribution(
  items: DistItem[],
  opts?: { maxSlices?: number; othersLabel?: string }
): DistSlice[] {
  const maxSlices = opts?.maxSlices ?? DEFAULT_MAX_SLICES
  const othersLabel = opts?.othersLabel ?? '其他'

  const positive = items
    .filter((item) => item.value > 0)
    .map((item) => ({ label: item.label, value: item.value }))
    .sort((a, b) => b.value - a.value)

  if (!positive.length) return []

  const capped =
    positive.length <= maxSlices
      ? positive
      : [
          ...positive.slice(0, maxSlices - 1),
          {
            label: othersLabel,
            value: positive.slice(maxSlices - 1).reduce((sum, item) => sum + item.value, 0)
          }
        ]

  const total = capped.reduce((sum, item) => sum + item.value, 0)
  return capped.map((item) => ({
    label: item.label,
    value: item.value,
    share: total > 0 ? (item.value / total) * 100 : 0
  }))
}
