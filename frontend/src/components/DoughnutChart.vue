<template>
  <div class="relative" :style="{ height: height + 'px' }">
    <canvas ref="canvasEl"></canvas>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount, watch } from 'vue'
import { storeToRefs } from 'pinia'
import {
  Chart,
  DoughnutController,
  ArcElement,
  Tooltip,
  Legend,
  type ChartConfiguration
} from 'chart.js'
import { useAppStore } from '@/stores/app'
import { chartColors, CATEGORY } from '@/utils/chartTheme'

Chart.register(DoughnutController, ArcElement, Tooltip, Legend)

const props = withDefaults(
  defineProps<{
    labels: string[]
    data: number[]
    colors?: string[]
    height?: number
    /** tooltip 数值格式化，缺省直接 toString */
    valueFormatter?: (v: number) => string
  }>(),
  { height: 280 }
)

const canvasEl = ref<HTMLCanvasElement | null>(null)
let chart: Chart<'doughnut'> | null = null

const { isDark } = storeToRefs(useAppStore())

function colors(): string[] {
  if (props.colors?.length) return props.colors
  return props.labels.map((_, i) => CATEGORY[i % CATEGORY.length])
}

function buildConfig(): ChartConfiguration<'doughnut'> {
  const { tick } = chartColors(isDark.value)
  const fmt = props.valueFormatter || ((v: number) => String(v))
  const gap = isDark.value ? '#1e293b' : '#ffffff'

  return {
    type: 'doughnut',
    data: {
      labels: props.labels,
      datasets: [
        {
          data: props.data,
          backgroundColor: colors(),
          borderColor: gap,
          borderWidth: 2,
          hoverOffset: 4
        }
      ]
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      cutout: '58%',
      plugins: {
        legend: {
          position: 'bottom',
          labels: {
            color: tick,
            boxWidth: 10,
            boxHeight: 10,
            padding: 12,
            usePointStyle: true,
            pointStyle: 'circle'
          }
        },
        tooltip: {
          callbacks: {
            label: (ctx) => {
              const raw = Number(ctx.parsed)
              const total = (ctx.dataset.data as number[]).reduce((sum, n) => sum + Number(n), 0)
              const pct = total > 0 ? ((raw / total) * 100).toFixed(1) : '0.0'
              return `${fmt(raw)} (${pct}%)`
            }
          }
        }
      }
    }
  }
}

function render() {
  if (!canvasEl.value) return
  chart?.destroy()
  chart = new Chart(canvasEl.value, buildConfig())
}

onMounted(render)
onBeforeUnmount(() => chart?.destroy())
watch(() => [props.labels, props.data, props.colors], render, { deep: true })
watch(isDark, render)
</script>
