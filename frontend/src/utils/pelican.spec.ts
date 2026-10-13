import { describe, expect, it } from 'vitest'
import { ARTIFACT_PREVIEW_VIEW } from './detectArtifact'
import {
  DEFAULT_PELICAN_PROMPT,
  PELICAN_DEFAULT_CONCURRENCY,
  PELICAN_DEFAULT_MAX_TOKENS,
  PELICAN_DEFAULT_TIMEOUT_SECONDS,
  PELICAN_MAX_TARGETS,
  pelicanAccountProtocol,
  pelicanAccountProtocolChoices,
  pelicanAccountsForGroup,
  pelicanDateRange,
  pelicanDeletable,
  pelicanDraftFingerprint,
  pelicanDraftFromRequest,
  pelicanDraftTargets,
  pelicanEmptyDraft,
  pelicanPending,
  pelicanPreviewScale,
  pelicanProtocolOptions,
  pelicanPruneAccountIDs,
  pelicanStatusVariant,
  type PelicanDraft
} from './pelican'
import type { PelicanAccount, PelicanRequest } from '@/types/pelican'

const account = (id: number, platform: string, group_ids: number[] = [1]): PelicanAccount => ({ id, name: `acct-${id}`, platform, base_url: 'https://api.example.com', group_ids })

describe('pelican default prompt', () => {
  // 与后端 service.DefaultPelicanPrompt 必须逐字一致：改一处不改另一处就是两边提示词不同。
  it('asks for an animated koala SVG', () => {
    expect(DEFAULT_PELICAN_PROMPT).toBe('生成一张考拉骑自行车的 SVG 图像并添加动画效果')
  })
})

describe('pelican history boundaries', () => {
  it('includes the whole selected final day without depending on server timezone', () => {
    const result = pelicanDateRange('2026-09-01', '2026-09-30')
    expect(new Date(result.from).getDate()).toBe(1)
    const end = new Date(result.to)
    expect(end.getMonth()).toBe(9)
    expect(end.getDate()).toBe(1)
    expect(end.getHours()).toBe(0)
  })
  it('omits unset bounds', () => { expect(pelicanDateRange('', '')).toEqual({}) })
  it('does not keep polling interrupted work as running', () => {
    expect(pelicanPending('running')).toBe(true)
    expect(pelicanPending('queued')).toBe(true)
    for (const status of ['completed', 'failed', 'cancelled', 'interrupted']) expect(pelicanPending(status)).toBe(false)
  })
})

describe('pelican preview scaling', () => {
  it('never upscales artwork beyond the virtual viewport', () => {
    expect(pelicanPreviewScale(2560, 1600)).toBe(1)
  })
  it('fits a wide box by height and a tall box by width', () => {
    expect(pelicanPreviewScale(1280, 400)).toBeCloseTo(0.5)
    expect(pelicanPreviewScale(320, 800)).toBeCloseTo(0.25)
  })
  it('keeps the virtual viewport at 16:10', () => {
    expect(ARTIFACT_PREVIEW_VIEW.width / ARTIFACT_PREVIEW_VIEW.height).toBeCloseTo(1.6)
  })
  it('returns zero for a collapsed box so nothing is rendered', () => {
    expect(pelicanPreviewScale(0, 400)).toBe(0)
    expect(pelicanPreviewScale(400, -1)).toBe(0)
    expect(pelicanPreviewScale(Number.NaN, 400)).toBe(0)
  })
})

describe('pelican protocol options', () => {
  it('restricts protocols by account platform', () => {
    expect(pelicanProtocolOptions('anthropic')).toEqual(['messages'])
    expect(pelicanProtocolOptions('OpenAI')).toEqual(['responses', 'chat'])
    expect(pelicanProtocolOptions()).toEqual(['messages', 'responses', 'chat'])
  })
  it('keeps anthropic on messages and lets openai pick responses or chat', () => {
    expect(pelicanAccountProtocol('anthropic', 'chat')).toBe('messages')
    expect(pelicanAccountProtocol('openai', 'chat')).toBe('chat')
    expect(pelicanAccountProtocol('openai', '')).toBe('responses')
    expect(pelicanAccountProtocol('anthropic', '')).toBe('messages')
  })
  it('offers one protocol for an all-anthropic selection and two when openai is picked', () => {
    expect(pelicanAccountProtocolChoices([account(1, 'anthropic'), account(2, 'anthropic')])).toEqual(['messages'])
    expect(pelicanAccountProtocolChoices([account(1, 'anthropic'), account(2, 'openai')])).toEqual(['responses', 'chat'])
    // 未选账号时不展示协议选项，提交与否由目标数决定。
    expect(pelicanAccountProtocolChoices([])).toEqual([])
  })
})

