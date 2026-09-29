<template>
  <section v-if="output && (output.document || output.text)" class="space-y-2">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <h4 class="text-xs font-semibold uppercase tracking-wider text-gray-400">{{ t('detect.output') }}</h4>
      <div v-if="output.document" class="flex rounded-lg bg-gray-100 p-0.5 dark:bg-dark-800">
        <button
          v-for="mode in VIEW_MODES"
          :key="mode"
          type="button"
          class="rounded-md px-2.5 py-1 text-xs font-medium transition-colors"
          :class="
            view === mode
              ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-700 dark:text-white'
              : 'text-gray-500 hover:text-gray-700 dark:text-dark-400 dark:hover:text-dark-200'
          "
          @click="view = mode"
        >
          {{ mode === 'preview' ? t('detect.outputPreview') : t('detect.outputSource') }}
        </button>
      </div>
    </div>

    <template v-if="output.document">
      <div
        v-if="view === 'preview'"
        class="h-[28rem] overflow-hidden rounded-xl border border-gray-100 dark:border-dark-800"
      >
        <!-- 切换脚本开关要换一个新 iframe：已加载文档的沙箱标志不会随属性变化而更新 -->
        <DetectArtifactFrame
          :key="scriptsOn ? 'scripts' : 'static'"
          :document="output.document"
          :kind="output.document_kind"
          :title="title"
          :allow-scripts="scriptsOn"
          interactive
        />
      </div>
      <pre v-else class="output-code max-h-[28rem]">{{ output.document }}</pre>

      <div
        v-if="hasScript && view === 'preview'"
        class="flex flex-wrap items-start justify-between gap-2 rounded-xl bg-amber-50 px-3 py-2 text-xs text-amber-700 dark:bg-amber-900/20 dark:text-amber-400"
      >
        <span class="min-w-0 flex-1 leading-relaxed">{{ t('detect.outputScriptsWarning') }}</span>
        <button type="button" class="btn btn-secondary btn-sm" @click="scriptsOn = !scriptsOn">
          {{ scriptsOn ? t('detect.outputScriptsOff') : t('detect.outputScriptsOn') }}
        </button>
      </div>
      <p class="text-xs text-gray-400">{{ t('detect.outputSandboxHint') }}</p>
    </template>

    <div v-if="output.text">
      <p class="mb-1 text-xs font-medium text-gray-500 dark:text-dark-400">{{ t('detect.outputReply') }}</p>
      <div class="output-reply">{{ output.text }}</div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import DetectArtifactFrame from '@/components/detect/DetectArtifactFrame.vue'
import { artifactHasScript } from '@/utils/detectArtifact'
import type { DetectCheckOutput } from '@/types/detect'

const { t } = useI18n()

const VIEW_MODES = ['preview', 'source'] as const

const props = defineProps<{
  output?: DetectCheckOutput
  title: string
}>()

const view = ref<(typeof VIEW_MODES)[number]>('preview')
/** 作品脚本默认不跑，用户看过风险提示后再手动开启 */
const scriptsOn = ref(false)

const hasScript = computed(() => !!props.output?.document && artifactHasScript(props.output.document))

// 换一道题或换一个目标时回到预览并关掉脚本，免得沿用上一份的状态
watch(
  () => props.output,
  () => {
    view.value = 'preview'
    scriptsOn.value = false
  }
)
</script>

<style scoped>
.output-code {
  @apply overflow-auto rounded-lg bg-gray-50 p-2 font-mono text-xs leading-relaxed text-gray-700;
  @apply dark:bg-dark-800 dark:text-dark-200;
  white-space: pre-wrap;
  word-break: break-all;
}

.output-reply {
  @apply max-h-96 overflow-auto rounded-lg bg-gray-50 p-3 text-sm leading-relaxed text-gray-700;
  @apply dark:bg-dark-800 dark:text-dark-200;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
</style>
