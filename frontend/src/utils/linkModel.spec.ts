import { describe, it, expect } from 'vitest'
import {
  searchLinkGroups,
  visibleSelectedCount,
  isGroupAllSelected,
  toggleGroup
} from '@/utils/linkModel'
import type { GroupedAccount, URLGroupItem } from '@/types'

function account(overrides: Partial<GroupedAccount> & { id: number }): GroupedAccount {
  return {
    name: `acc-${overrides.id}`,
    platform: 'anthropic',
    status: 'active',
    linked_to: '',
    ...overrides
  }
}

function group(base: string, accounts: GroupedAccount[], overrides?: Partial<URLGroupItem>): URLGroupItem {
  return {
    base_url: base,
    sample_url: base + '/v1',
    account_count: accounts.length,
    accounts,
    suggested_name: base,
    existing_provider: '',
    ...overrides
  }
}

const groups: URLGroupItem[] = [
  group('https://alpha.example.com', [
    account({ id: 1, name: '【甲站】key-one' }),
    account({ id: 2, name: '【甲站】key-two', platform: 'openai' })
  ]),
  group('https://beta.example.com', [
    account({ id: 30, name: 'zeta-key', linked_to: '乙站' })
  ], { existing_provider: '乙站' })
]

const names = (gs: URLGroupItem[]) => gs.flatMap((g) => g.accounts.map((a) => a.id))

describe('searchLinkGroups', () => {
  it('空查询返回全部', () => {
    expect(names(searchLinkGroups(groups, ''))).toEqual([1, 2, 30])
    expect(names(searchLinkGroups(groups, '   '))).toEqual([1, 2, 30])
  })

  it('按账号名匹配', () => {
    expect(names(searchLinkGroups(groups, 'key-one'))).toEqual([1])
  })

  it('按平台匹配', () => {
    expect(names(searchLinkGroups(groups, 'openai'))).toEqual([2])
  })

  it('按账号 id 匹配 —— 账号名有时是不可读的哈希', () => {
    expect(names(searchLinkGroups(groups, '30'))).toEqual([30])
  })

  it('按已归属站点名匹配', () => {
    expect(names(searchLinkGroups(groups, '乙站'))).toEqual([30])
  })

  it('按站点地址匹配时保留整组 —— 搜地址的意图是「这一站的全部账号」', () => {
    expect(names(searchLinkGroups(groups, 'alpha'))).toEqual([1, 2])
  })

  it('大小写不敏感', () => {
    expect(names(searchLinkGroups(groups, 'ALPHA'))).toEqual([1, 2])
    expect(names(searchLinkGroups(groups, 'OpenAI'))).toEqual([2])
  })

  it('无匹配的组整组剔除，不留空框', () => {
    const out = searchLinkGroups(groups, 'key-one')
    expect(out).toHaveLength(1)
    expect(out[0].base_url).toBe('https://alpha.example.com')
  })

  it('完全无匹配返回空数组', () => {
    expect(searchLinkGroups(groups, '不存在的账号')).toEqual([])
  })

  it('不修改入参 —— 否则清空搜索拿不回全量', () => {
    const before = groups[0].accounts.length
    searchLinkGroups(groups, 'key-one')
    expect(groups[0].accounts).toHaveLength(before)
  })
})

describe('visibleSelectedCount', () => {
  it('只数落在可见组里的勾选', () => {
    const visible = searchLinkGroups(groups, 'key-one') // 只剩 id 1
    expect(visibleSelectedCount(visible, [1, 2, 30])).toBe(1)
  })

  it('全部可见时等于勾选总数', () => {
    expect(visibleSelectedCount(groups, [1, 30])).toBe(2)
  })

  it('无勾选或无可见组时为 0', () => {
    expect(visibleSelectedCount(groups, [])).toBe(0)
    expect(visibleSelectedCount([], [1, 2])).toBe(0)
  })

  it('勾选了已不存在的账号不计入（悬垂关联）', () => {
    expect(visibleSelectedCount(groups, [999])).toBe(0)
  })
})

describe('isGroupAllSelected', () => {
  it('组内全部勾上才为 true', () => {
    expect(isGroupAllSelected(groups[0], [1, 2])).toBe(true)
    expect(isGroupAllSelected(groups[0], [1])).toBe(false)
  })

  it('组外勾选不影响判定', () => {
    expect(isGroupAllSelected(groups[0], [1, 2, 30])).toBe(true)
  })

  it('空组返回 false —— 显示成「已全选」会让人以为勾了东西', () => {
    expect(isGroupAllSelected(group('https://empty.example.com', []), [])).toBe(false)
    expect(isGroupAllSelected(group('https://empty.example.com', []), [1, 2])).toBe(false)
  })
})

describe('toggleGroup', () => {
  it('未全选时并入本组全部', () => {
    expect(toggleGroup(groups[0], []).sort()).toEqual([1, 2])
    expect(toggleGroup(groups[0], [1]).sort()).toEqual([1, 2])
  })

  it('已全选时取消本组', () => {
    expect(toggleGroup(groups[0], [1, 2])).toEqual([])
  })

  it('不影响组外勾选', () => {
    expect(toggleGroup(groups[0], [30]).sort()).toEqual([1, 2, 30])
    expect(toggleGroup(groups[0], [1, 2, 30])).toEqual([30])
  })

  it('搜索过滤后只作用于可见账号 —— 所见即所选', () => {
    // 组内两个账号，搜索后只剩 id 1；全选本组应只勾中 1，不碰隐藏的 2
    const visible = searchLinkGroups(groups, 'key-one')
    expect(toggleGroup(visible[0], [])).toEqual([1])
  })

  it('返回新数组，不修改入参', () => {
    const selected = [1]
    const out = toggleGroup(groups[0], selected)
    expect(out).not.toBe(selected)
    expect(selected).toEqual([1])
  })

  it('不产生重复 id', () => {
    expect(toggleGroup(groups[0], [1, 1, 2]).sort()).toEqual([])
    const merged = toggleGroup(groups[0], [1])
    expect(new Set(merged).size).toBe(merged.length)
  })
})
