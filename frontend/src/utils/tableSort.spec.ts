import { describe, it, expect } from 'vitest'
import { compareValuesWithOrder, sortRows, type SortOrder } from '@/utils/tableSort'

const ORDERS: SortOrder[] = ['asc', 'desc']

/** 只按 v 字段排序的极简行，name 用于断言顺序 */
interface Row {
  name: string
  v: unknown
}

const names = (rows: Row[]) => rows.map((r) => r.name)
const by = (rows: Row[], order: SortOrder) => names(sortRows(rows, (r) => r.v, order))

describe('compareValuesWithOrder — 空值沉底不随方向翻转', () => {
  it('空值恒排后，升序降序都一样', () => {
    for (const order of ORDERS) {
      expect(compareValuesWithOrder(null, 10, order)).toBeGreaterThan(0)
      expect(compareValuesWithOrder(10, null, order)).toBeLessThan(0)
    }
  })

  it('null / undefined / 空串三种空值等价，互比为 0', () => {
    const empties = [null, undefined, '']
    for (const order of ORDERS) {
      for (const a of empties) {
        for (const b of empties) {
          expect(compareValuesWithOrder(a, b, order)).toBe(0)
        }
        expect(compareValuesWithOrder(a, 0, order)).toBeGreaterThan(0)
      }
    }
  })

  it('有值的两侧才按方向翻转', () => {
    expect(compareValuesWithOrder(1, 2, 'asc')).toBeLessThan(0)
    expect(compareValuesWithOrder(1, 2, 'desc')).toBeGreaterThan(0)
  })

  it('0 与 false 是有值，不能被当成空值沉底', () => {
    // isEmpty 只认 null/undefined/''，数字 0 参与正常比较
    expect(compareValuesWithOrder(0, 5, 'asc')).toBeLessThan(0)
    expect(compareValuesWithOrder(0, null, 'desc')).toBeLessThan(0)
    expect(compareValuesWithOrder(false, true, 'asc')).toBeLessThan(0)
  })
})

describe('sortRows — 空值恒末尾', () => {
  const rows: Row[] = [
    { name: 'empty', v: null },
    { name: 'ten', v: 10 },
    { name: 'two', v: 2 }
  ]

  it('升序：有值升序在前，空值垫底', () => {
    expect(by(rows, 'asc')).toEqual(['two', 'ten', 'empty'])
  })

  it('降序：有值降序在前，空值依然垫底', () => {
    // 回归防线：曾经这里是 ['empty', 'ten', 'two']
    expect(by(rows, 'desc')).toEqual(['ten', 'two', 'empty'])
  })

  it('全空时保持入参顺序（后端业务默认序）', () => {
    const allEmpty: Row[] = [
      { name: 'c', v: null },
      { name: 'a', v: '' },
      { name: 'b', v: undefined }
    ]
    for (const order of ORDERS) {
      expect(by(allEmpty, order)).toEqual(['c', 'a', 'b'])
    }
  })

  it('同值组内保持入参顺序，排序稳定', () => {
    const tied: Row[] = [
      { name: 'x', v: 5 },
      { name: 'y', v: 5 },
      { name: 'z', v: 5 }
    ]
    for (const order of ORDERS) {
      expect(by(tied, order)).toEqual(['x', 'y', 'z'])
    }
  })
})

describe('sortRows — 取值语义', () => {
  it('数字优先的自然序：acc2 排在 acc10 之前', () => {
    const rows: Row[] = [
      { name: 'a10', v: 'acc10' },
      { name: 'a2', v: 'acc2' }
    ]
    expect(by(rows, 'asc')).toEqual(['a2', 'a10'])
  })

  it('数字字符串走数值比而非字典序', () => {
    const rows: Row[] = [
      { name: 'nine', v: '9' },
      { name: 'eighty', v: '80' }
    ]
    expect(by(rows, 'asc')).toEqual(['nine', 'eighty'])
  })

  it('NaN / Infinity 归入空值沉底', () => {
    // 这些值在业务上都来自「算不出来」（除零的成功率、无样本的均值），
    // 若不拦会回退字符串比较，"NaN" 按字典序排到数字前面
    const rows: Row[] = [
      { name: 'nan', v: NaN },
      { name: 'inf', v: Infinity },
      { name: 'five', v: 5 }
    ]
    for (const order of ORDERS) {
      expect(by(rows, order)[0]).toBe('five')
    }
    expect(compareValuesWithOrder(NaN, 5, 'desc')).toBeGreaterThan(0)
    expect(compareValuesWithOrder(Infinity, 5, 'desc')).toBeGreaterThan(0)
  })

  it('返回新数组且不修改入参', () => {
    const rows: Row[] = [
      { name: 'b', v: 2 },
      { name: 'a', v: 1 }
    ]
    const out = sortRows(rows, (r) => r.v, 'asc')
    expect(out).not.toBe(rows)
    expect(names(rows)).toEqual(['b', 'a'])
    expect(names(out)).toEqual(['a', 'b'])
  })

  it('空数组与单元素不报错', () => {
    for (const order of ORDERS) {
      expect(sortRows([] as Row[], (r) => r.v, order)).toEqual([])
      expect(by([{ name: 'only', v: null }], order)).toEqual(['only'])
    }
  })
})
