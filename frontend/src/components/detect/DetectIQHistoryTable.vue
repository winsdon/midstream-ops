<template>
  <div class="card">
    <div class="card-header">
      <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('detect.iqHistoryTitle') }}</h2>
      <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">{{ t('detect.iqHistoryHint') }}</p>
    </div>
    <div class="card-body">
      <div class="table-wrapper">
        <table class="table">
          <thead>
            <tr>
              <th>{{ t('detect.historyTime') }}</th>
              <th>{{ t('detect.targetName') }}</th>
              <th>{{ t('detect.historyModel') }}</th>
              <th>{{ t('detect.iqResults') }}</th>
              <th class="text-right">{{ t('detect.iqPassedColumn') }}</th>
              <th>{{ t('common.actions') }}</th>
            </tr>
          </thead>
          <tbody>
            <TableState
              :loading="loading"
              :empty="!items.length"
              :colspan="6"
              icon="clock"
              :title="t('detect.historyEmpty')"
            />
            <tr v-for="item in items" :key="item.id">
              <td class="whitespace-nowrap text-xs">{{ item.created_at }}</td>
              <td class="max-w-[180px] truncate text-sm">{{ item.target_name || item.account_name || '—' }}</td>
              <td class="text-xs text-gray-500">{{ item.model }}</td>
              <td>
                <div class="flex flex-wrap gap-1">
                  <span v-for="r in item.results" :key="r.id" :title="r.summary">
                    <Badge :variant="iqStatusVariant(r.status)">{{ r.title }} {{ iqStatusIcon(r.status) }}</Badge>
                  </span>
                </div>
              </td>
              <td class="text-right text-sm tabular-nums text-gray-900 dark:text-white">
                {{ item.passed }}<span class="text-xs text-gray-400">/{{ item.total }}</span>
              </td>
              <td>
                <div class="flex items-center gap-1">
                  <button type="button" class="btn btn-ghost btn-sm" @click="emit('open', item)">
                    {{ t('detect.historyLoad') }}
                  </button>
                  <button
                    type="button"
                    class="btn btn-ghost btn-sm text-red-600 hover:text-red-700 dark:text-red-400 dark:hover:text-red-300"
                    @click="emit('remove', item)"
                  >
                    {{ t('common.delete') }}
                  </button>
                </div>
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
import { iqStatusIcon, iqStatusVariant } from '@/utils/detectModel'
import type { DetectIQHistoryItem } from '@/types/detect'

const { t } = useI18n()

defineProps<{
  items: DetectIQHistoryItem[]
  loading: boolean
  page: number
  pages: number
  total: number
}>()

const emit = defineEmits<{
  (e: 'open', item: DetectIQHistoryItem): void
  (e: 'remove', item: DetectIQHistoryItem): void
  (e: 'update:page', page: number): void
}>()
</script>
