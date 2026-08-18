<template>
  <div class="flex flex-col items-center justify-center rounded-xl border border-gray-100 bg-gray-50/60 p-3 dark:border-dark-800 dark:bg-dark-800/40">
    <p class="mb-1 text-xs text-gray-500 dark:text-dark-400">{{ label }}</p>
    <!-- usdOnly：卡片只要 USD，省掉 CNY 双显占的高度 -->
    <template v-if="usdOnly">
      <span class="text-center text-sm font-bold" :class="mainTone">{{ usdText }}</span>
    </template>
    <template v-else>
      <!-- 有有效充值倍率时 CNY 主显、USD 副显；否则 USD 升为主显 -->
      <span v-if="cny !== null" class="text-center text-sm font-bold" :class="mainTone">{{ cny }}</span>
      <span :class="[cny !== null ? ['mt-0.5 text-[10px] font-medium', subTone] : ['text-sm font-bold', mainTone], 'text-center']">
        {{ usdText }}
      </span>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(
  defineProps<{
    label: string
    /** 上游原始 USD 值；null/undefined 表示无数据 */
    usd?: number | null
    /** 充值倍率：USD × rate = CNY；<= 0 视为无效 */
    rate: number
    tone?: 'primary' | 'warning' | 'muted'
    /** 只显示 USD（上游卡片用，腾出空间放子账号/成本入口） */
    usdOnly?: boolean
  }>(),
  { tone: 'primary', usdOnly: false }
)

// 必须写完整字面量：Tailwind 扫描源码文本提取类名
const MAIN_TONE = {
  primary: 'text-primary-600 dark:text-primary-400',
  warning: 'text-orange-500 dark:text-orange-400',
  muted: 'text-gray-900 dark:text-white'
} as const
const SUB_TONE = {
  primary: 'text-primary-600/70 dark:text-primary-400/70',
  warning: 'text-orange-500/70 dark:text-orange-400/70',
  muted: 'text-gray-400 dark:text-dark-400'
} as const

const hasValue = computed(() => props.usd !== null && props.usd !== undefined && Number.isFinite(props.usd))

const cny = computed(() => {
  if (!hasValue.value || !(props.rate > 0)) return null
  return (props.usd! * props.rate).toFixed(2) + ' CNY'
})

const usdText = computed(() => (hasValue.value ? trimZeros(props.usd!) + ' USD' : '-'))

// 去掉尾随 0 与小数点，避免 1.5000 这种噪声
function trimZeros(v: number): string {
  return v.toFixed(4).replace(/\.?0+$/, '')
}

// 成本为 0 时用中性色，避免全是橙色
const mainTone = computed(() => {
  if (props.tone === 'warning' && (!hasValue.value || props.usd === 0)) return MAIN_TONE.muted
  return MAIN_TONE[props.tone]
})
const subTone = computed(() => {
  if (props.tone === 'warning' && (!hasValue.value || props.usd === 0)) return SUB_TONE.muted
  return SUB_TONE[props.tone]
})
</script>
