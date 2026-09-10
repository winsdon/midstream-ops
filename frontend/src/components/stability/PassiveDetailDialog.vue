<template>
  <BaseDialog :show="show" :title="title" width="extra-wide" @close="emit('close')">
    <p v-if="bucketHint" class="mb-3 text-xs text-gray-500 dark:text-dark-400">{{ bucketHint }}</p>
    <div v-if="models.length" class="overflow-x-auto">
      <div class="grid items-center gap-x-3 border-b border-gray-100 px-3 pb-2 text-[11px] font-medium uppercase tracking-wider text-gray-400 dark:border-dark-800" :style="HEATMAP_GRID">
        <span>{{ t('stability.account') }}</span>
        <span class="text-right">{{ t('stability.successRate') }}</span>
        <span class="text-right">{{ t('stability.requests') }}</span>
        <span class="text-right">{{ t('stability.ftP50') }}</span>
        <span class="text-right">{{ t('stability.tokenPerSec') }}</span>
        <span class="text-right">{{ t('stability.cacheRate') }}</span>
        <span>{{ t('stability.availabilityTrend') }}</span>
      </div>
      <div class="divide-y divide-gray-50 dark:divide-dark-800">
        <StabilityHeatmapRow
          v-for="m in models"
          :key="m.key"
          :model="m"
          :minutes="minutes"
          :generated-at="generatedAt"
          :highlight="highlight"
          @open-row="emit('close')"
          @open-cell="emit('close')"
        />
      </div>
    </div>
    <EmptyState v-else icon="chart" />
  </BaseDialog>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import StabilityHeatmapRow from './StabilityHeatmapRow.vue'
import { HEATMAP_GRID } from '@/utils/stabilityTimeline'
import type { StatusCardModel } from '@/utils/stabilityMetrics'

defineProps<{
  show: boolean
  title: string
  bucketHint?: string
  models: StatusCardModel[]
  minutes: number
  generatedAt: string
  highlight?: number | null
}>()

const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()
</script>
