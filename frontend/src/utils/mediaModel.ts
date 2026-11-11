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
 * 上游对像素尺寸的硬约束（越界一律 400 Invalid image size）。
 *
 * 来自网关文档：宽高均为 16 的倍数、最长边 ≤3840、总像素 655360~8294400。
 * 宽高比约束（1:3 ~ 3:1）本表的比例全部满足，故不单独校验。
 */
export const IMAGE_SIZE_MIN_PIXELS = 655_360
export const IMAGE_SIZE_MAX_PIXELS = 8_294_400
export const IMAGE_SIZE_MAX_EDGE = 3840

/**
 * size 模式（gpt-image-*）的常用尺寸预设。
 *
 * 仅这类模型需要——它们的画面比例由真实像素尺寸决定。Grok 模型走 aspect_ratio，
 * 与本表无关。
 *
 * 【每一档都必须满足上面的硬约束】旧表里有 12 档越界（683/1365/3240 不是 16
 * 的倍数、1024x576 像素不足、4096 超出最长边），用户选中就直接收到上游 400。
 *
 * 【为什么某些比例没有 1K】计费档按最长边判定，1K 要求最长边 ≤1024，此时像素
 * 上限只有 1024×1024÷比例。16:9 / 2:1 这类宽比例在 1K 下最多 589824 像素，
 * 低于下限，数学上不存在合法取值——所以直接不给，而不是给一个必报错的档。
 */
export const IMAGE_SIZE_BY_RATIO: Record<string, Partial<Record<ImageSizeTier, string>>> = {
  '1:1': { '1K': '1024x1024', '2K': '2048x2048', '4K': '2160x2160' },
  '16:9': { '2K': '2048x1152', '4K': '3840x2160' },
  '9:16': { '2K': '1152x2048', '4K': '2160x3840' },
  '4:3': { '1K': '1024x768', '2K': '2048x1536', '4K': '2880x2160' },
  '3:4': { '1K': '768x1024', '2K': '1536x2048', '4K': '2160x2880' },
  '3:2': { '1K': '1008x672', '2K': '2016x1344', '4K': '3072x2048' },
  '2:3': { '1K': '672x1008', '2K': '1344x2016', '4K': '2048x3072' },
  '2:1': { '2K': '2048x1024', '4K': '3840x1920' },
  '1:2': { '2K': '1024x2048', '4K': '1920x3840' }
}

export const IMAGE_SIZE_PRESETS = Object.keys(IMAGE_SIZE_BY_RATIO).flatMap((ratio) =>
  (['1K', '2K', '4K'] as const).flatMap((tier) => {
    const value = IMAGE_SIZE_BY_RATIO[ratio][tier]
    return value ? [{ label: `${ratio} · ${tier}`, value, ratio, tier }] : []
  })
)


/**
 * 尺寸是否满足上游硬约束。格式不合法时同样返回 false。
 *
 * 与 `imageSizeTier` 分工不同：那个只管计费档，这个只管「上游收不收」。
 */
export function isValidImageSize(size: string): boolean {
  const m = /^(\d+)x(\d+)$/i.exec(size.trim())
  if (!m) return false
  const w = Number(m[1])
  const h = Number(m[2])
  if (w % 16 !== 0 || h % 16 !== 0) return false
  if (Math.max(w, h) > IMAGE_SIZE_MAX_EDGE) return false
  const pixels = w * h
  return pixels >= IMAGE_SIZE_MIN_PIXELS && pixels <= IMAGE_SIZE_MAX_PIXELS
}

/** ticks 转美元字符串（4 位小数，与后端 FormatTicksUSD 一致）。 */
export function ticksToUSD(ticks: number): string {
  return (ticks / TICKS_PER_USD).toFixed(4)
}

