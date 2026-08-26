import { describe, it, expect } from 'vitest'
import {
  balanceAlertRatio,
  balanceAlertBarWidth,
  balanceAlertTone,
  balanceAlertBarClass,
  balanceAlertTextClass
} from '@/utils/creditMeter'

describe('balanceAlertRatio', () => {
  it('未扫描返回 null', () => {
    expect(balanceAlertRatio(null, 10)).toBeNull()
    expect(balanceAlertRatio(undefined, 10)).toBeNull()
  })

  it('阈值 ≤ 0 返回 null（不参与告警）', () => {
    expect(balanceAlertRatio(42, 0)).toBeNull()
    expect(balanceAlertRatio(42, -1)).toBeNull()
  })

  it('余额 / 阈值', () => {
    expect(balanceAlertRatio(5, 10)).toBe(0.5)
    expect(balanceAlertRatio(10, 10)).toBe(1)
    expect(balanceAlertRatio(42, 10)).toBe(4.2)
    expect(balanceAlertRatio(0, 10)).toBe(0)
  })
})

describe('balanceAlertBarWidth', () => {
  it('无比例时条宽为 0', () => {
    expect(balanceAlertBarWidth(null)).toBe(0)
  })

  it('封顶 100，不撑破容器', () => {
    expect(balanceAlertBarWidth(0)).toBe(0)
    expect(balanceAlertBarWidth(0.32)).toBe(32)
    expect(balanceAlertBarWidth(1)).toBe(100)
    expect(balanceAlertBarWidth(4.2)).toBe(100)
  })

  it('负比例按 0', () => {
    expect(balanceAlertBarWidth(-0.1)).toBe(0)
  })
})

describe('balanceAlertTone', () => {
  it('无比例为 muted', () => {
    expect(balanceAlertTone(null)).toBe('muted')
  })

  it('严格低于阈值（ratio < 1）为 danger，对齐后端跌破即告警', () => {
    expect(balanceAlertTone(0)).toBe('danger')
    expect(balanceAlertTone(0.99)).toBe('danger')
  })

  it('达到或超过阈值为 ok', () => {
    expect(balanceAlertTone(1)).toBe('ok')
    expect(balanceAlertTone(4.2)).toBe('ok')
  })
})

describe('balanceAlert 样式类（完整字面量，供 Tailwind 扫描）', () => {
  it('danger / ok / muted 各有独立完整类名', () => {
    expect(balanceAlertBarClass('danger')).toBe('bg-red-500')
    expect(balanceAlertBarClass('ok')).toBe('bg-emerald-500')
    expect(balanceAlertBarClass('muted')).toContain('bg-gray-300')
    expect(balanceAlertTextClass('danger')).toContain('text-red-600')
    expect(balanceAlertTextClass('ok')).toContain('text-gray-500')
    expect(balanceAlertTextClass('muted')).toBe('text-gray-400')
  })
})