describe('pelican account selection', () => {
  const accounts = [account(1, 'anthropic', [1, 2]), account(2, 'openai', [2]), account(3, 'anthropic', [])]
  it('filters accounts by the owning group', () => {
    expect(pelicanAccountsForGroup(accounts, 1).map(item => item.id)).toEqual([1])
    expect(pelicanAccountsForGroup(accounts, 2).map(item => item.id)).toEqual([1, 2])
    expect(pelicanAccountsForGroup(accounts, 3)).toEqual([])
    // 未选分组时没有可选账号，避免提交出不属于任何分组的账号。
    expect(pelicanAccountsForGroup(accounts, 0)).toEqual([])
  })
  it('drops accounts that are no longer selectable and de-duplicates', () => {
    expect(pelicanPruneAccountIDs([1, 1, 3, 9], pelicanAccountsForGroup(accounts, 2))).toEqual([1])
  })
})

describe('pelican draft targets', () => {
  const draft: PelicanDraft = { ...pelicanEmptyDraft(), group_id: 1, model: 'claude-opus-5', account_ids: [1, 2] }
  const accounts = [account(1, 'anthropic'), account(2, 'openai')]
  it('submits one target per selected account', () => {
    expect(pelicanDraftTargets(draft, accounts)).toEqual([
      { group_id: 1, source: 'account', account_id: 1, model: 'claude-opus-5', protocol: 'messages' },
      { group_id: 1, source: 'account', account_id: 2, model: 'claude-opus-5', protocol: 'responses' }
    ])
  })
  it('uses the picked protocol for openai accounts only', () => {
    const targets = pelicanDraftTargets({ ...draft, protocol: 'chat' }, accounts)
    expect(targets.map(target => target.protocol)).toEqual(['messages', 'chat'])
  })
  it('is not ready without a group, a model or a selectable account', () => {
    expect(pelicanDraftTargets({ ...draft, group_id: 0 }, accounts)).toEqual([])
    expect(pelicanDraftTargets({ ...draft, model: '  ' }, accounts)).toEqual([])
    // 账号不在该分组下（调用方传的是过滤后的列表）时不提交，表单据此禁用按钮。
    expect(pelicanDraftTargets(draft, [account(2, 'openai')])).toEqual([
      { group_id: 1, source: 'account', account_id: 2, model: 'claude-opus-5', protocol: 'responses' }
    ])
    expect(pelicanDraftTargets({ ...draft, account_ids: [] }, accounts)).toEqual([])
  })
  it('submits a manual target only with a URL, a key and a protocol', () => {
    const manual: PelicanDraft = { ...pelicanEmptyDraft(), group_id: 1, source: 'manual', model: 'gpt-5', base_url: ' https://api.example.com ', api_key: ' sk-x ', protocol: 'responses' }
    expect(pelicanDraftTargets(manual, accounts)).toEqual([
      { group_id: 1, source: 'manual', base_url: 'https://api.example.com', api_key: 'sk-x', model: 'gpt-5', protocol: 'responses' }
    ])
    expect(pelicanDraftTargets({ ...manual, api_key: '  ' }, accounts)).toEqual([])
    expect(pelicanDraftTargets({ ...manual, protocol: '' }, accounts)).toEqual([])
  })
  it('leaves the protocol unset in a blank draft so a manual target must pick one', () => {
    expect(pelicanEmptyDraft().protocol).toBe('')
    expect(pelicanDraftTargets({ ...pelicanEmptyDraft(), group_id: 1, source: 'manual', model: 'm', base_url: 'https://api.example.com', api_key: 'k' }, [])).toEqual([])
  })
  // 后端 normalizePelicanRequest 超过 100 个目标就整单拒绝，前端不能把这种请求发出去。
  it('caps one batch at the backend target limit', () => {
    const many = Array.from({ length: PELICAN_MAX_TARGETS + 5 }, (_, index) => account(index + 1, 'anthropic'))
    const bulk: PelicanDraft = { ...pelicanEmptyDraft(), group_id: 1, model: 'm', account_ids: many.map(item => item.id) }
    expect(PELICAN_MAX_TARGETS).toBe(100)
    expect(pelicanDraftTargets(bulk, many)).toHaveLength(PELICAN_MAX_TARGETS)
  })
})

