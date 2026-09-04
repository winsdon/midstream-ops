import type {
  AuthenticityGrade,
  DetectCheckMeta,
  DetectCheckResult,
  DetectCheckStatus,
  DetectClass,
  DetectGroup,
  DetectLabel,
  DetectPreset,
  DetectTargetRun
} from '@/types/detect'

/** 与后端 maxDetectTargets 对齐。 */
export const MAX_DETECT_TARGETS = 10

/** 与后端 defaultDetectConcurrency / maxDetectConcurrency 对齐。 */
export const DEFAULT_DETECT_CONCURRENCY = 3
export const MAX_DETECT_CONCURRENCY = 10

/** Badge 变体，与 common/Badge.vue 的取值保持一致。 */
export type BadgeVariant = 'primary' | 'success' | 'warning' | 'danger' | 'gray' | 'purple'

/**
 * 判定标签 → 徽章配色。
 *
 * 配色表达的是「要不要警惕」而不是「好不好」：官方直连与 Max 号池都是绿/蓝，
 * Bedrock、Kiro、Vertex 是中性提示（买到的不一定是你以为的东西），
 * 包装伪装才是红色。
 */
const LABEL_VARIANT: Record<DetectLabel, BadgeVariant> = {
  claude_max_pool: 'primary',
  official_api: 'success',
  aws_bedrock: 'warning',
  kiro: 'purple',
  vertex: 'purple',
  wrapper: 'danger',
  info: 'gray',
  unknown: 'gray',
  unavailable: 'gray'
}

export function labelVariant(label: DetectLabel): BadgeVariant {
  return LABEL_VARIANT[label] ?? 'gray'
}

/** 单项结论 → 徽章配色。 */
const STATUS_VARIANT: Record<DetectCheckStatus, BadgeVariant> = {
  running: 'primary',
  passed: 'success',
  failed: 'danger',
  suspicious: 'warning',
  unsupported: 'gray',
  inconclusive: 'gray'
}

export function statusVariant(status: DetectCheckStatus): BadgeVariant {
  return STATUS_VARIANT[status] ?? 'gray'
}

/** 单项结论 → 单字符图标（矩阵格子用，避免大量 svg 拖慢渲染）。 */
const STATUS_ICON: Record<DetectCheckStatus, string> = {
  running: '…',
  passed: '✓',
  failed: '×',
  suspicious: '!',
  unsupported: '—',
  inconclusive: '?'
}

export function statusIcon(status?: DetectCheckStatus): string {
  return status ? (STATUS_ICON[status] ?? '·') : '·'
}

/** 真实性等级 → 配色。 */
const GRADE_VARIANT: Record<AuthenticityGrade, BadgeVariant> = {
  genuine: 'success',
  likely: 'primary',
  doubtful: 'warning',
  fake: 'danger'
}

export function gradeVariant(grade: AuthenticityGrade): BadgeVariant {
  return GRADE_VARIANT[grade] ?? 'gray'
}

/** 真实性评分 → 进度条颜色类（Tailwind 需要完整字面量，不能拼接）。 */
export function scoreBarClass(grade: AuthenticityGrade): string {
  switch (grade) {
    case 'genuine':
      return 'bg-emerald-500'
    case 'likely':
      return 'bg-primary-500'
    case 'doubtful':
      return 'bg-amber-500'
    default:
      return 'bg-red-500'
  }
}

/** 打分维度的展示顺序。与后端 classOrder 一致。 */
export const SCORE_CLASSES: DetectClass[] = [
  'claude_max_pool',
  'official_api',
  'aws_bedrock',
  'kiro',
  'vertex',
  'wrapper'
]

/** 分组展示顺序。 */
export const GROUP_ORDER: DetectGroup[] = ['gateway', 'protocol', 'identity', 'capability']

export interface CheckGroupView {
  group: DetectGroup
  items: DetectCheckMeta[]
}

/** 按分组归拢检测项，保持目录内的原始顺序。 */
export function groupChecks(checks: DetectCheckMeta[]): CheckGroupView[] {
  const buckets = new Map<DetectGroup, DetectCheckMeta[]>()
  for (const c of checks) {
    const list = buckets.get(c.group)
    if (list) list.push(c)
    else buckets.set(c.group, [c])
  }
  return GROUP_ORDER.filter((g) => buckets.has(g)).map((group) => ({
    group,
    items: buckets.get(group) as DetectCheckMeta[]
  }))
}

