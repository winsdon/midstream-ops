<template>
  <div class="space-y-5">
    <div class="flex items-center justify-between">
      <h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('plaza.filters.title') }}</h2>
      <button
        v-if="groupId !== null || platform !== null"
        type="button"
        class="text-xs text-primary-600 hover:text-primary-700 dark:text-primary-400"
        @click="reset"
      >
        {{ t('common.reset') }}
      </button>
    </div>

    <!-- 分组 -->
    <section>
      <h3 class="mb-2 text-xs font-medium uppercase tracking-wide text-gray-400 dark:text-dark-500">
        {{ t('plaza.filters.groups') }}
      </h3>
      <div class="flex flex-wrap gap-1.5 lg:flex-col lg:gap-1">
        <button type="button" :class="itemClass(groupId === null)" @click="emit('update:groupId', null)">
          <span class="min-w-0 flex-1 truncate text-left">{{ t('plaza.filters.all') }}</span>
          <span :class="countClass(groupId === null)">{{ total }}</span>
        </button>
        <button
          v-for="g in groups"
          :key="g.id"
          type="button"
          :class="itemClass(groupId === g.id)"
          :title="g.name"
          @click="emit('update:groupId', groupId === g.id ? null : g.id)"
        >
          <span class="min-w-0 flex-1 truncate text-left">{{ g.name }}</span>
          <span class="shrink-0 font-mono text-[10px] text-gray-400 dark:text-dark-500">
            x{{ formatRate(g.rate) }}
          </span>
          <span :class="countClass(groupId === g.id)">{{ g.count }}</span>
        </button>
      </div>
    </section>

    <!-- 供应商 -->
    <section>
      <h3 class="mb-2 text-xs font-medium uppercase tracking-wide text-gray-400 dark:text-dark-500">
        {{ t('plaza.filters.providers') }}
      </h3>
      <div class="flex flex-wrap gap-1.5 lg:flex-col lg:gap-1">
        <button type="button" :class="itemClass(platform === null)" @click="emit('update:platform', null)">
          <span class="min-w-0 flex-1 truncate text-left">{{ t('plaza.filters.all') }}</span>
          <span :class="countClass(platform === null)">{{ total }}</span>
        </button>
        <button
          v-for="p in platforms"
          :key="p.platform"
          type="button"
          :class="itemClass(platform === p.platform)"
          @click="emit('update:platform', platform === p.platform ? null : p.platform)"
        >
          <span class="min-w-0 flex-1 truncate text-left">{{ platformLabel(p.platform) }}</span>
          <span :class="countClass(platform === p.platform)">{{ p.count }}</span>
        </button>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { platformLabel, type GroupFilterOption, type PlatformFilterOption } from '@/utils/plazaModel'

defineProps<{
  groups: GroupFilterOption[]
  platforms: PlatformFilterOption[]
  groupId: number | null
  platform: string | null
  total: number
}>()

const emit = defineEmits<{
  (e: 'update:groupId', value: number | null): void
  (e: 'update:platform', value: string | null): void
}>()

const { t } = useI18n()

// 必须写完整字面量，Tailwind 扫描源码文本提取类名。
const ITEM_BASE = 'flex w-full items-center gap-1.5 rounded-lg border px-2.5 py-1.5 text-xs transition-colors'
const ITEM_ACTIVE = 'border-primary-500/40 bg-primary-500/10 font-semibold text-primary-700 dark:text-primary-300'
const ITEM_IDLE =
  'border-gray-200 bg-white text-gray-600 hover:border-gray-300 hover:text-gray-900 dark:border-dark-700 dark:bg-dark-800 dark:text-dark-300 dark:hover:border-dark-600 dark:hover:text-white'

const COUNT_BASE = 'shrink-0 rounded-full px-1.5 py-px font-mono text-[10px]'
const COUNT_ACTIVE = 'bg-primary-500/15 text-primary-700 dark:text-primary-300'
const COUNT_IDLE = 'bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-dark-400'

function itemClass(active: boolean): string {
  return `${ITEM_BASE} ${active ? ITEM_ACTIVE : ITEM_IDLE}`
}

function countClass(active: boolean): string {
  return `${COUNT_BASE} ${active ? COUNT_ACTIVE : COUNT_IDLE}`
}

function formatRate(v: number): string {
  return String(Number(v.toFixed(4)))
}

function reset() {
  emit('update:groupId', null)
  emit('update:platform', null)
}
</script>
