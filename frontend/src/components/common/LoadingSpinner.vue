<template>
  <span :class="['spinner', SIZE[size]]" role="status" :aria-label="t('common.loading')" />
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

type SpinnerSize = 'sm' | 'md' | 'lg'

// 全局 .spinner 固定 h-5 w-5，工具类层在组件层之后输出，所以这里的尺寸类能盖住它
const SIZE: Record<SpinnerSize, string> = {
  sm: 'h-4 w-4 border-2',
  md: 'h-6 w-6 border-2',
  lg: 'h-10 w-10 border-[3px]'
}

withDefaults(defineProps<{ size?: SpinnerSize }>(), { size: 'md' })
</script>
