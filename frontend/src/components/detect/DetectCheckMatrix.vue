<template>
  <div class="card">
    <div class="card-header flex flex-wrap items-center justify-between gap-3">
      <div>
        <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('detect.matrixTitle') }}</h2>
        <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">{{ t('detect.matrixHint') }}</p>
      </div>
      <button v-if="runs.length" type="button" class="btn btn-secondary btn-sm" @click="emit('export')">
        <Icon name="download" size="sm" />
        {{ t('detect.exportJson') }}
      </button>
    </div>

    <div class="card-body">
      <p class="mb-3 rounded-xl bg-gray-50 px-3 py-2 text-xs leading-relaxed text-gray-500 dark:bg-dark-800/50 dark:text-dark-400">
        {{ t('detect.evidenceBoundary') }}
      </p>
      <div class="table-wrapper">
        <table class="table">
          <thead>
            <tr>
              <th class="min-w-[180px]">{{ t('detect.checkColumn') }}</th>
              <th v-for="(run, i) in runs" :key="i" class="min-w-[220px]">
                <span class="truncate">{{ run.name }}</span>
              </th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in rows" :key="row.id">
              <td>
                <div class="font-medium text-gray-900 dark:text-white">{{ row.title }}</div>
                <div class="text-xs text-gray-400">{{ t(`detect.groups.${row.group}`) }}</div>
              </td>
              <td v-for="(run, i) in runs" :key="i">
                <div class="rounded-xl border p-2 transition-colors" :class="cellClass(run, row.id)">
                  <button
                    type="button"
                    class="w-full text-left"
                    :disabled="!canOpen(run, row.id)"
                    @click="openDetail(run, row.id)"
                  >
                    <span class="flex items-center gap-1.5">
                      <LoadingSpinner v-if="isRunning(run, row.id)" size="sm" />
                      <span v-else class="text-sm font-semibold">{{ statusIcon(findCheck(run, row.id)?.status) }}</span>
                      <span class="text-xs font-medium">{{ cellLabel(run, row.id) }}</span>
                    </span>
                    <span class="mt-0.5 block truncate text-xs text-gray-400" :title="cellSummary(run, row.id)">
                      {{ cellSummary(run, row.id) }}
                    </span>
                  </button>
                  <button
                    v-if="allowRetry && isRequestFailed(findCheck(run, row.id))"
                    type="button"
                    class="mt-1.5 inline-flex items-center gap-1 rounded-lg px-1.5 py-0.5 text-xs font-medium text-primary-600 hover:bg-primary-50 dark:text-primary-400 dark:hover:bg-primary-900/30"
                    :title="t('detect.retryHint')"
                    @click="emit('retry', { targetIndex: i, checkId: row.id })"
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
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import { findCheck, isRequestFailed, statusIcon } from '@/utils/detectModel'
import type { DetectCheckMeta, DetectCheckResult, DetectTargetRun } from '@/types/detect'

const { t } = useI18n()

const props = defineProps<{
  runs: DetectTargetRun[]
  checks: DetectCheckMeta[]
  /** 本轮实际执行的检测项 id，决定矩阵有哪些行 */
  activeIds: string[]
  /** 当前作业仍在内存里且未在跑时，才允许点重试 */
  allowRetry?: boolean
}>()

const emit = defineEmits<{
  (e: 'select', payload: { run: DetectTargetRun; check: DetectCheckResult }): void
  (e: 'export'): void
  (e: 'retry', payload: { targetIndex: number; checkId: string }): void
}>()

/** 跨项审计的 id。它不在勾选目录里（不发请求），但结果要能在矩阵里看到。 */
const AUDIT_ID = 'cross-audit'

/** 行取「本轮实际跑的项」，按目录顺序排列；这样没跑的项不会占位。 */
const rows = computed<DetectCheckMeta[]>(() => {
  const active = new Set(props.activeIds)
  const list = props.checks.filter((c) => active.has(c.id))
  // 审计项由后端在判定前追加，目录里没有它，用结果自带的 title / group 补一行。
  const audit = props.runs.map((r) => findCheck(r, AUDIT_ID)).find((c) => !!c)
  if (audit) {
    list.push({
      id: audit.id,
      title: audit.title,
      group: audit.group,
      default: false,
      cost: 'low',
      requests: 0
    })
  }
  return list
})

function isRunning(run: DetectTargetRun, id: string): boolean {
  return findCheck(run, id)?.status === 'running'
}

function canOpen(run: DetectTargetRun, id: string): boolean {
  const res = findCheck(run, id)
  return !!res && res.status !== 'running'
}

function cellLabel(run: DetectTargetRun, id: string): string {
  const res = findCheck(run, id)
  return res ? t(`detect.status.${res.status}`) : t('detect.notRun')
}

function cellSummary(run: DetectTargetRun, id: string): string {
  return findCheck(run, id)?.summary ?? ''
}

/** 格子配色：Tailwind 需要完整字面量，故逐个分支写全。 */
function cellClass(run: DetectTargetRun, id: string): string {
  const status = findCheck(run, id)?.status
  switch (status) {
    case 'running':
      return 'animate-pulse border-primary-200 bg-primary-50/60 text-primary-700 dark:border-primary-900 dark:bg-primary-900/20 dark:text-primary-400'
    case 'passed':
      return 'border-emerald-200 bg-emerald-50/60 text-emerald-700 hover:border-emerald-300 dark:border-emerald-900 dark:bg-emerald-900/20 dark:text-emerald-400'
    case 'failed':
      return 'border-red-200 bg-red-50/60 text-red-700 hover:border-red-300 dark:border-red-900 dark:bg-red-900/20 dark:text-red-400'
    case 'suspicious':
      return 'border-amber-200 bg-amber-50/60 text-amber-700 hover:border-amber-300 dark:border-amber-900 dark:bg-amber-900/20 dark:text-amber-400'
    case 'unsupported':
    case 'inconclusive':
      return 'border-gray-200 bg-gray-50/60 text-gray-600 hover:border-gray-300 dark:border-dark-700 dark:bg-dark-800/50 dark:text-dark-300'
    default:
      return 'cursor-default border-dashed border-gray-200 text-gray-400 dark:border-dark-700'
  }
}

function openDetail(run: DetectTargetRun, id: string): void {
  const check = findCheck(run, id)
  if (check) emit('select', { run, check })
}
</script>
