<template>
  <!--
    不透明源沙箱：不给 allow-same-origin，作品碰不到本站 localStorage 里的登录 token 与父页面。
    脚本默认不执行（SMIL / CSS 动画照样会动）：一旦允许脚本，作品就能把 iframe 自己导航到外站，
    而导航后的页面不受 srcdoc 里 CSP 的约束。检测页由用户显式开启；鹈鹕发布页经管理员审核后自动播放。
  -->
  <iframe
    :srcdoc="srcdoc"
    :title="title"
    :sandbox="allowScripts ? 'allow-scripts' : ''"
    referrerpolicy="no-referrer"
    loading="lazy"
    :tabindex="interactive ? 0 : -1"
    class="block h-full w-full border-0 bg-white"
    :class="{ 'pointer-events-none': !interactive }"
  />
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { artifactPreviewDocument, type ArtifactKind } from '@/utils/detectArtifact'

const props = defineProps<{
  document: string
  kind?: ArtifactKind
  title: string
  /** 缩略图不接收鼠标（点击交给外层去开详情）；详情里的大图可以交互 */
  interactive?: boolean
  /** 检测页手动开启；鹈鹕发布页允许自动播放 */
  allowScripts?: boolean
}>()

const srcdoc = computed(() => artifactPreviewDocument(props.document, props.kind ?? 'svg'))
</script>
