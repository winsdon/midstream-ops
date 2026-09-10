<template>
  <BaseDialog
    :show="show"
    :title="check ? `${targetName} · ${check.title}` : t('detect.detailTitle')"
    width="extra-wide"
    @close="emit('close')"
  >
    <div v-if="check" class="space-y-5">
      <!-- 结论 -->
      <div class="flex flex-wrap items-center gap-2">
        <Badge :variant="statusVariant(check.status)">{{ t(`detect.status.${check.status}`) }}</Badge>
        <span class="text-sm text-gray-700 dark:text-dark-200">{{ check.summary }}</span>
        <span v-if="check.status !== 'running'" class="ml-auto text-xs text-gray-400">{{ (check.duration_ms / 1000).toFixed(2) }}s</span>
      </div>
      <p v-if="check.auth_cap_reason" class="rounded-xl bg-red-50 px-3 py-2 text-xs text-red-600 dark:bg-red-900/20 dark:text-red-400">
        {{ check.auth_cap_reason }}
      </p>

      <!-- 断言 -->
      <section v-if="check.assertions?.length" class="space-y-2">
        <h4 class="text-xs font-semibold uppercase tracking-wider text-gray-400">{{ t('detect.assertions') }}</h4>
        <div
          v-for="(a, i) in check.assertions"
          :key="i"
          class="flex items-start gap-2 rounded-xl border border-gray-100 p-2.5 dark:border-dark-800"
        >
          <span
            class="mt-0.5 flex h-4 w-4 flex-shrink-0 items-center justify-center rounded-full text-xs font-bold"
            :class="
              a.ok
                ? 'bg-emerald-100 text-emerald-600 dark:bg-emerald-900/40 dark:text-emerald-400'
                : 'bg-red-100 text-red-600 dark:bg-red-900/40 dark:text-red-400'
            "
          >
            {{ a.ok ? '✓' : '×' }}
          </span>
          <div class="min-w-0 flex-1">
            <p class="text-sm text-gray-900 dark:text-white">
              {{ a.label }}
              <span v-if="a.diagnostic" class="ml-1 text-xs text-gray-400">({{ t('detect.diagnostic') }})</span>
            </p>
            <p v-if="a.detail" class="mt-0.5 break-words text-xs text-gray-500 dark:text-dark-400">{{ a.detail }}</p>
          </div>
        </div>
      </section>

      <!-- 证据 -->
      <section v-if="check.evidence?.length" class="space-y-2">
        <h4 class="text-xs font-semibold uppercase tracking-wider text-gray-400">{{ t('detect.evidence') }}</h4>
        <div
          v-for="(ev, i) in check.evidence"
          :key="i"
          class="flex items-start justify-between gap-3 rounded-xl border border-gray-100 p-2.5 dark:border-dark-800"
        >
          <div class="min-w-0">
            <p class="text-sm text-gray-900 dark:text-white">{{ ev.label }}</p>
            <p v-if="ev.detail" class="mt-0.5 break-words text-xs text-gray-500 dark:text-dark-400">{{ ev.detail }}</p>
          </div>
          <div class="flex flex-shrink-0 items-center gap-2">
            <Badge :variant="labelVariant(ev.class)">{{ t(`detect.labels.${ev.class}`) }}</Badge>
            <span v-if="ev.weight > 0" class="text-xs tabular-nums text-gray-500">+{{ ev.weight }}</span>
          </div>
        </div>
      </section>

      <!-- 请求记录 -->
      <section v-if="check.exchanges?.length" class="space-y-2">
        <h4 class="text-xs font-semibold uppercase tracking-wider text-gray-400">{{ t('detect.exchanges') }}</h4>
        <details
          v-for="(ex, i) in check.exchanges"
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
    </div>
    <template v-if="canRetry" #footer>
      <button type="button" class="btn btn-secondary btn-md" @click="emit('close')">
        {{ t('common.close') }}
      </button>
      <button type="button" class="btn btn-primary btn-md" :title="t('detect.retryHint')" @click="emit('retry')">
        <Icon name="refresh" size="sm" />
        {{ t('detect.retry') }}
      </button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Badge from '@/components/common/Badge.vue'
import Icon from '@/components/icons/Icon.vue'
import { isRequestFailed, labelVariant, statusVariant } from '@/utils/detectModel'
import type { DetectCheckResult } from '@/types/detect'

const { t } = useI18n()

const props = defineProps<{
  show: boolean
  check: DetectCheckResult | null
  targetName: string
  allowRetry?: boolean
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'retry'): void
}>()

const canRetry = computed(() => !!props.allowRetry && isRequestFailed(props.check ?? undefined))

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
