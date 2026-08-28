import { describe, it, expect } from 'vitest'
import { latencyBand, latencyBandThresholds } from '@/utils/latencyBand'

describe('latencyBand — 首字延迟（ttft）', () => {
  it('按 10s / 30s / 60s 分档', () => {
    expect(latencyBand(0)).toBe('fast')
    expect(latencyBand(5000)).toBe('fast')
    expect(latencyBand(9999)).toBe('fast')
    expect(latencyBand(10000)).toBe('ok')
    expect(latencyBand(29999)).toBe('ok')
    expect(latencyBand(30000)).toBe('slow')
    expect(latencyBand(59999)).toBe('slow')
    expect(latencyBand(60000)).toBe('bad')
    expect(latencyBand(120000)).toBe('bad')
  })

  it('默认 kind 为 ttft', () => {
    expect(latencyBand(4000)).toBe(latencyBand(4000, 'ttft'))
  })

  it('参考图量级（P50 5s / P90 10s）不触发告警色', () => {
    expect(latencyBand(4100)).toBe('fast')
    expect(latencyBand(5000)).toBe('fast')
    expect(latencyBand(10000)).toBe('ok')
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

  it('同一个值在两套阈值下档位可以相同（两套阈值现已同量级），但语义仍分 kind', () => {
    expect(latencyBand(20000, 'ttft')).toBe('ok')
    expect(latencyBand(20000, 'total')).toBe('ok')
    expect(latencyBand(5000, 'ttft')).toBe('fast')
    expect(latencyBand(5000, 'total')).toBe('fast')
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
    expect(latencyBandThresholds('ttft')).toEqual([10000, 30000, 60000])
    expect(latencyBandThresholds('total')).toEqual([10000, 30000, 60000])
    expect(latencyBandThresholds()).toEqual([10000, 30000, 60000])
  })
})
