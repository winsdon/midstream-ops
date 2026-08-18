<template>
  <div class="flex flex-wrap items-center gap-2">
    <Select
      v-for="g in groups" :key="g.key"
      class="!w-36"
      :model-value="g.selected ?? ''"
      :options="g.selectOptions"
      :searchable="false"
      @update:model-value="g.onSelect($event === '' || $event == null ? null : String($event))"
    />
    <Select
      class="!w-36"
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
  credentialsPending: 'provider.statusCredentialsPending',
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
  ]
    .filter((g) => g.options.length > 1)
    .map((g) => ({
      ...g,
      selectOptions: [
        { value: '', label: t('provider.filterAll') },
        ...g.options.map((opt) => ({
          value: opt.value,
          label: `${g.label(opt.value)} (${opt.count})`
        }))
      ]
    }))
)
</script>
