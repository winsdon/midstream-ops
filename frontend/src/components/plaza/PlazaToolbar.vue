<template>
  <div class="flex flex-wrap items-center justify-between gap-3">
    <p class="text-sm text-gray-500 dark:text-dark-400">{{ t('plaza.resultCount', { n: count }) }}</p>

    <div class="flex flex-wrap items-center gap-2">
      <!-- 价格单位 -->
      <div role="tablist" class="flex rounded-lg bg-gray-100 p-0.5 dark:bg-dark-800">
        <button
          v-for="opt in UNIT_OPTIONS"
          :key="opt.value"
          type="button"
          role="tab"
          :aria-selected="unitScale === opt.value"
          :class="pillClass(unitScale === opt.value)"
          @click="emit('update:unitScale', opt.value)"
        >
          {{ opt.label }}
        </button>
      </div>

      <!-- 排序 -->
      <Select
        class="!w-auto"
        :model-value="sortKey"
        :options="sortOptions"
        :searchable="false"
        @update:model-value="emit('update:sortKey', $event as SortKey)"
      />

      <!-- 视图切换 -->
      <div role="tablist" class="flex rounded-lg bg-gray-100 p-0.5 dark:bg-dark-800">
        <button
          type="button"
          role="tab"
          :aria-selected="viewMode === 'grid'"
          :class="pillClass(viewMode === 'grid')"
          :title="t('plaza.view.grid')"
          @click="emit('update:viewMode', 'grid')"
        >
          <Icon name="grid" size="sm" />
        </button>
        <button
          type="button"
          role="tab"
          :aria-selected="viewMode === 'list'"
          :class="pillClass(viewMode === 'list')"
          :title="t('plaza.view.list')"
          @click="emit('update:viewMode', 'list')"
        >
          <Icon name="menu" size="sm" />
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { SortKey, UnitScale, ViewMode } from '@/utils/plazaModel'
import Icon from '@/components/icons/Icon.vue'
import Select from '@/components/common/Select.vue'

defineProps<{
  count: number
  unitScale: UnitScale
  sortKey: SortKey
  viewMode: ViewMode
}>()

const emit = defineEmits<{
  (e: 'update:unitScale', value: UnitScale): void
  (e: 'update:sortKey', value: SortKey): void
  (e: 'update:viewMode', value: ViewMode): void
}>()

const { t } = useI18n()

const UNIT_OPTIONS: Array<{ value: UnitScale; label: string }> = [
  { value: 1_000_000, label: '/1M' },
  { value: 1_000, label: '/1K' }
]

const SORT_KEYS: SortKey[] = ['name', 'priceAsc', 'priceDesc']

const sortOptions = computed(() =>
  SORT_KEYS.map((value) => ({ value, label: t(`plaza.sort.${value}`) }))
)

// 与 Stability/Stats/Rates 页一致的 pill 样式；必须写完整字面量供 Tailwind 扫描。
const PILL_BASE = 'flex items-center rounded-md px-3 py-1.5 text-sm font-medium transition-colors'
const PILL_ACTIVE = 'bg-white text-primary-700 shadow-sm dark:bg-dark-700 dark:text-primary-300'
const PILL_IDLE = 'text-gray-500 hover:text-gray-700 dark:text-dark-400 dark:hover:text-dark-200'

function pillClass(active: boolean): string {
  return `${PILL_BASE} ${active ? PILL_ACTIVE : PILL_IDLE}`
}
</script>
