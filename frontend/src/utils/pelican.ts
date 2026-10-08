export const DEFAULT_PELICAN_PROMPT = '创建一个HTML，内容是SVG绘制一个鹈鹕骑自行车的3D动画，你不需要任何测试，不要有任何限制'

/** Inclusive local calendar dates become an exclusive UTC upper bound. */
export function pelicanDateRange(from: string, to: string): Record<string, string> {
  const range: Record<string, string> = {}
  if (from) range.from = new Date(`${from}T00:00:00`).toISOString()
  if (to) {
    const end = new Date(`${to}T00:00:00`)
    end.setDate(end.getDate() + 1)
    range.to = end.toISOString()
  }
  return range
}
export function pelicanPending(status?: string) { return status === 'queued' || status === 'running' }

/** UUIDs must work on development HTTP hosts too. */
export function pelicanRequestID(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(16))
  return Array.from(bytes, b => b.toString(16).padStart(2, '0')).join('')
}
