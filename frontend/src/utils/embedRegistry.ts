/**
 * 可嵌入 sub2api 的页面登记表。
 *
 * sub2api 的自定义菜单会把本站这些页面以 iframe 嵌套进自己的后台，鉴权走
 * sub2api 透传的用户 token（见 utils/embedQuery.ts 的协议说明）。
 *
 * 这份清单是哪些页面支持嵌入的唯一定义，新的 /embed/* 页面必须三处同时登记——
 * 本表（让 Hub 页与调试页展示出来）:
 *   · 后端 middleware.embedPagePaths（否则 CSP frame-ancestors 拒绝嵌入）
 *   · 前端 router index.ts（否则路由不存在）
 * router 里的注释也复述了这条约定，改任一处都要顺手对一下另外两处。
 *
 * label / description 是 i18n key，Hub 页用 t() 渲染。
 */

import type { IconName } from '@/components/icons'

export interface EmbedPageDef {
  /** 前端路由路径，如 /embed/plaza。 */
  path: string
  /** 展示用 i18n key（导航 / 卡片标题）。 */
  labelKey: string
  /** 说明用 i18n key。 */
  descriptionKey: string
  /** 侧栏 / 卡片图标名。 */
  icon: IconName
  /** 该页依赖的后端功能开关，用于本地生成提示。 */
  dependsOn: string
}

export const EMBED_PAGES: readonly EmbedPageDef[] = [
  {
    path: '/embed/plaza',
    labelKey: 'embedHub.plaza',
    descriptionKey: 'embedHub.plazaDesc',
    icon: 'grid',
    dependsOn: 'plaza.enabled'
  },
  {
    path: '/embed/kyc',
    labelKey: 'embedHub.kyc',
    descriptionKey: 'embedHub.kycDesc',
    icon: 'clipboard',
    dependsOn: 'plaza.enabled'
  },
  {
    path: '/embed/media',
    labelKey: 'embedHub.media',
    descriptionKey: 'embedHub.mediaDesc',
    icon: 'play',
    dependsOn: 'plaza.enabled + media.enabled'
  }
]
