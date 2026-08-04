import { describe, it, expect } from 'vitest'
import {
  WINDOW_OPTIONS,
  PROBE_INTERVAL_MINUTES,
  RATE_BANDS,
  JITTER_MIN_SAMPLES,
  healthRank,
  normalizeHealth,
  filterRows,
  providerOptions,
  healthOptions,
  searchStabilityRows,
  jitterRatio,
  rateBand,
  rowGrade,
  GRADE_RANK,
  type FilterableRow
} from '@/utils/stabilityModel'

function row(accountId: number, providerName: string): FilterableRow {
  return { account_id: accountId, provider_name: providerName }
}

/** 固定的健康状态查表，模拟组件里的 ref<Map> */
const health: Record<number, string | undefined> = {
  1: 'healthy',
  2: 'degraded',
  3: 'suspended',
  4: undefined // 从未探测
}
const healthOf = (id: number) => health[id]

describe('窗口档位', () => {
  it('是实时盯盘口径，最短 5 分钟', () => {
    expect(WINDOW_OPTIONS).toEqual([5, 30, 60, 300, 1440])
  })

  it('探测间隔常量用于短窗口提示', () => {
    expect(PROBE_INTERVAL_MINUTES).toBe(15)
    // 至少有一档短于探测间隔，否则提示永远不会触发、常量就没意义
    expect(WINDOW_OPTIONS.some((m) => m < PROBE_INTERVAL_MINUTES)).toBe(true)
  })
})

describe('healthRank / normalizeHealth', () => {
  it('越糟排位越大', () => {
    expect(healthRank('healthy')).toBeLessThan(healthRank('degraded'))
    expect(healthRank('degraded')).toBeLessThan(healthRank('suspended'))
    expect(healthRank('suspended')).toBeLessThan(healthRank('disabled'))
  })

  it('无记录视作 healthy —— 必须与徽标兜底一致', () => {
    expect(healthRank(undefined)).toBe(healthRank('healthy'))
    expect(normalizeHealth(undefined)).toBe('healthy')
  })

  it('未知状态不抛错，按最健康处理', () => {
    expect(healthRank('未来新增的状态')).toBe(0)
  })
})

describe('filterRows', () => {
  const rows = [row(1, '甲站'), row(2, '甲站'), row(3, '乙站'), row(4, '')]

  it('null 表示不限，返回全部', () => {
    expect(filterRows(rows, null, null, healthOf)).toHaveLength(4)
  })

  it('按供应商筛选', () => {
    expect(filterRows(rows, '甲站', null, healthOf).map((r) => r.account_id)).toEqual([1, 2])
  })

  it("provider 传空串筛「未归属」桶 —— 故不限必须用 null 而非 ''", () => {
    expect(filterRows(rows, '', null, healthOf).map((r) => r.account_id)).toEqual([4])
  })

  it('按健康状态筛选', () => {
    expect(filterRows(rows, null, 'degraded', healthOf).map((r) => r.account_id)).toEqual([2])
  })

  it('从未探测的账号归入 healthy 桶', () => {
    expect(filterRows(rows, null, 'healthy', healthOf).map((r) => r.account_id)).toEqual([1, 4])
  })

  it('两维度可叠加', () => {
    expect(filterRows(rows, '甲站', 'healthy', healthOf).map((r) => r.account_id)).toEqual([1])
  })

  it('返回新数组，不修改入参', () => {
    const out = filterRows(rows, null, null, healthOf)
    expect(out).not.toBe(rows)
    expect(rows).toHaveLength(4)
  })
})

describe('providerOptions', () => {
  it('按供应商分桶计数', () => {
    const opts = providerOptions([row(1, '甲站'), row(2, '甲站'), row(3, '乙站')])
    expect(opts).toEqual([
      { value: '甲站', count: 2 },
      { value: '乙站', count: 1 }
    ])
  })

  it('未归属账号自成一桶，不被丢弃', () => {
    const opts = providerOptions([row(1, '甲站'), row(4, ''), row(5, '')])
    expect(opts.find((o) => o.value === '')?.count).toBe(2)
  })

  it('空列表返回空数组（工具栏据此隐藏整组 pill）', () => {
    expect(providerOptions([])).toEqual([])
  })
})

