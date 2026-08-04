<template>
  <div class="empty-state">
    <div class="mb-5 flex h-20 w-20 items-center justify-center rounded-2xl bg-gray-100 dark:bg-dark-800">
      <Icon :name="icon" size="xl" class="text-gray-300 dark:text-dark-600" aria-hidden="true" />
    </div>
    <h3 class="empty-state-title">{{ title || t('common.empty') }}</h3>
    <p v-if="description" class="empty-state-description">{{ description }}</p>
    <div v-if="$slots.action" class="mt-6">
      <slot name="action" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { IconName } from '@/components/icons/paths'

const { t } = useI18n()

withDefaults(
  defineProps<{
    icon?: IconName
    title?: string
    description?: string
  }>(),
  { icon: 'inbox' }
)
</script>
