<template>
  <div class="grid items-center gap-x-3 px-3 py-2.5" :style="HEATMAP_GRID">
    <button
      type="button"
      class="flex min-w-0 items-center gap-2 text-left"
      @click="emit('open-row')"
    >
      <span class="h-2 w-2 shrink-0 rounded-full" :class="TONE_DOT[tone]" />
      <span class="truncate text-sm font-medium text-gray-900 dark:text-white" :title="model.title">
        {{ model.title }}
      </span>
    </button>
    <span class="text-right font-mono text-sm tabular-nums text-gray-800 dark:text-dark-200">
      {{ fmtPct(model.sla, 1) }}
    </span>
    <span class="text-right font-mono text-sm tabular-nums text-gray-800 dark:text-dark-200">
      {{ fmtNum(model.requests) }}
    </span>
    <span class="text-right font-mono text-sm tabular-nums text-gray-800 dark:text-dark-200">
      {{ fmtMs(model.firstTokenP50) }}
    </span>
    <span class="text-right font-mono text-sm tabular-nums text-gray-800 dark:text-dark-200">
      {{ formatTokenRate(model.tokensPerSecond) }}
    </span>
    <span class="text-right font-mono text-sm tabular-nums text-gray-800 dark:text-dark-200">
      {{ fmtPct(model.cacheRate, 1) }}
    </span>
    <StabilityTimeline
      :cells="cells"
      :minutes="minutes"
      :highlight="highlight"
      @select="emit('open-cell', $event)"
    />
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { fmtMs, fmtNum, fmtPct } from '@/utils/format'
import { formatTokenRate, type StatusCardModel } from '@/utils/stabilityMetrics'
import {
  HEATMAP_GRID,
  TONE_DOT,
  displayCells,
  liveToneFromCells
} from '@/utils/stabilityTimeline'
import StabilityTimeline from './StabilityTimeline.vue'

const props = defineProps<{
  model: StatusCardModel
  minutes: number
  generatedAt: string
  highlight?: number | null
}>()

const emit = defineEmits<{
  'open-row': []
  'open-cell': [index: number]
}>()

const cells = computed(() => displayCells(props.model.timeline, props.minutes, props.generatedAt))
const tone = computed(() => liveToneFromCells(cells.value))
</script>
