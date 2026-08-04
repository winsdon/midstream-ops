/**
 * 图表配色单一来源
 *
 * 网格线与刻度文字需随主题切换，散落在各图表组件里会导致改一处漏三处。
 * 曲线色板供业务页面挑选，保证多个图表之间语义一致（收益恒为绿、成本恒为红）。
 */

/** 随主题变化的图表基础配色 */
export function chartColors(isDark: boolean) {
  return {
    /** 网格线 */
    grid: isDark ? 'rgba(148,163,184,0.12)' : 'rgba(100,116,139,0.12)',
    /** 坐标轴刻度与图例文字 */
    tick: isDark ? '#94a3b8' : '#64748b'
  }
}

/** 曲线语义色板。与 tailwind.config.js 的色值对应 */
export const SERIES = {
  /** 收益 - emerald-500 */
  profit: '#10b981',
  /** 成本 - red-500 */
  cost: '#ef4444',
  /** 收入 - primary-500 (teal) */
  revenue: '#14b8a6',
  /** 官价对照 - amber-500 */
  official: '#f59e0b',
  /** 中性/次要 - slate-400 */
  neutral: '#94a3b8'
} as const

/** 面积填充色：在曲线色后追加 alpha 通道（约 12% 不透明度） */
export function fillOf(color: string): string {
  return `${color}20`
}
