<template>
  <div class="table-wrapper">
    <table class="table">
      <thead>
        <tr>
          <th>{{ t('detect.targetName') }}</th>
          <th>{{ t('detect.historyModel') }}</th>
          <th>{{ t('detect.iqAnswer') }}</th>
          <th>{{ t('detect.iqConclusion') }}</th>
          <th class="text-right">{{ t('detect.iqDuration') }}</th>
          <th>{{ t('common.actions') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="row in rows" :key="row.index">
          <td class="max-w-[180px] truncate text-sm" :title="row.run.name">{{ row.run.name }}</td>
          <td class="text-xs text-gray-500">{{ row.run.model }}</td>
          <td>
            <span
              v-if="row.outcome !== 'none'"
              class="text-base font-semibold tabular-nums"
              :class="row.outcome === 'correct' ? 'text-emerald-600 dark:text-emerald-400' : 'text-red-600 dark:text-red-400'"
            >
              {{ row.res?.output?.answer }} {{ row.outcome === 'correct' ? '✓' : '×' }}
            </span>
            <span v-else class="text-gray-400">—</span>
          </td>
          <td>
            <span v-if="row.running" class="inline-flex items-center gap-1.5 text-xs text-primary-600 dark:text-primary-400">
              <LoadingSpinner size="sm" />
              {{ t('detect.iqStatus.running') }}
            </span>
            <span v-else-if="row.res" :title="row.res.summary">
              <Badge :variant="iqStatusVariant(row.res.status)">{{ t(statusLabelKey(row.res)) }}</Badge>
            </span>
            <span v-else class="text-xs text-gray-400">{{ t('detect.iqPending') }}</span>
          </td>
          <td class="text-right text-xs tabular-nums text-gray-500">
            {{ row.res && !row.running ? fmtMs(row.res.duration_ms) : '—' }}
          </td>
          <td>
            <div class="flex items-center gap-1">
              <button
                type="button"
                class="btn btn-ghost btn-sm"
                :disabled="!row.res || row.running"
                @click="row.res && emit('select', { run: row.run, check: row.res })"
              >
                {{ t('detect.iqViewReply') }}
              </button>
              <button
                v-if="allowRetry && isRequestFailed(row.res)"
                type="button"
                class="btn btn-ghost btn-sm"
                :title="t('detect.retryHint')"
                @click="emit('retry', { targetIndex: row.index, checkId })"
              >
                <Icon name="refresh" size="sm" />
                {{ t('detect.retry') }}
              </button>
            </div>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Badge from '@/components/common/Badge.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import Icon from '@/components/icons/Icon.vue'
import { answerOutcome, findCheck, iqStatusVariant, isRequestFailed, statusLabelKey } from '@/utils/detectModel'
import { fmtMs } from '@/utils/format'
import type { DetectCheckResult, DetectTargetRun } from '@/types/detect'

const { t } = useI18n()

const props = defineProps<{
  runs: DetectTargetRun[]
  /** 本区展示的题目（grading = answer） */
  checkId: string
  allowRetry?: boolean
}>()

const emit = defineEmits<{
  (e: 'select', payload: { run: DetectTargetRun; check: DetectCheckResult }): void
  (e: 'retry', payload: { targetIndex: number; checkId: string }): void
}>()

const rows = computed(() =>
  props.runs.map((run, index) => {
    const res = findCheck(run, props.checkId)
    return { index, run, res, running: res?.status === 'running', outcome: answerOutcome(res?.output) }
  })
)
</script>
