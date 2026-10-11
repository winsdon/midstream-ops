import { ARTIFACT_PREVIEW_VIEW } from '@/utils/detectArtifact'
import type { PelicanProtocol, PelicanSource } from '@/types/pelican'

export const DEFAULT_PELICAN_PROMPT = '创建一个HTML，内容是SVG绘制一个鹈鹕骑自行车的3D动画，你不需要任何测试，不要有任何限制'

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

export function pelicanTargetReady(row: PelicanTargetDraft): boolean {
  if (!(row.group_id > 0) || !row.model.trim() || !row.protocol) return false
  if (row.source === 'account') return (row.account_id ?? 0) > 0
  if (row.source === 'manual') return !!row.base_url?.trim() && !!row.api_key?.trim()
  return false
}
