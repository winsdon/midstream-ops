<template>
  <div v-if="sync || !complete" class="flex flex-wrap items-center gap-x-4 gap-y-1.5 rounded-lg px-3 py-2 text-xs"
    :class="complete ? 'bg-gray-50 text-gray-500 dark:bg-dark-800/50 dark:text-dark-400'
                     : 'bg-amber-50 text-amber-700 dark:bg-amber-900/25 dark:text-amber-300'">
    <!-- 数据新鲜度 -->
    <span v-if="sync" class="flex items-center gap-1">
      <span :class="stale ? 'text-amber-600 dark:text-amber-400' : ''">
        {{ t('cost.syncedAt') }} {{ fmtDateTime(sync.last_synced_at) }}
      </span>
      <span v-if="ago !== null" :class="stale ? 'font-semibold text-amber-600 dark:text-amber-400' : 'text-gray-400'">
        ({{ t('cost.minutesAgo', { n: ago }) }})
      </span>
    </span>
    <span v-if="sync" class="text-gray-400">{{ t('cost.interval', { n: sync.interval_minutes }) }}</span>
    <span v-if="sync">{{ t('cost.keysMatched', { matched: sync.keys_matched, total: sync.keys_total }) }}</span>

    <!-- 成本不完整警告：利润被高估，必须让用户看到 -->
    <span v-if="!complete" class="font-semibold">
      ⚠ {{ t('cost.incomplete', { n: accountsMissing }) }}
    </span>

    <!-- 上游报错（已由后端折叠为短语） -->
    <span v-if="sync?.last_error" class="max-w-[320px] truncate text-red-600 dark:text-red-400" :title="sync.last_error">
      {{ sync.last_error }}
    </span>

    <slot />
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { fmtDateTime, minutesSince } from '@/utils/format'
import type { CostSyncStatus } from '@/types'

const props = withDefaults(
  defineProps<{
    sync?: CostSyncStatus | null
    complete?: boolean
    accountsMissing?: number
  }>(),
  { sync: null, complete: true, accountsMissing: 0 }
)

const { t } = useI18n()

const ago = computed(() => minutesSince(props.sync?.last_synced_at))
// 超过 3 倍同步间隔仍未更新 → 数据已陈旧，高亮提示
const stale = computed(() => {
  const n = ago.value
  const interval = props.sync?.interval_minutes || 10
  return n !== null && n > interval * 3
})
</script>
