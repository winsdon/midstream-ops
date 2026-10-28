import { ARTIFACT_PREVIEW_VIEW } from '@/utils/detectArtifact'
import type { PelicanAccount, PelicanProtocol, PelicanRequest, PelicanSource, PelicanTarget } from '@/types/pelican'

export const DEFAULT_PELICAN_PROMPT = '生成一张考拉骑自行车的 SVG 图像并添加动画效果'
/** 全部目标都走 Messages（Claude）时的默认题目，与后端 DefaultPelicanClaudePrompt 一致。 */
export const DEFAULT_PELICAN_CLAUDE_PROMPT = '做个初音未来.html带人物'
/**
 * 「禁止思考」的提示词后缀：只是提示词层面的引导，不改请求参数。
 *
 * 实测（Claude Opus 5.5，thinking adaptive + effort low）：同一道初音题，
 * 加上这句后思考 token 从 6785 / 12315 降到 0，耗时分别从 333.7s / 389.8s 降到 186.8s / 184.9s，
 * 作品仍完整返回。它不是 API 层的关闭（`{"type":"disabled"}` 会被上游 400），
 * 所以复杂推理题上模型仍可能思考，也可能因为“不要思考”掉质量。
 */
export const PELICAN_NO_THINKING_SUFFIX = '直接输出完整 HTML，不要思考，不要解释。'
/** 思考强度（output_config.effort），只对 Messages 协议生效；与后端 modeldetect.EffortLevels 一致。 */
export const PELICAN_EFFORTS = ['low', 'medium', 'high', 'xhigh', 'max'] as const
export type PelicanEffort = typeof PELICAN_EFFORTS[number]
export const PELICAN_DEFAULT_EFFORT: PelicanEffort = 'low'
/** 模型检测里鹈鹕题的默认思考强度，与后端 modeldetect.DefaultPelicanEffort 一致。 */
export const DETECT_PELICAN_DEFAULT_EFFORT: PelicanEffort = 'medium'
/** 与后端 normalizePelicanRequest 的默认值一致：高级选项不填时按这套参数提交。 */
export const PELICAN_DEFAULT_CONCURRENCY = 3
export const PELICAN_DEFAULT_MAX_TOKENS = 32000
export const PELICAN_DEFAULT_TIMEOUT_SECONDS = 900
/** 单个批次的目标数上限，与后端 normalizePelicanRequest 的校验保持一致。 */
export const PELICAN_MAX_TARGETS = 100

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

export function pelicanProtocolLabel(protocol: string): string {
  const labels: Record<PelicanProtocol, string> = { messages: 'Messages', responses: 'Responses', chat: 'Chat Completions' }
  return labels[protocol as PelicanProtocol] ?? protocol
}

/** 发起测试弹窗的表单：一个归属分组 + 一个模型，账号可多选，每个账号生成一件作品。 */
export interface PelicanDraft {
  group_id: number
  source: PelicanSource
  /** 账号来源勾选的账号 id（提交时一个账号一个测试目标）。 */
  account_ids: number[]
  base_url: string
  api_key: string
  model: string
  /** 手填来源三选一；账号来源只用于 OpenAI 账号（anthropic 恒为 messages）。 */
  protocol: PelicanProtocol | ''
  prompt: string
  concurrency: number
  max_tokens: number
  timeout_seconds: number
  /** 思考强度；空串表示不指定（只会出现在旧的待确认提交里）。 */
  effort: PelicanEffort | ''
}

/** 空白表单。分组与账号等账号列表加载完成后再由调用方水合。 */
export function pelicanEmptyDraft(): PelicanDraft {
  return {
    group_id: 0,
    source: 'account',
    account_ids: [],
    base_url: '',
    api_key: '',
    model: '',
    // 手填来源必须自己挑协议（和改造前一致，避免默认值打错端点）；
    // 账号来源下这个空值不影响提交：anthropic 恒为 messages，OpenAI 落到 responses。
    protocol: '',
    prompt: DEFAULT_PELICAN_PROMPT,
    concurrency: PELICAN_DEFAULT_CONCURRENCY,
    max_tokens: PELICAN_DEFAULT_MAX_TOKENS,
    timeout_seconds: PELICAN_DEFAULT_TIMEOUT_SECONDS,
    effort: PELICAN_DEFAULT_EFFORT
  }
}

