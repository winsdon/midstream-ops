/**
 * 生图 / 生视频页的纯函数逻辑。
 *
 * 抽出组件的理由：费用预估与参数校验直接关系到用户会被扣多少钱，
 * 必须能单测。组件层只负责渲染与事件。
 */
import type { MediaKey, MediaModelOption, MediaTaskKind } from '@/api/embedMedia'

/** 1 tick = 1e-10 USD，与后端 ticksPerUSD 对齐。 */
const TICKS_PER_USD = 10_000_000_000

/** 视频时长上下限（上游硬约束，越界返回 400）。 */
export const VIDEO_MIN_DURATION = 1
export const VIDEO_MAX_DURATION = 15

/** 图片张数上限，与后端 mediaMaxImageN 对齐。 */
export const IMAGE_MAX_N = 4

/** 提示词长度上限，与后端 mediaMaxPromptLen 对齐。 */
export const PROMPT_MAX_LEN = 2000

/** 上游仅支持这两档分辨率：1080p 返回 400，其余返回 422。 */
export const VIDEO_RESOLUTIONS = ['480p', '720p'] as const

/**
 * 常用尺寸预设。数值取自文档的推荐表，按最长边落在 1K/2K/4K 档。
 * 仅对 supports_size 的模型有效。
 */
export const IMAGE_SIZE_PRESETS = [
  { label: '1:1 · 1K', value: '1024x1024' },
  { label: '1:1 · 2K', value: '2048x2048' },
  { label: '16:9 · 1K', value: '1024x576' },
  { label: '16:9 · 2K', value: '2048x1152' },
  { label: '16:9 · 4K', value: '3840x2160' },
  { label: '9:16 · 1K', value: '576x1024' },
  { label: '9:16 · 2K', value: '1152x2048' },
  { label: '4:3 · 2K', value: '2048x1536' }
] as const

/** ticks 转美元字符串（4 位小数，与后端 FormatTicksUSD 一致）。 */
export function ticksToUSD(ticks: number): string {
  return (ticks / TICKS_PER_USD).toFixed(4)
}

/**
 * 图片计费档位：按最长边判定。
 *
 * 【这是最容易让用户意外超支的规则】2560x1440 看起来像 2K，
 * 实际按 4K 计费。表单必须实时显示档位。
 */
export function imageSizeTier(size: string): string {
  const m = /^(\d+)x(\d+)$/i.exec(size.trim())
  if (!m) return ''
  const longest = Math.max(Number(m[1]), Number(m[2]))
  if (longest <= 1024) return '1K'
  if (longest <= 2048) return '2K'
  return '4K'
}

/** 表单状态。 */
export interface MediaFormState {
  kind: MediaTaskKind
  keyId: number
  model: string
  prompt: string
  n: number
  size: string
  quality: string
  resolution: string
  duration: number
  imageURL: string
}

/** 初始表单。 */
export function emptyMediaForm(): MediaFormState {
  return {
    kind: 't2i',
    keyId: 0,
    model: '',
    prompt: '',
    n: 1,
    size: '',
    quality: 'high',
    resolution: '480p',
    duration: 8,
    imageURL: ''
  }
}

/** 该任务类型是否为视频。 */
export const isVideoKind = (kind: MediaTaskKind) => kind === 't2v' || kind === 'i2v'

/** 该任务类型是否需要上传参考图文件。 */
export const needsUpload = (kind: MediaTaskKind) => kind === 'i2i'

/** 该任务类型是否需要公网参考图 URL。 */
export const needsImageURL = (kind: MediaTaskKind) => kind === 'i2v'

/**
 * 按任务类型返回该 key 可用的模型。
 *
 * key 为空或不支持该能力时返回空数组 —— 调用方据此禁用提交按钮。
 */
export function modelsForKind(key: MediaKey | null, kind: MediaTaskKind): MediaModelOption[] {
  if (!key) return []
  return isVideoKind(kind) ? key.video_models : key.image_models
}

/**
 * 费用预估（ticks）。返回 0 表示无法预估（按分组定价）。
 *
 * 【必须在提交前展示】视频任务提交即扣费且审核拒绝不退款，
 * 720p×15s 是 $1.05，用户有权先知道这个数。
 */
export function estimateTicks(form: MediaFormState, key: MediaKey | null): number {
  const models = modelsForKind(key, form.kind)
  const model = models.find((m) => m.name === form.model)
  if (!model) return 0

  if (model.capability === 'video') {
    const perSec = key?.video_pricing?.[form.resolution] ?? 0
    return perSec * Math.max(1, form.duration)
  }
  return model.unit_price_ticks * Math.max(1, form.n)
}

/**
 * 本地表单校验，返回 i18n key；通过时返回空串。
 *
 * 后端会再校验一遍——这里只为省一次往返并给出即时反馈。
 */
export function validateMediaForm(form: MediaFormState, key: MediaKey | null): string {
  if (!key) return 'media.errors.selectKey'
  if (!form.model) return 'media.errors.selectModel'
  if (!form.prompt.trim()) return 'media.errors.emptyPrompt'
  if (form.prompt.length > PROMPT_MAX_LEN) return 'media.errors.promptTooLong'

  if (isVideoKind(form.kind)) {
    if (!VIDEO_RESOLUTIONS.includes(form.resolution as (typeof VIDEO_RESOLUTIONS)[number])) {
      return 'media.errors.badResolution'
    }
    if (form.duration < VIDEO_MIN_DURATION || form.duration > VIDEO_MAX_DURATION) {
      return 'media.errors.badDuration'
    }
    if (needsImageURL(form.kind) && !/^https?:\/\//i.test(form.imageURL.trim())) {
      return 'media.errors.badImageURL'
    }
    return ''
  }

  if (form.n < 1 || form.n > IMAGE_MAX_N) return 'media.errors.badCount'
  return ''
}

/**
 * 生成幂等键。
 *
 * 【为什么必须有】视频提交即扣费。用户狂点按钮或网络重试时，
 * 后端靠这个键复用既有任务而不是重复下单。
 *
 * crypto.randomUUID 在非安全上下文不可用（如 http 局域网调试），
 * 故有回退分支。
 */
export function newClientRequestID(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`
}

/** 任务状态对应的 badge 类名。必须写完整字面量：Tailwind 扫的是源码文本。 */
export function mediaStatusClass(status: string): string {
  switch (status) {
    case 'succeeded':
      return 'badge-success'
    case 'failed':
      return 'badge-danger'
    default:
      return 'badge-info'
  }
}
