import { describe, expect, it } from 'vitest'
import { ARTIFACT_PREVIEW_VIEW } from './detectArtifact'
import {
  DEFAULT_PELICAN_PROMPT,
  pelicanDateRange,
  pelicanPending,
  pelicanPreviewScale,
  pelicanProtocolOptions,
  pelicanTargetReady,
  type PelicanTargetDraft
} from './pelican'

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

describe('pelican target protocol options', () => {
  it('restricts protocols by account platform', () => {
    expect(pelicanProtocolOptions('anthropic')).toEqual(['messages'])
    expect(pelicanProtocolOptions('OpenAI')).toEqual(['responses', 'chat'])
    expect(pelicanProtocolOptions()).toEqual(['messages', 'responses', 'chat'])
  })
})

describe('pelican target readiness', () => {
  const base: PelicanTargetDraft = { group_id: 1, source: 'account', account_id: 2, model: 'claude-opus-5', protocol: 'messages' }
  it('accepts a complete account target', () => { expect(pelicanTargetReady(base)).toBe(true) })
  it('accepts a complete manual target', () => {
    expect(pelicanTargetReady({ group_id: 1, source: 'manual', base_url: 'https://api.example.com', api_key: 'sk-x', model: 'gpt-5', protocol: 'responses' })).toBe(true)
  })
  it('rejects missing group, model, protocol, credential or source', () => {
    expect(pelicanTargetReady({ ...base, group_id: 0 })).toBe(false)
    expect(pelicanTargetReady({ ...base, model: '   ' })).toBe(false)
    expect(pelicanTargetReady({ ...base, protocol: '' })).toBe(false)
    expect(pelicanTargetReady({ ...base, source: '' })).toBe(false)
    expect(pelicanTargetReady({ ...base, account_id: 0 })).toBe(false)
    expect(pelicanTargetReady({ group_id: 1, source: 'manual', base_url: 'https://api.example.com', model: 'm', protocol: 'chat' })).toBe(false)
  })
})
