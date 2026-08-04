<template>
  <div class="flex flex-col items-center justify-center gap-3 py-10 text-gray-400 dark:text-dark-500">
    <LoadingSpinner :size="size" />
    <span v-if="label !== ''" class="text-sm">{{ label || t('common.loading') }}</span>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import LoadingSpinner from './LoadingSpinner.vue'

const { t } = useI18n()

withDefaults(
  defineProps<{
    /** 传空串可只显示转圈，不显示文字 */
    label?: string
    size?: 'sm' | 'md' | 'lg'
  }>(),
  { size: 'md' }
)
</script>
