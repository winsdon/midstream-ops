import { describe, it, expect } from 'vitest'
import {
  isLowBalance, sortProviders, providerStatus, filterProviders, statusOptions,
  type ProviderSortKey
} from '@/utils/providerModel'
import type { Provider } from '@/types'

/**
 * 只填测试关心的字段，其余走类型必填的哑值。
 *
 * credentials_ready 默认 true：绝大多数用例关注的是排序/阈值，不该被凭据门禁
 * 干扰；需要「待配置凭据」态的用例显式传 false。
 */
function provider(overrides: Partial<Provider> & { name: string }): Provider {
  return {
    id: 0,
    note: '',
    balance_type: 'sub2api',
    platform: 'sub2api',
    auth_mode: 'password',
    base_url: '',
    login_email: '',
    has_password: false,
    has_access_token: false,
    has_refresh_token: false,
    upstream_user_id: '',
    low_balance_threshold: 0,
    recharge_rate: 1,
    credentials_ready: true,
    probe_enabled: false,
    ignore_balance_alert: false,
    self_operated: false,
    account_count: 0,
    created_at: '',
    ...overrides
  }
}

const names = (list: Provider[]) => list.map((p) => p.name)

describe('isLowBalance', () => {
  it('uses the system default threshold in CNY when a provider has no override', () => {
    const lowBalance = isLowBalance(
      provider({ name: 'pet', last_balance: 5, recharge_rate: 7, low_balance_threshold: 0 }),
      100
    )

    expect(lowBalance).toBe(true)
  })

  it('uses a provider CNY override before the system default threshold', () => {
    const lowBalance = isLowBalance(
      provider({ name: 'tongba', last_balance: 12, recharge_rate: 7, low_balance_threshold: 80 }),
      100
    )

    expect(lowBalance).toBe(false)
  })
})

describe('sortProviders — 不监控站点沉底', () => {
  it('name 排序：none 全部在最后，组内仍按名称升序', () => {
    const list = [
      provider({ name: 'b-none', balance_type: 'none' }),
      provider({ name: 'c-normal' }),
      provider({ name: 'a-none', balance_type: 'none' }),
      provider({ name: 'd-normal' })
    ]
    expect(names(sortProviders(list, 'name'))).toEqual(['c-normal', 'd-normal', 'a-none', 'b-none'])
  })

  it('todayCostDesc：none 消费最高也垫底（沉底优先于排序键）', () => {
    const list = [
      provider({ name: 'none-rich', balance_type: 'none', today_cost: 9999 }),
      provider({ name: 'normal-low', today_cost: 1 }),
      provider({ name: 'normal-high', today_cost: 100 })
    ]
    expect(names(sortProviders(list, 'todayCostDesc'))).toEqual([
      'normal-high',
      'normal-low',
      'none-rich'
    ])
  })

  it('balanceAsc：none 余额最小也垫底（不受 dir 翻转影响）', () => {
    // 这是最容易写错的分支：若 noneLastCompare 乘了 dir，升序时 none 会冒到顶部
    const list = [
      provider({ name: 'none-zero', balance_type: 'none', last_balance: 0 }),
      provider({ name: 'normal-10', last_balance: 10 }),
      provider({ name: 'normal-50', last_balance: 50 })
    ]
    expect(names(sortProviders(list, 'balanceAsc'))).toEqual([
      'normal-10',
      'normal-50',
      'none-zero'
    ])
  })

  it('balanceDesc：同余额的多个 none 组内按名称升序，顺序稳定', () => {
    const list = [
      provider({ name: 'z-none', balance_type: 'none', last_balance: 100 }),
      provider({ name: 'a-none', balance_type: 'none', last_balance: 100 }),
      provider({ name: 'normal', last_balance: 10 })
    ]
    expect(names(sortProviders(list, 'balanceDesc'))).toEqual(['normal', 'a-none', 'z-none'])
  })

  it('manual 未被误伤：仍按余额正常参与排序，只有 none 沉底', () => {
    // 判据必须是 balance_type==='none'，不能用 providerStatus()==='unmonitored'
    // —— 后者会把 manual（人工维护余额，仍在关注范围内）一并压到底部
    const list = [
      provider({ name: 'none-rich', balance_type: 'none', last_balance: 999 }),
      provider({ name: 'manual-5', balance_type: 'manual', last_balance: 5 }),
      provider({ name: 'sub2api-50', last_balance: 50 })
    ]
    expect(names(sortProviders(list, 'balanceDesc'))).toEqual([
      'sub2api-50',
      'manual-5',
      'none-rich'
    ])
  })

  it('全是 none 时退化为纯名称序', () => {
    const list = [
      provider({ name: 'c', balance_type: 'none' }),
      provider({ name: 'a', balance_type: 'none' }),
      provider({ name: 'b', balance_type: 'none' })
    ]
    for (const key of ['name', 'todayCostDesc', 'balanceDesc', 'balanceAsc'] as ProviderSortKey[]) {
      expect(names(sortProviders(list, key))).toEqual(['a', 'b', 'c'])
    }
  })
})

