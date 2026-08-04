<template>
  <span :class="['badge', VARIANT[variant]]">
    <slot />
  </span>
</template>

<script setup lang="ts">
type BadgeVariant = 'primary' | 'success' | 'warning' | 'danger' | 'gray' | 'purple'

// 必须写完整字面量：Tailwind 扫描源码文本提取类名，拼接出来的类名会被漏掉
const VARIANT: Record<BadgeVariant, string> = {
  primary: 'badge-primary',
  success: 'badge-success',
  warning: 'badge-warning',
  danger: 'badge-danger',
  gray: 'badge-gray',
  purple: 'badge-purple'
}

withDefaults(defineProps<{ variant?: BadgeVariant }>(), { variant: 'gray' })
</script>
