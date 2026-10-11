<template>
  <div ref="host" class="relative h-full overflow-hidden bg-white">
    <!--
      作品渲染在固定虚拟视口里，再整体等比缩放：固定像素尺寸的 HTML 也能整幅看到，
      且不会出现 iframe 自己的滚动条。缩放后多出来的余量用 left/top 居中。
    -->
    <div v-if="visible && result?.output?.document && scale > 0" class="absolute" :style="frameStyle">
      <DetectArtifactFrame :document="result.output.document" :kind="result.output.kind" :title="title" allow-scripts :interactive="interactive" fit />
    </div>
    <div v-else class="flex h-full items-center justify-center p-5 text-sm text-gray-500">
      <button v-if="error" class="text-red-600" @click="load">{{ error }} · {{ t('pelican.reload') }}</button>
      <span v-else>{{ visible ? t('common.loading') : t('pelican.preview') }}</span>
    </div>
  </div>
</template>
<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { errorMessage } from '@/api/client'
import DetectArtifactFrame from '@/components/detect/DetectArtifactFrame.vue'
import { ARTIFACT_PREVIEW_VIEW } from '@/utils/detectArtifact'
import { pelicanPreviewScale } from '@/utils/pelican'
import type { PelicanResult } from '@/types/pelican'
const props = defineProps<{ id: number; title: string; fetchResult: (id: number) => Promise<PelicanResult>; interactive?: boolean }>()
const { t } = useI18n()
const host = ref<HTMLElement>()
const visible = ref(false)
const result = ref<PelicanResult>()
const error = ref('')
const boxWidth = ref(0), boxHeight = ref(0)
let observer: IntersectionObserver | undefined
let resizer: ResizeObserver | undefined
let version = 0
const view = ARTIFACT_PREVIEW_VIEW
const scale = computed(() => pelicanPreviewScale(boxWidth.value, boxHeight.value, view))
const frameStyle = computed(() => ({
  width: `${view.width}px`, height: `${view.height}px`,
  transform: `scale(${scale.value})`, transformOrigin: 'top left',
  left: `${Math.max(0, (boxWidth.value - view.width * scale.value) / 2)}px`,
  top: `${Math.max(0, (boxHeight.value - view.height * scale.value) / 2)}px`
}))
function measure() {
  const rect = host.value?.getBoundingClientRect()
  boxWidth.value = rect?.width ?? 0
  boxHeight.value = rect?.height ?? 0
}
async function load() {
  const ownVersion = ++version
  error.value = ''
  try {
    const next = await props.fetchResult(props.id)
    if (ownVersion !== version || !visible.value) return
    if (!next.output?.document) throw new Error('pelican.errors.noDocument')
    result.value = next
  } catch (e) { if (ownVersion === version) error.value = t(errorMessage(e)) }
}
watch(visible, value => { if (value) void load(); else { version++; result.value = undefined; error.value = '' } })
watch(() => props.id, () => { result.value = undefined; if (visible.value) void load() })
onMounted(() => {
  measure()
  observer = new IntersectionObserver(entries => { visible.value = entries.some(e => e.isIntersecting) }, { threshold: 0.01 })
  if (host.value) observer.observe(host.value)
  resizer = new ResizeObserver(measure)
  if (host.value) resizer.observe(host.value)
})
onBeforeUnmount(() => { version++; observer?.disconnect(); resizer?.disconnect(); result.value = undefined })
</script>
