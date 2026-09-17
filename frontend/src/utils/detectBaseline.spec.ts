import { describe, expect, it } from 'vitest'
import { baselineApplies, sameBaselineModel } from './detectBaseline'

describe('sameBaselineModel', () => {
  it('trims and ignores case', () => {
    expect(sameBaselineModel('claude-opus-5', ' Claude-Opus-5 ')).toBe(true)
  })
  it('rejects different models', () => {
    expect(sameBaselineModel('claude-opus-5', 'claude-sonnet-4-6')).toBe(false)
  })
})

describe('baselineApplies', () => {
  it('is false when baseline missing or failed', () => {
    expect(baselineApplies(null, 'claude-opus-5')).toBe(false)
    expect(baselineApplies({ quality_ok: false, model: 'claude-opus-5' }, 'claude-opus-5')).toBe(false)
  })
  it('is false when models differ', () => {
    expect(baselineApplies({ quality_ok: true, model: 'claude-opus-5' }, 'claude-sonnet-4-6')).toBe(false)
  })
  it('is true when ready and models match', () => {
    expect(baselineApplies({ quality_ok: true, model: 'claude-opus-5' }, 'claude-opus-5')).toBe(true)
  })
})