/** 生成耗时展示：毫秒 / 秒 / 分秒。无效值返回 '-'。 */
export function formatElapsed(ms?: number | null): string {
  if (ms === null || ms === undefined || !Number.isFinite(ms) || ms < 0) return '-'
  if (ms < 1000) return Math.round(ms) + 'ms'
  const sec = ms / 1000
  if (sec < 10) return sec.toFixed(1) + 's'
  if (sec < 60) return Math.round(sec) + 's'
  const m = Math.floor(sec / 60)
  const s = Math.round(sec % 60)
  return `${m}m ${s}s`
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
    // 表是稀疏的（某些比例没有 1K），缺档返回 undefined，只在有值的档里找
    const sizes = Object.values(IMAGE_SIZE_BY_RATIO[ratio]).filter((v): v is string => !!v)
    if (sizes.includes(size)) return ratio
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
  /** 输出编码格式（png / jpeg / webp），仅 size 模式。 */
  outputFormat: string
  /** 压缩质量 0-100，仅 jpeg / webp 生效。 */
  outputCompression: number
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
    size: '1024x1024',
    aspectRatio: '1:1',
    imageResolution: '1k',
    quality: 'high',
    outputFormat: 'png',
    outputCompression: 100,
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
  let model = selectedModelOf(form, key)
  if (!model) return 0

  if (isVideoKind(form.kind)) {
    // 文生视频的 grok-imagine-video-1.5 会在上游静默降级；
    // 报价必须使用实际计费模型，图生视频则保留 1.5 的价格。
    const downgradeTarget = downgradeTargetOf(form, key)
    if (downgradeTarget) {
      model = modelsForKind(key, form.kind).find((candidate) => candidate.name === downgradeTarget) ?? model
    }
    const perSec = model.price_by_tier?.[form.resolution] ?? model.unit_price_ticks
    return perSec * Math.max(1, form.duration)
  }
  return imageUnitPrice(form, model) * Math.max(1, form.n)
}

/** 当前表单选中的模型。 */
export function selectedModelOf(form: MediaFormState, key: MediaKey | null): MediaModelOption | null {
  return modelsForKind(key, form.kind).find((m) => m.name === form.model) ?? null
}

/**
 * 该模型是否用「宽高比 + 清晰度档」控制画面（Grok 的 aspect_ratio 模式、
 * Gemini 的 image_size 模式），而不是真实像素尺寸（gpt-image 的 size 模式）。
 */
export function usesAspectRatio(model: MediaModelOption | null | undefined): boolean {
  return !!model && model.size_mode !== 'size'
}

