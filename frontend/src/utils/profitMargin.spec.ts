import { describe, it, expect } from 'vitest'
import { profitMargin, marginBand, marginBandThresholds } from '@/utils/profitMargin'

describe('profitMargin', () => {
  it('返回百分点而非 0~1 比值', () => {
    expect(profitMargin(1000, 340)).toBe(34)
    expect(profitMargin(860, 70)).toBeCloseTo(8.14, 2)
  })

  it('亏损是可表示的值，不是错误', () => {
    expect(profitMargin(410, -60)).toBeCloseTo(-14.63, 2)
    expect(profitMargin(100, -100)).toBe(-100)
  })

  it('利润为 0 时返回 0 而非 null —— 不赚不亏是确定的事实', () => {
    expect(profitMargin(620, 0)).toBe(0)
  })

  it('收益为 0 返回 null —— 数学上未定义，不是 0%', () => {
    expect(profitMargin(0, 0)).toBeNull()
    expect(profitMargin(0, 100)).toBeNull()
  })

  it('负收益返回 null，绝不让符号翻转', () => {
    // -50 / -100 = +50%，亏损会显示成盈利
    expect(profitMargin(-100, -50)).toBeNull()
  })

  it('缺值一律返回 null', () => {
    expect(profitMargin(null, 100)).toBeNull()
    expect(profitMargin(undefined, 100)).toBeNull()
    expect(profitMargin(1000, null)).toBeNull()
    expect(profitMargin(1000, undefined)).toBeNull()
    expect(profitMargin(NaN, 100)).toBeNull()
    expect(profitMargin(1000, NaN)).toBeNull()
    expect(profitMargin(Infinity, 100)).toBeNull()
    expect(profitMargin(1000, Infinity)).toBeNull()
  })
})

describe('marginBand', () => {
  it('按 0 / 10 / 30 分档', () => {
    expect(marginBand(-0.1)).toBe('loss')
    expect(marginBand(-100)).toBe('loss')
    expect(marginBand(0)).toBe('thin')
    expect(marginBand(9.9)).toBe('thin')
    expect(marginBand(10)).toBe('ok')
    expect(marginBand(29.9)).toBe('ok')
    expect(marginBand(30)).toBe('good')
    expect(marginBand(100)).toBe('good')
  })

  it('薄利与良好分属不同档 —— 这正是利润列的正负着色区分不了的', () => {
    expect(marginBand(8.1)).toBe('thin')
    expect(marginBand(33.9)).toBe('good')
  })

  it('缺值一律 unknown，绝不落 loss 给出虚假的亏损信号', () => {
    expect(marginBand(null)).toBe('unknown')
    expect(marginBand(undefined)).toBe('unknown')
    expect(marginBand(NaN)).toBe('unknown')
    expect(marginBand(Infinity)).toBe('unknown')
  })
})

describe('marginBandThresholds', () => {
  it('阈值常量与分档行为一致', () => {
    const [zero, thin, good] = marginBandThresholds()
    expect(marginBand(zero)).toBe('thin')
    expect(marginBand(thin)).toBe('ok')
    expect(marginBand(good)).toBe('good')
  })
})

describe('profitMargin + marginBand 组合', () => {
  it('收益为 0 的行落到 unknown 而非 loss', () => {
    expect(marginBand(profitMargin(0, 0))).toBe('unknown')
  })

  it('真实场景：三档利润率各归其位', () => {
    expect(marginBand(profitMargin(1240, 420))).toBe('good') // 33.9%
    expect(marginBand(profitMargin(860, 70))).toBe('thin') // 8.1%
    expect(marginBand(profitMargin(410, -60))).toBe('loss') // -14.6%
  })
})
