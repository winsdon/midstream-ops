<template>
  <button
    type="button"
    class="card card-hover w-full p-2.5 text-left"
    @click="emit('click')"
  >
    <div class="flex items-start justify-between gap-2">
      <div class="min-w-0">
        <p class="truncate text-base font-semibold leading-5 text-gray-900 dark:text-white" :title="row.account_name">
          {{ row.account_name }}
        </p>
        <p v-if="sub" class="truncate text-xs leading-4 text-gray-400 dark:text-dark-500">{{ sub }}</p>
      </div>
      <span class="shrink-0 rounded bg-gray-100 px-1.5 py-0.5 text-xs leading-4 dark:bg-dark-800">{{ row.platform }}</span>
    </div>

    <dl class="mt-1.5 space-y-0.5 text-sm leading-5">
      <div v-for="line in lines" :key="line.label" class="flex items-baseline gap-1.5">
        <dt class="shrink-0 text-gray-500 dark:text-dark-400">{{ line.label }}</dt>
        <dd class="min-w-0 font-mono tabular-nums" :class="line.klass">{{ line.value }}</dd>
      </div>
    </dl>
  </button>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { fmtNum, fmtPct } from '@/utils/format'
import { latencyBand, type LatencyBand } from '@/utils/latencyBand'
import { passiveRateClass } from '@/utils/stabilityModel'
import {
  accountHealthScore,
  errorRatePercent,
  formatLatencyTriple,
  formatRpm,
  formatTokenRate,
  healthScoreClass,
  rpm
} from '@/utils/stabilityMetrics'
import type { PassiveRow } from '@/types'

const props = defineProps<{
  row: PassiveRow
  minutes: number
  secondary?: 'groups' | 'provider'
}>()

const emit = defineEmits<{ click: [] }>()
const { t } = useI18n()

const TONE: Record<LatencyBand, string> = {
  fast: 'text-emerald-600 dark:text-emerald-400',
  ok: 'text-gray-800 dark:text-dark-200',
  slow: 'text-amber-600 dark:text-amber-400',
  bad: 'text-red-600 dark:text-red-400',
  unknown: 'text-gray-400 dark:text-dark-500'
}

const sub = computed(() => {
  if (props.secondary === 'provider') return props.row.provider_name || ''
  return (props.row.groups ?? []).join(' · ')
})

const errPct = computed(() => errorRatePercent(props.row.success_count, props.row.error_count))
const score = computed(() =>
  accountHealthScore({
    sla: props.row.sla,
    ttftP50: props.row.first_token_p50,
    errorRate: errPct.value
  })
)

const lines = computed(() => {
  const r = props.row
  const slaTone = passiveRateClass(r.sla)
  return [
    { label: t('stability.healthScore'), value: String(score.value), klass: healthScoreClass(score.value) },
    { label: t('stability.successRate'), value: fmtPct(r.sla), klass: slaTone },
    {
      label: t('stability.firstToken'),
      value: formatLatencyTriple({ avg: r.first_token_avg, p50: r.first_token_p50, p90: r.first_token_p90 }),
      klass: TONE[latencyBand(r.first_token_p50, 'ttft')]
    },
    { label: t('stability.tokenPerSec'), value: formatTokenRate(r.tokens_per_second), klass: 'text-gray-800 dark:text-dark-200' },
    { label: t('stability.cacheRate'), value: fmtPct(r.cache_rate), klass: 'text-gray-800 dark:text-dark-200' },
    {
      label: t('stability.errorRate'),
      value: errPct.value == null ? '-' : fmtPct(errPct.value, 2),
      klass: slaTone
    },
    { label: t('stability.requests'), value: fmtNum(r.requests), klass: 'text-gray-800 dark:text-dark-200' },
    { label: t('stability.rpm'), value: formatRpm(rpm(r.requests, props.minutes)), klass: 'text-gray-800 dark:text-dark-200' },
    {
      label: t('stability.requestDuration'),
      value: formatLatencyTriple({ avg: r.duration_avg, p50: r.duration_p50, p90: r.duration_p90 }),
      klass: TONE[latencyBand(r.duration_p50, 'total')]
    }
  ]
})
</script>
