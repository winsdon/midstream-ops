import { describe, it, expect } from 'vitest'
import {
  accountHealthScore,
  errorRatePercent,
  rpm,
  formatTokenRate,
  formatLatencyTriple,
  formatRpm,
  healthScoreClass,
  weightedAvg,
  aggregatePassiveRows,
  passiveDetailFromRow,
  cardFromRow,
  cardFromSection,
  cellTip,
  nextHeatmapSort,
  sortHeatmapModels,
  DEFAULT_HEATMAP_SORT
} from '@/utils/stabilityMetrics'
import { cellTone, displayCells, liveToneFromCells } from '@/utils/stabilityTimeline'
import type { PassiveRow } from '@/types'

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
  it('≥90 绿色，70–89 琥珀，<70 红色', () => {
    expect(healthScoreClass(95)).toContain('emerald')
    expect(healthScoreClass(90)).toContain('emerald')
    expect(healthScoreClass(80)).toContain('amber')
    expect(healthScoreClass(70)).toContain('amber')
    expect(healthScoreClass(69)).toContain('red')
    expect(healthScoreClass(30)).toContain('red')
  })
})

describe('weightedAvg', () => {
  it('按权重平均，跳过缺值', () => {
    expect(
      weightedAvg([
        { weight: 90, value: 100 },
        { weight: 10, value: 200 },
        { weight: 50, value: null }
      ])
    ).toBeCloseTo(110)
  })

  it('无有效样本返回 null', () => {
    expect(weightedAvg([])).toBeNull()
    expect(weightedAvg([{ weight: 10, value: null }])).toBeNull()
    expect(weightedAvg([{ weight: 0, value: 5 }])).toBeNull()
  })
})

describe('passiveDetailFromRow', () => {
  it('请求数包含失败', () => {
    const out = passiveDetailFromRow({
      account_id: 1,
      account_name: 'a',
      platform: '',
      provider_id: 0,
      provider_name: '',
      groups: [],
      requests: 2,
      success_count: 2,
      error_count: 16,
      sla: 11.1,
      duration_avg: null,
      duration_p50: null,
      duration_p90: null,
      first_token_avg: null,
      first_token_p50: null,
      first_token_p90: null,
      tokens_per_second: null,
      cache_rate: null
    })
    expect(out.requests).toBe(18)
    expect(out.success_count).toBe(2)
    expect(out.error_count).toBe(16)
  })
})

describe('aggregatePassiveRows', () => {
  function row(partial: Partial<PassiveRow> & Pick<PassiveRow, 'account_id' | 'requests'>): PassiveRow {
    return {
      account_name: 'n',
      platform: '',
      provider_id: 0,
      provider_name: '',
      groups: [],
      success_count: partial.requests,
      error_count: 0,
      sla: 100,
      duration_avg: null,
      duration_p50: null,
      duration_p90: null,
      first_token_avg: null,
      first_token_p50: null,
      first_token_p90: null,
      tokens_per_second: null,
      cache_rate: null,
      ...partial
    }
  }

  it('单行时数值与原行一致', () => {
    const a = row({
      account_id: 1,
      requests: 40,
      success_count: 40,
      error_count: 10,
      sla: 80,
      platform: 'anthropic',
      provider_name: '甲',
      groups: ['pro'],
      first_token_p50: 5000,
      duration_avg: 8000,
      tokens_per_second: 20,
      cache_rate: 30
    })
    const out = aggregatePassiveRows([a], '供应商甲')
    expect(out.title).toBe('供应商甲')
    expect(out.accountCount).toBe(1)
    expect(out.requests).toBe(50)
    expect(out.success_count).toBe(40)
    expect(out.error_count).toBe(10)
    expect(out.sla).toBeCloseTo(80)
    expect(out.first_token_p50).toBe(5000)
    expect(out.duration_avg).toBe(8000)
    expect(out.tokens_per_second).toBe(20)
    expect(out.cache_rate).toBe(30)
    expect(out.platform).toBe('anthropic')
    expect(out.provider_name).toBe('甲')
    expect(out.groups).toEqual(['pro'])
  })

  it('多行按请求次数加权平均延迟，SLA 用求和', () => {
    const out = aggregatePassiveRows(
      [
        row({
          account_id: 1,
          requests: 90,
          success_count: 90,
          error_count: 10,
          first_token_p50: 1000,
          platform: 'anthropic',
          provider_name: '甲',
          groups: ['pro', 'default']
        }),
        row({
          account_id: 2,
          requests: 10,
          success_count: 10,
          error_count: 0,
          first_token_p50: 2000,
          platform: 'openai',
          provider_name: '甲',
          groups: ['default']
        })
      ],
      '甲'
    )
    expect(out.accountCount).toBe(2)
    expect(out.requests).toBe(110)
    expect(out.success_count).toBe(100)
    expect(out.error_count).toBe(10)
    expect(out.sla).toBeCloseTo((100 / 110) * 100)
    expect(out.first_token_p50).toBeCloseTo(1100)
    expect(out.platform).toBe('anthropic · openai')
    expect(out.groups).toEqual(['pro', 'default'])
  })

  it('不修改入参', () => {
    const rows = [row({ account_id: 1, requests: 5, groups: ['a'] })]
    aggregatePassiveRows(rows, 'x')
    expect(rows[0].groups).toEqual(['a'])
  })
})

