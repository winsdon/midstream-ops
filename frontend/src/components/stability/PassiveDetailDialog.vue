<template>
  <BaseDialog :show="show && !!detail" :title="detail?.title || ''" width="narrow" @close="emit('close')">
    <div v-if="detail" class="space-y-3 text-sm">
      <p class="text-xs text-gray-500 dark:text-dark-400">
        <template v-if="detail.accountCount != null">
          {{ t('stability.sectionAccounts', { n: detail.accountCount }) }} ·
        </template>
        {{ t('common.platform') }} {{ detail.platform || '-' }}
        · {{ t('stability.provider') }} {{ detail.provider_name || t('stability.unassigned') }}
        · {{ t('stability.groups') }} {{ (detail.groups ?? []).join(' · ') || t('stability.ungrouped') }}
      </p>
      <p class="text-xs text-gray-400 dark:text-dark-500">
        {{ t('stability.requests') }} {{ fmtNum(detail.requests) }}
        · {{ t('stability.successCount') }} {{ fmtNum(detail.success_count) }}
        · {{ t('stability.errorCount') }} {{ fmtNum(detail.error_count) }}
      </p>
      <dl class="space-y-0.5 font-mono text-xs leading-5">
        <div v-for="line in lines" :key="line.label" class="flex items-baseline gap-1.5">
          <dt class="shrink-0 font-sans text-gray-500">{{ line.label }}</dt>
          <dd class="min-w-0 whitespace-pre-wrap" :class="line.klass">{{ line.value }}</dd>
        </div>
      </dl>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { fmtMs, fmtNum, fmtPct } from '@/utils/format'
import { latencyBand, type LatencyBand } from '@/utils/latencyBand'
import { passiveRateClass } from '@/utils/stabilityModel'
import {
  accountHealthScore,
  errorRatePercent,
  healthScoreClass,
  formatRpm,
  formatTokenRate,
  rpm,
  type PassiveDetail
} from '@/utils/stabilityMetrics'
import BaseDialog from '@/components/common/BaseDialog.vue'

const props = defineProps<{
  show: boolean
  detail: PassiveDetail | null
  minutes: number
}>()

const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()

const TONE: Record<LatencyBand, string> = {
  fast: 'text-emerald-600 dark:text-emerald-400',
  ok: 'text-gray-800 dark:text-dark-200',
  slow: 'text-amber-600 dark:text-amber-400',
  bad: 'text-red-600 dark:text-red-400',
  unknown: 'text-gray-400 dark:text-dark-500'
}

const errPct = computed(() =>
  props.detail ? errorRatePercent(props.detail.success_count, props.detail.error_count) : null
)
const score = computed(() =>
  props.detail
    ? accountHealthScore({
        sla: props.detail.sla,
        ttftP50: props.detail.first_token_p50,
        errorRate: errPct.value
      })
    : 0
)

function triple(avg?: number | null, p50?: number | null, p90?: number | null): string {
  return `AVG ${fmtMs(avg)} · P50 ${fmtMs(p50)} · P90 ${fmtMs(p90)}`
}

const lines = computed(() => {
  const r = props.detail
  if (!r) return []
  const slaTone = passiveRateClass(r.sla)
  return [
    { label: t('stability.healthScore'), value: String(score.value), klass: healthScoreClass(score.value) },
    { label: t('stability.successRate'), value: fmtPct(r.sla), klass: slaTone },
    {
      label: t('stability.firstToken'),
      value: triple(r.first_token_avg, r.first_token_p50, r.first_token_p90),
      klass: TONE[latencyBand(r.first_token_p50, 'ttft')]
    },
    { label: t('stability.tokenPerSec'), value: formatTokenRate(r.tokens_per_second), klass: 'text-gray-800 dark:text-dark-200' },
    { label: t('stability.cacheRate'), value: fmtPct(r.cache_rate), klass: 'text-gray-800 dark:text-dark-200' },
    {
      label: t('stability.errorRate'),
      value: errPct.value == null ? '-' : fmtPct(errPct.value, 2),
      klass: slaTone
    },
    { label: t('stability.rpm'), value: formatRpm(rpm(r.requests, props.minutes)), klass: 'text-gray-800 dark:text-dark-200' },
    {
      label: t('stability.requestDuration'),
      value: triple(r.duration_avg, r.duration_p50, r.duration_p90),
      klass: TONE[latencyBand(r.duration_p50, 'total')]
    }
  ]
})
</script>