/** 按目标协议取默认题目：全部走 Messages（Claude）用初音未来，含 GPT 协议或尚无目标时用考拉。 */
export function pelicanDefaultPrompt(targets: readonly Pick<PelicanTarget, 'protocol'>[]): string {
  return targets.length && targets.every(target => target.protocol === 'messages') ? DEFAULT_PELICAN_CLAUDE_PROMPT : DEFAULT_PELICAN_PROMPT
}

/** 提示词是否已经带上了「禁止思考」后缀（只看末尾，避免误伤正文里出现的同一句话）。 */
export function pelicanHasNoThinking(prompt: string): boolean {
  return prompt.trimEnd().endsWith(PELICAN_NO_THINKING_SUFFIX)
}

/** 去掉末尾的「禁止思考」后缀，得到管理员原本写的提示词。 */
export function pelicanWithoutNoThinking(prompt: string): string {
  const trimmed = prompt.trimEnd()
  if (!pelicanHasNoThinking(trimmed)) return trimmed
  return trimmed.slice(0, -PELICAN_NO_THINKING_SUFFIX.length).trimEnd()
}

/**
 * 打开/关闭「禁止思考」后缀：重复勾选不会叠加，取消勾选只拿掉末尾那一句，
 * 不会动管理员自己写的内容。
 */
export function pelicanToggleNoThinking(prompt: string, enabled: boolean): string {
  const trimmed = prompt.trimEnd()
  const bare = pelicanWithoutNoThinking(trimmed)
  if (!enabled) return bare
  if (pelicanHasNoThinking(trimmed)) return trimmed
  return bare ? `${bare}\n${PELICAN_NO_THINKING_SUFFIX}` : PELICAN_NO_THINKING_SUFFIX
}

/**
 * 提示词仍是某一句默认题目（管理员没改过），换目标时可以跟着协议切换。
 * 「禁止思考」后缀不算改写：带着后缀的默认题目仍然跟随协议切换。
 */
export function pelicanIsDefaultPrompt(prompt: string): boolean {
  const bare = pelicanWithoutNoThinking(prompt)
  return bare === DEFAULT_PELICAN_PROMPT || bare === DEFAULT_PELICAN_CLAUDE_PROMPT
}

/**
 * 换默认题目时是否保留「禁止思考」后缀：只在切到 Claude 的 HTML 默认题目时保留。
 * GPT 默认题目要的是 SVG，后缀里的“直接输出完整 HTML”会和它打架，所以切过去时丢掉后缀。
 */
export function pelicanDefaultPromptWithNoThinking(next: string, current: string): string {
  if (next !== DEFAULT_PELICAN_CLAUDE_PROMPT || !pelicanHasNoThinking(current)) return next
  return pelicanToggleNoThinking(next, true)
}

/** 归属分组下的可选账号：后端按 account_groups 给出 group_ids，前端只做匹配。 */
export function pelicanAccountsForGroup(accounts: readonly PelicanAccount[], groupID: number): PelicanAccount[] {
  if (!(groupID > 0)) return []
  return accounts.filter(account => (account.group_ids ?? []).includes(groupID))
}

/** 勾选账号归一化：去重，并丢掉当前分组下已不可选的账号（保持勾选顺序）。 */
export function pelicanPruneAccountIDs(ids: readonly number[], available: readonly PelicanAccount[]): number[] {
  const selectable = new Set(available.map(account => account.id))
  return [...new Set(ids)].filter(id => selectable.has(id))
}

/** 账号平台决定协议：anthropic 固定 Messages，其余（OpenAI）用弹窗里选的 Responses / Chat。 */
export function pelicanAccountProtocol(platform: string | undefined, choice: PelicanProtocol | ''): PelicanProtocol {
  if ((platform ?? '').toLowerCase() === 'anthropic') return 'messages'
  return choice === 'chat' ? 'chat' : 'responses'
}

