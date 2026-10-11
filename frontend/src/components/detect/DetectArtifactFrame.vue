<template>
  <!--
    不透明源沙箱：不给 allow-same-origin，作品碰不到本站 localStorage 里的登录 token 与父页面。
    脚本默认不执行（SMIL / CSS 动画照样会动）：一旦允许脚本，作品就能把 iframe 自己导航到外站，
    而导航后的页面不受 srcdoc 里 CSP 的约束。检测页由用户显式开启；鹈鹕发布页经管理员审核后自动播放。
    fit 由鹈鹕卡片/详情使用：整幅作品渲染在固定尺寸里再交给外层缩放，并去掉作品自己的滚动条。
  -->
  <iframe
    :srcdoc="srcdoc"
    :title="title"
    :sandbox="allowScripts ? 'allow-scripts' : ''"
    referrerpolicy="no-referrer"
    loading="lazy"
    :scrolling="fit ? 'no' : undefined"
    :style="fit ? { width: `${view.width}px`, height: `${view.height}px` } : undefined"
    :tabindex="interactive ? 0 : -1"
    class="block h-full w-full border-0 bg-white"
    :class="{ 'pointer-events-none': !interactive }"
  />
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { ARTIFACT_PREVIEW_VIEW, artifactPreviewDocument, type ArtifactKind } from '@/utils/detectArtifact'

const props = withDefaults(
  defineProps<{
    document: string
    kind?: ArtifactKind
    title: string
    /** 缩略图不接收鼠标（点击交给外层去开详情）；详情里的大图可以交互 */
    interactive?: boolean
    /** 检测页手动开启；鹈鹕发布页允许自动播放 */
    allowScripts?: boolean
    /** 按虚拟视口渲染、由外层等比缩放（鹈鹕列表与详情） */
    fit?: boolean
  }>(),
  { fit: false }
)

const view = ARTIFACT_PREVIEW_VIEW
const srcdoc = computed(() => artifactPreviewDocument(props.document, props.kind ?? 'svg', props.fit))
</script>
