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

/** 参考图张数上限（图生图 / 图生视频），与后端 mediaMaxRefImages 对齐。 */
export const REF_IMAGE_MAX = 4

/** 提示词长度上限，与后端 mediaMaxPromptLen 对齐。 */
export const PROMPT_MAX_LEN = 2000

/**
 * 【宽高比与分辨率的可选值不在前端硬编码】
 *
 * 它们随每个模型的 `aspect_ratios` / `resolutions` 从后端下发。前端曾经自带一份
 * 8 档清单，里面的 `21:9` 根本不在 xAI 的合法取值里，用户选中后只能收到上游报错。
 * 能力清单的唯一来源是后端的模型登记表（`service/media_catalog.go`）。
 */

/** 图片计费档位。 */
export type ImageSizeTier = '1K' | '2K' | '4K'

/**
 * size 模式（gpt-image-*）的常用尺寸预设。
 *
 * 仅这类模型需要——它们的画面比例由真实像素尺寸决定。Grok 模型走 aspect_ratio，
 * 与本表无关。
 */
export const IMAGE_SIZE_BY_RATIO: Record<string, Record<ImageSizeTier, string>> = {
  '1:1': { '1K': '1024x1024', '2K': '2048x2048', '4K': '2160x2160' },
  '16:9': { '1K': '1024x576', '2K': '2048x1152', '4K': '3840x2160' },
  '9:16': { '1K': '576x1024', '2K': '1152x2048', '4K': '2160x3840' },
  '4:3': { '1K': '1024x768', '2K': '2048x1536', '4K': '2880x2160' },
  '3:4': { '1K': '768x1024', '2K': '1536x2048', '4K': '2160x2880' },
  '3:2': { '1K': '1024x683', '2K': '2048x1365', '4K': '3240x2160' },
  '2:3': { '1K': '683x1024', '2K': '1365x2048', '4K': '2160x3240' },
  '2:1': { '1K': '1024x512', '2K': '2048x1024', '4K': '4096x2048' },
  '1:2': { '1K': '512x1024', '2K': '1024x2048', '4K': '2048x4096' }
}

export const IMAGE_SIZE_PRESETS = Object.keys(IMAGE_SIZE_BY_RATIO).flatMap((ratio) =>
  (['1K', '2K', '4K'] as const).map((tier) => ({
    label: `${ratio} · ${tier}`,
    value: IMAGE_SIZE_BY_RATIO[ratio][tier],
    ratio,
    tier
  }))
)

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

export function imageSizeForAspectRatio(ratio: string, tier: ImageSizeTier = '1K'): string {
  return IMAGE_SIZE_BY_RATIO[ratio]?.[tier] ?? ''
}

export function imageAspectRatioFromSize(size: string): string {
  for (const ratio of Object.keys(IMAGE_SIZE_BY_RATIO)) {
    if (Object.values(IMAGE_SIZE_BY_RATIO[ratio]).includes(size)) return ratio
  }
  return ''
}

/** 表单状态。 */
export interface MediaFormState {
  kind: MediaTaskKind
  keyId: number
  model: string
  prompt: string
  n: number
  /** 仅 size 模式使用。与 aspectRatio 互斥。 */
  size: string
  /** 仅 aspect_ratio 模式（图片）与视频使用。 */
  aspectRatio: string
  /** Grok 图片的分辨率档（1k / 2k），同时决定计费档。 */
  imageResolution: string
  quality: string
  /** 视频分辨率。 */
  resolution: string
  duration: number
  imageURL: string
  /** 复用任务时带回的参考图地址（含第一张）。新上传走 files。 */
  imageURLs: string[]
  stream: boolean
}

/** 初始表单。 */
export function emptyMediaForm(): MediaFormState {
  return {
    kind: 't2i',
    keyId: 0,
    model: '',
    prompt: '',
    n: 1,
    size: '1024x576',
    aspectRatio: '1:1',
    imageResolution: '1k',
    quality: 'high',
    resolution: '480p',
    duration: 8,
    imageURL: '',
    imageURLs: [],
    stream: false
  }
}

/** Reset generation options on tab switch while keeping session context. */
export function resetMediaFormForKind(form: MediaFormState, kind: MediaTaskKind): MediaFormState {
  return {
    ...emptyMediaForm(),
    keyId: form.keyId,
    prompt: form.prompt,
    kind
  }
}

/** 该任务类型是否为视频。 */
export const isVideoKind = (kind: MediaTaskKind) => kind === 't2v' || kind === 'i2v'

/** 该任务类型是否需要上传参考图文件。 */
export const needsUpload = (kind: MediaTaskKind) => kind === 'i2i' || kind === 'i2v'

/** 该任务类型可以把公网 URL 当参考图（选文件优先）。 */
export const needsImageURL = (kind: MediaTaskKind) => kind === 'i2v'

export function isPublicImageURL(raw: string): boolean {
  return /^https?:\/\//i.test(raw.trim())
}

/** 按出现顺序去重，只留公网 http(s)。 */
export function uniquePublicImageURLs(...groups: Array<string | string[] | undefined>): string[] {
  const out: string[] = []
  const seen = new Set<string>()
  for (const group of groups) {
    const items = Array.isArray(group) ? group : group ? [group] : []
    for (const raw of items) {
      const url = raw.trim()
      if (!isPublicImageURL(url) || seen.has(url)) continue
      seen.add(url)
      out.push(url)
    }
  }
  return out
}

