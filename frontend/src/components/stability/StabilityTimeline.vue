<template>
  <div class="flex h-5 w-full items-stretch gap-[3px]">
    <button
      v-for="(cell, idx) in cells"
      :key="idx"
      type="button"
      class="min-w-0 flex-1 rounded-[3px] transition-shadow"
      :class="highlight === idx ? 'ring-2 ring-gray-800 ring-offset-1 dark:ring-white' : 'hover:ring-1 hover:ring-gray-400'"
      :style="{ backgroundColor: cell.color }"
      @mouseenter="onEnter(idx, $event)"
      @mousemove="onMove($event)"
      @mouseleave="onLeave"
      @click.stop="emit('select', idx)"
    />
    <Teleport to="body">
      <div
        v-if="tip"
        class="pointer-events-none fixed z-[80] w-64 rounded-xl border border-gray-100 bg-white p-3 text-sm shadow-lg dark:border-dark-700 dark:bg-dark-800"
        :style="{ left: tipX + 'px', top: tipY + 'px' }"
      >
        <p class="text-xs text-gray-400">{{ tip.timeRange }}</p>
        <template v-if="tip.empty">
          <p class="mt-2 text-gray-500">{{ t('stability.live.empty') }}</p>
        </template>
        <div v-else class="mt-1.5 space-y-0.5 text-[13px] leading-6 text-gray-800 dark:text-dark-200">
          <p>
            {{ t('stability.healthScore') }}
            <span class="tabular-nums" :class="healthScoreClass(tip.health)">{{ tip.health }}</span>
          </p>
          <p>{{ t('stability.successRate') }} <span class="tabular-nums">{{ fmtPct(tip.sla, 1) }}</span></p>
          <p>{{ t('stability.firstToken') }} <span class="tabular-nums">{{ tip.firstToken }}</span></p>
          <p>{{ t('stability.tokenPerSec') }} <span class="tabular-nums">{{ formatTokenRate(tip.tps) }}</span></p>
          <p>{{ t('stability.cacheRate') }} <span class="tabular-nums">{{ fmtPct(tip.cache, 1) }}</span></p>
          <p>{{ t('stability.errorRate') }} <span class="tabular-nums">{{ tip.errorRate == null ? '-' : fmtPct(tip.errorRate, 2) }}</span></p>
          <p>{{ t('stability.rpm') }} <span class="tabular-nums">{{ formatRpm(tip.rpm) }}</span></p>
          <p>{{ t('stability.requestDuration') }} <span class="tabular-nums">{{ tip.duration }}</span></p>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { fmtPct } from '@/utils/format'
import {
  cellTip,
  formatLatencyTriple,
  formatRpm,
  formatTokenRate,
  healthScoreClass
} from '@/utils/stabilityMetrics'
import { displayCellCount, type TimelineBar } from '@/utils/stabilityTimeline'

const props = defineProps<{
  cells: TimelineBar[]
  minutes: number
  highlight?: number | null
}>()

const emit = defineEmits<{ select: [index: number] }>()
const { t } = useI18n()

const hoverIdx = ref<number | null>(null)
const tipX = ref(0)
const tipY = ref(0)

const cellMinutes = computed(() => props.minutes / displayCellCount(props.minutes))

function formatClockRange(startIso: string, endIso: string): string {
  const s = new Date(startIso)
  const e = new Date(endIso)
  if (Number.isNaN(s.getTime()) || Number.isNaN(e.getTime())) return ''
  const pad = (n: number) => String(n).padStart(2, '0')
  const md = (d: Date) => `${pad(d.getMonth() + 1)}/${pad(d.getDate())}`
  const hm = (d: Date) => `${pad(d.getHours())}:${pad(d.getMinutes())}`
  if (md(s) === md(e)) return `${md(s)} ${hm(s)} - ${hm(e)}`
  return `${md(s)} ${hm(s)} - ${md(e)} ${hm(e)}`
}

const tip = computed(() => {
  if (hoverIdx.value == null) return null
  const bar = props.cells[hoverIdx.value]
  if (!bar) return null
  const raw = cellTip(bar, { windowMinutes: props.minutes, cellMinutes: cellMinutes.value })
  return {
    ...raw,
    timeRange: formatClockRange(raw.start, raw.end),
    firstToken: formatLatencyTriple({
      avg: raw.firstTokenAvg,
      p50: raw.firstTokenP50,
      p90: raw.firstTokenP90
    }),
    duration: formatLatencyTriple({
      avg: raw.durationAvg,
      p50: raw.durationP50,
      p90: raw.durationP90
    })
  }
})

function place(ev: MouseEvent) {
  const w = 256
  const h = 280
  let x = ev.clientX + 12
  let y = ev.clientY + 12
  if (x + w > window.innerWidth - 8) x = ev.clientX - w - 12
  if (y + h > window.innerHeight - 8) y = ev.clientY - h - 12
  tipX.value = Math.max(8, x)
  tipY.value = Math.max(8, y)
}

function onEnter(idx: number, ev: MouseEvent) {
  hoverIdx.value = idx
  place(ev)
}

function onMove(ev: MouseEvent) {
  if (hoverIdx.value == null) return
  place(ev)
}

function onLeave() {
  hoverIdx.value = null
}
</script>