describe('healthOptions', () => {
  it('按严重度排序而非字母序，与表格排序方向一致', () => {
    const opts = healthOptions([row(3, 'x'), row(2, 'x'), row(1, 'x')], healthOf)
    expect(opts.map((o) => o.value)).toEqual(['healthy', 'degraded', 'suspended'])
  })

  it('从未探测的账号计入 healthy', () => {
    const opts = healthOptions([row(1, 'x'), row(4, 'x')], healthOf)
    expect(opts).toEqual([{ value: 'healthy', count: 2 }])
  })
})

describe('searchStabilityRows', () => {
  const rows = [
    { account_name: 'Alpha-01', platform: 'anthropic', provider_name: '上游甲' },
    { account_name: 'beta-02', platform: 'openai', provider_name: '上游乙' },
    { account_name: 'Gamma-03', platform: 'anthropic', provider_name: '' }
  ]

  it('匹配账号名 / 平台 / 供应商三个字段', () => {
    expect(searchStabilityRows(rows, 'alpha').map((r) => r.account_name)).toEqual(['Alpha-01'])
    expect(searchStabilityRows(rows, 'anthropic')).toHaveLength(2)
    expect(searchStabilityRows(rows, '上游乙').map((r) => r.account_name)).toEqual(['beta-02'])
  })

  it('大小写不敏感', () => {
    expect(searchStabilityRows(rows, 'ALPHA')).toHaveLength(1)
    expect(searchStabilityRows(rows, 'BeTa')).toHaveLength(1)
  })

  it('空查询或纯空白返回全部', () => {
    expect(searchStabilityRows(rows, '')).toHaveLength(3)
    expect(searchStabilityRows(rows, '   ')).toHaveLength(3)
  })

  it('无命中返回空数组', () => {
    expect(searchStabilityRows(rows, 'zzz')).toEqual([])
  })

  it('不修改入参', () => {
    const copy = [...rows]
    searchStabilityRows(rows, 'alpha')
    expect(rows).toEqual(copy)
  })
})

describe('jitterRatio', () => {
  it('样本充足时返回 P95/P50', () => {
    expect(jitterRatio(1000, 3000, 100)).toBe(3)
    expect(jitterRatio(800, 900, JITTER_MIN_SAMPLES)).toBeCloseTo(1.125)
  })

  it('样本不足返回 null —— 三次请求算出的 P95 基本就是最大值，不含信息', () => {
    expect(jitterRatio(1000, 6200, 3)).toBeNull()
    expect(jitterRatio(1000, 3000, JITTER_MIN_SAMPLES - 1)).toBeNull()
    expect(jitterRatio(1000, 3000, undefined)).toBeNull()
  })

  it('P50 为 0 或缺值返回 null，绝不产生 Infinity', () => {
    expect(jitterRatio(0, 3000, 100)).toBeNull()
    expect(jitterRatio(null, 3000, 100)).toBeNull()
    expect(jitterRatio(undefined, 3000, 100)).toBeNull()
    expect(jitterRatio(-1, 3000, 100)).toBeNull()
  })

  it('P95 缺值返回 null', () => {
    expect(jitterRatio(1000, null, 100)).toBeNull()
    expect(jitterRatio(1000, undefined, 100)).toBeNull()
    expect(jitterRatio(1000, NaN, 100)).toBeNull()
  })
})

describe('rateBand', () => {  it('按 95 / 80 分档', () => {
    expect(rateBand(100)).toBe('good')
    expect(rateBand(95)).toBe('good')
    expect(rateBand(94.9)).toBe('warn')
    expect(rateBand(80)).toBe('warn')
    expect(rateBand(79.9)).toBe('bad')
    expect(rateBand(0)).toBe('bad')
  })

  it('缺值返 unknown —— 「缺值意味着什么」交给调用方决定', () => {
    expect(rateBand(null)).toBe('unknown')
    expect(rateBand(undefined)).toBe('unknown')
    expect(rateBand(NaN)).toBe('unknown')
    expect(rateBand(Infinity)).toBe('unknown')
  })

  it('阈值常量与分档行为一致', () => {
    const [good, warn] = RATE_BANDS
    expect(rateBand(good)).toBe('good')
    expect(rateBand(warn)).toBe('warn')
  })
})

