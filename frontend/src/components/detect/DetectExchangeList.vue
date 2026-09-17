<template>
  <section v-if="exchanges?.length" class="space-y-2">
    <h4 class="text-xs font-semibold uppercase tracking-wider text-gray-400">{{ t('detect.exchanges') }}</h4>
    <details
      v-for="(ex, i) in exchanges"
      :key="i"
      class="rounded-xl border border-gray-100 dark:border-dark-800"
      :open="i === 0"
    >
      <summary class="cursor-pointer px-3 py-2 text-xs text-gray-600 dark:text-dark-300">
        {{ i + 1 }}. {{ ex.method }} {{ ex.url }} ·
        <span :class="ex.status >= 200 && ex.status < 300 ? 'text-emerald-600' : 'text-red-500'">
          {{ ex.status ? `HTTP ${ex.status}` : t('detect.networkError') }}
        </span>
        · {{ ex.duration_ms }}ms
      </summary>
      <div class="space-y-3 px-3 pb-3">
        <p v-if="ex.network_error" class="text-xs text-red-500">{{ ex.network_error }}</p>
        <div>
          <p class="mb-1 text-xs font-medium text-gray-500 dark:text-dark-400">{{ t('detect.requestHeaders') }}</p>
          <pre class="detect-pre">{{ pretty(ex.request_headers) }}</pre>
        </div>
        <div v-if="ex.request_body">
          <p class="mb-1 text-xs font-medium text-gray-500 dark:text-dark-400">{{ t('detect.requestBody') }}</p>
          <pre class="detect-pre">{{ pretty(ex.request_body) }}</pre>
        </div>
        <div>
          <p class="mb-1 text-xs font-medium text-gray-500 dark:text-dark-400">{{ t('detect.responseHeaders') }}</p>
          <pre class="detect-pre">{{ pretty(ex.headers) }}</pre>
        </div>
        <div>
          <p class="mb-1 text-xs font-medium text-gray-500 dark:text-dark-400">{{ t('detect.responseRaw') }}</p>
          <pre class="detect-pre">{{ ex.raw || '—' }}</pre>
        </div>
      </div>
    </details>
  </section>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { DetectExchange } from '@/types/detect'

defineProps<{
  exchanges?: DetectExchange[]
}>()

const { t } = useI18n()

function pretty(value: unknown): string {
  if (typeof value === 'string') return value
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return String(value)
  }
}
</script>

<style scoped>
.detect-pre {
  @apply max-h-64 overflow-auto rounded-lg bg-gray-50 p-2 font-mono text-xs leading-relaxed text-gray-700;
  @apply dark:bg-dark-800 dark:text-dark-200;
  white-space: pre-wrap;
  word-break: break-all;
}
</style>
