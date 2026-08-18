/**
 * 从上游分组建号用的命名、匹配、默认值（纯函数，不依赖 Vue）。
 *
 * 与 backend/internal/service/sub2api_provision.go 同构：预览和落库必须长一样。
 */

export const SELF_KEY_BRAND = 'kaola'

export interface MatchableAccount {
  id: number
  name: string
  status?: string
  rate_multiplier?: number
  groups?: string[] | null
}

export interface GroupConnection {
  upstream_group: string
  local_account_id: number
}

export interface LocalGroupOpt {
  id: number
  name: string
  rate: number
  platform?: string
}

/** 整数原样，否则去尾零（1.00 → 1，0.50 → 0.5）。 */
export function formatRate(rate: number): string {
  if (!Number.isFinite(rate) || rate === 0) return '0'
  const s = rate.toFixed(4).replace(/\.?0+$/, '')
  return s === '' || s === '-0' ? '0' : s
}

export function upstreamKeyName(group: string, rate: number): string {
  return `【${SELF_KEY_BRAND}】${group}-${formatRate(rate)}`
}

export function localAccountName(provider: string, group: string, rate: number): string {
  return `【${provider}】${group}-${formatRate(rate)}`
}

/**
 * 某上游分组下的本站账号 = key 指纹命中 ∪ 本系统建号对接记录。
 * 不再按账号名猜分组，那套和成本明细对不上。
 */
export function matchAccountsToGroup<T extends MatchableAccount>(
  accounts: T[],
  groupName: string,
  keyHits: MatchableAccount[] = [],
  connections: GroupConnection[] = []
): MatchableAccount[] {
  const byID = new Map(accounts.map((a) => [a.id, a]))
  const out = new Map<number, MatchableAccount>()

  for (const a of keyHits) out.set(a.id, byID.get(a.id) ?? a)

  for (const c of connections) {
    if (c.upstream_group !== groupName) continue
    const acc = byID.get(c.local_account_id)
    if (acc) out.set(acc.id, acc)
  }

  return [...out.values()].sort((a, b) => a.id - b.id)
}

/** 关联组默认：名称全等 → 同平台最近倍率 → 空。 */
export function defaultLocalGroupIds(
  localGroups: LocalGroupOpt[],
  upstreamGroupName: string,
  upstreamRate: number,
  upstreamPlatform?: string
): number[] {
  const exact = localGroups.filter((g) => g.name === upstreamGroupName)
  if (exact.length > 0) return [exact[0].id]

  const platform = (upstreamPlatform || '').trim()
  if (!platform) return []

  const same = localGroups.filter((g) => (g.platform || '') === platform)
  if (same.length === 0) return []

  let best = same[0]
  let bestDist = Math.abs(best.rate - upstreamRate)
  for (const g of same.slice(1)) {
    const d = Math.abs(g.rate - upstreamRate)
    if (d < bestDist || (d === bestDist && g.id < best.id)) {
      best = g
      bestDist = d
    }
  }
  return [best.id]
}

/** 去空白、去空串、保序去重。 */
export function uniqueAccountBaseURLs(urls: Array<string | null | undefined>): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const raw of urls) {
    const s = (raw || '').trim()
    if (!s || seen.has(s)) continue
    seen.add(s)
    out.push(s)
  }
  return out
}

/** pick(n) 返回 [0, n) 的下标；缺省用 Math.random。 */
export function pickAccountBaseURL(
  urls: Array<string | null | undefined>,
  pick: (n: number) => number = (n) => Math.floor(Math.random() * n)
): string {
  const uniq = uniqueAccountBaseURLs(urls)
  if (uniq.length === 0) return ''
  if (uniq.length === 1) return uniq[0]
  const i = pick(uniq.length)
  const idx = Number.isInteger(i) && i >= 0 && i < uniq.length ? i : 0
  return uniq[idx]
}
