<template>
  <div v-if="groups.length" class="flex flex-wrap items-center gap-x-4 gap-y-2">
    <!-- 每组一条 pill tab；只有一个取值的维度不渲染（无筛选意义，纯噪音） -->
    <div v-for="g in groups" :key="g.key" role="tablist" class="flex rounded-lg bg-gray-100 p-0.5 dark:bg-dark-800">
      <button
        type="button" role="tab"
        :aria-selected="g.selected === null"
        :class="pillClass(g.selected === null)"
        @click="g.onSelect(null)"
      >
        {{ t('provider.filterAll') }}
      </button>
      <button
        v-for="opt in g.options" :key="opt.value"
        type="button" role="tab"
        :aria-selected="g.selected === opt.value"
        :class="pillClass(g.selected === opt.value)"
        @click="g.onSelect(opt.value)"
      >
        {{ g.label(opt.value) }}
        <span class="ml-1 text-xs opacity-60">{{ opt.count }}</span>
      </button>
    </div>

    <Select
      class="!w-auto"
      :model-value="sortKey"
      :options="sortOptions"
      :searchable="false"
      @update:model-value="emit('update:sortKey', $event as ProviderSortKey)"
    />
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { FilterOption, ProviderSortKey, ProviderStatus } from '@/utils/providerModel'
import Select from '@/components/common/Select.vue'

const props = defineProps<{
  platformOpts: FilterOption<string>[]
  statusOpts: FilterOption<ProviderStatus>[]
  balanceTypeOpts: FilterOption<string>[]
  platform: string | null
  status: ProviderStatus | null
  balanceType: string | null
  sortKey: ProviderSortKey
}>()

const emit = defineEmits<{
  (e: 'update:platform', v: string | null): void
  (e: 'update:status', v: ProviderStatus | null): void
  (e: 'update:balanceType', v: string | null): void
  (e: 'update:sortKey', v: ProviderSortKey): void
}>()

const { t } = useI18n()

const STATUS_LABELS: Record<ProviderStatus, string> = {
  connected: 'provider.statusConnected',
  error: 'provider.statusError',
  pending: 'provider.statusPending',
  unmonitored: 'provider.notMonitored'
}

const BALANCE_TYPE_LABELS: Record<string, string> = {
  sub2api: 'provider.balanceTypes.sub2api',
  manual: 'provider.balanceTypes.manual',
  none: 'provider.balanceTypes.none'
}

const SORT_KEYS: ProviderSortKey[] = ['todayCostDesc', 'balanceDesc', 'balanceAsc', 'name']

const sortOptions = computed(() =>
  SORT_KEYS.map((value) => ({ value, label: t(`provider.sort.${value}`) }))
)

/**
 * 三个维度的结构完全一致（选项 + 当前值 + 标签 + 回调），
 * 统一成一份数据驱动模板，而不是把同一段 pill 复制三遍。
 * 只剩一个取值的维度直接剔除——此时筛选不产生任何区分。
 */
const groups = computed(() =>
  [
    {
      key: 'platform',
      options: props.platformOpts,
      selected: props.platform as string | null,
      label: (v: string) => v,
      onSelect: (v: string | null) => emit('update:platform', v)
    },
    {
      key: 'status',
      options: props.statusOpts,
      selected: props.status as string | null,
      label: (v: string) => t(STATUS_LABELS[v as ProviderStatus] ?? v),
      onSelect: (v: string | null) => emit('update:status', v as ProviderStatus | null)
    },
    {
      key: 'balanceType',
      options: props.balanceTypeOpts,
      selected: props.balanceType as string | null,
      label: (v: string) => t(BALANCE_TYPE_LABELS[v] ?? v),
      onSelect: (v: string | null) => emit('update:balanceType', v)
    }
  ].filter((g) => g.options.length > 1)
)

// 与广场页/统计页一致的 pill 样式；必须写完整字面量供 Tailwind 扫描。
const PILL_BASE = 'flex items-center rounded-md px-3 py-1.5 text-sm font-medium transition-colors'
const PILL_ACTIVE = 'bg-white text-primary-700 shadow-sm dark:bg-dark-700 dark:text-primary-300'
const PILL_IDLE = 'text-gray-500 hover:text-gray-700 dark:text-dark-400 dark:hover:text-dark-200'

function pillClass(active: boolean): string {
  return `${PILL_BASE} ${active ? PILL_ACTIVE : PILL_IDLE}`
}
</script>