describe('rowGrade', () => {
  // 注：本组的 ttftMs 取值经由 rowGrade → latencyToGrade → latencyBand 耦合到
  // latencyBand.ts 的阈值。故一律取深入档位内部的值（30s 必 bad、8s 必 warn、
  // 0.5s 必 good），而非贴着边界 —— 贴边界的断言会被一次温和的阈值调整打断，
  // 而它们本意测的是「取最差」的合成逻辑，不是阈值本身。
  it('取最差维度，而非平均', () => {
    // 成功率满分但首字 30 秒 —— 平均会摊成中间色，恰好掩盖唯一要报告的事实
    expect(rowGrade({ ttftMs: 30000, successRate: 100, healthState: 'healthy' })).toBe('bad')
  })

  it('各维度独立触发', () => {
    expect(rowGrade({ ttftMs: 8000, successRate: 100, healthState: 'healthy' })).toBe('warn')
    expect(rowGrade({ ttftMs: 500, successRate: 85, healthState: 'healthy' })).toBe('warn')
    expect(rowGrade({ ttftMs: 500, successRate: 100, healthState: 'degraded' })).toBe('warn')
    expect(rowGrade({ ttftMs: 500, successRate: 50, healthState: 'healthy' })).toBe('bad')
    expect(rowGrade({ ttftMs: 500, successRate: 100, healthState: 'suspended' })).toBe('bad')
  })

  it('全部正常为 good；ok 档延迟仍算正常', () => {
    expect(rowGrade({ ttftMs: 680, successRate: 100, healthState: 'healthy' })).toBe('good')
    expect(rowGrade({ ttftMs: 4000, successRate: 99, healthState: 'healthy' })).toBe('good')
  })

  it('成功率阈值与 rateClass 的 95/80 对齐', () => {
    expect(rowGrade({ ttftMs: 500, successRate: 95 })).toBe('good')
    expect(rowGrade({ ttftMs: 500, successRate: 94.9 })).toBe('warn')
    expect(rowGrade({ ttftMs: 500, successRate: 80 })).toBe('warn')
    expect(rowGrade({ ttftMs: 500, successRate: 79.9 })).toBe('bad')
  })

  it('评级用的成功率分档就是 rateBand，不是另一份拷贝', () => {
    // 阈值双写过一次（rateToGrade 与 ActiveTable.rateClass），
    // 收口后这条断言守住「两者永远同源」
    for (const v of [100, 95, 94.9, 80, 79.9, 0]) {
      expect(rowGrade({ ttftMs: 500, successRate: v })).toBe(rateBand(v))
    }
  })

  it('被动表无成功率时该维度弃权，不把整行拖成 unknown', () => {
    // 关键降级：若 undefined 返 unknown，被动表每行都至少 unknown，点全灰 = 零信息
    expect(rowGrade({ ttftMs: 680, healthState: 'healthy' })).toBe('good')
    expect(rowGrade({ ttftMs: 680, successRate: null, healthState: 'healthy' })).toBe('good')
    // 弃权不等于免疫：其他维度出问题照样点亮
    expect(rowGrade({ ttftMs: 30000, healthState: 'healthy' })).toBe('bad')
  })

  it('无延迟样本为 unknown', () => {
    expect(rowGrade({ ttftMs: null, healthState: 'healthy' })).toBe('unknown')
    expect(rowGrade({})).toBe('unknown')
  })

  it('人工停用归 unknown 而非 bad —— 停用不是故障', () => {
    expect(rowGrade({ ttftMs: 680, successRate: 100, healthState: 'disabled' })).toBe('unknown')
  })

  it('有确定问题时压过 unknown', () => {
    expect(rowGrade({ ttftMs: null, successRate: 50, healthState: 'healthy' })).toBe('bad')
  })
})

describe('GRADE_RANK', () => {
  it('unknown 介于 good 与 warn 之间', () => {
    expect(GRADE_RANK.good).toBeLessThan(GRADE_RANK.unknown)
    expect(GRADE_RANK.unknown).toBeLessThan(GRADE_RANK.warn)
    expect(GRADE_RANK.warn).toBeLessThan(GRADE_RANK.bad)
  })
})
