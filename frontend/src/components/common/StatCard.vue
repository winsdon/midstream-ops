<template>
  <div class="stat-card">
    <div v-if="icon" :class="['stat-icon', ICON_VARIANT[iconVariant]]">
      <Icon :name="icon" size="lg" aria-hidden="true" />
    </div>
    <div class="min-w-0 flex-1">
      <p class="stat-label flex items-center gap-1 truncate">
        {{ title }}
        <slot name="title-suffix" />
      </p>
      <p class="stat-value mt-1" :class="valueClass" :title="String(value)">{{ value }}</p>
      <slot name="footer" />
    </div>
  </div>
</template>

<script setup lang="ts">
import Icon from '@/components/icons/Icon.vue'
import type { IconName } from '@/components/icons/paths'

type IconVariant = 'primary' | 'success' | 'warning' | 'danger'

// 必须写完整字面量：Tailwind 扫描源码文本提取类名，拼接出来的类名会被漏掉
const ICON_VARIANT: Record<IconVariant, string> = {
  primary: 'stat-icon-primary',
  success: 'stat-icon-success',
  warning: 'stat-icon-warning',
  danger: 'stat-icon-danger'
}

withDefaults(
  defineProps<{
    title: string
    /** 已格式化好的展示值，格式化由调用方负责（金额 / 数量口径各不相同） */
    value: string
    icon?: IconName
    iconVariant?: IconVariant
    /** 额外的数值配色，如利润的正负色 */
    valueClass?: string
  }>(),
  { iconVariant: 'primary' }
)
</script>
