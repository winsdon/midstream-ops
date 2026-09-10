<template>
  <div role="tablist" class="flex flex-wrap rounded-lg bg-gray-100 p-0.5 dark:bg-dark-800">
    <button
      v-for="key in HEATMAP_SORT_KEYS"
      :key="key"
      type="button"
      role="tab"
      :aria-selected="sort.key === key"
      class="flex items-center gap-1 rounded-md px-2.5 py-1.5 text-sm font-medium transition-colors"
      :class="sort.key === key
        ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-700 dark:text-primary-300'
        : 'text-gray-500 hover:text-gray-700 dark:text-dark-400 dark:hover:text-dark-200'"
      @click="emit('update:sort', nextHeatmapSort(sort, key))"
    >
      {{ labelOf(key) }}
      <Icon
        v-if="sort.key === key"
        name="arrowUp"
        size="xs"
        class="shrink-0 text-primary-600 transition-transform dark:text-primary-400"
        :class="sort.order === 'desc' && 'rotate-180'"
      />
    </button>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import {
  HEATMAP_SORT_KEYS,
  nextHeatmapSort,
  type HeatmapSort,
  type HeatmapSortKey
} from '@/utils/stabilityMetrics'

defineProps<{
  sort: HeatmapSort
}>()

const emit = defineEmits<{
  (e: 'update:sort', value: HeatmapSort): void
}>()

const { t } = useI18n()

function labelOf(key: HeatmapSortKey): string {
  switch (key) {
    case 'sla':
      return t('stability.successRate')
    case 'requests':
      return t('stability.sortRequests')
    case 'firstToken':
      return t('stability.ftP50')
    case 'tps':
      return t('stability.tokenPerSec')
    case 'cache':
      return t('stability.cacheRate')
  }
}
</script>
