<template>
  <div class="card">
    <div class="card-header">
      <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('detect.iqPickerTitle') }}</h2>
      <p class="mt-1 text-xs leading-relaxed text-gray-500 dark:text-dark-400">{{ t('detect.iqPickerHint') }}</p>
    </div>

    <div class="card-body space-y-3">
      <LoadingState v-if="loading" :label="t('common.loading')" size="sm" />
      <template v-else>
        <div
          v-for="check in checks"
          :key="check.id"
          class="rounded-xl border p-3 transition-colors"
          :class="
            selectedSet.has(check.id)
              ? 'border-primary-300 bg-primary-50/60 dark:border-primary-700 dark:bg-primary-900/20'
              : 'border-gray-200 hover:border-gray-300 dark:border-dark-700 dark:hover:border-dark-600'
          "
        >
          <label class="flex cursor-pointer items-start gap-2.5">
            <input
              type="checkbox"
              class="mt-0.5 h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
              :checked="selectedSet.has(check.id)"
              @change="toggle(check.id)"
            />
            <span class="min-w-0 flex-1">
              <span class="flex flex-wrap items-center gap-1.5">
                <span class="text-sm font-medium text-gray-900 dark:text-white">{{ check.title }}</span>
                <Badge :variant="costVariant(check.cost)">{{ t(costLabelKey(check.cost)) }}</Badge>
                <span class="text-xs text-gray-400">×{{ check.requests }}</span>
              </span>
              <span v-if="check.note" class="mt-0.5 block text-xs leading-relaxed text-gray-500 dark:text-dark-400">
                {{ check.note }}
              </span>
            </span>
          </label>
          <!-- 题目放在 label 外面：展开题目不该顺带勾掉 / 勾上这道题 -->
          <details v-if="check.prompt" class="mt-2 pl-6">
            <summary class="cursor-pointer text-xs text-primary-600 dark:text-primary-400">
              {{ t('detect.iqShowPrompt') }}
            </summary>
            <pre
              class="mt-1.5 whitespace-pre-wrap break-words rounded-lg bg-gray-50 p-2 font-sans text-xs leading-relaxed text-gray-700 dark:bg-dark-800 dark:text-dark-200"
            >{{ check.prompt }}</pre>
          </details>
          <div v-if="check.id === 'iq-pelican'" class="mt-3 pl-6">
            <label class="block text-xs font-medium text-gray-700 dark:text-dark-200" :for="`pelican-prompt-${check.id}`">
              {{ t('detect.iqCustomPrompt') }}
            </label>
            <textarea
              :id="`pelican-prompt-${check.id}`"
              :value="pelicanPrompt"
              :placeholder="check.prompt"
              maxlength="20000"
              rows="4"
              class="input mt-1.5 min-h-24 w-full resize-y text-xs leading-relaxed"
              @input="onPelicanPromptInput"
            />
            <p class="mt-1 text-xs leading-relaxed text-gray-400 dark:text-dark-500">
              {{ t('detect.iqCustomPromptHint') }}
            </p>
          </div>
        </div>

        <p class="text-xs text-gray-500 dark:text-dark-400">
          {{ t('detect.requestsEstimate', { n: requestEstimate }) }}
        </p>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Badge from '@/components/common/Badge.vue'
import LoadingState from '@/components/common/LoadingState.vue'
import { costLabelKey, costVariant, estimateRequests } from '@/utils/detectModel'
import type { DetectCheckMeta } from '@/types/detect'

const { t } = useI18n()

const props = defineProps<{
  /** 只含智商题 */
  checks: DetectCheckMeta[]
  selected: string[]
  loading: boolean
  targetCount: number
  pelicanPrompt: string
}>()

const emit = defineEmits<{
  (e: 'update:selected', ids: string[]): void
  (e: 'update:pelicanPrompt', prompt: string): void
}>()

const selectedSet = computed(() => new Set(props.selected))
const requestEstimate = computed(() => estimateRequests(props.checks, selectedSet.value, props.targetCount))

function toggle(id: string): void {
  const next = new Set(selectedSet.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  // 按目录顺序回传，保持与后端执行顺序一致
  emit(
    'update:selected',
    props.checks.filter((c) => next.has(c.id)).map((c) => c.id)
  )
}

function onPelicanPromptInput(e: Event): void {
  emit('update:pelicanPrompt', (e.target as HTMLTextAreaElement).value)
}
</script>
