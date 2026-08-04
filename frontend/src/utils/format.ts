// 数字与日期格式化工具。

export function fmtMoney(v?: number | null, digits = 2): string {
  if (v === null || v === undefined || Number.isNaN(v)) return '-'
  return v.toLocaleString('zh-CN', { minimumFractionDigits: digits, maximumFractionDigits: digits })
}

export function fmtNum(v?: number | null): string {
  if (v === null || v === undefined || Number.isNaN(v)) return '-'
  return v.toLocaleString('zh-CN')
}

export function fmtMs(v?: number | null): string {
  if (v === null || v === undefined) return '-'
  if (v >= 1000) return (v / 1000).toFixed(2) + 's'
  return Math.round(v) + 'ms'
}

export function fmtPct(v?: number | null, digits = 1): string {
  if (v === null || v === undefined || Number.isNaN(v)) return '-'
  return v.toFixed(digits) + '%'
}

// 金额正负着色
export function moneyClass(v?: number | null): string {
  if (v === null || v === undefined) return 'text-gray-500'
  if (v > 0) return 'text-emerald-600 dark:text-emerald-400'
  if (v < 0) return 'text-red-600 dark:text-red-400'
  return 'text-gray-600 dark:text-dark-400'
}

export function todayStr(): string {
  const d = new Date()
  return toDateStr(d)
}

export function daysAgoStr(n: number): string {
  const d = new Date()
  d.setDate(d.getDate() - n)
  return toDateStr(d)
}

function toDateStr(d: Date): string {
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${day}`
}

// 后端时间戳（RFC3339，UTC）→ 本地 "MM-DD HH:mm"。
export function fmtDateTime(v?: string | null): string {
  if (!v) return '-'
  const d = new Date(v)
  if (Number.isNaN(d.getTime())) return '-'
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  const hh = String(d.getHours()).padStart(2, '0')
  const mm = String(d.getMinutes()).padStart(2, '0')
  return `${m}-${day} ${hh}:${mm}`
}

// 距今分钟数；无效时间返回 null。用于判断同步数据是否已过期。
export function minutesSince(v?: string | null): number | null {
  if (!v) return null
  const d = new Date(v)
  if (Number.isNaN(d.getTime())) return null
  return Math.max(0, Math.floor((Date.now() - d.getTime()) / 60000))
}

