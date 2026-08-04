/**
 * 表格本地排序的比较与排序纯函数（不依赖 Vue，便于复用与单测）。
 *
 * 沿用 providerModel.ts 确立的排序哲学：后端 List 全量返回且不解析 query 参数，
 * 数据本就全在前端，故排序一律本地完成，零后端改动。
 *
 * 唯一的例外是授信台账 —— 它有后端分页，本地排序只能排到当前页，
 * 「这 20 人里敞口最大的」会被误读成「敞口最大的」，故那张表走后端排序。
 */

export type SortOrder = 'asc' | 'desc'

/** 数字优先的自然序比较器：'acc2' < 'acc10'，且大小写不敏感。 */
const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' })

/**
 * 空值判定：null / undefined / 空串，以及非有限数字（NaN、±Infinity）。
 *
 * 把 NaN 归入空值而不是留给字符串比较，是因为它在这些表里的来源都是
 * 「算不出来」（除零的成功率、无样本的均值）—— 那就是没有数据。若不拦，
 * toFiniteNumber 会返回 null 使其回退字符串比较，"NaN" 按字典序排到
 * 数字前面，缺数据的行反而冒到榜首。
 */
function isEmpty(v: unknown): boolean {
  if (v === null || v === undefined || v === '') return true
  return typeof v === 'number' && !Number.isFinite(v)
}

/** 能转成有限数字则返回该数，否则 null（回退字符串比较）。 */
function toFiniteNumber(v: unknown): number | null {
  if (typeof v === 'number') return Number.isFinite(v) ? v : null
  if (typeof v === 'boolean') return v ? 1 : 0
  if (typeof v === 'string') {
    const s = v.trim()
    if (!s) return null
    const n = Number(s)
    return Number.isFinite(n) ? n : null
  }
  return null
}

function toStr(v: unknown): string {
  if (v === null || v === undefined) return ''
  if (typeof v === 'string') return v
  if (v instanceof Date) return v.toISOString()
  return String(v)
}

/**
 * 空值沉底比较：仅看「谁是空的」，两侧同为空或同为非空都返回 0。
 *
 * 单独拆出来是因为它的结果**不可乘方向**——「没有数据」不是「值最小」，
 * 升序降序下空值都该在最后。调用方若把它和有值比较的结果混在一个数字里
 * 再统一乘 dir，沉底语义就会在降序时被翻成冒顶，故用 compareValuesWithOrder
 * 把两者的方向处理隔开。
 */
function compareEmptyLast(a: unknown, b: unknown): number {
  const ae = isEmpty(a)
  const be = isEmpty(b)
  if (ae === be) return 0
  return ae ? 1 : -1
}

/**
 * 比较两个非空单元格值：两侧可转数字则数值比，否则自然序字符串比。
 * 空值语义由 compareEmptyLast 单独负责，这里不掺和。
 */
function comparePresent(a: unknown, b: unknown): number {
  const an = toFiniteNumber(a)
  const bn = toFiniteNumber(b)
  if (an !== null && bn !== null) return an === bn ? 0 : an < bn ? -1 : 1

  return collator.compare(toStr(a), toStr(b))
}

/**
 * 带方向的单元格比较：空值恒末尾（不受方向影响），有值的两行之间才按 order 翻转。
 *
 * 把方向收进比较器而不是让调用方乘 dir，是为了让「空值沉底」在结构上无法被翻转 ——
 * 曾经三个调用方各写一遍 `compareValues(...) * dir`，三处都把空值在降序时顶到了
 * 榜首，正是因为方向是在比较之外施加的。方向无关的裸比较器不再导出，就没人能再
 * 踩同一个坑。
 */
export function compareValuesWithOrder(a: unknown, b: unknown, order: SortOrder): number {
  const empty = compareEmptyLast(a, b)
  if (empty !== 0) return empty
  const c = comparePresent(a, b)
  // c 为 0 时直接返回，避免 -c 产出 -0（排序无害，但比较器返回 -0 不干净）
  if (c === 0) return 0
  return order === 'desc' ? -c : c
}

/**
 * 按 pick 取值排序，返回新数组（不修改入参）。
 *
 * 用下标做 tie-break 保证稳定：空值恒末尾的规则会让「空值组内部」全部返回 0，
 * 此时保留入参顺序（通常是后端给的业务默认序）比让引擎自由决定更可预期。
 */
export function sortRows<T>(rows: readonly T[], pick: (row: T) => unknown, order: SortOrder): T[] {
  return rows
    .map((row, i) => ({ row, i }))
    .sort((x, y) => compareValuesWithOrder(pick(x.row), pick(y.row), order) || x.i - y.i)
    .map((x) => x.row)
}
