<template>
  <!--
    不透明源沙箱：不给 allow-same-origin，作品碰不到本站 localStorage 里的登录 token 与父页面。
    脚本默认不执行（SMIL / CSS 动画照样会动）：一旦允许脚本，作品就能把 iframe 自己导航到外站，
    而导航后的页面不受 srcdoc 里 CSP 的约束，所以只在用户显式开启时才给 allow-scripts。
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
  /** 用户在详情里显式开启后才执行作品脚本 */
  allowScripts?: boolean
}>()

const srcdoc = computed(() => artifactPreviewDocument(props.document, props.kind ?? 'svg'))
</script>