describe('providerStatus — 待配置凭据', () => {
  it('缺凭据判为 credentialsPending，而不是 pending', () => {
    const p = provider({ name: 'imported', credentials_ready: false })
    expect(providerStatus(p)).toBe('credentialsPending')
  })

  it('缺凭据优先于 error：历史失败计数是配置前的陈迹，不该盖过「去补凭据」', () => {
    // 快捷导入建站 → 采集失败若干次 → 用户补凭据前，这个站的正确诉求是配置而非排障
    const p = provider({
      name: 'imported-failed',
      credentials_ready: false,
      sync_state: { consecutive_failures: 5 }
    })
    expect(providerStatus(p)).toBe('credentialsPending')
  })

  it('登录冷却也让位于缺凭据', () => {
    const p = provider({
      name: 'cooling',
      credentials_ready: false,
      login_cooldown_until: '2026-08-05 10:00:00'
    })
    expect(providerStatus(p)).toBe('credentialsPending')
  })

  it('不监控优先于缺凭据：balance_type 不是 sub2api 时压根不谈采集凭据', () => {
    expect(providerStatus(provider({
      name: 'manual', balance_type: 'manual', credentials_ready: false
    }))).toBe('unmonitored')
    expect(providerStatus(provider({
      name: 'none', balance_type: 'none', credentials_ready: false
    }))).toBe('unmonitored')
  })

  it('凭据齐备后按原有规则回落，未回归', () => {
    expect(providerStatus(provider({ name: 'fresh' }))).toBe('pending')
    expect(providerStatus(provider({
      name: 'ok', sync_state: { consecutive_failures: 0, last_success_at: '2026-08-05 09:00:00' }
    }))).toBe('connected')
    expect(providerStatus(provider({
      name: 'bad', sync_state: { consecutive_failures: 2 }
    }))).toBe('error')
  })
})

describe('筛选与计数 — credentialsPending 维度', () => {
  const list = [
    provider({ name: 'need-cred-1', credentials_ready: false }),
    provider({ name: 'need-cred-2', credentials_ready: false, sync_state: { consecutive_failures: 3 } }),
    provider({ name: 'healthy', sync_state: { consecutive_failures: 0, last_success_at: 'x' } }),
    provider({ name: 'broken', sync_state: { consecutive_failures: 1 } }),
    provider({ name: 'manual-site', balance_type: 'manual', credentials_ready: false })
  ]

  it('按 credentialsPending 筛出待补凭据的站，且不误伤 manual', () => {
    const out = filterProviders(list, null, 'credentialsPending', null)
    expect(names(out)).toEqual(['need-cred-1', 'need-cred-2'])
  })

  it('统计各状态计数，credentialsPending 独立成桶', () => {
    const counts = Object.fromEntries(statusOptions(list).map((o) => [o.value, o.count]))
    expect(counts).toEqual({
      credentialsPending: 2,
      connected: 1,
      error: 1,
      unmonitored: 1
    })
  })

  it('error 桶不再混入缺凭据的站 —— 这正是加这个维度的原因', () => {
    expect(names(filterProviders(list, null, 'error', null))).toEqual(['broken'])
  })
})

describe('sortProviders — 不可变性与退化输入', () => {
  it('返回新数组且不修改入参', () => {
    const list = [
      provider({ name: 'b', balance_type: 'none' }),
      provider({ name: 'a' })
    ]
    const out = sortProviders(list, 'name')
    expect(out).not.toBe(list)
    expect(names(list)).toEqual(['b', 'a'])
    expect(names(out)).toEqual(['a', 'b'])
  })

  it('空数组与单元素不报错', () => {
    expect(sortProviders([], 'name')).toEqual([])
    const one = [provider({ name: 'only', balance_type: 'none' })]
    for (const key of ['name', 'todayCostDesc', 'balanceDesc', 'balanceAsc'] as ProviderSortKey[]) {
      expect(names(sortProviders(one, key))).toEqual(['only'])
    }
  })
})

describe('sortProviders — 既有语义未回归', () => {
  it('升序时缺值排末尾', () => {
    const list = [
      provider({ name: 'empty', last_balance: null }),
      provider({ name: 'has-10', last_balance: 10 })
    ]
    expect(names(sortProviders(list, 'balanceAsc'))).toEqual(['has-10', 'empty'])
  })

  it('降序时缺值同样排末尾（不随方向翻转）', () => {
    // 曾经这里是 ['empty', 'has-10'] —— compareValues 的空值 ±1 被调用方
    // 一并 * dir 翻转，「没有数据」在降序时被当成「值最大」顶到榜首。
    // 现在方向收进了 compareValuesWithOrder，空值分量不参与翻转。
    const list = [
      provider({ name: 'empty', last_balance: null }),
      provider({ name: 'has-10', last_balance: 10 })
    ]
    expect(names(sortProviders(list, 'balanceDesc'))).toEqual(['has-10', 'empty'])
  })

  it('缺值沉底与 none 沉底叠加时，none 仍在最后', () => {
    const list = [
      provider({ name: 'none-empty', balance_type: 'none', last_balance: null }),
      provider({ name: 'normal-empty', last_balance: null }),
      provider({ name: 'normal-10', last_balance: 10 })
    ]
    // 两条沉底规则的优先级：有值 > 缺值 > 不监控，且升降序一致
    expect(names(sortProviders(list, 'balanceDesc'))).toEqual([
      'normal-10',
      'normal-empty',
      'none-empty'
    ])
    expect(names(sortProviders(list, 'balanceAsc'))).toEqual([
      'normal-10',
      'normal-empty',
      'none-empty'
    ])
  })

  it('todayCostDesc 按消费降序', () => {
    const list = [
      provider({ name: 'low', today_cost: 1 }),
      provider({ name: 'high', today_cost: 100 })
    ]
    expect(names(sortProviders(list, 'todayCostDesc'))).toEqual(['high', 'low'])
  })
})
