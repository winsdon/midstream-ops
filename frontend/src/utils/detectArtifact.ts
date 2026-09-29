/**
 * 智商测试作品（模型生成的 SVG / HTML）的预览文档。
 *
 * 作品是不可信内容，只放进不带 allow-same-origin 的沙箱 iframe：不透明源，碰不到本站
 * localStorage 里的登录 token，也碰不到父页面 DOM。这里再用 CSP 禁掉一切子资源外连
 * （追踪像素、fetch 回传查看者 IP）。
 *
 * 脚本默认不执行：带 allow-scripts 时作品能用 location.href 或 meta refresh 把 iframe
 * 自己导航到外站，导航后的页面不受这里的 CSP 约束。SMIL 与 CSS 动画不需要脚本照样会动；
 * 只有靠 JS 驱动的作品，才由用户在详情里显式开启脚本。做法参照 cockpit-tools 的 Codex Pelican 预览。
 */

/** 只放行内联脚本 / 样式与 data: 资源；动画要靠内联脚本和样式才能动。 */
const PREVIEW_CSP = [
  "default-src 'none'",
  "script-src 'unsafe-inline'",
  "style-src 'unsafe-inline'",
  'img-src data: blob:',
  'font-src data:',
  "media-src 'none'",
  "connect-src 'none'",
  "frame-src 'none'",
  "child-src 'none'",
  "worker-src 'none'",
  "object-src 'none'",
  "base-uri 'none'",
  "form-action 'none'"
].join('; ')

/**
 * CSP 的 connect-src 管不住 WebRTC：用户开启脚本时，在作品脚本执行前把相关构造器置空并锁死。
 * alert / confirm 之类不用管——沙箱没给 allow-modals，本来就弹不出来。
 */
const LOCKDOWN_SCRIPT = `(() => {
  for (const key of ['RTCPeerConnection', 'webkitRTCPeerConnection', 'mozRTCPeerConnection', 'RTCDataChannel', 'WebTransport']) {
    try { Object.defineProperty(globalThis, key, { value: undefined, writable: false, configurable: false }) } catch (_) {}
  }
})()`

/** 独立 SVG 白底居中、按 viewBox 等比撑满预览框（缩略图与大图共用）。 */
const SVG_FIT_STYLE =
  'html,body{margin:0;width:100%;height:100%;overflow:hidden;background:#fff}' +
  'body{display:flex;align-items:center;justify-content:center}' +
  'body>svg{width:100%;height:100%}'

/** 安全头：必须排在作品之前，作品里再写 CSP 也只能收紧、放不开。 */
const PREVIEW_HEAD =
  `<meta http-equiv="Content-Security-Policy" content="${PREVIEW_CSP}">` +
  '<meta name="referrer" content="no-referrer">' +
  `<script>${LOCKDOWN_SCRIPT}</script>`

export type ArtifactKind = 'svg' | 'html'

/** 拼出 iframe srcdoc。作品原样嵌入，不做任何修补。 */
export function artifactPreviewDocument(document: string, kind: ArtifactKind): string {
  if (kind === 'html') return PREVIEW_HEAD + document
  return `<!doctype html><html><head>${PREVIEW_HEAD}<style>${SVG_FIT_STYLE}</style></head><body>${document}</body></html>`
}

/** 作品是否带脚本（<script> 或 onload= 之类的事件属性）。带脚本的才需要给用户「运行脚本」的开关。 */
export function artifactHasScript(document: string): boolean {
  return /<script[\s>]|\son[a-z]+\s*=/i.test(document)
}
