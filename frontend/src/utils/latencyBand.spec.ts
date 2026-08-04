import { describe, it, expect } from 'vitest'
import { latencyBand, latencyBandThresholds } from '@/utils/latencyBand'

describe('latencyBand — 首字延迟（ttft）', () => {
  it('按 2s / 6s / 12s 分档', () => {
    expect(latencyBand(0)).toBe('fast')
    expect(latencyBand(680)).toBe('fast')
    expect(latencyBand(1999)).toBe('fast')
    expect(latencyBand(2000)).toBe('ok')
    expect(latencyBand(5999)).toBe('ok')
    expect(latencyBand(6000)).toBe('slow')
    expect(latencyBand(11999)).toBe('slow')
    expect(latencyBand(12000)).toBe('bad')
    expect(latencyBand(60000)).toBe('bad')
  })

  it('默认 kind 为 ttft', () => {
    expect(latencyBand(4000)).toBe(latencyBand(4000, 'ttft'))
  })

  it('现网常见首字（3-5s）落在中性档，不触发告警色', () => {
    // 旧档（1.5/3/6s）下这批值全是琥珀，颜色常亮等于没有颜色
    expect(latencyBand(3020)).toBe('ok')
    expect(latencyBand(4130)).toBe('ok')
    expect(latencyBand(4570)).toBe('ok')
  })
})

describe('latencyBand — 总耗时（total）', () => {
  it('按 10s / 30s / 60s 分档', () => {
    expect(latencyBand(9999, 'total')).toBe('fast')
    expect(latencyBand(10000, 'total')).toBe('ok')
    expect(latencyBand(29999, 'total')).toBe('ok')
    expect(latencyBand(30000, 'total')).toBe('slow')
    expect(latencyBand(59999, 'total')).toBe('slow')
    expect(latencyBand(60000, 'total')).toBe('bad')
  })

  it('同一个值在两套阈值下档位不同 —— 否则总耗时列会齐刷刷变红', () => {
    expect(latencyBand(20000, 'ttft')).toBe('bad')
    expect(latencyBand(20000, 'total')).toBe('ok')
  })
})

describe('latencyBand — 缺值', () => {
  it('无样本一律 unknown，绝不落 fast', () => {
    expect(latencyBand(null)).toBe('unknown')
    expect(latencyBand(undefined)).toBe('unknown')
    expect(latencyBand(NaN)).toBe('unknown')
    expect(latencyBand(Infinity)).toBe('unknown')
    expect(latencyBand(-1)).toBe('unknown')
    expect(latencyBand(null, 'total')).toBe('unknown')
  })
})

describe('latencyBandThresholds', () => {
  it('返回对应 kind 的阈值供 tooltip 使用', () => {
    expect(latencyBandThresholds('ttft')).toEqual([2000, 6000, 12000])
    expect(latencyBandThresholds('total')).toEqual([10000, 30000, 60000])
    expect(latencyBandThresholds()).toEqual([2000, 6000, 12000])
  })

  it('总耗时每一档都比首字宽松 —— 两列同色不同义会误导', () => {
    const ttft = latencyBandThresholds('ttft')
    const total = latencyBandThresholds('total')
    expect(total.every((v, i) => v > ttft[i])).toBe(true)
  })
})