describe('cardFromRow / cardFromSection', () => {
  const generatedAt = '2026-04-08T12:00:00.000Z'

  function rowWithTl(
    partial: Partial<PassiveRow> & Pick<PassiveRow, 'account_id' | 'requests'>
  ): PassiveRow {
    return {
      account_name: 'n',
      platform: '',
      provider_id: 0,
      provider_name: '',
      groups: [],
      success_count: partial.requests,
      error_count: 0,
      sla: 100,
      duration_avg: null,
      duration_p50: null,
      duration_p90: null,
      first_token_avg: null,
      first_token_p50: null,
      first_token_p90: null,
      tokens_per_second: null,
      cache_rate: null,
      timeline: [],
      ...partial
    }
  }

  it('子行与外层行走同一套 cellTone / 色块色', () => {
    const child = rowWithTl({
      account_id: 1,
      account_name: 'key-a',
      requests: 10,
      success_count: 8,
      error_count: 2,
      sla: 80,
      timeline: [{ t: '2026-04-08T11:55:00.000Z', ok: 8, err: 2 }]
    })
    const inner = cardFromRow(child, '甲')
    const outer = cardFromSection([child], '甲')
    const innerCells = displayCells(inner.timeline, 60, generatedAt)
    const outerCells = displayCells(outer.timeline, 60, generatedAt)
    expect(liveToneFromCells(innerCells)).toBe(liveToneFromCells(outerCells))
    expect(innerCells[11].color).toBe(outerCells[11].color)
    expect(cellTone(inner.sla)).toBe(cellTone(outer.sla))
  })

  it('块级趋势同桶求和，不是平均', () => {
    const a = rowWithTl({
      account_id: 1,
      requests: 9,
      timeline: [{ t: '2026-04-08T11:59:00.000Z', ok: 9, err: 1 }]
    })
    const b = rowWithTl({
      account_id: 2,
      requests: 1,
      timeline: [{ t: '2026-04-08T11:59:00.000Z', ok: 1, err: 9 }]
    })
    const outer = cardFromSection([a, b], '甲')
    expect(outer.timeline[0]).toMatchObject({ t: '2026-04-08T11:59:00.000Z', ok: 10, err: 10 })
    expect(outer.accountCount).toBe(2)
  })

  it('色块 tooltip 含健康分与分位数', () => {
    const cells = displayCells(
      [
        {
          t: '2026-04-08T11:55:00.000Z',
          ok: 10,
          err: 0,
          first_token_avg: 6400,
          first_token_p50: 8000,
          first_token_p90: 10000,
          duration_avg: 14700,
          duration_p50: 5000,
          duration_p90: 60000,
          output_tokens: 10300,
          duration_ms_sum: 1000,
          input_tokens: 37,
          cache_read_tokens: 963
        }
      ],
      60,
      generatedAt
    )
    const tip = cellTip(cells[11], { windowMinutes: 60, cellMinutes: 5 })
    expect(tip.empty).toBe(false)
    expect(tip.health).toBe(100)
    expect(tip.sla).toBe(100)
    expect(tip.errorRate).toBe(0)
    expect(tip.firstTokenP50).toBe(8000)
    expect(tip.tps).toBe(10300)
    expect(tip.cache).toBeCloseTo(96.3, 0)
    expect(tip.rpm).toBe(2)
  })

  it('不修改入参 timeline', () => {
    const child = rowWithTl({
      account_id: 1,
      requests: 1,
      timeline: [{ t: 't', ok: 1, err: 0 }]
    })
    cardFromRow(child, '')
    cardFromSection([child], 'x')
    expect(child.timeline).toEqual([{ t: 't', ok: 1, err: 0 }])
  })
})

