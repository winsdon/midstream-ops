import { describe, it, expect } from 'vitest'
import { buildDistribution } from '@/utils/statsDistribution'

const labels = (slices: { label: string }[]) => slices.map((s) => s.label)
const values = (slices: { value: number }[]) => slices.map((s) => s.value)

describe('buildDistribution', () => {
  it('按数值降序排列正值切片', () => {
    const slices = buildDistribution([
      { label: 'B', value: 20 },
      { label: 'A', value: 50 },
      { label: 'C', value: 30 }
    ])
    expect(labels(slices)).toEqual(['A', 'C', 'B'])
    expect(values(slices)).toEqual([50, 30, 20])
  })

  it('丢弃零值和负值 —— 环形图无法表示非正份额', () => {
    const slices = buildDistribution([
      { label: '正', value: 10 },
      { label: '零', value: 0 },
      { label: '负', value: -4 }
    ])
    expect(labels(slices)).toEqual(['正'])
  })

  it('切片不超过 maxSlices：前 N-1 具名，其余并入「其他」', () => {
    const items = [
      { label: 'a', value: 8 },
      { label: 'b', value: 7 },
      { label: 'c', value: 6 },
      { label: 'd', value: 5 },
      { label: 'e', value: 4 }
    ]
    const slices = buildDistribution(items, { maxSlices: 3, othersLabel: '其他' })
    expect(labels(slices)).toEqual(['a', 'b', '其他'])
    expect(values(slices)).toEqual([8, 7, 15])
  })

  it('条目不超过上限时不生成「其他」桶', () => {
    const slices = buildDistribution(
      [
        { label: 'a', value: 3 },
        { label: 'b', value: 1 }
      ],
      { maxSlices: 3, othersLabel: '其他' }
    )
    expect(labels(slices)).toEqual(['a', 'b'])
  })

  it('空输入或全非正值返回空数组', () => {
    expect(buildDistribution([])).toEqual([])
    expect(buildDistribution([{ label: 'x', value: 0 }])).toEqual([])
    expect(buildDistribution([{ label: 'x', value: -1 }])).toEqual([])
  })

  it('share 是占正值合计的百分比，加总为 100', () => {
    const slices = buildDistribution([
      { label: 'a', value: 70 },
      { label: 'b', value: 30 }
    ])
    expect(slices[0].share).toBe(70)
    expect(slices[1].share).toBe(30)
    expect(slices.reduce((n, s) => n + s.share, 0)).toBe(100)
  })

  it('不改动入参数组', () => {
    const items = [
      { label: 'b', value: 1 },
      { label: 'a', value: 2 }
    ]
    const frozen = items.map((i) => ({ ...i }))
    buildDistribution(items)
    expect(items).toEqual(frozen)
  })
})