/**
 * 追加参考图，超出 max 的丢掉。
 * overflow 表示这次选择里有没装下的。
 */
export function appendRefImages<T>(existing: T[], incoming: T[], max: number): { items: T[]; overflow: boolean } {
  const room = Math.max(0, max - existing.length)
  return {
    items: [...existing, ...incoming.slice(0, room)],
    overflow: incoming.length > room
  }
}

/** 从粘贴/输入框拆出公网图片地址：空白、逗号、分号都当分隔符。 */
export function splitRefImageInput(raw: string): string[] {
  return uniquePublicImageURLs(raw.split(/[\s,，;；]+/))
}

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
 * 费用预估（ticks）。返回 0 表示无法预估（后端拿不到该模型的价）。
 *
 * 【前端只做乘法】单价来自后端下发的 price_by_tier，已含分组自定义单价与计费倍率。
 * 倍率与分组定价的复杂度全部留在后端一处——它们过去散落在两端各算一半，
 * 结果是页面报价与账单对不上。
 *
 * 【必须在提交前展示】视频任务提交即扣费且审核拒绝不退款，
 * 720p×15s 是 $1.05，用户有权先知道这个数。
 */
export function estimateTicks(form: MediaFormState, key: MediaKey | null): number {
  const model = selectedModelOf(form, key)
  if (!model) return 0

  if (isVideoKind(form.kind)) {
    const perSec = model.price_by_tier?.[form.resolution] ?? model.unit_price_ticks
    return perSec * Math.max(1, form.duration)
  }
  return imageUnitPrice(form, model) * Math.max(1, form.n)
}

/** 当前表单选中的模型。 */
export function selectedModelOf(form: MediaFormState, key: MediaKey | null): MediaModelOption | null {
  return modelsForKind(key, form.kind).find((m) => m.name === form.model) ?? null
}

/** 本次请求会落在哪个图片计费档，与后端 billingTierOf 同口径。 */
export function billingTierOf(form: MediaFormState, model: MediaModelOption): ImageSizeTier {
  if (model.size_mode === 'aspect_ratio') {
    return form.imageResolution.toLowerCase() === '2k' ? '2K' : '1K'
  }
  // 未指定尺寸时后端按 2K 兜底（对齐 sub2api NormalizeImageBillingTierOrDefault）
  return (imageSizeTier(form.size) || '2K') as ImageSizeTier
}

function imageUnitPrice(form: MediaFormState, model: MediaModelOption): number {
  return model.price_by_tier?.[billingTierOf(form, model)] ?? model.unit_price_ticks
}

/**
 * 上游会静默替换掉当前选择时，返回替换后的模型名；否则返回空串。
 *
 * 【为什么要暴露给 UI】grok-imagine-video-1.5 在文生视频时被换成
 * grok-imagine-video，响应里没有任何提示。不告知的话，用户以为自己在用 1.5。
 */
export function downgradeTargetOf(form: MediaFormState, key: MediaKey | null): string {
  const model = selectedModelOf(form, key)
  if (!model?.downgrades_to) return ''
  // 缺省 downgrade_kinds 表示所有类型都降级
  if (!model.downgrade_kinds?.length) return model.downgrades_to
  return model.downgrade_kinds.includes(form.kind) ? model.downgrades_to : ''
}

/**
 * 本地表单校验，返回 i18n key；通过时返回空串。
 *
 * 后端会再校验一遍——这里只为省一次往返并给出即时反馈。
 *
 * 【可选值不再硬编码】分辨率与宽高比都对着模型下发的清单校验，
 * 前端自带清单的做法曾让 `21:9` 这个上游不认的取值一路走到网关。
 */
export function validateMediaForm(form: MediaFormState, key: MediaKey | null): string {
  if (!key) return 'media.errors.selectKey'
  const model = selectedModelOf(form, key)
  if (!model) return 'media.errors.selectModel'
  if (!form.prompt.trim()) return 'media.errors.emptyPrompt'
  if (form.prompt.length > PROMPT_MAX_LEN) return 'media.errors.promptTooLong'

  if (isVideoKind(form.kind)) {
    if (!model.resolutions?.includes(form.resolution)) return 'media.errors.badResolution'
    if (form.duration < VIDEO_MIN_DURATION || form.duration > VIDEO_MAX_DURATION) {
      return 'media.errors.badDuration'
    }
    if (form.aspectRatio && !model.aspect_ratios?.includes(form.aspectRatio)) {
      return 'media.errors.badAspectRatio'
    }
    if (form.imageURL.trim() && !isPublicImageURL(form.imageURL)) {
      return 'media.errors.badImageURL'
    }
    if (form.imageURLs.filter(Boolean).length > REF_IMAGE_MAX) {
      return 'media.errors.tooManyImages'
    }
    return ''
  }

  if (form.n < 1 || form.n > IMAGE_MAX_N) return 'media.errors.badCount'
  if (model.size_mode === 'aspect_ratio') {
    if (form.aspectRatio && !model.aspect_ratios?.includes(form.aspectRatio)) {
      return 'media.errors.badAspectRatio'
    }
    if (form.imageResolution && !model.resolutions?.includes(form.imageResolution)) {
      return 'media.errors.badResolution'
    }
  } else if (form.size && !imageSizeTier(form.size)) {
    return 'media.errors.badSize'
  }
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
