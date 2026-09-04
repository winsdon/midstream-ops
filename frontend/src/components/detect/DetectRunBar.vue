<template>
  <div class="card">
    <div class="card-body flex flex-wrap items-center justify-between gap-4">
      <div class="flex min-w-0 flex-1 items-center gap-4">
        <div class="relative h-12 w-12 flex-shrink-0">
          <svg class="h-12 w-12 -rotate-90" viewBox="0 0 36 36">
            <circle
              cx="18"
              cy="18"
              r="15.9"
              fill="none"
              stroke-width="3"
              class="stroke-gray-200 dark:stroke-dark-700"
            />
            <circle
              cx="18"
              cy="18"
              r="15.9"
              fill="none"
              stroke-width="3"
              stroke-linecap="round"
              class="stroke-primary-500 transition-all duration-300"
              :stroke-dasharray="`${percent}, 100`"
            />
          </svg>
          <span
            class="absolute inset-0 flex items-center justify-center text-xs font-semibold text-gray-700 dark:text-dark-200"
          >
            {{ percent }}%
          </span>
        </div>
        <div class="min-w-0">
          <p class="text-sm font-semibold text-gray-900 dark:text-white">{{ headline }}</p>
          <p class="mt-0.5 truncate text-xs text-gray-500 dark:text-dark-400">{{ subline }}</p>
        </div>
      </div>

      <div class="flex flex-wrap items-center gap-2">
        <label class="flex items-center gap-2 text-xs text-gray-500 dark:text-dark-400" :title="t('detect.concurrencyHint')">
          {{ t('detect.concurrency') }}
          <input
            :value="concurrency"
            type="number"
            min="1"
            max="10"
            class="input !w-16 !py-1 text-center"
            :disabled="running"
            @change="onConcurrency"
          />
        </label>
        <button v-if="running" type="button" class="btn btn-secondary btn-md" @click="emit('cancel')">
          {{ t('detect.cancel') }}
        </button>
        <button
          type="button"
          class="btn btn-primary btn-md"
          :disabled="running || disabled"
          @click="emit('start')"
        >
          <Icon name="play" size="sm" />
          {{ t('detect.start') }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { DEFAULT_DETECT_CONCURRENCY, MAX_DETECT_CONCURRENCY, progressPercent } from '@/utils/detectModel'
import type { DetectJob } from '@/types/detect'

const { t } = useI18n()

const props = defineProps<{
  job: DetectJob | null
  running: boolean
  disabled: boolean
  requestEstimate: number
  concurrency: number
}>()

const emit = defineEmits<{
  (e: 'start'): void
  (e: 'cancel'): void
  (e: 'update:concurrency', n: number): void
}>()

function onConcurrency(e: Event): void {
  const raw = Number((e.target as HTMLInputElement).value)
  let n = Number.isFinite(raw) ? Math.round(raw) : DEFAULT_DETECT_CONCURRENCY
  if (n < 1) n = 1
  if (n > MAX_DETECT_CONCURRENCY) n = MAX_DETECT_CONCURRENCY
  emit('update:concurrency', n)
}

const percent = computed(() => progressPercent(props.job?.done ?? 0, props.job?.total ?? 0))

const headline = computed(() => {
  if (props.running) return t('detect.progress', { done: props.job?.done ?? 0, total: props.job?.total ?? 0 })
  if (props.job?.status === 'cancelled') return t('detect.cancelled')
  if (props.job?.status === 'completed') return t('detect.completed')
  return t('detect.idle')
})

const subline = computed(() => {
  if (props.running && props.job?.current) return props.job.current
  return t('detect.requestsEstimate', { n: props.requestEstimate })
})
</script>