describe('nextHeatmapSort / sortHeatmapModels', () => {
  function rowWithTl(
    partial: Partial<PassiveRow> & Pick<PassiveRow, 'account_id' | 'requests'>
  ): PassiveRow {
    return {
      account_name: 'n',
      platform: '',
      provider_id: 0,
      provider_name: '',
      groups: [],
      success_count: partial.requests,
      error_count: 0,
      sla: 100,
      duration_avg: null,
      duration_p50: null,
      duration_p90: null,
      first_token_avg: null,
      first_token_p50: null,
      first_token_p90: null,
      tokens_per_second: null,
      cache_rate: null,
      timeline: [],
      ...partial
    }
  }

  it('默认按请求次数降序', () => {
    expect(DEFAULT_HEATMAP_SORT).toEqual({ key: 'requests', order: 'desc' })
  })

  it('点当前列翻转方向', () => {
    expect(nextHeatmapSort({ key: 'sla', order: 'asc' }, 'sla')).toEqual({ key: 'sla', order: 'desc' })
    expect(nextHeatmapSort({ key: 'requests', order: 'desc' }, 'requests')).toEqual({
      key: 'requests',
      order: 'asc'
    })
  })

  it('点新列：成功率/缓存/吞吐从升序起，请求数/首字从降序起', () => {
    expect(nextHeatmapSort({ key: 'sla', order: 'asc' }, 'requests')).toEqual({
      key: 'requests',
      order: 'desc'
    })
    expect(nextHeatmapSort({ key: 'sla', order: 'asc' }, 'firstToken')).toEqual({
      key: 'firstToken',
      order: 'desc'
    })
    expect(nextHeatmapSort({ key: 'requests', order: 'desc' }, 'cache')).toEqual({
      key: 'cache',
      order: 'asc'
    })
    expect(nextHeatmapSort({ key: 'requests', order: 'desc' }, 'tps')).toEqual({
      key: 'tps',
      order: 'asc'
    })
  })

  it('按缓存率升序，空值沉底', () => {
    const rows = [
      cardFromRow(rowWithTl({ account_id: 1, requests: 1, cache_rate: 80 }), ''),
      cardFromRow(rowWithTl({ account_id: 2, requests: 1, cache_rate: null }), ''),
      cardFromRow(rowWithTl({ account_id: 3, requests: 1, cache_rate: 10 }), '')
    ]
    expect(sortHeatmapModels(rows, 'cache', 'asc').map((r) => r.key)).toEqual(['3', '1', '2'])
  })

  it('不修改入参', () => {
    const rows = [cardFromRow(rowWithTl({ account_id: 1, requests: 1, sla: 90 }), '')]
    sortHeatmapModels(rows, 'sla', 'asc')
    expect(rows[0].key).toBe('1')
  })
})
