<template>
  <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-6">
    <div v-for="item in items" :key="item.label" class="card px-4 py-3">
      <p class="text-[11px] font-medium uppercase tracking-wider text-gray-400">{{ item.label }}</p>
      <p class="mt-1 text-2xl font-semibold tabular-nums" :style="item.color ? { color: item.color } : undefined">
        {{ item.value }}
      </p>
      <p v-if="item.hint" class="mt-0.5 text-[11px] text-gray-400">{{ item.hint }}</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { fmtMs, fmtNum, fmtPct } from '@/utils/format'
import { formatTokenRate, formatRpm, type PageKpis } from '@/utils/stabilityMetrics'
import { cellColor, hslForPct } from '@/utils/stabilityTimeline'

const props = defineProps<{
  kpis: PageKpis
}>()

const { t } = useI18n()

const items = computed(() => {
  const k = props.kpis
  const err = k.sla == null ? null : 100 - k.sla
  return [
    {
      label: t('stability.successRate'),
      value: fmtPct(k.sla, 1),
      hint: err == null ? '' : t('stability.errorRateHint', { n: fmtPct(err, 1) }),
      color: cellColor(k.sla)
    },
    {
      label: t('stability.requests'),
      value: fmtNum(k.requests),
      hint: t('stability.windowRequests'),
      color: undefined as string | undefined
    },
    {
      label: t('stability.ftP50'),
      value: fmtMs(k.firstTokenP50),
      hint: t('stability.p50Hint'),
      color: undefined as string | undefined
    },
    {
      label: t('stability.tokenPerSec'),
      value: formatTokenRate(k.tokensPerSecond),
      hint: t('stability.tpsHint'),
      color: undefined
    },
    {
      label: t('stability.cacheRate'),
      value: fmtPct(k.cacheRate, 1),
      hint: t('stability.cacheHint'),
      color: hslForPct(k.cacheRate)
    },
    {
      label: t('stability.rpm'),
      value: formatRpm(k.rpm),
      hint: t('stability.rpmHint'),
      color: undefined
    }
  ]
})
</script>
