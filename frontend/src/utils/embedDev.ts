/**
 * 嵌入页本地联调工具：签发 dev token 并拼装嵌入 URL。
 *
 * 嵌入页的身份来自 sub2api iframe 透传的 token，本地没有 sub2api 站点时，
 * 借用后端 dev 端点（仅 plaza.dev_mode=true 时注册，见 embed_dev_handler.go）
 * 自签一个任意 user_id 的合法 token。**该端点生产绝不可开**，这里的失败文案
 * 要能引导人把 dev_mode 打开。
 *
 * 被两处复用：
 *   · views/embed/EmbedDevPage.vue —— /embed/_dev 专属调试页（仅开发构建）
 *   · views/EmbedHub.vue —— 主应用内的「嵌入页面」Hub（登录后可见）
 * 签发 + 拼 URL 的逻辑只有这一份，避免两份清单与两套拼法漂移。
 */

export interface EmbedUrlParams {
  path: string
  userId: string
  theme: string
  lang: string
}

export const EMBED_THEMES = ['light', 'dark'] as const
export const EMBED_LANGS = ['zh-CN', 'en-US'] as const

/**
 * 探测后端的 dev token 端点是否可用。
 *
 * 端点 404 即未注册（dev_mode 关/生产构建），本地生成能力随之禁用。
 * 正常响应会签出一个 token，探测本身不产生任何持久化副作用，只是代价极低的
 * 一次 GET。返回值用 [ok, errorHint] 表达：ok 用于控制 UI，errorHint 在不可用
 * 时给出可执行的修复动作。
 */
export async function probeDevToken(): Promise<[boolean, string]> {
  try {
    const resp = await fetch('/api/v1/embed/_dev/token?user_id=1')
    const body = await resp.json().catch(() => null)
    if (resp.ok && body && body.code === 0) return [true, '']
    if (resp.status === 404) {
      return [false, 'embedHub.devUnavailable']
    }
    return [false, 'embedHub.devError']
  } catch {
    return [false, 'embedHub.devNetwork']
  }
}

/** 向后端换取自签 token，失败时抛出带 i18n key 的可读信息。 */
export async function issueDevToken(userId: string): Promise<string> {
  const resp = await fetch(`/api/v1/embed/_dev/token?user_id=${encodeURIComponent(userId.trim() || '1')}`)
  const body = await resp.json().catch(() => null)
  if (!resp.ok || !body || body.code !== 0) {
    // 未注册时后端返回 404，这是最常见的失败原因，直接给出修复动作。
    throw new Error(resp.status === 404 ? 'embedHub.devUnavailable' : 'embedHub.devError')
  }
  return body.data.token as string
}

/** 拼装嵌入页 URL：参数与 sub2api 自定义菜单实际追加的一致（见 utils/embedQuery.ts）。 */
export async function buildEmbedUrl(params: EmbedUrlParams): Promise<string> {
  const token = await issueDevToken(params.userId)
  const query = new URLSearchParams({
    token,
    user_id: params.userId.trim() || '1',
    theme: params.theme,
    lang: params.lang,
    ui_mode: 'embedded'
  })
  return `${params.path}?${query.toString()}`
}
