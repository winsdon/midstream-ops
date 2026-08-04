<template>
  <div class="flex flex-wrap items-center justify-between gap-3">
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

    <div class="flex flex-wrap items-center gap-3">
      <!-- 搜索不防抖：本地过滤几十行，与 Providers / CreditToolbar 的 300ms 防抖
           刻意分歧 —— 那两处一个查后端、一个数据量大，这里都不成立。
           也不在此 trim：searchStabilityRows 已经 trim，在输入时 trim 会吃掉
           词中空格，让「上游 甲」这类查询打不出来 -->
      <div class="relative">
        <Icon name="search" size="sm" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
        <input
          :value="keyword"
          class="input !w-full !py-2 !pl-9 text-sm sm:!w-56"
          :placeholder="t('stability.searchPlaceholder')"
          @input="emit('update:keyword', ($event.target as HTMLInputElement).value)"
        />
      </div>

      <!-- 每组一条 pill tab；只有一个取值的维度不渲染（无筛选意义，纯噪音） -->
      <div
        v-for="g in groups" :key="g.key"
        role="tablist"
        class="flex rounded-lg bg-gray-100 p-0.5 dark:bg-dark-800"
      >
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
        :model-value="minutes"
        :options="windowOptions"
        :searchable="false"
        @update:model-value="emit('update:minutes', $event as WindowMinutes)"
      />
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
import { WINDOW_OPTIONS, type WindowMinutes } from '@/utils/stabilityModel'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'

const props = defineProps<{
  tab: 'passive' | 'active'
  /** value 为 provider_name，'' = 未归属 */
  providerOpts: FilterOption<string>[]
  healthOpts: FilterOption<string>[]
  provider: string | null
  health: string | null
  keyword: string
  minutes: WindowMinutes
  loading: boolean
}>()

const emit = defineEmits<{
  (e: 'update:tab', v: 'passive' | 'active'): void
  (e: 'update:provider', v: string | null): void
  (e: 'update:health', v: string | null): void
  (e: 'update:keyword', v: string): void
  (e: 'update:minutes', v: WindowMinutes): void
  (e: 'refresh'): void
}>()

const { t } = useI18n()

const windowOptions = computed(() =>
  WINDOW_OPTIONS.map((value) => ({ value, label: t(`stability.win${value}`) }))
)

/**
 * 两个维度结构完全一致（选项 + 当前值 + 标签 + 回调），统一成一份数据驱动模板，
 * 而不是把同一段 pill 复制两遍。只剩一个取值的维度直接剔除 ——
 * 此时筛选不产生任何区分。
 *
 * 两个 tab 共用这一份筛选：provider / health 是账号属性，与统计口径无关。
 */
const groups = computed(() =>
  [
    {
      key: 'provider',
      options: props.providerOpts,
      selected: props.provider as string | null,
      // 空串是「未归属」桶，不能直接渲染成空按钮
      label: (v: string) => v || t('stability.unassigned'),
      onSelect: (v: string | null) => emit('update:provider', v)
    },
    {
      key: 'health',
      options: props.healthOpts,
      selected: props.health as string | null,
      label: (v: string) => t('health.states.' + v),
      onSelect: (v: string | null) => emit('update:health', v)
    }
  ].filter((g) => g.options.length > 1)
)

// 与上游管理/广场页一致的 pill 样式；必须写完整字面量供 Tailwind 扫描。
const PILL_BASE = 'flex items-center rounded-md px-3 py-1.5 text-sm font-medium transition-colors'
const PILL_ACTIVE = 'bg-white text-primary-700 shadow-sm dark:bg-dark-700 dark:text-primary-300'
const PILL_IDLE = 'text-gray-500 hover:text-gray-700 dark:text-dark-400 dark:hover:text-dark-200'

function pillClass(active: boolean): string {
  return `${PILL_BASE} ${active ? PILL_ACTIVE : PILL_IDLE}`
}
</script>