/** 勾选项的总请求数（用于提示会消耗多少上游额度）。 */
export function estimateRequests(checks: DetectCheckMeta[], selected: Set<string>, targets: number): number {
  const per = checks.filter((c) => selected.has(c.id)).reduce((sum, c) => sum + c.requests, 0)
  return per * Math.max(targets, 1)
}

/** 勾中的高成本项，用于二次确认。 */
export function highCostSelection(checks: DetectCheckMeta[], selected: Set<string>): DetectCheckMeta[] {
  return checks.filter((c) => selected.has(c.id) && c.cost === 'high')
}

/**
 * 把选中项补齐前置依赖。
 *
 * 与后端 ResolveCheckIDs 同一套规则，在前端也做一遍是为了让勾选面板
 * 当场显示「因为选了签名完整性，thinking 签名也会跑」，而不是提交后才发现多跑了两项。
 */
export function withDependencies(checks: DetectCheckMeta[], selected: Set<string>): Set<string> {
  const byID = new Map(checks.map((c) => [c.id, c]))
  const out = new Set<string>()
  const visit = (id: string): void => {
    if (out.has(id)) return
    const meta = byID.get(id)
    if (!meta) return
    out.add(id)
    for (const dep of meta.requires ?? []) visit(dep)
  }
  selected.forEach(visit)
  return out
}

/** 从一次目标结果里按 id 取某项结论（矩阵格子用）。 */
export function findCheck(run: DetectTargetRun | undefined, checkID: string): DetectCheckResult | undefined {
  return (run?.checks ?? undefined)?.find((c) => c.id === checkID)
}

/** 目标结果里各结论的计数，用于卡片上的一行小结。 */
export interface StatusCounts {
  passed: number
  failed: number
  suspicious: number
  unsupported: number
  inconclusive: number
}

export function countStatuses(run: DetectTargetRun | undefined): StatusCounts {
  const counts: StatusCounts = { passed: 0, failed: 0, suspicious: 0, unsupported: 0, inconclusive: 0 }
  for (const c of run?.checks ?? []) {
    if (c.status === 'running') continue
    counts[c.status] += 1
  }
  return counts
}

/**
 * 证据分成「支持主判定」与「其他线索」两组。
 *
 * 用户最想先看到的是「凭什么判成这个」，把不相关类别的证据混在一起会淹没重点；
 * 但其他线索也不能丢——它们往往是次高分类别的依据，值得单独一栏。
 */
export function splitReasons<T extends { class: DetectClass; weight: number }>(
  reasons: T[],
  label: DetectLabel
): { primary: T[]; others: T[] } {
  const primary: T[] = []
  const others: T[] = []
  for (const r of reasons) {
    if (r.weight <= 0) {
      others.push(r)
      continue
    }
    if (r.class === label) primary.push(r)
    else others.push(r)
  }
  return { primary, others }
}

/** 目标同一性 key：优先账号 id，手填目标退回「地址 + 名称」。 */
export function targetKey(run: DetectTargetRun): string {
  return run.account_id ? `account:${run.account_id}` : `manual:${run.base_url}:${run.name}`
}

/** 进度百分比，避免除零。 */
export function progressPercent(done: number, total: number): number {
  if (total <= 0) return 0
  return Math.min(100, Math.round((done / total) * 100))
}

/** 两份勾选是否同一集合（忽略顺序）。 */
export function sameCheckSet(a: readonly string[], b: readonly string[]): boolean {
  if (a.length !== b.length) return false
  const set = new Set(a)
  return b.every((id) => set.has(id))
}

/**
 * 当前勾选对应哪个预设。对不上任何一套就是自定义（含微调）。
 * 比较的是用户勾选本身，不含自动补上的依赖。
 */
export function matchingPresetId(selected: readonly string[], presets: readonly DetectPreset[]): string | null {
  for (const p of presets) {
    if (sameCheckSet(selected, p.checks)) return p.id
  }
  return null
}
