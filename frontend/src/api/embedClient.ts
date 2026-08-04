/**
 * sub2api iframe 嵌入页通用 API 客户端。
 *
 * 刻意不复用 api/client.ts：那个 axios 实例会无条件注入 monitor 管理员 token，
 * 并在 401 时清空 token + 跳转登录页——嵌入页是另一套身份体系（sub2api 用户），
 * 两者混用会互相破坏登录态。
 *
 * 会话 token 只存模块级变量，不落 localStorage，避免与后台登录态混淆。
 * 也刻意不用 Cookie：嵌入页的 CSRF 安全性完全依赖「凭据只在 Authorization 头里」
 * 这一前提，一旦引入 Cookie，跨站表单提交就能带上凭据。
 *
 * 每个嵌入页各自 createEmbedClient 一次，会话互相隔离 —— 广场页的会话过期
 * 不该把 KYC 页一起顶掉。
 */

interface Envelope<T> {
  code: number
  message: string
  data: T
}

export interface EmbedClient {
  /** 用 sub2api 透传的 token 换取本站短期会话。userId 仅供后端比对，不作为身份依据。 */
  createSession(sub2apiToken: string, userId: string): Promise<void>
  request<T>(path: string, options?: RequestInit): Promise<T>
}

/**
 * 创建一个绑定到指定 API 前缀的嵌入客户端。
 * @param apiBase 形如 `/api/v1/embed/plaza`
 */
export function createEmbedClient(apiBase: string): EmbedClient {
  let sessionToken: string | null = null

  /**
   * 发起请求并解包后端 envelope。
   * 错误信息是 i18n key（后端约定），调用方直接 t(key) 渲染。
   */
  async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
    let resp: Response
    try {
      resp = await fetch(`${apiBase}${path}`, {
        ...options,
        headers: {
          Accept: 'application/json',
          'Content-Type': 'application/json',
          ...(sessionToken ? { Authorization: `Bearer ${sessionToken}` } : {}),
          ...(options.headers ?? {})
        }
      })
    } catch {
      throw new Error('plaza.errors.network')
    }

    const text = await resp.text()
    let body: Partial<Envelope<T>>
    try {
      body = text ? JSON.parse(text) : {}
    } catch {
      throw new Error('plaza.errors.loadFailed')
    }

    if (!resp.ok || body.code !== 0) {
      throw new Error(body.message || 'plaza.errors.loadFailed')
    }
    return body.data as T
  }

  async function createSession(sub2apiToken: string, userId: string): Promise<void> {
    const data = await request<{ session_token: string; expires_in: number }>('/session', {
      method: 'POST',
      body: JSON.stringify({ sub2api_token: sub2apiToken, user_id: userId })
    })
    sessionToken = data.session_token
  }

  return { createSession, request }
}
