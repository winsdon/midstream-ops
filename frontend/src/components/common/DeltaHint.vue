<template>
  <p v-if="delta !== null && delta !== undefined" class="mt-0.5 flex items-center gap-1 text-xs" :class="toneClass">
    <span>{{ arrow }}</span>
    <span>{{ formatter ? formatter(Math.abs(delta)) : Math.abs(delta) }}</span>
    <span class="text-gray-400">{{ t('dash.vsPrevDay') }}</span>
  </p>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

const props = withDefaults(
  defineProps<{
    /** 与上一周期的差值；null 表示数据不足不展示 */
    delta?: number | null
    formatter?: (v: number) => string
    /**
     * 上涨是坏消息时置 true（如成本）：箭头方向不变，只反转配色。
     * 箭头永远跟随数值方向，避免误导。
     */
    negativeWhenUp?: boolean
  }>(),
  { negativeWhenUp: false }
)

const arrow = computed(() => {
  if (!props.delta) return '—'
  return props.delta > 0 ? '↑' : '↓'
})

const toneClass = computed(() => {
  const d = props.delta
  if (!d) return 'text-gray-400'
  const good = props.negativeWhenUp ? d < 0 : d > 0
  return good ? 'text-emerald-600 dark:text-emerald-400' : 'text-red-500 dark:text-red-400'
})
</script>
