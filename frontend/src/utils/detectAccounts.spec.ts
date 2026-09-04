import { describe, it, expect } from 'vitest'
import {
  UNGROUPED_NAME,
  groupDetectAccounts,
  isDetectGroupAllSelected,
  searchDetectAccounts,
  toggleDetectGroup
} from '@/utils/detectAccounts'
import type { DetectAccount } from '@/types/detect'

function acc(overrides: Partial<DetectAccount> & { account_id: number }): DetectAccount {
  return {
    account_name: `acc-${overrides.account_id}`,
    platform: 'anthropic',
    base_url: 'https://api.example.com',
    provider_id: 0,
    provider_name: '',
    probe_model: '',
    groups: [],
    ...overrides
  }
}

describe('groupDetectAccounts', () => {
  it('按本站分组归拢，同一账号可出现在多个组，未分组沉底', () => {
    const accounts = [
      acc({ account_id: 1, account_name: 'max-1', groups: ['claude-max', 'vip'] }),
      acc({ account_id: 2, account_name: 'orphan' }),
      acc({ account_id: 3, account_name: 'max-2', groups: ['claude-max'] })
    ]
    const groups = groupDetectAccounts(accounts)
    expect(groups.map((g) => g.name)).toEqual(['claude-max', 'vip', UNGROUPED_NAME])
    expect(groups[0].accounts.map((a) => a.account_id)).toEqual([1, 3])
    expect(groups[1].accounts.map((a) => a.account_id)).toEqual([1])
    expect(groups[2].ungrouped).toBe(true)
    expect(groups[2].accounts.map((a) => a.account_id)).toEqual([2])
  })

  it('空列表返回空，不造一个空的未分组', () => {
    expect(groupDetectAccounts([])).toEqual([])
  })
})

describe('searchDetectAccounts', () => {
  const grouped = groupDetectAccounts([
    acc({ account_id: 1, account_name: '【甲】max-key', groups: ['claude-max'], provider_name: '甲站' }),
    acc({ account_id: 2, account_name: 'bedrock-key', groups: ['bedrock'], base_url: 'https://bedrock.example.com' }),
    acc({ account_id: 3, account_name: 'loose', groups: [] })
  ])

  it('空查询返回全部组', () => {
    expect(searchDetectAccounts(grouped, '  ').map((g) => g.name)).toEqual([
      'bedrock',
      'claude-max',
      UNGROUPED_NAME
    ])
  })

  it('按账号名 / 分组名 / 供应商 / 地址 / id 匹配，无命中的组剔除', () => {
    expect(searchDetectAccounts(grouped, '甲').flatMap((g) => g.accounts.map((a) => a.account_id))).toEqual([1])
    expect(searchDetectAccounts(grouped, 'bedrock').map((g) => g.name)).toEqual(['bedrock'])
    expect(searchDetectAccounts(grouped, '2').flatMap((g) => g.accounts.map((a) => a.account_id))).toEqual([2])
  })

  it('分组名命中时保留整组', () => {
    const hit = searchDetectAccounts(grouped, 'claude-max')
    expect(hit).toHaveLength(1)
    expect(hit[0].accounts.map((a) => a.account_id)).toEqual([1])
  })
})

describe('toggleDetectGroup', () => {
  const grouped = groupDetectAccounts([
    acc({ account_id: 1, groups: ['g'] }),
    acc({ account_id: 2, groups: ['g'] }),
    acc({ account_id: 3, groups: ['other'] })
  ])
  const g = grouped[0]

  it('未全选则并入本组，已全选则只去掉本组', () => {
    expect(isDetectGroupAllSelected(g, [])).toBe(false)
    const selected = toggleDetectGroup(g, [3], 10)
    expect(selected.sort((a, b) => a - b)).toEqual([1, 2, 3])
    expect(isDetectGroupAllSelected(g, selected)).toBe(true)
    expect(toggleDetectGroup(g, selected, 10)).toEqual([3])
  })

  it('全选本组不超过上限，溢出的账号不加进去', () => {
    const got = toggleDetectGroup(g, [9, 8], 3)
    expect(got).toHaveLength(3)
    expect(got.slice(0, 2)).toEqual([9, 8])
    expect([1, 2]).toContain(got[2])
  })

  it('空组切换是空操作', () => {
    expect(toggleDetectGroup({ name: 'x', ungrouped: false, accounts: [] }, [1], 10)).toEqual([1])
  })
})
