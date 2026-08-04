/**
 * 模型广场的筛选、排序与价格格式化纯函数（便于单测，不依赖 Vue）。
 */

import type { PlazaModel } from '@/types/plaza'
import { compareValuesWithOrder, type SortOrder } from '@/utils/tableSort'

export type SortKey = 'name' | 'priceAsc' | 'priceDesc'
export type ViewMode = 'grid' | 'list'
/** 价格单位：每 100 万 token 或每 1000 token。 */
export type UnitScale = 1_000_000 | 1_000

/**
 * 格式化按 token 计费的价格。
 * 后端给的是 USD/token，乘以 scale 得到「每 1M/1K token」的展示价。
 * 用 toPrecision 再去尾零，避免 IEEE 754 的显示噪声（如 2.9999999999996）。
 */
export function formatScaled(value: number | null | undefined, scale: number): string {
  if (value === null || value === undefined || Number.isNaN(value)) return '-'
  const scaled = value * scale
  if (scaled === 0) return '$0'
  return `$${Number(scaled.toPrecision(10))}`
}

/** 格式化按次计费的价格（不受单位切换影响）。 */
export function formatPerRequest(value: number | null | undefined): string {
  if (value === null || value === undefined || Number.isNaN(value)) return '-'
  return `$${Number(value.toPrecision(10))}`
}

/** 吞吐展示："82 t/s"；0 或缺失显示 "-"。 */
export function formatThroughput(tps: number | null | undefined): string {
  if (tps === null || tps === undefined || tps <= 0) return '-'
  return `${Math.round(tps)} t/s`
}

/**
 * 排序用有效价：per_request 比每次价，其余比输入价（缺省回退输出价）。
 * null 表示无价可比，排序时恒排末尾。
 */
export function sortPrice(model: PlazaModel): number | null {
  if (model.billing_mode === 'per_request') return model.price.per_request
  if (model.price.input !== null) return model.price.input
  return model.price.output
}

/** 按 sortKey 返回新数组（不修改入参）。无价模型始终排末尾。 */
export function sortModels(models: PlazaModel[], key: SortKey): PlazaModel[] {
  const sorted = [...models]
  if (key === 'name') {
    sorted.sort((a, b) => a.name.localeCompare(b.name))
    return sorted
  }
  const order: SortOrder = key === 'priceAsc' ? 'asc' : 'desc'
  sorted.sort((a, b) => {
    // 无价模型恒末尾、有价部分按方向翻转，两者的方向处理由
    // compareValuesWithOrder 内部隔开；同价回退按名称保证顺序稳定
    return compareValuesWithOrder(sortPrice(a), sortPrice(b), order) || a.name.localeCompare(b.name)
  })
  return sorted
}

/** 按模型名做大小写不敏感的子串搜索。 */
export function searchModels(models: PlazaModel[], query: string): PlazaModel[] {
  const q = query.trim().toLowerCase()
  if (!q) return models
  return models.filter((m) => m.name.toLowerCase().includes(q))
}

/** 侧栏筛选项：分组。 */
export interface GroupFilterOption {
  id: number
  name: string
  rate: number
  count: number
}

/** 侧栏筛选项：供应商平台。 */
export interface PlatformFilterOption {
  platform: string
  count: number
}

/** 统计各分组下的模型数（计数随搜索结果联动）。 */
export function groupOptions(models: PlazaModel[]): GroupFilterOption[] {
  const map = new Map<number, GroupFilterOption>()
  for (const m of models) {
    for (const g of m.groups ?? []) {
      const existing = map.get(g.id)
      if (existing) {
        existing.count++
      } else {
        map.set(g.id, { id: g.id, name: g.name, rate: g.rate_multiplier, count: 1 })
      }
    }
  }
  return [...map.values()].sort((a, b) => a.name.localeCompare(b.name))
}

/** 统计各平台下的模型数。 */
export function platformOptions(models: PlazaModel[]): PlatformFilterOption[] {
  const map = new Map<string, number>()
  for (const m of models) {
    for (const p of m.platforms ?? []) {
      map.set(p, (map.get(p) ?? 0) + 1)
    }
  }
  return [...map.entries()]
    .map(([platform, count]) => ({ platform, count }))
    .sort((a, b) => a.platform.localeCompare(b.platform))
}

/** 按分组与平台过滤（null 表示不限）。 */
export function filterModels(
  models: PlazaModel[],
  groupId: number | null,
  platform: string | null
): PlazaModel[] {
  let out = models
  if (groupId !== null) {
    out = out.filter((m) => (m.groups ?? []).some((g) => g.id === groupId))
  }
  if (platform !== null) {
    out = out.filter((m) => (m.platforms ?? []).includes(platform))
  }
  return out
}

/**
 * 探测状态 → 三根色条的等级。
 * 无探测数据时返回 'unknown'（渲染为灰条）。
 */
export type ProbeLevel = 'operational' | 'degraded' | 'failed' | 'unknown'

export function probeLevel(model: PlazaModel): ProbeLevel {
  const p = model.probe
  if (!p || p.total === 0) return 'unknown'
  const rate = p.success_count / p.total
  if (rate >= 0.95) return 'operational'
  if (rate >= 0.6) return 'degraded'
  return 'failed'
}

/** 平台展示名。未知平台原样返回。 */
export function platformLabel(platform: string): string {
  switch (platform) {
    case 'anthropic':
      return 'Anthropic'
    case 'openai':
      return 'OpenAI'
    case 'gemini':
      return 'Gemini'
    case 'antigravity':
      return 'Antigravity'
    default:
      return platform || 'API'
  }
}
