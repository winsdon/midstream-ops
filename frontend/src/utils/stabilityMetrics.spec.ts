import { describe, it, expect } from 'vitest'
import {
  accountHealthScore,
  errorRatePercent,
  rpm,
  formatTokenRate,
  formatLatencyTriple,
  formatRpm,
  healthScoreClass
} from '@/utils/stabilityMetrics'

describe('accountHealthScore', () => {
  it('参考图量级（错误率 0.38%、P50 5s）不扣分', () => {
    expect(accountHealthScore({ sla: 99.6, ttftP50: 5000, errorRate: 0.38 })).toBe(100)
  })

  it('首字 10s 内不因延迟扣分', () => {
    expect(accountHealthScore({ sla: 100, ttftP50: 0, errorRate: 0 })).toBe(100)
    expect(accountHealthScore({ sla: 100, ttftP50: 9999, errorRate: 0 })).toBe(100)
  })

  it('首字 10–30s 线性扣分，25s 仍 ≥70', () => {
    expect(accountHealthScore({ sla: 100, ttftP50: 25000, errorRate: 0 })).toBeGreaterThanOrEqual(70)
    expect(accountHealthScore({ sla: 100, ttftP50: 25000, errorRate: 0 })).toBeLessThan(100)
  })

  it('错误率 50% 把分数打到 60 以下', () => {
    expect(accountHealthScore({ sla: 50, ttftP50: 5000, errorRate: 50 })).toBeLessThan(60)
  })

  it('无延迟样本不因缺值扣分', () => {
    expect(accountHealthScore({ sla: 100, ttftP50: null, errorRate: 0 })).toBe(100)
    expect(accountHealthScore({ sla: 100, errorRate: 0 })).toBe(100)
  })

  it('错误率缺省时从 SLA 反推', () => {
    expect(accountHealthScore({ sla: 100, ttftP50: 5000 })).toBe(100)
    expect(accountHealthScore({ sla: 50, ttftP50: 5000 })).toBeLessThan(60)
  })
})

describe('errorRatePercent', () => {
  it('失败 / (成功 + 失败) × 100', () => {
    expect(errorRatePercent(996, 4)).toBeCloseTo(0.4)
    expect(errorRatePercent(100, 0)).toBe(0)
    expect(errorRatePercent(0, 5)).toBe(100)
  })

  it('两边都 0 返回 null', () => {
    expect(errorRatePercent(0, 0)).toBeNull()
  })
})

describe('rpm', () => {
  it('成功请求 / 窗口分钟', () => {
    expect(rpm(939.4 * 5, 5)).toBeCloseTo(939.4)
    expect(rpm(60, 1)).toBe(60)
  })

  it('窗口非法时返回 null', () => {
    expect(rpm(100, 0)).toBeNull()
    expect(rpm(100, -1)).toBeNull()
  })
})

describe('formatRpm', () => {
  it('整数原样、小数一位', () => {
    expect(formatRpm(60)).toBe('60')
    expect(formatRpm(939.4)).toBe('939.4')
    expect(formatRpm(null)).toBe('-')
  })
})

describe('formatTokenRate', () => {
  it('百万用 M、千用 K、其余取整', () => {
    expect(formatTokenRate(1_200_000)).toBe('1.2M')
    expect(formatTokenRate(12_300)).toBe('12.3K')
    expect(formatTokenRate(939.4)).toBe('939')
  })

  it('缺值或非正显示 -', () => {
    expect(formatTokenRate(null)).toBe('-')
    expect(formatTokenRate(undefined)).toBe('-')
    expect(formatTokenRate(0)).toBe('-')
    expect(formatTokenRate(-1)).toBe('-')
  })
})

describe('formatLatencyTriple', () => {
  it('秒值一位小数，对齐参考图', () => {
    expect(formatLatencyTriple({ avg: 4100, p50: 5000, p90: 10000 })).toBe(
      'AVG 4.1s · P50 5.0s · P90 10.0s'
    )
  })

  it('不足 1s 用 ms', () => {
    expect(formatLatencyTriple({ avg: 410, p50: 500, p90: 900 })).toBe(
      'AVG 410ms · P50 500ms · P90 900ms'
    )
  })

  it('缺值显示 -', () => {
    expect(formatLatencyTriple({ avg: null, p50: 5000, p90: null })).toBe(
      'AVG - · P50 5.0s · P90 -'
    )
  })
})

describe('healthScoreClass', () => {
  it('≥90 绿色，<70 琥珀，中间中性', () => {
    expect(healthScoreClass(95)).toContain('emerald')
    expect(healthScoreClass(90)).toContain('emerald')
    expect(healthScoreClass(80)).toContain('gray')
    expect(healthScoreClass(69)).toContain('amber')
  })
})
