/**
 * sub2api iframe 嵌入参数解析。
 *
 * sub2api 的自定义菜单会向配置的 URL 追加：
 *   ?user_id=&token=<用户JWT>&theme=light|dark&lang=&ui_mode=embedded
 *    &src_host=<sub2api origin>&src_url=<完整URL>
 *
 * 注意 src_host 是不受信输入，仅作排查用途；换会话时后端只用配置文件里的地址。
 */

/** 收敛 vue-router query 的 string | null | (string|null)[] 三态。 */
export function queryString(value: unknown): string {
  if (Array.isArray(value)) {
    const first = value[0]
    return typeof first === 'string' ? first : ''
  }
  return typeof value === 'string' ? value : ''
}

/** 应用 sub2api 传来的主题（Tailwind darkMode: 'class'）。 */
export function applyTheme(theme: string): void {
  if (theme === 'dark') {
    document.documentElement.classList.add('dark')
  } else if (theme === 'light') {
    document.documentElement.classList.remove('dark')
  }
}

/** 把 sub2api 的 lang 映射到本站 locale，兼容 zh-CN / zh_TW / ZH 等写法。 */
export function resolveLocale(lang: string): 'zh-CN' | 'en-US' {
  return lang.toLowerCase().startsWith('zh') ? 'zh-CN' : 'en-US'
}

/**
 * 从地址栏移除 sub2api token，保留其余参数便于排查。
 *
 * 必须在拿到 token 后立即调用、且先于任何网络请求：一旦请求失败或用户分享/收藏
 * 当前地址，明文 token 就会外泄（浏览器历史、书签、截图皆可见）。
 */
export function stripTokenFromUrl(): void {
  const params = new URLSearchParams(window.location.search)
  if (!params.has('token')) return
  params.delete('token')
  const query = params.toString()
  const next = query ? `${window.location.pathname}?${query}` : window.location.pathname
  window.history.replaceState(window.history.state, '', next)
}