/** 已选账号可用的协议选项：全 anthropic 时只剩 Messages；未选账号时为空。 */
export function pelicanAccountProtocolChoices(selected: readonly PelicanAccount[]): PelicanProtocol[] {
  if (!selected.length) return []
  return selected.some(account => pelicanAccountProtocol(account.platform, 'responses') !== 'messages') ? ['responses', 'chat'] : ['messages']
}

/**
 * 表单转提交目标。缺项、或账号已不在可选列表里时返回空数组，表单据此禁用提交。
 * `available` 应当传 pelicanAccountsForGroup 的结果：这样提交的账号一定属于所选分组。
 * 目标数封顶 PELICAN_MAX_TARGETS（后端超过就整单拒绝，这里不让它发生）。
 */
export function pelicanDraftTargets(draft: PelicanDraft, available: readonly PelicanAccount[]): PelicanTarget[] {
  const group_id = draft.group_id
  const model = draft.model.trim()
  if (!(group_id > 0) || !model) return []
  if (draft.source === 'manual') {
    const base_url = draft.base_url.trim()
    const api_key = draft.api_key.trim()
    if (!base_url || !api_key || !draft.protocol) return []
    return [{ group_id, source: 'manual', base_url, api_key, model, protocol: draft.protocol }]
  }
  const targets: PelicanTarget[] = []
  const seen = new Set<number>()
  for (const id of draft.account_ids) {
    if (targets.length >= PELICAN_MAX_TARGETS) break
    const account = available.find(item => item.id === id)
    if (!account || seen.has(id)) continue
    seen.add(id)
    targets.push({ group_id, source: 'account', account_id: account.id, model, protocol: pelicanAccountProtocol(account.platform, draft.protocol) })
  }
  return targets
}

/**
 * 表单指纹：判断待确认提交恢复之后管理员有没有改过参数。手填 Key 永不落库，不参与比较；
 * 账号按集合比较，勾选顺序不算改动（提交顺序按勾选顺序，但同一次提交的目标集合没变）。
 */
export function pelicanDraftFingerprint(draft: PelicanDraft): string {
  return JSON.stringify({ ...draft, api_key: '', account_ids: [...new Set(draft.account_ids)].sort((a, b) => a - b) })
}

/**
 * 待确认提交回填表单（手填 Key 除外）：目标必须同分组、同模型、同来源，也就是多选
 * 账号一次性提交的形状；其他形状（旧版多行目标）返回 null，按新测试处理。
 */
export function pelicanDraftFromRequest(request: PelicanRequest): PelicanDraft | null {
  const targets = request.targets ?? []
  if (!targets.length) return null
  const first = targets[0]
  const source = first.source
  if (source !== 'account' && source !== 'manual') return null
  if (!(first.group_id > 0) || !first.model?.trim()) return null
  if (!targets.every(target => target.group_id === first.group_id && target.model === first.model && target.source === source)) return null
  const draft = pelicanEmptyDraft()
  draft.group_id = first.group_id
  draft.source = source
  draft.model = first.model
  if (request.prompt?.trim()) draft.prompt = request.prompt
  if (request.concurrency > 0) draft.concurrency = request.concurrency
  if (request.max_tokens > 0) draft.max_tokens = request.max_tokens
  if (request.timeout_seconds > 0) draft.timeout_seconds = request.timeout_seconds
  // 旧提交没有 effort 字段：回填空串，重放时不凭空多出一个参数（否则 hash 对不上）。
  draft.effort = PELICAN_EFFORTS.find(level => level === request.effort) ?? ''
  if (source === 'account') {
    draft.account_ids = [...new Set(targets.map(target => target.account_id ?? 0).filter(id => id > 0))]
    draft.protocol = targets.some(target => target.protocol === 'chat') ? 'chat' : 'responses'
  } else {
    draft.base_url = first.base_url ?? ''
    draft.protocol = first.protocol
  }
  return draft
}
