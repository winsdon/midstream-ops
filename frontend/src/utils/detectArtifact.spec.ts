import { describe, expect, it } from 'vitest'
import { artifactHasScript, artifactPreviewDocument } from '@/utils/detectArtifact'

const SVG = '<svg viewBox="0 0 10 10"><animate attributeName="x"/></svg>'

describe('artifactPreviewDocument', () => {
  it('CSP 与锁定脚本排在作品之前', () => {
    const doc = artifactPreviewDocument(SVG, 'svg')
    const csp = doc.indexOf('Content-Security-Policy')
    const lockdown = doc.indexOf('RTCPeerConnection')
    const artwork = doc.indexOf(SVG)
    expect(csp).toBeGreaterThanOrEqual(0)
    expect(lockdown).toBeGreaterThan(csp)
    expect(artwork).toBeGreaterThan(lockdown)
  })

  it('禁止一切外连，只放行内联与 data: 资源', () => {
    const doc = artifactPreviewDocument(SVG, 'svg')
    expect(doc).toContain("default-src 'none'")
    expect(doc).toContain("connect-src 'none'")
    expect(doc).toContain("form-action 'none'")
    expect(doc).toContain('img-src data: blob:')
    expect(doc).toContain('no-referrer')
  })

  it('独立 SVG 包进撑满预览框的白底页面，作品原样嵌入', () => {
    const doc = artifactPreviewDocument(SVG, 'svg')
    expect(doc.startsWith('<!doctype html>')).toBe(true)
    expect(doc).toContain(`<body>${SVG}</body>`)
    expect(doc).toContain('body>svg{width:100%;height:100%}')
  })

  it('完整 HTML 作品只在前面加安全头，不再套一层页面', () => {
    const html = '<!DOCTYPE html><html><body>鹈鹕</body></html>'
    const doc = artifactPreviewDocument(html, 'html')
    expect(doc.endsWith(html)).toBe(true)
    expect(doc.indexOf('Content-Security-Policy')).toBeLessThan(doc.indexOf(html))
    expect(doc).not.toContain('body>svg')
  })
})

describe('artifactHasScript', () => {
  it('识别 script 标签与内联事件属性', () => {
    expect(artifactHasScript('<svg><script>go()</script></svg>')).toBe(true)
    expect(artifactHasScript('<svg><SCRIPT type="text/javascript">go()</SCRIPT></svg>')).toBe(true)
    expect(artifactHasScript('<svg onload="go()"></svg>')).toBe(true)
  })

  it('纯 SMIL / CSS 动画不算带脚本', () => {
    expect(artifactHasScript('<svg><animateTransform attributeName="transform"/></svg>')).toBe(false)
    expect(artifactHasScript('<svg><style>@keyframes a{}</style><g data-once="1"/></svg>')).toBe(false)
  })
})
