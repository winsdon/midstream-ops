/**
 * 稳定性列表的供应商 / 分组分块。纯函数，不依赖 Vue。
 *
 * 外层可切换（供应商 ↔ 分组），内层自动带另一维；内层只剩一个桶时省略，
 * 免得整页都是「未分组」小标题。账号行仍是「每账号一行」，
 * 多分组账号在按分组分块时会在各组各出现一次。
 */

import {
  GRADE_RANK,
  slaPercent,
  type FilterableRow,
  type RowGrade
} from '@/utils/stabilityModel'

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
  /** 内层块。长度 ≤1 时为空，调用方不要渲染内层标题 */
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

function innerDim(mode: GroupingMode): GroupingMode {
  return mode === 'provider' ? 'group' : 'provider'
}

function makeSection<T extends FilterableRow>(
  key: string,
  rows: T[],
  gradeOf: (r: T) => RowGrade,
  countsOf: (r: T) => Countable,
  children: StabilitySection<T>[]
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
    children,
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

export function buildSections<T extends FilterableRow>(
  rows: readonly T[],
  mode: GroupingMode,
  gradeOf: (r: T) => RowGrade,
  countsOf: (r: T) => Countable
): StabilitySection<T>[] {
  const outer = bucketize(rows, mode)
  const inner = innerDim(mode)
  const sections: StabilitySection<T>[] = []
  for (const [key, list] of outer) {
    const innerMap = bucketize(list, inner)
    const childSecs: StabilitySection<T>[] = []
    for (const [ik, ilist] of innerMap) {
      childSecs.push(makeSection(ik, ilist, gradeOf, countsOf, []))
    }
    const children = innerMap.size > 1 ? sortSections(childSecs) : []
    sections.push(makeSection(key, list, gradeOf, countsOf, children))
  }
  return sortSections(sections)
}
