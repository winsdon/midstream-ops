import { describe, expect, it } from 'vitest'
import {
  OPERATING_COST_CATEGORIES,
  categoryLabelKey,
  isOperatingCostCategory,
  roundAmount,
  sumByCategory,
  totalAmount
} from './operatingCostModel'
import type { OperatingCost } from '@/types'

/** 造一条运营成本，只填测试关心的字段。 */
function entry(category: string, amount: number): OperatingCost {
  return {
    id: 1,
    provider_id: 1,
    category: category as OperatingCost['category'],
    amount,
    currency: 'USD',
    occurred_on: '2026-07-15',
    note: '',
    operator: '',
    created_at: '2026-07-15T00:00:00Z'
  }
}

describe('isOperatingCostCategory', () => {
  it('识别全部合法类别', () => {
    for (const c of OPERATING_COST_CATEGORIES) {
      expect(isOperatingCostCategory(c)).toBe(true)
    }
  })

  it('拒绝未知类别', () => {
    expect(isOperatingCostCategory('rent')).toBe(false)
    expect(isOperatingCostCategory('')).toBe(false)
  })
})

describe('categoryLabelKey', () => {
  it('返回对应的 i18n key', () => {
    expect(categoryLabelKey('account')).toBe('opcost.categories.account')
    expect(categoryLabelKey('server')).toBe('opcost.categories.server')
  })

  // 后端新增枚举而前端未同步时，落回 other 而不是渲染出裸的原始值
  it('未知类别落回 other', () => {
    expect(categoryLabelKey('rent')).toBe('opcost.categories.other')
  })
})

describe('sumByCategory', () => {
  it('空输入返回空数组', () => {
    expect(sumByCategory([])).toEqual([])
    expect(sumByCategory(null)).toEqual([])
    expect(sumByCategory(undefined)).toEqual([])
  })

  it('同类别累加', () => {
    const got = sumByCategory([entry('account', 200), entry('account', 100)])
    expect(got).toEqual([{ category: 'account', amount: 300 }])
  })

  // 顺序恒定，让同一站点在不同区间之间切换时小计不会跳来跳去
  it('按 OPERATING_COST_CATEGORIES 固定顺序返回，与输入顺序无关', () => {
    const got = sumByCategory([entry('other', 1), entry('server', 2), entry('account', 3)])
    expect(got.map((s) => s.category)).toEqual(['account', 'server', 'other'])
  })

  it('过滤零值类别', () => {
    const got = sumByCategory([entry('account', 100), entry('server', 0)])
    expect(got).toEqual([{ category: 'account', amount: 100 }])
  })

  it('未知类别归入 other', () => {
    const got = sumByCategory([entry('rent', 50), entry('other', 20)])
    expect(got).toEqual([{ category: 'other', amount: 70 }])
  })

  it('累加后归一到分', () => {
    const got = sumByCategory([entry('account', 0.1), entry('account', 0.2)])
    expect(got).toEqual([{ category: 'account', amount: 0.3 }])
  })
})

describe('totalAmount', () => {
  it('空输入返回 0 而非 null，便于直接参与算术', () => {
    expect(totalAmount([])).toBe(0)
    expect(totalAmount(null)).toBe(0)
    expect(totalAmount(undefined)).toBe(0)
  })

  it('求和', () => {
    expect(totalAmount([entry('account', 200), entry('server', 50)])).toBe(250)
  })

  // 浮点求和会产生 0.30000000000000004，直接展示会露出实现细节
  it('消除浮点尾数', () => {
    expect(totalAmount([entry('account', 0.1), entry('server', 0.2)])).toBe(0.3)
  })
})

describe('roundAmount', () => {
  it('归一到分', () => {
    expect(roundAmount(10.005)).toBe(10.01)
    expect(roundAmount(10.004)).toBe(10)
    expect(roundAmount(0.1 + 0.2)).toBe(0.3)
  })

  it('与后端 roundAmount 同口径：0 与负数原样通过', () => {
    expect(roundAmount(0)).toBe(0)
    expect(roundAmount(-1.005)).toBe(-1)
  })
})
