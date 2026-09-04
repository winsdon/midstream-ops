<template>
  <div class="card">
    <div class="card-header">
      <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('detect.historyTitle') }}</h2>
      <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">{{ t('detect.historyHint') }}</p>
    </div>
    <div class="card-body">
      <div class="table-wrapper">
        <table class="table">
          <thead>
            <tr>
              <th>{{ t('detect.historyTime') }}</th>
              <th>{{ t('detect.targetName') }}</th>
              <th>{{ t('detect.historyModel') }}</th>
              <th>{{ t('detect.verdict') }}</th>
              <th>{{ t('detect.confidence') }}</th>
              <th class="text-right">{{ t('detect.authenticity') }}</th>
              <th>{{ t('common.actions') }}</th>
            </tr>
          </thead>
          <tbody>
            <TableState
              :loading="loading"
              :empty="!items.length"
              :colspan="7"
              icon="clock"
              :title="t('detect.historyEmpty')"
            />
            <tr v-for="item in items" :key="item.id">
              <td class="whitespace-nowrap text-xs">{{ item.created_at }}</td>
              <td class="max-w-[180px] truncate text-sm">{{ item.target_name || item.account_name || '—' }}</td>
              <td class="text-xs text-gray-500">{{ item.model }}</td>
              <td>
                <Badge :variant="labelVariant(item.label)">{{ t(`detect.labels.${item.label}`) }}</Badge>
              </td>
              <td class="text-xs text-gray-500">
                {{ t(`detect.confidence${capitalize(item.confidence)}`) }}
              </td>
              <td class="text-right">
                <span class="text-sm tabular-nums text-gray-900 dark:text-white">{{ item.authenticity_score }}</span>
                <span class="ml-1 text-xs text-gray-400">{{ t(`detect.grades.${item.authenticity_grade}`) }}</span>
              </td>
              <td>
                <button type="button" class="btn btn-ghost btn-sm" @click="emit('open', item)">
                  {{ t('detect.historyLoad') }}
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <Pagination
        v-if="pages > 1"
        class="mt-3"
        :page="page"
        :pages="pages"
        :total="total"
        @change="emit('update:page', $event)"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Badge from '@/components/common/Badge.vue'
import TableState from '@/components/common/TableState.vue'
import Pagination from '@/components/Pagination.vue'
import { labelVariant } from '@/utils/detectModel'
import type { DetectHistoryItem } from '@/types/detect'

const { t } = useI18n()

defineProps<{
  items: DetectHistoryItem[]
  loading: boolean
  page: number
  pages: number
  total: number
}>()

const emit = defineEmits<{
  (e: 'open', item: DetectHistoryItem): void
  (e: 'update:page', page: number): void
}>()

function capitalize(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1)
}
</script>
