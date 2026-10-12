import { ARTIFACT_PREVIEW_VIEW } from '@/utils/detectArtifact'
import type { PelicanProtocol, PelicanSource } from '@/types/pelican'

export const DEFAULT_PELICAN_PROMPT = '生成一张考拉骑自行车的 SVG 图像并添加动画效果'

/** Inclusive local calendar dates become an exclusive UTC upper bound. */
export function pelicanDateRange(from: string, to: string): Record<string, string> {
  const range: Record<string, string> = {}
  if (from) range.from = new Date(`${from}T00:00:00`).toISOString()
  if (to) {
    const end = new Date(`${to}T00:00:00`)
    end.setDate(end.getDate() + 1)
    range.to = end.toISOString()
  }
  return range
}
export function pelicanPending(status?: string) { return status === 'queued' || status === 'running' }

/** UUIDs must work on development HTTP hosts too. */
export function pelicanRequestID(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(16))
  return Array.from(bytes, b => b.toString(16).padStart(2, '0')).join('')
}

/** 缩放系数：取宽高比例的较小值，永不放大。框尺寸无效时返回 0（不渲染）。 */
export function pelicanPreviewScale(boxWidth: number, boxHeight: number, view = ARTIFACT_PREVIEW_VIEW): number {
  if (!(boxWidth > 0) || !(boxHeight > 0) || !(view.width > 0) || !(view.height > 0)) return 0
  return Math.min(boxWidth / view.width, boxHeight / view.height, 1)
}

/** 账号列表里的地址展示：去掉协议与尾斜杠，避免长 URL 撑破下拉。 */
export function pelicanHost(url: string): string {
  return (url ?? '').replace(/^https?:\/\//i, '').replace(/\/+$/, '')
}

/** 账号来源按账号平台限定协议；手填来源可以三选一。 */
export function pelicanProtocolOptions(platform?: string): PelicanProtocol[] {
  switch ((platform ?? '').toLowerCase()) {
    case 'anthropic': return ['messages']
    case 'openai': return ['responses', 'chat']
    default: return ['messages', 'responses', 'chat']
  }
}

/** 目标行草稿：协议在未选定时为空串。 */
export interface PelicanTargetDraft {
  group_id: number
  source: PelicanSource | ''
  account_id?: number
  base_url?: string
  api_key?: string
  model: string
  protocol: PelicanProtocol | ''
}

/** 表单里的一行。uid 只用于列表 key，不提交。 */
export interface PelicanTargetRow extends PelicanTargetDraft {
  uid: number
  account_id: number
  base_url: string
  api_key: string
}

export type PelicanStatusVariant = 'primary' | 'success' | 'warning' | 'danger' | 'gray'

/** 状态徽章颜色。未知状态用灰色，避免进行中和失败看起来一样。 */
export function pelicanStatusVariant(status?: string): PelicanStatusVariant {
  switch (status) {
    case 'completed': return 'success'
    case 'failed': return 'danger'
    case 'queued':
    case 'running': return 'primary'
    case 'cancelled':
    case 'interrupted': return 'warning'
    default: return 'gray'
  }
}

/** 已发布或仍在生成的记录不能删除。 */
export function pelicanDeletable(item?: { published_at?: string | null; status?: string } | null): boolean {
  return !!item && !item.published_at && !pelicanPending(item.status)
}

export function pelicanAccountPlatform(
  accounts: ReadonlyArray<{ id: number; platform: string }>,
  row: { source: PelicanSource | ''; account_id?: number }
): string | undefined {
  if (row.source !== 'account') return undefined
  return accounts.find(account => account.id === row.account_id)?.platform
}

/** 协议落在当前来源允许的范围内；不允许时改成该范围的第一项。 */
export function pelicanNormalizeRow(row: PelicanTargetRow, platform?: string): PelicanTargetRow {
  const allowed = pelicanProtocolOptions(platform)
  if (row.protocol && allowed.includes(row.protocol)) return row
  return { ...row, protocol: allowed[0] ?? '' }
}

/** 新建一行。只有一个账号时直接选中，并按该账号平台锁定协议。 */
export function pelicanBlankRow(uid: number, accounts: ReadonlyArray<{ id: number; platform: string }>): PelicanTargetRow {
  const only = accounts.length === 1 ? accounts[0] : undefined
  const row: PelicanTargetRow = {
    uid,
    group_id: 0,
    source: accounts.length ? 'account' : 'manual',
    account_id: only?.id ?? 0,
    base_url: '',
    api_key: '',
    model: '',
    protocol: ''
  }
  return pelicanNormalizeRow(row, only ? only.platform : undefined)
}

export function pelicanProtocolLabel(protocol: string): string {
  const labels: Record<PelicanProtocol, string> = { messages: 'Messages', responses: 'Responses', chat: 'Chat Completions' }
  return labels[protocol as PelicanProtocol] ?? protocol
}

export function pelicanTargetReady(row: PelicanTargetDraft): boolean {
  if (!(row.group_id > 0) || !row.model.trim() || !row.protocol) return false
  if (row.source === 'account') return (row.account_id ?? 0) > 0
  if (row.source === 'manual') return !!row.base_url?.trim() && !!row.api_key?.trim()
  return false
}
