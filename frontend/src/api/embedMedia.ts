/**
 * 生图 / 生视频嵌入页 API。
 *
 * 独立 createEmbedClient：会话与广场页 / KYC 页互相隔离，
 * 一个页面的会话过期不该把其他页顶掉。
 */
import { createEmbedClient } from './embedClient'

const client = createEmbedClient('/api/v1/embed/media')

/** 一个可选模型。 */
export interface MediaModelOption {
  name: string
  capability: 'image' | 'video'
  /** 该模型是否真的接受 size 参数。Grok 图片模型会静默忽略它。 */
  supports_size: boolean
  /** 图片为每张价格，视频为每秒价格，单位 tick（1e-10 USD）。0 表示未知。 */
  unit_price_ticks: number
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
  /** 分辨率 → 每秒 ticks */
  video_pricing: Record<string, number>
}

export type MediaTaskKind = 't2i' | 'i2i' | 't2v' | 'i2v'
export type MediaTaskStatus = 'pending' | 'succeeded' | 'failed'

export interface MediaTask {
  id: number
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
  /** 产物是否可通过代理端点取回。**只有视频为 true** —— 图片不落库。 */
  has_content: boolean
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

/** JSON 路径的生成请求（文生图 / 文生视频 / 图生视频）。 */
export interface MediaGenerateInput {
  key_id: number
  kind: Exclude<MediaTaskKind, 'i2i'>
  model: string
  prompt: string
  n?: number
  size?: string
  quality?: string
  resolution?: string
  duration?: number
  image_url?: string
  client_request_id: string
}

export const createSession = (token: string, userId: string) => client.createSession(token, userId)

export const fetchKeys = () => client.request<{ items: MediaKey[] }>('/keys').then((r) => r.items)

export const generate = (input: MediaGenerateInput) =>
  client.request<MediaSubmitResult>('/generate', { method: 'POST', body: JSON.stringify(input) })

export const fetchTasks = (limit = 30) =>
  client.request<{ items: MediaTask[] }>(`/tasks?limit=${limit}`).then((r) => r.items)

/**
 * 图生图：multipart 上传参考图。
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
