/**
 * 自营站运营成本的展示模型（纯函数，便于单测与跨组件复用）。
 *
 * 类别枚举必须与后端 service/operating_cost_service.go 的常量一致：
 * 后端是唯一校验入口，前端多出的值提交时会被 400 打回。
 */

import type { OperatingCost, OperatingCostCategory } from '@/types'

/** 类别选项顺序 = 表单下拉与统计小计的展示顺序，按使用频率排列。 */
export const OPERATING_COST_CATEGORIES: readonly OperatingCostCategory[] = [
  'account',
  'subscription',
  'server',
  'other'
] as const

/** i18n key，供 t() 取标签。集中在此避免各组件各拼一次字符串。 */
export function categoryLabelKey(category: string): string {
  return isOperatingCostCategory(category)
    ? `opcost.categories.${category}`
    : 'opcost.categories.other'
}

/** 收窄类型：后端新增枚举而前端未同步时，落回 other 而不是渲染出裸的原始值。 */
export function isOperatingCostCategory(v: string): v is OperatingCostCategory {
  return (OPERATING_COST_CATEGORIES as readonly string[]).includes(v)
}

/**
 * 按类别汇总，返回值恒按 OPERATING_COST_CATEGORIES 顺序，且只含有金额的类别。
 *
 * 过滤零值类别：弹窗里列出四个「$0.00」只是噪音；但保持顺序稳定，
 * 让同一站点在不同区间之间切换时小计不会跳来跳去。
 */
export function sumByCategory(
  items: readonly OperatingCost[] | null | undefined
): { category: OperatingCostCategory; amount: number }[] {
  if (!items?.length) return []

  const sums = new Map<OperatingCostCategory, number>()
  for (const it of items) {
    const key = isOperatingCostCategory(it.category) ? it.category : 'other'
    sums.set(key, (sums.get(key) ?? 0) + it.amount)
  }
  return OPERATING_COST_CATEGORIES.filter((c) => (sums.get(c) ?? 0) !== 0).map((c) => ({
    category: c,
    amount: roundAmount(sums.get(c) ?? 0)
  }))
}

/**
 * 金额归一到分。
 *
 * 与后端 roundAmount 同口径：浮点求和会产生 0.30000000000000004 这类尾数，
 * 直接展示会露出实现细节。合计由前端从明细算出（见 sumByCategory / totalAmount），
 * 故这一步必须在前端也做一次。
 */
export function roundAmount(v: number): number {
  return Math.round(v * 100) / 100
}

/** 明细合计。空列表返回 0，不返回 null —— 调用方可直接参与算术。 */
export function totalAmount(items: readonly OperatingCost[] | null | undefined): number {
  if (!items?.length) return 0
  return roundAmount(items.reduce((sum, it) => sum + it.amount, 0))
}
