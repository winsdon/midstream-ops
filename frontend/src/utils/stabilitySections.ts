/**
 * 稳定性列表的供应商 / 分组分块。纯函数，不依赖 Vue。
 *
 * 外层可切换（供应商 ↔ 分组），不再拆内层：供应商下按组拆会把同一账号复制多份，
 * 指标是账号级的，组只是标签。按分组分块时账号在每个所属组各出现一次（组成员视图）。
 */

import { compareValuesWithOrder, type SortOrder } from '@/utils/tableSort'
import {
  GRADE_RANK,
  slaPercent,
  type FilterableRow,
  type RowGrade
} from '@/utils/stabilityModel'

/** 面板列表的用户排序：成功率 / 请求次数。 */
export type StabilitySortKey = 'sla' | 'requests'

export interface StabilitySort {
  key: StabilitySortKey
  order: SortOrder
}

/** 成功率默认升序（差的在前）；请求次数默认降序（量大的在前）。 */
export const DEFAULT_STABILITY_SORT: StabilitySort = { key: 'sla', order: 'asc' }

export function nextStabilitySort(current: StabilitySort, clicked: StabilitySortKey): StabilitySort {
  if (current.key === clicked) {
    return { key: clicked, order: current.order === 'asc' ? 'desc' : 'asc' }
  }
  return { key: clicked, order: clicked === 'requests' ? 'desc' : 'asc' }
}

/** 每个块都显式为开才算全部展开；缺省（未写入）视为收起。无块不算展开。 */
export function allSectionsOpen(
  openByKey: Readonly<Record<string, boolean>>,
  keys: readonly string[]
): boolean {
  return keys.length > 0 && keys.every((k) => openByKey[k] === true)
}

export type GroupingMode = 'provider' | 'group'

export interface Countable {
  success: number
  error: number
}

export interface StabilitySection<T> {
  /** 桶键：供应商名或分组名；'' = 未归属 / 未分组 */
  key: string
  label: string
  rows: T[]
  /** 恒为空：不再按另一维拆内层 */
  children: StabilitySection<T>[]
  sla: number | null
  successCount: number
  errorCount: number
  accountCount: number
  /** SLA 分母：成功 + 失败（被动）或探测总次数（主动） */
  requestCount: number
  grade: RowGrade
}

export function passiveCounts(r: {
  requests: number
  success_count?: number
  error_count?: number
}): Countable {
  return {
    success: r.success_count ?? r.requests,
    error: r.error_count ?? 0
  }
}

export function activeCounts(r: { success_count: number; total: number }): Countable {
  return {
    success: r.success_count,
    error: Math.max(0, r.total - r.success_count)
  }
}

function bucketKeys(r: FilterableRow, dim: GroupingMode): string[] {
  if (dim === 'provider') return [r.provider_name]
  const gs = r.groups ?? []
  return gs.length === 0 ? [''] : [...gs]
}

function bucketize<T extends FilterableRow>(rows: readonly T[], dim: GroupingMode): Map<string, T[]> {
  const map = new Map<string, T[]>()
  for (const r of rows) {
    for (const key of bucketKeys(r, dim)) {
      const list = map.get(key)
      if (!list) {
        map.set(key, [r])
        continue
      }
      if (!list.some((x) => x.account_id === r.account_id)) list.push(r)
    }
  }
  return map
}

function makeSection<T extends FilterableRow>(
  key: string,
  rows: T[],
  gradeOf: (r: T) => RowGrade,
  countsOf: (r: T) => Countable
): StabilitySection<T> {
  let success = 0
  let error = 0
  let worst: RowGrade = 'good'
  for (const r of rows) {
    const c = countsOf(r)
    success += c.success
    error += c.error
    const g = gradeOf(r)
    if (GRADE_RANK[g] > GRADE_RANK[worst]) worst = g
  }
  return {
    key,
    label: key,
    rows,
    children: [],
    sla: slaPercent(success, error),
    successCount: success,
    errorCount: error,
    accountCount: rows.length,
    requestCount: success + error,
    grade: rows.length === 0 ? 'unknown' : worst
  }
}

/**
 * 空桶沉底；其余按最差评级（坏的在前）、再按 SLA 升序（低的在前）、再按名称。
 * SLA 缺值（无样本）排在有数字的后面、空桶前面。
 */
export function sortSections<T>(sections: readonly StabilitySection<T>[]): StabilitySection<T>[] {
  return [...sections].sort((a, b) => {
    const aEmpty = a.label === '' ? 1 : 0
    const bEmpty = b.label === '' ? 1 : 0
    if (aEmpty !== bEmpty) return aEmpty - bEmpty
    if (GRADE_RANK[b.grade] !== GRADE_RANK[a.grade]) return GRADE_RANK[b.grade] - GRADE_RANK[a.grade]
    if (a.sla === null && b.sla === null) {
      /* fall through */
    } else if (a.sla === null) return 1
    else if (b.sla === null) return -1
    else if (a.sla !== b.sla) return a.sla - b.sla
    return a.label.localeCompare(b.label)
  })
}

function sortValue<T>(sec: StabilitySection<T>, key: StabilitySortKey): number | null {
  return key === 'sla' ? sec.sla : sec.requestCount
}

function compareSections<T>(
  a: StabilitySection<T>,
  b: StabilitySection<T>,
  key: StabilitySortKey,
  order: SortOrder
): number {
  const aEmpty = a.label === '' ? 1 : 0
  const bEmpty = b.label === '' ? 1 : 0
  if (aEmpty !== bEmpty) return aEmpty - bEmpty
  return compareValuesWithOrder(sortValue(a, key), sortValue(b, key), order) || a.label.localeCompare(b.label)
}

/**
 * 按用户选择的成功率 / 请求次数重排分块。空桶仍沉底。
 * 返回新数组，不改入参。
 */
export function applySectionSort<T>(
  sections: readonly StabilitySection<T>[],
  key: StabilitySortKey,
  order: SortOrder
): StabilitySection<T>[] {
  return [...sections].sort((a, b) => compareSections(a, b, key, order))
}

export function buildSections<T extends FilterableRow>(
  rows: readonly T[],
  mode: GroupingMode,
  gradeOf: (r: T) => RowGrade,
  countsOf: (r: T) => Countable
): StabilitySection<T>[] {
  const outer = bucketize(rows, mode)
  const sections: StabilitySection<T>[] = []
  for (const [key, list] of outer) {
    sections.push(makeSection(key, list, gradeOf, countsOf))
  }
  return sortSections(sections)
}
