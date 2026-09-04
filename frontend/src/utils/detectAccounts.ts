/**
 * 模型检测账号弹窗的搜索与分组纯函数（不依赖 Vue，便于单测）。
 *
 * 分组口径是本站 account_groups，不是供应商、也不是站点地址。
 * 同一账号可出现在多个组，勾选 id 全局共享。
 */

import type { DetectAccount } from '@/types/detect'

/** 未加入任何本站分组的账号归入此哨兵名，展示时再 i18n。恒排最后。 */
export const UNGROUPED_NAME = '__ungrouped__'

export interface DetectAccountGroup {
  name: string
  ungrouped: boolean
  accounts: DetectAccount[]
}

/** 按本站分组归拢。命名组按名称排序，未分组沉底。组内保持账号原顺序。 */
export function groupDetectAccounts(accounts: readonly DetectAccount[]): DetectAccountGroup[] {
  const named = new Map<string, DetectAccount[]>()
  const ungrouped: DetectAccount[] = []

  for (const a of accounts) {
    const groups = (a.groups ?? []).map((g) => g.trim()).filter(Boolean)
    if (groups.length === 0) {
      ungrouped.push(a)
      continue
    }
    for (const name of groups) {
      const list = named.get(name)
      if (list) list.push(a)
      else named.set(name, [a])
    }
  }

  const names = [...named.keys()].sort((a, b) => a.localeCompare(b, 'zh'))
  const out: DetectAccountGroup[] = names.map((name) => ({
    name,
    ungrouped: false,
    accounts: named.get(name) as DetectAccount[]
  }))
  if (ungrouped.length) {
    out.push({ name: UNGROUPED_NAME, ungrouped: true, accounts: ungrouped })
  }
  return out
}

/**
 * 按账号名 / 分组名 / 供应商 / 站点地址 / id 做大小写不敏感子串搜索。
 *
 * 组名命中则保留整组；否则只留匹配的账号。空组剔除。
 */
export function searchDetectAccounts(
  groups: readonly DetectAccountGroup[],
  query: string
): DetectAccountGroup[] {
  const q = query.trim().toLowerCase()
  if (!q) return groups.map(cloneGroup)

  const out: DetectAccountGroup[] = []
  for (const g of groups) {
    const groupHit = !g.ungrouped && g.name.toLowerCase().includes(q)
    const accounts = groupHit ? g.accounts : g.accounts.filter((a) => matchAccount(a, q))
    if (accounts.length === 0) continue
    out.push({ ...g, accounts })
  }
  return out
}

function cloneGroup(g: DetectAccountGroup): DetectAccountGroup {
  return { ...g, accounts: [...g.accounts] }
}

function matchAccount(a: DetectAccount, q: string): boolean {
  if (a.account_name.toLowerCase().includes(q)) return true
  if (a.provider_name.toLowerCase().includes(q)) return true
  if (a.base_url.toLowerCase().includes(q)) return true
  if (String(a.account_id).includes(q)) return true
  return (a.groups ?? []).some((g) => g.toLowerCase().includes(q))
}

export function isDetectGroupAllSelected(
  group: DetectAccountGroup,
  selected: readonly number[]
): boolean {
  if (group.accounts.length === 0) return false
  const picked = new Set(selected)
  return group.accounts.every((a) => picked.has(a.account_id))
}

/**
 * 切换整组，返回新数组。
 *
 * 未全选则并入（受 max 截断，先到先得）；已全选则只去掉本组，组外勾选保留。
 */
export function toggleDetectGroup(
  group: DetectAccountGroup,
  selected: readonly number[],
  max: number
): number[] {
  const groupIds = group.accounts.map((a) => a.account_id)
  if (groupIds.length === 0) return [...selected]
  if (isDetectGroupAllSelected(group, selected)) {
    const drop = new Set(groupIds)
    return selected.filter((id) => !drop.has(id))
  }
  const next = [...selected]
  const have = new Set(selected)
  for (const id of groupIds) {
    if (have.has(id)) continue
    if (next.length >= max) break
    next.push(id)
    have.add(id)
  }
  return next
}