/** 本次请求会落在哪个图片计费档，与后端 billingTierOf 同口径。 */
export function billingTierOf(form: MediaFormState, model: MediaModelOption): ImageSizeTier {
  if (model.size_mode === 'aspect_ratio') {
    return form.imageResolution.toLowerCase() === '2k' ? '2K' : '1K'
  }
  if (model.size_mode === 'image_size') {
    // imageSize 就是计费档；不填时上游按 2K 计费
    const tier = form.imageResolution as ImageSizeTier
    return tier === '1K' || tier === '2K' || tier === '4K' ? tier : '2K'
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
  if (usesAspectRatio(model)) {
    if (form.aspectRatio && !model.aspect_ratios?.includes(form.aspectRatio)) {
      return 'media.errors.badAspectRatio'
    }
    if (form.imageResolution && !model.resolutions?.includes(form.imageResolution)) {
      return 'media.errors.badResolution'
    }
  } else {
    // size 模式：先确认是「尺寸」而不是乱填的字符串，再拦住上游必拒的越界值。
    if (form.size && !imageSizeTier(form.size)) return 'media.errors.badSize'
    if (form.size && !isValidImageSize(form.size)) return 'media.errors.badSizeRange'
    if (form.quality && !imageQualityOptions(form.model).includes(form.quality)) {
      return 'media.errors.badQuality'
    }
    if (form.outputFormat && !isImageOutputFormat(form.outputFormat)) {
      return 'media.errors.badOutputFormat'
    }
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

/** 质量档位。前四档全系通用，后两档仅 2.5 及更高版本支持。 */
export const IMAGE_QUALITY_BASE = ['low', 'medium', 'high', 'auto'] as const
export const IMAGE_QUALITY_25_PLUS = ['xhigh', 'max'] as const

/** 输出编码格式。 */
export const IMAGE_OUTPUT_FORMATS = ['png', 'jpeg', 'webp'] as const

/** 是否为受支持的输出格式。 */
export function isImageOutputFormat(format: string): boolean {
  return (IMAGE_OUTPUT_FORMATS as readonly string[]).includes(format)
}

/** 需要压缩档位的格式（png 是无损编码，上游不认 output_compression）。 */
export function supportsOutputCompression(format: string): boolean {
  return format === 'jpeg' || format === 'webp'
}

/**
 * 解析 gpt-image 版本号，返回形如 [2, 5] 的数组；非 gpt-image 模型返回 null。
 *
 * 后缀（-flare / -sunburst）不影响版本，2.5-flare 就是 2.5。
 */
export function gptImageVersion(name: string): [number, number] | null {
  const m = /^gpt-image-(\d+)(?:\.(\d+))?/.exec(name)
  if (!m) return null
  return [Number(m[1]), Number(m[2] ?? 0)]
}

/**
 * 是否为 gpt-image-2.5 或更新的版本。
 *
 * 【为什么按版本比而不是比对字面量】早先这里是三个模型的硬编码清单，
 * gpt-image-2.6 一上线就被漏掉，xhigh / max 两档白白不可用。
 * 新版本应当自动继承，不该每次发布都回来改这张表。
 */
export function isGptImage25Plus(name: string): boolean {
  const v = gptImageVersion(name)
  if (!v) return false
  return v[0] > 2 || (v[0] === 2 && v[1] >= 5)
}

/** 该模型可用的质量档位。 */
export function imageQualityOptions(name: string): string[] {
  return isGptImage25Plus(name)
    ? [...IMAGE_QUALITY_BASE, ...IMAGE_QUALITY_25_PLUS]
    : [...IMAGE_QUALITY_BASE]
}

/**
 * 模型定位文案的 i18n key。
 *
 * 模型名带点号（gpt-image-2.5-flare），不能直接当 vue-i18n 嵌套路径，
 * 所以用这张显式表把名字映射到不含多余点的 key。
 */
export function mediaModelHintKey(name: string): string {
  switch (name) {
    case 'gpt-image-1':
      return 'media.modelHints.gptImage1'
    case 'gpt-image-1.5':
      return 'media.modelHints.gptImage15'
    case 'gpt-image-2':
      return 'media.modelHints.gptImage2'
    case 'gpt-image-2.5':
      return 'media.modelHints.gptImage25'
    case 'gpt-image-2.5-flare':
      return 'media.modelHints.gptImage25Flare'
    case 'gpt-image-2.5-sunburst':
      return 'media.modelHints.gptImage25Sunburst'
    case 'gpt-image-2.6':
      return 'media.modelHints.gptImage26'
    case 'grok-imagine-image':
      return 'media.modelHints.grokImagineImage'
    case 'grok-imagine-image-quality':
      return 'media.modelHints.grokImagineImageQuality'
    case 'grok-imagine':
      return 'media.modelHints.grokImagine'
    case 'grok-imagine-video':
      return 'media.modelHints.grokImagineVideo'
    case 'grok-imagine-video-1.5':
      return 'media.modelHints.grokImagineVideo15'
    case 'gemini-nano-banana-2.1':
      return 'media.modelHints.geminiNanoBanana21'
    case 'gemini-3.1-flash-image':
      return 'media.modelHints.gemini31FlashImage'
    case 'gemini-3.1-flash-image-preview':
      return 'media.modelHints.gemini31FlashImagePreview'
    case 'gemini-3-pro-image':
      return 'media.modelHints.gemini3ProImage'
    case 'gemini-3-pro-image-preview':
      return 'media.modelHints.gemini3ProImagePreview'
    default:
      return ''
  }
}

/** 模型下拉的一项。 */
export interface MediaModelSelectOption {
  value: string
  label: string
  description?: string
  kind?: 'group'
  disabled?: boolean
  [key: string]: unknown
}

/** 把当前 key 可用的模型编成下拉选项：每项底部带定位说明。 */
export function mediaModelSelectOptions(
  models: Array<{ name: string }>,
  translate: (key: string) => string
): MediaModelSelectOption[] {
  return models.map((model) => {
    const hintKey = mediaModelHintKey(model.name)
    return {
      value: model.name,
      label: model.name,
      description: hintKey ? translate(hintKey) : undefined
    }
  })
}
