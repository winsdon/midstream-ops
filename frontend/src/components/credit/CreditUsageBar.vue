<template>
  <div class="min-w-[7rem]">
    <div class="flex items-center justify-between gap-2 text-xs">
      <span :class="granted ? textClass : 'text-gray-400'">
        {{ granted ? fmtPct(percent) : t('credit.notGranted') }}
      </span>
      <slot />
    </div>
    <div class="mt-1 h-1.5 w-full overflow-hidden rounded-full bg-gray-200 dark:bg-dark-700">
      <div class="h-full rounded-full transition-all duration-300" :class="barClass" :style="{ width: barWidth }"></div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { fmtPct } from '@/utils/format'

const props = defineProps<{
  /** 敞口占额度的比例，后端给的是 0~1 小数 */
  ratio: number
  /** ≤ 0 表示未授信，此时比例无意义 */
  limit: number
}>()

const { t } = useI18n()

/**
 * 变色阈值必须与后端 credit_service.go 的 creditBandWarning / creditBandOverflow 一致，
 * 否则条子变橙了却没收到告警（或反之），运营方会不再信任这个指示。
 */
const BAND_WARNING = 80
const BAND_OVERFLOW = 100

const granted = computed(() => props.limit > 0)
const percent = computed(() => props.ratio * 100)

// 超额时条子填满即可，不撑破容器
const barWidth = computed(() => `${Math.min(Math.max(percent.value, 0), 100)}%`)

// 必须写完整字面量：Tailwind 扫描源码文本提取类名，拼接出来的类名会被漏掉
const barClass = computed(() => {
  if (!granted.value) return 'bg-gray-300 dark:bg-dark-600'
  if (percent.value >= BAND_OVERFLOW) return 'bg-red-500'
  if (percent.value >= BAND_WARNING) return 'bg-amber-500'
  return 'bg-emerald-500'
})

const textClass = computed(() => {
  if (percent.value >= BAND_OVERFLOW) return 'font-semibold text-red-600 dark:text-red-400'
  if (percent.value >= BAND_WARNING) return 'font-semibold text-amber-600 dark:text-amber-400'
  return 'text-gray-500 dark:text-dark-400'
})
</script>
