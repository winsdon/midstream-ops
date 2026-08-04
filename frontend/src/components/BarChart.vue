<template>
  <div class="relative" :style="{ height: height + 'px' }">
    <canvas ref="canvasEl"></canvas>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount, watch } from 'vue'
import { storeToRefs } from 'pinia'
import {
  Chart,
  BarController,
  BarElement,
  LinearScale,
  CategoryScale,
  Tooltip,
  Legend,
  type ChartConfiguration
} from 'chart.js'
import { useAppStore } from '@/stores/app'
import { chartColors } from '@/utils/chartTheme'

// Legend 必须注册：多系列图缺了它图例会静默消失，无法分辨哪根柱是哪个指标
Chart.register(BarController, BarElement, LinearScale, CategoryScale, Tooltip, Legend)

/** 一条柱系列 */
export interface BarSeries {
  label: string
  data: number[]
  color: string
}

const props = withDefaults(
  defineProps<{
    labels: string[]
    /** 单系列语法糖；与 datasets 二选一，datasets 优先 */
    data?: number[]
    /** 多系列并排柱 */
    datasets?: BarSeries[]
    /** 横向条形（分组贡献榜用）；false 则为竖直柱状 */
    horizontal?: boolean
    color?: string
    height?: number
    /** tooltip 与数值轴刻度的格式化，缺省直接 toString */
    valueFormatter?: (v: number) => string
  }>(),
  { horizontal: true, color: '#14b8a6', height: 240 }
)

const canvasEl = ref<HTMLCanvasElement | null>(null)
let chart: Chart | null = null

const { isDark } = storeToRefs(useAppStore())

// 把单系列的 data/color 归一化成 datasets，下游只需面对一种形状
const series = computed<BarSeries[]>(() =>
  props.datasets?.length ? props.datasets : [{ label: '', data: props.data || [], color: props.color }]
)

function buildConfig(): ChartConfiguration {
  const { grid, tick } = chartColors(isDark.value)
  const fmt = props.valueFormatter || ((v: number) => String(v))
  // 分类轴放标签、数值轴放数字；横竖切换时两者对调
  const catAxis = props.horizontal ? 'y' : 'x'
  const valAxis = props.horizontal ? 'x' : 'y'
  const multi = series.value.length > 1

  return {
    type: 'bar',
    data: {
      labels: props.labels,
      datasets: series.value.map((s) => ({
        label: s.label,
        data: s.data,
        backgroundColor: s.color,
        borderRadius: 4,
        barPercentage: 0.9,
        categoryPercentage: 0.8
      }))
    },
    options: {
      indexAxis: catAxis,
      responsive: true,
      maintainAspectRatio: false,
      plugins: {
        legend: {
          display: multi,
          position: 'top',
          labels: { color: tick, boxWidth: 12, boxHeight: 12, usePointStyle: true, pointStyle: 'rectRounded' }
        },
        tooltip: {
          // Chart.js 走 canvas 渲染，标签不会被当 HTML 解析，无 XSS 风险；
          // 这里只做数值格式化
          callbacks: {
            label: (ctx) => {
              const v = fmt(ctx.parsed[valAxis] as number)
              return ctx.dataset.label ? `${ctx.dataset.label}: ${v}` : v
            }
          }
        }
      },
      scales: {
        [catAxis]: {
          grid: { color: 'transparent' },
          ticks: {
            color: tick,
            // 分组名可能很长，截断避免挤压图形区
            callback(value) {
              const raw = this.getLabelForValue(value as number)
              return raw.length > 14 ? raw.slice(0, 13) + '…' : raw
            }
          }
        },
        [valAxis]: {
          grid: { color: grid },
          ticks: { color: tick, callback: (v) => fmt(Number(v)) }
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
watch(() => [props.labels, props.data, props.datasets], render, { deep: true })
// 主题切换需重绘：网格线与刻度颜色写死在 config 里，Chart.js 不会自动更新
watch(isDark, render)
</script>
