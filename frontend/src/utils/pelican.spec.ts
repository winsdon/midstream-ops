import { describe, expect, it } from 'vitest'
import { pelicanDateRange, pelicanPending } from './pelican'
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
