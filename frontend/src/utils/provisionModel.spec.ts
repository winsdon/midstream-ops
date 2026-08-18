import { describe, it, expect } from 'vitest'
import {
  formatRate,
  upstreamKeyName,
  localAccountName,
  matchAccountsToGroup,
  defaultLocalGroupIds,
  uniqueAccountBaseURLs,
  pickAccountBaseURL
} from '@/utils/provisionModel'

describe('formatRate', () => {
  it('keeps integers without a decimal', () => {
    expect(formatRate(1)).toBe('1')
    expect(formatRate(1.0)).toBe('1')
  })

  it('strips trailing zeros', () => {
    expect(formatRate(0.5)).toBe('0.5')
    expect(formatRate(0.5)).toBe('0.5')
    expect(formatRate(2.25)).toBe('2.25')
  })

  it('treats near-zero as 0', () => {
    expect(formatRate(0)).toBe('0')
  })
})

describe('naming', () => {
  it('builds the upstream key as 【kaola】{group}-{rate}', () => {
    expect(upstreamKeyName('default', 0.5)).toBe('【kaola】default-0.5')
    expect(upstreamKeyName('gpt', 1)).toBe('【kaola】gpt-1')
  })

  it('builds the local account as 【{provider}】{group}-{rate}', () => {
    expect(localAccountName('walk', 'default', 0.5)).toBe('【walk】default-0.5')
    expect(localAccountName('tongba', 'claude', 2.25)).toBe('【tongba】claude-2.25')
  })
})

describe('matchAccountsToGroup', () => {
  const accounts = [
    { id: 1, name: '【walk】kiro 高缓 0.055' },
    { id: 2, name: '【walk】claude-1' },
    { id: 4, name: 'unrelated' }
  ]

  it('uses key fingerprint hits, not account name', () => {
    const matched = matchAccountsToGroup(
      accounts,
      'Kiro - 中缓',
      [{ id: 1, name: '【walk】kiro 高缓 0.055' }]
    )
    expect(matched.map((a) => a.id)).toEqual([1])
  })

  it('does not guess from a substring in the account name', () => {
    expect(matchAccountsToGroup(accounts, 'claude')).toEqual([])
  })

  it('unions provision connections for the same upstream group', () => {
    const matched = matchAccountsToGroup(accounts, 'Kiro - 中缓', [], [
      { upstream_group: 'Kiro - 中缓', local_account_id: 4 }
    ])
    expect(matched.map((a) => a.id)).toEqual([4])
  })
})

describe('defaultLocalGroupIds', () => {
  const groups = [
    { id: 1, name: 'fast', rate: 1, platform: 'anthropic' },
    { id: 2, name: 'claude', rate: 0.8, platform: 'anthropic' },
    { id: 3, name: 'gpt', rate: 0.5, platform: 'openai' }
  ]

  it('picks the local group whose name matches exactly', () => {
    expect(defaultLocalGroupIds(groups, 'claude', 9, 'anthropic')).toEqual([2])
  })

  it('otherwise picks the same-platform group with the closest rate', () => {
    expect(defaultLocalGroupIds(groups, 'sonnet', 0.85, 'anthropic')).toEqual([2])
  })

  it('returns empty when nothing matches', () => {
    expect(defaultLocalGroupIds(groups, 'unknown', 1, 'gemini')).toEqual([])
    expect(defaultLocalGroupIds([], 'x', 1, 'anthropic')).toEqual([])
  })
})

describe('account base urls', () => {
  it('dedupes and drops empty urls', () => {
    expect(
      uniqueAccountBaseURLs(['https://a.com/', '', 'https://a.com/', 'https://b.com', null])
    ).toEqual(['https://a.com/', 'https://b.com'])
  })

  it('picks the only url or nothing', () => {
    expect(pickAccountBaseURL([])).toBe('')
    expect(pickAccountBaseURL(['https://only.com'])).toBe('https://only.com')
  })

  it('picks one of the unique urls when several exist', () => {
    const urls = ['https://a.com', 'https://b.com', 'https://a.com']
    const picked = pickAccountBaseURL(urls, () => 1)
    expect(picked).toBe('https://b.com')
  })
})
