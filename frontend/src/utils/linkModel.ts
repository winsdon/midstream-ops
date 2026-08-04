/**
 * 关联账号弹窗的搜索与分组选择纯函数（不依赖 Vue，便于单测）。
 *
 * 后端 /providers/scan-urls 全量返回所有账号（无分页、无 LIMIT），
 * 故搜索一律本地完成，与 providerModel 的筛选哲学一致。
 */

import type { GroupedAccount, URLGroupItem } from '@/types'

/**
 * 按账号名 / 平台 / 站点地址 / 已归属站点名 / 账号 id 做大小写不敏感子串搜索。
 *
 * 五个字段都匹配：用户可能记得账号名，也可能只记得「某个 anthropic 的号」
 * 或「甲站下面那个号」。id 一并匹配，因为账号名有时是不可读的哈希。
 *
 * 组内无匹配则整组剔除，不留下一排只有 URL 标题的空框；
 * 但组的 base_url 本身命中时保留整组 —— 用户搜站点地址的意图是「这一站的全部账号」。
 */
export function searchLinkGroups(groups: readonly URLGroupItem[], query: string): URLGroupItem[] {
  const q = query.trim().toLowerCase()
  if (!q) return [...groups]

  const out: URLGroupItem[] = []
  for (const g of groups) {
    const groupHit =
      g.base_url.toLowerCase().includes(q) ||
      g.sample_url.toLowerCase().includes(q) ||
      g.existing_provider.toLowerCase().includes(q)

    const accounts = groupHit ? g.accounts : g.accounts.filter((a) => matchAccount(a, q))
    if (accounts.length === 0) continue
    // 返回新对象而非改 g.accounts：入参是响应数据，改它会让「清空搜索」拿不回全量
    out.push({ ...g, accounts })
  }
  return out
}

/** q 必须已 trim + toLowerCase。 */
function matchAccount(a: GroupedAccount, q: string): boolean {
  return (
    a.name.toLowerCase().includes(q) ||
    a.platform.toLowerCase().includes(q) ||
    a.linked_to.toLowerCase().includes(q) ||
    String(a.id).includes(q)
  )
}

/** 展开所有组的账号 id（去重）。 */
function accountIdsOf(groups: readonly URLGroupItem[]): Set<number> {
  const ids = new Set<number>()
  for (const g of groups) {
    for (const a of g.accounts) ids.add(a.id)
  }
  return ids
}

/**
 * 已勾选 id 中落在当前可见组里的数量。
 *
 * 保存是全量替换语义，被搜索隐藏的勾选项会照样提交 —— 这是对的，但用户
 * 搜到 2 个勾选时容易以为保存后就只剩这 2 个。调用方用它算出隐藏数量并明示。
 */
export function visibleSelectedCount(
  groups: readonly URLGroupItem[],
  selected: readonly number[]
): number {
  const visible = accountIdsOf(groups)
  return selected.filter((id) => visible.has(id)).length
}

/**
 * 组内是否全选。
 *
 * 空组返回 false —— 把空组显示成「已全选」会让人以为勾了东西。
 */
export function isGroupAllSelected(group: URLGroupItem, selected: readonly number[]): boolean {
  if (group.accounts.length === 0) return false
  const picked = new Set(selected)
  return group.accounts.every((a) => picked.has(a.id))
}

/**
 * 切换整组，返回新数组（不修改入参）。
 *
 * 只作用于传入组的账号，不影响组外勾选 —— 搜索过滤后传进来的是可见子集，
 * 「全选本组」于是自然地只作用于看得见的账号，符合所见即所选的直觉。
 */
export function toggleGroup(group: URLGroupItem, selected: readonly number[]): number[] {
  const groupIds = group.accounts.map((a) => a.id)
  if (isGroupAllSelected(group, selected)) {
    const drop = new Set(groupIds)
    return selected.filter((id) => !drop.has(id))
  }
  const merged = new Set(selected)
  for (const id of groupIds) merged.add(id)
  return [...merged]
}
