import { describe, it, expect } from 'vitest'
import {
  buildSections,
  passiveCounts,
  activeCounts,
  applySectionSort,
  nextStabilitySort,
  allSectionsOpen,
  type GroupingMode
} from '@/utils/stabilitySections'
import type { RowGrade } from '@/utils/stabilityModel'

interface Row {
  account_id: number
  provider_name: string
  groups?: string[]
  requests: number
  success_count?: number
  error_count?: number
  grade: RowGrade
}

function r(
  id: number,
  provider: string,
  groups: string[],
  success: number,
  error: number,
  grade: RowGrade
): Row {
  return {
    account_id: id,
    provider_name: provider,
    groups,
    requests: success,
    success_count: success,
    error_count: error,
    grade
  }
}

const gradeOf = (row: Row) => row.grade

describe('passiveCounts / activeCounts', () => {
  it('被动优先 success_count，缺省回退 requests', () => {
    expect(passiveCounts({ requests: 10, error_count: 2 })).toEqual({ success: 10, error: 2 })
    expect(passiveCounts({ requests: 10, success_count: 8, error_count: 2 })).toEqual({
      success: 8,
      error: 2
    })
  })

  it('主动失败数 = total - success，且不为负', () => {
    expect(activeCounts({ success_count: 8, total: 10 })).toEqual({ success: 8, error: 2 })
    expect(activeCounts({ success_count: 10, total: 8 })).toEqual({ success: 10, error: 0 })
  })
})

describe('buildSections', () => {
  const rows = [
    r(1, '甲', ['pro', 'default'], 90, 10, 'warn'),
    r(2, '甲', ['default'], 50, 0, 'good'),
    r(3, '乙', ['pro'], 10, 10, 'bad'),
    r(4, '', [], 5, 0, 'good')
  ]

  function keys(mode: GroupingMode) {
    return buildSections(rows, mode, gradeOf, passiveCounts).map((s) => s.key)
  }

  it('按供应商分块不重复账号；未归属沉底', () => {
    const secs = buildSections(rows, 'provider', gradeOf, passiveCounts)
    expect(keys('provider')).toEqual(['乙', '甲', ''])
    const jia = secs.find((s) => s.key === '甲')!
    expect(jia.accountCount).toBe(2)
    expect(jia.rows.map((x) => x.account_id).sort()).toEqual([1, 2])
  })

  it('块 SLA 用求和而不是平均', () => {
    // 甲：acc1 90/100 + acc2 50/50 → 140/150，不是 (90+100)/2=95
    const jia = buildSections(rows, 'provider', gradeOf, passiveCounts).find((s) => s.key === '甲')!
    expect(jia.successCount).toBe(140)
    expect(jia.errorCount).toBe(10)
    expect(jia.sla).toBeCloseTo((140 / 150) * 100)
    expect(jia.grade).toBe('warn')
  })

  it('供应商外层在内层分组多于 1 个时展开；单桶省略内层标题', () => {
    const secs = buildSections(rows, 'provider', gradeOf, passiveCounts)
    const jia = secs.find((s) => s.key === '甲')!
    expect(jia.children.map((c) => c.key).sort()).toEqual(['default', 'pro'])
    const yi = secs.find((s) => s.key === '乙')!
    expect(yi.children).toEqual([])
  })

  it('按分组分块时多分组账号会在各组各出现一次', () => {
    const secs = buildSections(rows, 'group', gradeOf, passiveCounts)
    const pro = secs.find((s) => s.key === 'pro')!
    expect(pro.rows.map((x) => x.account_id).sort()).toEqual([1, 3])
    const def = secs.find((s) => s.key === 'default')!
    expect(def.rows.map((x) => x.account_id).sort()).toEqual([1, 2])
    expect(secs[secs.length - 1].key).toBe('')
  })

  it('最差评级上浮，坏的块排在前面', () => {
    const secs = buildSections(rows, 'provider', gradeOf, passiveCounts)
    expect(secs[0].key).toBe('乙')
    expect(secs[0].grade).toBe('bad')
  })
})

describe('applySectionSort', () => {
  const rows = [
    r(1, '甲', ['pro', 'default'], 90, 10, 'warn'),
    r(2, '甲', ['default'], 50, 0, 'good'),
    r(3, '乙', ['pro'], 10, 10, 'bad'),
    r(4, '', [], 5, 0, 'good')
  ]

  it('按成功率升序：低的在前，空桶沉底', () => {
    const secs = applySectionSort(
      buildSections(rows, 'provider', gradeOf, passiveCounts),
      'sla',
      'asc'
    )
    expect(secs.map((s) => s.key)).toEqual(['乙', '甲', ''])
  })

  it('按成功率降序：高的在前，空桶仍沉底', () => {
    const secs = applySectionSort(
      buildSections(rows, 'provider', gradeOf, passiveCounts),
      'sla',
      'desc'
    )
    expect(secs.map((s) => s.key)).toEqual(['甲', '乙', ''])
  })

  it('按请求次数降序：量大的在前', () => {
    const secs = applySectionSort(
      buildSections(rows, 'provider', gradeOf, passiveCounts),
      'requests',
      'desc'
    )
    // 甲 150、乙 20、空桶 5
    expect(secs.map((s) => s.key)).toEqual(['甲', '乙', ''])
  })

  it('内层子块跟随同一排序条件', () => {
    const jia = applySectionSort(
      buildSections(rows, 'provider', gradeOf, passiveCounts),
      'requests',
      'desc'
    ).find((s) => s.key === '甲')!
    // default: acc1+acc2  vis-à-vis pro: acc1 only
    expect(jia.children.map((c) => c.key)).toEqual(['default', 'pro'])
  })

  it('不修改入参数组', () => {
    const original = buildSections(rows, 'provider', gradeOf, passiveCounts)
    const snapshot = original.map((s) => s.key)
    applySectionSort(original, 'requests', 'desc')
    expect(original.map((s) => s.key)).toEqual(snapshot)
  })
})

describe('nextStabilitySort', () => {
  it('点当前列翻转方向', () => {
    expect(nextStabilitySort({ key: 'sla', order: 'asc' }, 'sla')).toEqual({
      key: 'sla',
      order: 'desc'
    })
    expect(nextStabilitySort({ key: 'requests', order: 'desc' }, 'requests')).toEqual({
      key: 'requests',
      order: 'asc'
    })
  })

  it('点新列：成功率从升序起，请求次数从降序起', () => {
    expect(nextStabilitySort({ key: 'requests', order: 'desc' }, 'sla')).toEqual({
      key: 'sla',
      order: 'asc'
    })
    expect(nextStabilitySort({ key: 'sla', order: 'asc' }, 'requests')).toEqual({
      key: 'requests',
      order: 'desc'
    })
  })
})

describe('allSectionsOpen', () => {
  it('无块不算全部展开', () => {
    expect(allSectionsOpen({}, [])).toBe(false)
  })

  it('每个块都开着才是全部展开', () => {
    expect(allSectionsOpen({ a: true, b: true }, ['a', 'b'])).toBe(true)
    expect(allSectionsOpen({ a: true, b: false }, ['a', 'b'])).toBe(false)
    expect(allSectionsOpen({ a: true }, ['a', 'b'])).toBe(false)
  })

  it('缺省（未写入）视为收起', () => {
    expect(allSectionsOpen({}, ['a'])).toBe(false)
  })
})
