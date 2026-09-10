import { describe, it, expect } from 'vitest'
import {
  TIMELINE_LENGTH,
  timelineBucketMs,
  displayCellCount,
  cellTone,
  barFromCounts,
  padTimeline,
  displayCells,
  mergeTimelines,
  liveToneFromCells,
  hslForPct,
  type TimelinePoint
} from '@/utils/stabilityTimeline'

describe('timelineBucketMs', () => {
  it('把窗口均分成 60 细桶', () => {
    expect(timelineBucketMs(5)).toBe(5_000)
    expect(timelineBucketMs(30)).toBe(30_000)
    expect(timelineBucketMs(60)).toBe(60_000)
    expect(timelineBucketMs(360)).toBe(360_000)
    expect(timelineBucketMs(1440)).toBe(1_440_000)
  })
})

describe('displayCellCount', () => {
  it('格数整除 60', () => {
    expect(60 % displayCellCount(5)).toBe(0)
    expect(60 % displayCellCount(30)).toBe(0)
    expect(60 % displayCellCount(60)).toBe(0)
    expect(60 % displayCellCount(360)).toBe(0)
    expect(60 % displayCellCount(1440)).toBe(0)
  })
})

describe('cellTone', () => {
  it('健康 ≥80 / 需关注 50–79 / 异常 <50', () => {
    expect(cellTone(80)).toBe('healthy')
    expect(cellTone(79.9)).toBe('watch')
    expect(cellTone(50)).toBe('watch')
    expect(cellTone(49.9)).toBe('bad')
    expect(cellTone(0)).toBe('bad')
  })

  it('无样本是 empty 而不是 bad', () => {
    expect(cellTone(null)).toBe('empty')
    expect(cellTone(Number.NaN)).toBe('empty')
  })
})

describe('barFromCounts', () => {
  it('空桶灰，满分绿，对半需关注', () => {
    const empty = barFromCounts(0, 0)
    expect(empty.tone).toBe('empty')
    expect(empty.color).toContain('229')

    const ok = barFromCounts(10, 0)
    expect(ok.tone).toBe('healthy')
    expect(ok.sla).toBe(100)

    const watch = barFromCounts(6, 4)
    expect(watch.tone).toBe('watch')
    expect(watch.sla).toBe(60)

    const bad = barFromCounts(0, 5)
    expect(bad.tone).toBe('bad')
    expect(bad.sla).toBe(0)
  })
})

describe('padTimeline', () => {
  const generatedAt = '2026-04-08T12:00:00.000Z'

  it('固定 60 细桶，左边空、右边对齐 generated_at', () => {
    const points: TimelinePoint[] = [
      { t: '2026-04-08T11:58:00.000Z', ok: 10, err: 0 },
      { t: '2026-04-08T11:59:00.000Z', ok: 6, err: 4 }
    ]
    const bars = padTimeline(points, 60, generatedAt)
    expect(bars).toHaveLength(TIMELINE_LENGTH)
    expect(bars[0].tone).toBe('empty')
    expect(bars[58].tone).toBe('healthy')
    expect(bars[59].tone).toBe('watch')
  })

  it('无点时 60 格全空', () => {
    const bars = padTimeline([], 60, generatedAt)
    expect(bars).toHaveLength(60)
    expect(bars.every((b) => b.tone === 'empty')).toBe(true)
  })

  it('不修改入参', () => {
    const points: TimelinePoint[] = [{ t: '2026-04-08T11:59:00.000Z', ok: 1, err: 0 }]
    padTimeline(points, 60, generatedAt)
    expect(points).toEqual([{ t: '2026-04-08T11:59:00.000Z', ok: 1, err: 0 }])
  })
})

describe('displayCells', () => {
  const generatedAt = '2026-04-08T12:00:00.000Z'

  it('1h 折成 12 格，同格 ok/err 相加', () => {
    // 1h 细桶 1min；显示格 5min。11:55–11:59 五分钟并进最后一格。
    const points: TimelinePoint[] = [
      { t: '2026-04-08T11:55:00.000Z', ok: 2, err: 0 },
      { t: '2026-04-08T11:56:00.000Z', ok: 3, err: 1 },
      { t: '2026-04-08T11:59:00.000Z', ok: 1, err: 0 }
    ]
    const cells = displayCells(points, 60, generatedAt)
    expect(cells).toHaveLength(12)
    expect(cells[11].ok).toBe(6)
    expect(cells[11].err).toBe(1)
    expect(cells[0].tone).toBe('empty')
  })

  it('折格时 P50 按成功次数加权', () => {
    const points: TimelinePoint[] = [
      { t: '2026-04-08T11:55:00.000Z', ok: 9, err: 0, first_token_p50: 1000 },
      { t: '2026-04-08T11:59:00.000Z', ok: 1, err: 0, first_token_p50: 2000 }
    ]
    const cells = displayCells(points, 60, generatedAt)
    expect(cells[11].first_token_p50).toBeCloseTo(1100)
  })
})

describe('mergeTimelines', () => {
  it('同桶 ok/err 相加，不是平均', () => {
    const merged = mergeTimelines([
      [{ t: '2026-04-08T11:00:00.000Z', ok: 9, err: 1 }],
      [
        { t: '2026-04-08T11:00:00.000Z', ok: 1, err: 9 },
        { t: '2026-04-08T11:01:00.000Z', ok: 4, err: 0 }
      ]
    ])
    expect(merged[0]).toMatchObject({ t: '2026-04-08T11:00:00.000Z', ok: 10, err: 10 })
    expect(merged[1]).toMatchObject({ t: '2026-04-08T11:01:00.000Z', ok: 4, err: 0 })
  })

  it('空列表得到空数组', () => {
    expect(mergeTimelines([])).toEqual([])
    expect(mergeTimelines([[], []])).toEqual([])
  })
})

describe('liveToneFromCells', () => {
  it('取最右非空格，左边失败不影响行点', () => {
    const cells = displayCells(
      [
        { t: '2026-04-08T11:00:00.000Z', ok: 0, err: 10 },
        { t: '2026-04-08T11:55:00.000Z', ok: 10, err: 0 }
      ],
      60,
      '2026-04-08T12:00:00.000Z'
    )
    expect(liveToneFromCells(cells)).toBe('healthy')
  })

  it('全空则 empty', () => {
    expect(liveToneFromCells(displayCells([], 5, '2026-04-08T12:00:00.000Z'))).toBe('empty')
  })
})

describe('hslForPct', () => {
  it('0 红 50 黄 100 绿，缺值 undefined', () => {
    expect(hslForPct(0)).toBe('hsl(0 72% 42%)')
    expect(hslForPct(50)).toBe('hsl(60 72% 42%)')
    expect(hslForPct(100)).toBe('hsl(120 72% 42%)')
    expect(hslForPct(null)).toBeUndefined()
  })
})
