<template>
  <div class="flex flex-wrap items-center justify-between gap-3">
    <div class="relative">
      <Icon name="search" size="sm" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
      <input
        v-model.trim="search"
        class="input !w-full !py-2 !pl-9 text-sm sm:!w-72"
        :placeholder="t('credit.searchPlaceholder')"
      />
    </div>

    <div role="tablist" class="flex rounded-lg bg-gray-100 p-0.5 dark:bg-dark-800">
      <button
        v-for="opt in STATUS_OPTIONS"
        :key="opt.value ?? 'all'"
        type="button"
        role="tab"
        :aria-selected="status === opt.value"
        :class="pillClass(status === opt.value)"
        @click="emit('update:status', opt.value)"
      >
        {{ t(opt.label) }}
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { CustomerStatus } from '@/types/credit'

const props = defineProps<{
  keyword: string
  /** null = 全部状态 */
  status: CustomerStatus | null
}>()

const emit = defineEmits<{
  (e: 'update:keyword', v: string): void
  (e: 'update:status', v: CustomerStatus | null): void
}>()

const { t } = useI18n()

const STATUS_OPTIONS: { value: CustomerStatus | null; label: string }[] = [
  { value: null, label: 'common.all' },
  { value: 'active', label: 'credit.statusActive' },
  { value: 'archived', label: 'credit.statusArchived' }
]

/**
 * 关键词走服务端查询，必须防抖，否则逐字触发一次请求。
 * 项目未引入 @vueuse，沿用 Providers.vue 的手写 setTimeout 防抖。
 */
const DEBOUNCE_MS = 300
const search = ref(props.keyword)
let timer: ReturnType<typeof setTimeout> | undefined

watch(search, (v) => {
  clearTimeout(timer)
  timer = setTimeout(() => emit('update:keyword', v), DEBOUNCE_MS)
})

// 组件卸载后定时器仍会触发，向已销毁的父组件发事件
onBeforeUnmount(() => clearTimeout(timer))

// 与上游管理/统计页一致的 pill 样式；必须写完整字面量供 Tailwind 扫描。
const PILL_BASE = 'rounded-md px-3 py-1.5 text-sm font-medium transition-colors'
const PILL_ACTIVE = 'bg-white text-primary-700 shadow-sm dark:bg-dark-700 dark:text-primary-300'
const PILL_IDLE = 'text-gray-500 hover:text-gray-700 dark:text-dark-400 dark:hover:text-dark-200'

function pillClass(active: boolean): string {
  return `${PILL_BASE} ${active ? PILL_ACTIVE : PILL_IDLE}`
}
</script>
