<template>
  <div class="min-w-[5.5rem] text-right">
    <span :class="toneClass">{{ displayMargin(pct) }}</span>
    <!-- 条与数字双编码：数字回答「多少」，条长回答「相比之下如何」。
         隐私模式与无值时都不渲染 —— 条长本身就是利润率的模拟量泄露 -->
    <div v-if="showBar" class="mt-1 h-1 w-full overflow-hidden rounded-full bg-gray-200 dark:bg-dark-700">
      <div class="h-full rounded-full transition-all duration-300" :class="barClass" :style="{ width: barWidth }"></div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { usePrivacyMoney } from '@/composables/usePrivacyMoney'
import { marginBand, type MarginBand } from '@/utils/profitMargin'

const props = defineProps<{
  /** 百分点；null = 无法计算（收益为 0 / 成本未匹配） */
  pct?: number | null
}>()

const { privacyMode, displayMargin } = usePrivacyMoney()

/**
 * 必须写完整字面量：Tailwind 扫描源码文本提取类名，拼接出来的会被漏掉。
 *
 * ok 档（10-30%）用中性灰而非绿，沿用 LatencyCell 的反色彩通胀规则 ——
 * 四档里两档给绿色会让绿色泛滥，只有真正高毛利才值得一个正向信号。
 */
const TONE: Record<MarginBand, string> = {
  loss: 'font-semibold text-red-600 dark:text-red-400',
  thin: 'font-semibold text-amber-600 dark:text-amber-400',
  ok: 'text-gray-700 dark:text-dark-300',
  good: 'font-semibold text-emerald-600 dark:text-emerald-400',
  unknown: 'text-gray-400 dark:text-dark-500'
}

const BAR: Record<MarginBand, string> = {
  loss: 'bg-red-500',
  thin: 'bg-amber-500',
  ok: 'bg-gray-400 dark:bg-dark-500',
  good: 'bg-emerald-500',
  unknown: 'bg-gray-300 dark:bg-dark-600'
}

const band = computed(() => marginBand(props.pct))

// 打了码就不能再着色，与 displayMoneyClass 同构
const toneClass = computed(() => (privacyMode.value ? '' : TONE[band.value]))

const barClass = computed(() => BAR[band.value])

const showBar = computed(() => !privacyMode.value && band.value !== 'unknown')

/**
 * 利润率上界恒为 100%（成本非负 ⇒ 利润 ≤ 收益），故不必担心撑破容器；
 * 但仍夹紧以防后端口径变化。负利润条宽 0，只留红色数字说话。
 */
const barWidth = computed(() => `${Math.min(Math.max(props.pct ?? 0, 0), 100)}%`)
</script>
