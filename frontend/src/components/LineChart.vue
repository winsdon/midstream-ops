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
  LineController,
  LineElement,
  PointElement,
  LinearScale,
  CategoryScale,
  Filler,
  Tooltip,
  Legend,
  type ChartConfiguration
} from 'chart.js'
import { useAppStore } from '@/stores/app'
import { chartColors } from '@/utils/chartTheme'

Chart.register(LineController, LineElement, PointElement, LinearScale, CategoryScale, Filler, Tooltip, Legend)

const props = withDefaults(
  defineProps<{
    labels: string[]
    // borderDash 用于把「对照/参考」曲线画成虚线，与实际数据区分
    // yAxisID='y1' 会把该曲线挂到右侧独立坐标轴，供量级差异大的对照数据使用，
    // 避免大数值把主轴曲线压成贴地直线
    datasets: {
      label: string
      data: number[]
      borderColor: string
      backgroundColor?: string
      fill?: boolean
      borderDash?: number[]
      yAxisID?: 'y' | 'y1'
    }[]
    height?: number
  }>(),
  { height: 300 }
)

const canvasEl = ref<HTMLCanvasElement | null>(null)
let chart: Chart | null = null

const { isDark } = storeToRefs(useAppStore())

function buildConfig(): ChartConfiguration {
  const { grid, tick } = chartColors(isDark.value)
  const hasRightAxis = props.datasets.some((d) => d.yAxisID === 'y1')
  return {
    type: 'line',
    data: {
      labels: props.labels,
      datasets: props.datasets.map((d) => ({
        label: d.label,
        data: d.data,
        borderColor: d.borderColor,
        backgroundColor: d.backgroundColor || d.borderColor,
        fill: d.fill ?? false,
        borderDash: d.borderDash,
        yAxisID: d.yAxisID || 'y',
        tension: 0.3,
        pointRadius: 2,
        borderWidth: 2
      }))
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      interaction: { mode: 'index', intersect: false },
      plugins: {
        legend: { labels: { color: tick, boxWidth: 12 } },
        tooltip: { enabled: true }
      },
      scales: {
        x: { grid: { color: grid }, ticks: { color: tick } },
        y: { grid: { color: grid }, ticks: { color: tick } },
        // 右轴只在有 y1 曲线时创建；不画网格线，避免与左轴网格交错干扰
        ...(hasRightAxis
          ? {
              y1: {
                position: 'right' as const,
                grid: { drawOnChartArea: false },
                ticks: { color: tick }
              }
            }
          : {})
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
watch(() => [props.labels, props.datasets], render, { deep: true })
// 主题切换需重绘：网格线与刻度文字颜色写死在 config 里，Chart.js 不会自动更新
watch(isDark, render)
</script>
