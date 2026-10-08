<template>
  <div ref="host" class="relative h-full min-h-48 bg-white">
    <DetectArtifactFrame v-if="visible && result?.output?.document" :document="result.output.document" :kind="result.output.kind" :title="title" allow-scripts :interactive="interactive" />
    <div v-else class="flex h-full min-h-48 items-center justify-center p-5 text-sm text-gray-500">
      <button v-if="error" class="text-red-600" @click="load">{{ error }} · {{ t('pelican.reload') }}</button>
      <span v-else>{{ visible ? t('common.loading') : t('pelican.preview') }}</span>
    </div>
  </div>
</template>
<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { errorMessage } from '@/api/client'
import DetectArtifactFrame from '@/components/detect/DetectArtifactFrame.vue'
import type { PelicanResult } from '@/types/pelican'
const props = defineProps<{ id: number; title: string; fetchResult: (id: number) => Promise<PelicanResult>; interactive?: boolean }>()
const { t } = useI18n()
const host = ref<HTMLElement>()
const visible = ref(false)
const result = ref<PelicanResult>()
const error = ref('')
let observer: IntersectionObserver | undefined
let version = 0
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
  observer = new IntersectionObserver(entries => { visible.value = entries.some(e => e.isIntersecting) }, { threshold: 0.01 })
  if (host.value) observer.observe(host.value)
})
onBeforeUnmount(() => { version++; observer?.disconnect(); result.value = undefined })
</script>
