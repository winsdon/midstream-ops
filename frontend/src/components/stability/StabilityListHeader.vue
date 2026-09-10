<template>
  <div class="flex flex-wrap items-center justify-between gap-2">
    <div class="flex min-w-0 items-center gap-1.5">
      <button
        v-if="showExpand"
        type="button"
        class="flex shrink-0 items-center gap-1 rounded-md px-1.5 py-1 text-xs font-medium text-gray-600 transition-colors hover:bg-gray-50 dark:text-dark-300 dark:hover:bg-dark-800/60"
        :title="allOpen ? t('stability.collapseAll') : t('stability.expandAll')"
        :aria-expanded="allOpen"
        @click="emit('toggleAll')"
      >
        <Icon
          :name="allOpen ? 'chevronDown' : 'chevronRight'"
          size="sm"
          class="text-gray-400"
        />
        {{ allOpen ? t('stability.collapseAll') : t('stability.expandAll') }}
      </button>
      <p v-if="hint" class="min-w-0 text-xs text-gray-500 dark:text-dark-400">{{ hint }}</p>
    </div>

    <div role="tablist" class="flex rounded-lg bg-gray-100 p-0.5 dark:bg-dark-800">
      <button
        v-for="opt in SORT_KEYS"
        :key="opt"
        type="button"
        role="tab"
        :aria-selected="sort.key === opt"
        :class="pillClass(sort.key === opt)"
        @click="emit('update:sort', nextStabilitySort(sort, opt))"
      >
        {{ opt === 'sla' ? t('stability.successRate') : t('stability.sortRequests') }}
        <Icon
          v-if="sort.key === opt"
          name="arrowUp"
          size="xs"
          class="shrink-0 text-primary-600 transition-transform dark:text-primary-400"
          :class="sort.order === 'desc' && 'rotate-180'"
        />
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import {
  nextStabilitySort,
  type StabilitySort,
  type StabilitySortKey
} from '@/utils/stabilitySections'

withDefaults(
  defineProps<{
    allOpen: boolean
    hint?: string
    sort: StabilitySort
    showExpand?: boolean
  }>(),
  { showExpand: true }
)

const emit = defineEmits<{
  (e: 'toggleAll'): void
  (e: 'update:sort', value: StabilitySort): void
}>()

const { t } = useI18n()

const SORT_KEYS: StabilitySortKey[] = ['sla', 'requests']

const PILL_BASE = 'flex items-center gap-1 rounded-md px-2.5 py-1.5 text-sm font-medium transition-colors'
const PILL_ACTIVE = 'bg-white text-primary-700 shadow-sm dark:bg-dark-700 dark:text-primary-300'
const PILL_IDLE = 'text-gray-500 hover:text-gray-700 dark:text-dark-400 dark:hover:text-dark-200'

function pillClass(active: boolean): string {
  return `${PILL_BASE} ${active ? PILL_ACTIVE : PILL_IDLE}`
}
</script>
