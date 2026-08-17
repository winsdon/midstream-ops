/**
 * 生图 / 生视频嵌入页 API。
 *
 * 独立 createEmbedClient：会话与广场页 / KYC 页互相隔离，
 * 一个页面的会话过期不该把其他页顶掉。
 */
import { createEmbedClient } from './embedClient'

const client = createEmbedClient('/api/v1/embed/media')

/**
 * 尺寸参数模式。**这不是「支不支持尺寸」的布尔，而是两条互斥的上游路径。**
 *
 * - `aspect_ratio`：Grok 图片模型。用 aspect_ratio（14 档）+ resolution（1k/2k）。
 *   传 size 无效——sub2api 网关在转发前会主动删掉它。
 * - `size`：OpenAI 格式图片模型。用 size = "宽x高"，不认 aspect_ratio。
 */
export type MediaSizeMode = 'aspect_ratio' | 'size'

/** 一个可选模型。 */
export interface MediaModelOption {
  name: string
  capability: 'image' | 'video'
  /** 该模型用哪一套尺寸参数。 */
  size_mode: MediaSizeMode
  /** 可选宽高比（仅 aspect_ratio 模式的图片模型 + 全部视频模型）。 */
  aspect_ratios?: string[]
  /** 可选分辨率档（图片为 1k/2k，视频为 480p/720p/1080p）。 */
  resolutions?: string[]
  /**
   * 展示基准单价，单位 tick（1e-10 USD）。0 表示无法预估。
   *
   * **已含分组自定义单价与计费倍率** —— 前端只做「单价 × 数量」，
   * 定价的复杂度全部留在后端。
   */
  unit_price_ticks: number
  /** 各档单价：图片按 1K/2K/4K，视频按 480p/720p/1080p。 */
  price_by_tier?: Record<string, number>
  /**
   * 上游会把本模型静默替换成的模型名。
   *
   * 典型例子：grok-imagine-video-1.5 在文生视频时被换成 grok-imagine-video
   * 并按后者计费，响应里没有任何提示。必须告知用户「你选的不是你得到的」。
   */
  downgrades_to?: string
  /** 触发降级的任务类型；缺省表示全部类型都降级。 */
  downgrade_kinds?: MediaTaskKind[]
}

/** 一把 key 的客户侧视图。绝不含明文 key。 */
export interface MediaKey {
  id: number
  name: string
  masked_key: string
  group_name: string
  platform: string
  image_models: MediaModelOption[]
  video_models: MediaModelOption[]
  /** false 表示本站拿不到该分组的定价参数，预估只能当参考值。 */
  pricing_known: boolean
}

export type MediaTaskKind = 't2i' | 'i2i' | 't2v' | 'i2v'
export type MediaTaskStatus = 'pending' | 'succeeded' | 'failed'

/** 一份已转存到对象存储的产物。URL 长期有效，刷新页面后依然可用。 */
export interface MediaArtifact {
  url: string
  mime_type: string
}

export interface MediaTask {
  id: number
  key_id: number
  kind: MediaTaskKind
  model: string
  prompt: string
  params: string
  status: MediaTaskStatus
  progress: number
  cost_usd: string
  est_cost_usd: string
  error_message: string
  created_at: string
  /** 产物是否可通过代理端点取回（仅视频，且转存未完成时才需要）。 */
  has_content: boolean
  result_url: string
  /**
   * 已转存的产物列表。
   *
   * 有值时前端直接用这些 URL 渲染——它们不需要认证头、不会过期。
   * 为空表示转存未完成或未启用，回落到 data URI（图片）/ 代理 blob（视频）。
   */
  artifacts?: MediaArtifact[]
  /** '' 未涉及 | 'pending' 转存中 | 'stored' 已转存 | 'failed' 转存失败 */
  storage_status?: string
}

/**
 * 提交响应：任务元数据 + 本次生成的图片。
 *
 * images 是 data URI，只在本次响应里存在一次。图片走 b64 从网关取回后不落库
 * （xAI CDN 直链国内不可达，存链接等于存一个打不开的地址），刷新页面即丢失。
 */
export interface MediaSubmitResult {
  task: MediaTask
  images: string[]
}

/** JSON 路径的生成请求。参考图先直传对象存储，再带公网 URL。 */
export interface MediaGenerateInput {
  key_id: number
  kind: MediaTaskKind
  model: string
  prompt: string
  n?: number
  /** 仅 size 模式的图片模型。与 aspect_ratio 互斥。 */
  size?: string
  /** 仅 aspect_ratio 模式的图片模型 + 视频模型。与 size 互斥。 */
  aspect_ratio?: string
  /** Grok 图片的分辨率档（1k / 2k），同时决定计费档。 */
  image_resolution?: string
  quality?: string
  /** 视频分辨率（480p / 720p / 1080p）。 */
  resolution?: string
  duration?: number
  image_url?: string
  image_urls?: string[]
  stream?: boolean
  client_request_id: string
}

export interface MediaPrepareUploadInput {
  filename: string
  content_type: string
  size: number
}

export interface MediaPrepareUploadSlot {
  upload_url: string
  public_url: string
  headers: Record<string, string>
  content_type: string
}

export const createSession = (token: string, userId: string) => client.createSession(token, userId)

export const fetchKeys = () => client.request<{ items: MediaKey[] }>('/keys').then((r) => r.items)

export const generate = (input: MediaGenerateInput) =>
  client.request<MediaSubmitResult>('/generate', { method: 'POST', body: JSON.stringify(input) })

export const prepareUploads = (files: MediaPrepareUploadInput[]) =>
  client
    .request<{ items: MediaPrepareUploadSlot[] }>('/uploads/prepare', {
      method: 'POST',
      body: JSON.stringify({ files })
    })
    .then((r) => r.items)

export const fetchTasks = (limit = 30) =>
  client.request<{ items: MediaTask[] }>(`/tasks?limit=${limit}`).then((r) => r.items)

export const deleteTask = (taskId: number) =>
  client.request(`/tasks/${taskId}`, { method: 'DELETE' })

/**
 * 图生图 / 带本地文件的图生视频：multipart 上传参考图。
 * 后端先把文件落到对象存储，再带公开 URL 打上游。
 *
 * 不手动设 Content-Type —— embedClient 检测到 FormData 会自动跳过，
 * 交给浏览器带上正确的 multipart boundary。
 */
export const edit = (form: FormData) =>
  client.request<MediaSubmitResult>('/edits', { method: 'POST', body: form })

/**
 * 取视频产物并返回 object URL。
 *
 * **只用于视频**：产物端点需要认证头（后端代理上游），而浏览器的 src 属性
 * 发不出自定义头，只能 fetch 成 blob。图片不走这里 —— 它们随提交响应
 * 以 data URI 直接返回。
 *
 * 【调用方必须 revokeObjectURL】否则 blob 会一直占内存。
 */
export const fetchContent = (taskId: number) => client.fetchObjectURL(`/tasks/${taskId}/content`)