describe('pelican draft fingerprint', () => {
  const base: PelicanDraft = { ...pelicanEmptyDraft(), group_id: 1, model: 'm', account_ids: [2, 1], api_key: 'sk-a' }
  it('ignores the hand-entered key and the order accounts were picked in', () => {
    expect(pelicanDraftFingerprint(base)).toBe(pelicanDraftFingerprint({ ...base, account_ids: [1, 2], api_key: 'sk-b' }))
  })
  it('changes as soon as a submitted parameter changes', () => {
    const changed: Array<Partial<PelicanDraft>> = [
      { group_id: 2 }, { model: 'n' }, { account_ids: [1] }, { prompt: 'other' },
      { protocol: 'chat' }, { concurrency: 5 }, { max_tokens: 100 }, { timeout_seconds: 60 },
      { source: 'manual' }, { base_url: 'https://api.example.com' }
    ]
    for (const patch of changed) {
      expect(pelicanDraftFingerprint({ ...base, ...patch })).not.toBe(pelicanDraftFingerprint(base))
    }
  })
})

describe('pelican pending submission restore', () => {
  const request = (targets: PelicanRequest['targets']): PelicanRequest => ({ request_id: 'r', prompt: 'p', concurrency: 2, max_tokens: 100, timeout_seconds: 60, targets })
  it('restores a multi-account submission without the hand-entered key', () => {
    const restored = pelicanDraftFromRequest(request([
      { group_id: 3, source: 'account', account_id: 7, model: 'm', protocol: 'messages' },
      { group_id: 3, source: 'account', account_id: 8, model: 'm', protocol: 'chat' }
    ]))
    expect(restored).toMatchObject({ group_id: 3, source: 'account', account_ids: [7, 8], model: 'm', protocol: 'chat', prompt: 'p', concurrency: 2, max_tokens: 100, timeout_seconds: 60 })
  })
  it('keeps the manual gateway but never a key', () => {
    const restored = pelicanDraftFromRequest(request([{ group_id: 3, source: 'manual', base_url: 'https://api.example.com', api_key: 'sk-x', model: 'm', protocol: 'chat' }]))
    expect(restored).toMatchObject({ source: 'manual', base_url: 'https://api.example.com', api_key: '', protocol: 'chat' })
  })
  it('ignores submissions that are not one group, model and source', () => {
    expect(pelicanDraftFromRequest(request([
      { group_id: 3, source: 'account', account_id: 7, model: 'm', protocol: 'messages' },
      { group_id: 4, source: 'account', account_id: 8, model: 'm', protocol: 'messages' }
    ]))).toBeNull()
    expect(pelicanDraftFromRequest(request([{ group_id: 0, source: 'account', account_id: 7, model: 'm', protocol: 'messages' }]))).toBeNull()
    expect(pelicanDraftFromRequest(request([{ group_id: 3, source: 'account', account_id: 7, model: 'm', protocol: 'messages' }]))).not.toBeNull()
    expect(pelicanDraftFromRequest(request([]))).toBeNull()
  })
  it('falls back to the built-in defaults for missing parameters', () => {
    const restored = pelicanDraftFromRequest({ request_id: 'r', prompt: '', concurrency: 0, max_tokens: 0, timeout_seconds: 0, targets: [{ group_id: 3, source: 'account', account_id: 7, model: 'm', protocol: 'messages' }] })
    expect(restored).toMatchObject({ prompt: DEFAULT_PELICAN_PROMPT, concurrency: PELICAN_DEFAULT_CONCURRENCY, max_tokens: PELICAN_DEFAULT_MAX_TOKENS, timeout_seconds: PELICAN_DEFAULT_TIMEOUT_SECONDS })
  })
})

describe('pelican status and delete rules', () => {
  it('colors in-flight work differently from failures', () => {
    expect(pelicanStatusVariant('completed')).toBe('success')
    expect(pelicanStatusVariant('failed')).toBe('danger')
    expect(pelicanStatusVariant('running')).toBe('primary')
    expect(pelicanStatusVariant('queued')).toBe('primary')
    expect(pelicanStatusVariant('cancelled')).toBe('warning')
    expect(pelicanStatusVariant('interrupted')).toBe('warning')
    expect(pelicanStatusVariant(undefined)).toBe('gray')
  })
  it('refuses delete while published or still generating', () => {
    expect(pelicanDeletable({ published_at: null, status: 'completed' })).toBe(true)
    expect(pelicanDeletable({ published_at: '2026-01-01', status: 'completed' })).toBe(false)
    expect(pelicanDeletable({ published_at: null, status: 'running' })).toBe(false)
    expect(pelicanDeletable(null)).toBe(false)
  })
})
