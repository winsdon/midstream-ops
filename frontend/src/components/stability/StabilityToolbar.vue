<template>
  <div class="flex flex-wrap items-center justify-between gap-2">
    <div class="flex flex-wrap items-center gap-2">
      <!-- 口径 tab：被动在左且为默认 —— 它查的是真实流量，主动探测每 15 分钟
           才一轮，短窗口下样本稀疏，不适合当默认视图 -->
      <div role="tablist" class="flex rounded-lg bg-gray-100 p-0.5 dark:bg-dark-800">
        <button
          type="button" role="tab"
          :aria-selected="tab === 'passive'"
          :class="pillClass(tab === 'passive')"
          @click="emit('update:tab', 'passive')"
        >
          {{ t('stability.passive') }}
        </button>
        <button
          type="button" role="tab"
          :aria-selected="tab === 'active'"
          :class="pillClass(tab === 'active')"
          @click="emit('update:tab', 'active')"
        >
          {{ t('stability.active') }}
        </button>
      </div>

      <div role="tablist" class="flex rounded-lg bg-gray-100 p-0.5 dark:bg-dark-800">
        <button
          type="button" role="tab"
          :aria-selected="grouping === 'provider'"
          :class="pillClass(grouping === 'provider')"
          @click="emit('update:grouping', 'provider')"
        >
          {{ t('stats.byProvider') }}
        </button>
        <button
          type="button" role="tab"
          :aria-selected="grouping === 'group'"
          :class="pillClass(grouping === 'group')"
          @click="emit('update:grouping', 'group')"
        >
          {{ t('stats.byGroup') }}
        </button>
      </div>
    </div>

    <div class="flex flex-wrap items-center gap-2">
      <!-- 搜索不防抖：本地过滤几十行，与 Providers / CreditToolbar 的 300ms 防抖
           刻意分歧 —— 那两处一个查后端、一个数据量大，这里都不成立。
           也不在此 trim：searchStabilityRows 已经 trim，在输入时 trim 会吃掉
           词中空格，让「上游 甲」这类查询打不出来 -->
      <div class="relative">
        <Icon name="search" size="sm" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
        <input
          :value="keyword"
          class="input !w-full !py-2 !pl-9 text-sm sm:!w-48"
          :placeholder="t('stability.searchPlaceholder')"
          @input="emit('update:keyword', ($event.target as HTMLInputElement).value)"
        />
      </div>

      <!-- 分组 / 供应商 / 健康：选项一多 pill 会换行占半屏，收成下拉。
           只剩一个取值的维度不渲染（无筛选意义）。 -->
      <Select
        v-for="g in groups" :key="g.key"
        class="!w-36"
        :model-value="g.selected"
        :options="g.selectOptions"
        :searchable="g.searchable"
        @update:model-value="g.onSelect"
      />

      <div role="tablist" class="flex rounded-lg bg-gray-100 p-0.5 dark:bg-dark-800">
        <button
          v-for="m in WINDOW_OPTIONS" :key="m"
          type="button" role="tab"
          :aria-selected="minutes === m"
          :class="pillClass(minutes === m)"
          @click="emit('update:minutes', m)"
        >
          {{ t(`stability.win${m}`) }}
        </button>
      </div>

      <button class="btn btn-secondary text-sm" :title="t('common.refresh')" @click="emit('refresh')">
        <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { FilterOption } from '@/utils/providerModel'
import {
  WINDOW_OPTIONS,
  SELECT_ALL,
  SELECT_EMPTY,
  selectValue,
  parseSelectValue,
  type WindowMinutes
} from '@/utils/stabilityModel'
import type { GroupingMode } from '@/utils/stabilitySections'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'

const props = defineProps<{
  tab: 'passive' | 'active'
  grouping: GroupingMode
  /** value 为 provider_name，'' = 未归属 */
  providerOpts: FilterOption<string>[]
  healthOpts: FilterOption<string>[]
  groupOpts: FilterOption<string>[]
  provider: string | null
  health: string | null
  group: string | null
  keyword: string
  minutes: WindowMinutes
  loading: boolean
}>()

const emit = defineEmits<{
  (e: 'update:tab', v: 'passive' | 'active'): void
  (e: 'update:grouping', v: GroupingMode): void
  (e: 'update:provider', v: string | null): void
  (e: 'update:health', v: string | null): void
  (e: 'update:group', v: string | null): void
  (e: 'update:keyword', v: string): void
  (e: 'update:minutes', v: WindowMinutes): void
  (e: 'refresh'): void
}>()

const { t } = useI18n()

const groups = computed(() =>
  [
    {
      key: 'group',
      options: props.groupOpts,
      selected: selectValue(props.group),
      searchable: true as const,
      allLabel: t('stability.filterAllGroups'),
      label: (v: string) => v || t('stability.ungrouped'),
      onSelect: (v: string | number | boolean | null) => emit('update:group', parseSelectValue(v))
    },
    {
      key: 'provider',
      options: props.providerOpts,
      selected: selectValue(props.provider),
      searchable: true as const,
      allLabel: t('stability.filterAllProviders'),
      label: (v: string) => v || t('stability.unassigned'),
      onSelect: (v: string | number | boolean | null) => emit('update:provider', parseSelectValue(v))
    },
    {
      key: 'health',
      options: props.healthOpts,
      selected: selectValue(props.health),
      searchable: false as const,
      allLabel: t('stability.filterAllHealth'),
      label: (v: string) => t('health.states.' + v),
      onSelect: (v: string | number | boolean | null) => emit('update:health', parseSelectValue(v))
    }
  ]
    .filter((g) => g.options.length > 1)
    .map((g) => ({
      ...g,
      selectOptions: [
        { value: SELECT_ALL, label: g.allLabel },
        ...g.options.map((opt) => ({
          value: opt.value === '' ? SELECT_EMPTY : opt.value,
          label: `${g.label(opt.value)} (${opt.count})`
        }))
      ]
    }))
)

// 与上游管理/广场页一致的 pill 样式；必须写完整字面量供 Tailwind 扫描。
const PILL_BASE = 'flex items-center rounded-md px-2.5 py-1.5 text-sm font-medium transition-colors'
const PILL_ACTIVE = 'bg-white text-primary-700 shadow-sm dark:bg-dark-700 dark:text-primary-300'
const PILL_IDLE = 'text-gray-500 hover:text-gray-700 dark:text-dark-400 dark:hover:text-dark-200'

function pillClass(active: boolean): string {
  return `${PILL_BASE} ${active ? PILL_ACTIVE : PILL_IDLE}`
}
</script>
