<template>
  <div class="space-y-5">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <h1 class="text-xl font-bold text-gray-900 dark:text-white">{{ t('nav.detect') }}</h1>
    </div>

    <div class="grid items-start gap-5 lg:grid-cols-[minmax(20rem,24rem)_minmax(0,1fr)]">
      <div class="space-y-4 lg:sticky lg:top-4">
        <DetectTargetPanel
          :accounts="accounts"
          :accounts-loading="accountsLoading"
          :accounts-error="accountsError"
          @change="onTargetChange"
        />

        <DetectBaselinePanel
          :accounts="accounts"
          :accounts-loading="accountsLoading"
          :accounts-error="accountsError"
          :baseline="baseline"
          :loading="baselineLoading"
          :test-model="runOptions.model"
          @generate="createBaseline"
        />

        <DetectCheckPicker
          :checks="checks"
          :defaults="defaults"
          :presets="presets"
          :loading="checksLoading"
          :selected="selected"
          :target-count="targets.length"
          @update:selected="selected = $event"
        />

        <DetectRunBar
          :job="job.job"
          :running="job.running"
          :disabled="!targets.length || !selected.length"
          :request-estimate="requestEstimate"
          :concurrency="concurrency"
          @update:concurrency="concurrency = $event"
          @start="confirmAndStart"
          @cancel="job.cancel()"
          @retry-failed="retryFailed"
        />
      </div>

      <div class="space-y-4">
        <div class="flex rounded-xl bg-gray-100 p-1 dark:bg-dark-800">
          <button
            v-for="tab in RESULT_TABS"
            :key="tab"
            type="button"
            class="flex-1 rounded-lg px-3 py-1.5 text-xs font-medium transition-colors"
            :class="
              resultTab === tab
                ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-700 dark:text-white'
                : 'text-gray-500 hover:text-gray-700 dark:text-dark-400 dark:hover:text-dark-200'
            "
            @click="resultTab = tab"
          >
            {{ tab === 'result' ? t('detect.resultTab') : t('detect.historyTab') }}
          </button>
        </div>

        <template v-if="resultTab === 'result'">
          <EmptyState
            v-if="!displayRuns.length"
            icon="beaker"
            :title="t('detect.resultEmpty')"
            :description="t('detect.resultEmptyHint')"
          />
          <template v-else>
            <div class="grid gap-4 xl:grid-cols-2">
              <DetectVerdictCard v-for="(run, i) in displayRuns" :key="i" :run="run" />
            </div>
            <DetectCheckMatrix
              :runs="displayRuns"
              :checks="checks"
              :active-ids="activeCheckIds"
              :allow-retry="allowRetry"
              @select="openDetail"
              @export="exportReport"
              @retry="retryOne"
            />
          </template>
        </template>

        <DetectHistoryTable
          v-else
          :items="history.items"
          :loading="history.loading"
          :page="history.page"
          :pages="history.pages"
          :total="history.total"
          @open="openHistory"
          @remove="askDeleteHistory"
          @update:page="history.load"
        />
      </div>
    </div>

    <DetectCheckDrawer
      :show="showDetail"
      :check="detailCheck"
      :target-name="detailTarget"
      :allow-retry="allowRetry"
      @close="showDetail = false"
      @retry="retryDetail"
    />

    <ConfirmDialog
      :show="showHighCostConfirm"
      :title="t('detect.highCostConfirmTitle')"
      :message="highCostMessage"
      @confirm="startRun"
      @cancel="showHighCostConfirm = false"
    />

    <ConfirmDialog
      :show="showHistoryDelete"
      :title="t('common.delete')"
      :message="pendingHistoryDelete ? t('common.confirmDelete', { name: pendingHistoryDelete.label }) : ''"
      danger
      @confirm="confirmDeleteHistory"
      @cancel="showHistoryDelete = false"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { detectApi } from '@/api'
import { errorMessage } from '@/api/client'
import { useAppStore } from '@/stores/app'
import { useDetectJob } from '@/composables/useDetectJob'
import { usePagedHistory } from '@/composables/usePagedHistory'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import DetectTargetPanel from '@/components/detect/DetectTargetPanel.vue'
import DetectBaselinePanel from '@/components/detect/DetectBaselinePanel.vue'
import DetectCheckPicker from '@/components/detect/DetectCheckPicker.vue'
import DetectRunBar from '@/components/detect/DetectRunBar.vue'
import DetectVerdictCard from '@/components/detect/DetectVerdictCard.vue'
import DetectCheckMatrix from '@/components/detect/DetectCheckMatrix.vue'
import DetectCheckDrawer from '@/components/detect/DetectCheckDrawer.vue'
import DetectHistoryTable from '@/components/detect/DetectHistoryTable.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import {
  DEFAULT_DETECT_CONCURRENCY,
  estimateRequests,
  highCostSelection,
  withDependencies
} from '@/utils/detectModel'
import { baselineApplies, sameBaselineModel } from '@/utils/detectBaseline'
import type {
  DetectAccount,
  DetectBaseline,
  DetectCheckMeta,
  DetectCheckResult,
  DetectHistoryItem,
  DetectPreset,
  DetectRetryPayload,
  DetectTargetInput,
  DetectTargetRun
} from '@/types/detect'

const { t } = useI18n()
const app = useAppStore()

const HISTORY_PAGE_SIZE = 10
const RESULT_TABS = ['result', 'history'] as const

const checks = ref<DetectCheckMeta[]>([])
const defaults = ref<string[]>([])
const presets = ref<DetectPreset[]>([])
const checksLoading = ref(false)
const selected = ref<string[]>([])
const concurrency = ref(DEFAULT_DETECT_CONCURRENCY)
const resultTab = ref<(typeof RESULT_TABS)[number]>('result')

const accounts = ref<DetectAccount[]>([])
const accountsLoading = ref(false)
const accountsError = ref('')
const baseline = ref<DetectBaseline | null>(null)
const baselineLoading = ref(false)

const targets = ref<DetectTargetInput[]>([])
const runOptions = ref({ model: '', authMode: 'both', timeoutMs: 90000, extraHeaders: {} as Record<string, string> })

/** reactive 包一层，模板里直接读值。 */
const history = reactive(
  usePagedHistory<DetectHistoryItem>((p) => detectApi.history(p), HISTORY_PAGE_SIZE)
)

const job = reactive(useDetectJob({ onFinished: () => void history.load(1) }))

/** 从历史里载入的报告：与当轮结果共用同一套展示组件。 */
const historyRun = ref<DetectTargetRun | null>(null)
/** 当前载入的历史行 id。删掉这一条时要把结果区里的旧报告一起清掉。 */
const loadedHistoryId = ref<number | null>(null)
const showHistoryDelete = ref(false)
const deletingHistory = ref(false)
const pendingHistoryDelete = ref<{ id: number; label: string } | null>(null)

const showDetail = ref(false)
const detailCheck = ref<DetectCheckResult | null>(null)
const detailTarget = ref('')
const detailTargetIndex = ref(0)
const showHighCostConfirm = ref(false)

const effectiveChecks = computed(() => withDependencies(checks.value, new Set(selected.value)))

const requestEstimate = computed(() =>
  estimateRequests(checks.value, effectiveChecks.value, targets.value.length)
)

/**
 * 展示优先级：本轮结果 > 从历史载入的报告。
 * 一旦重新开跑，历史报告就让位——同屏混着两个时间点的结论只会误导。
 */
const displayRuns = computed<DetectTargetRun[]>(() => {
  if (job.job?.targets?.length) return job.job.targets
  return historyRun.value ? [historyRun.value] : []
})

/** 结果行：本轮作业用后端返回的 checks，历史报告用它自己跑过的项。 */
const activeCheckIds = computed<string[]>(() => {
  if (job.job?.checks?.length) return job.job.checks
  return (historyRun.value?.checks ?? []).map((c) => c.id)
})

/** 当前作业仍在内存里且未在跑时，才允许重试。 */
const allowRetry = computed(() => !!job.job && !job.running)

const highCostMessage = computed(() => {
  const high = highCostSelection(checks.value, effectiveChecks.value)
  return t('detect.highCostConfirm', { n: high.length, names: high.map((c) => c.title).join('、') })
})

function onTargetChange(payload: {
  targets: DetectTargetInput[]
  model: string
  authMode: string
  timeoutMs: number
  extraHeaders: Record<string, string>
}): void {
  targets.value = payload.targets
  runOptions.value = {
    model: payload.model,
    authMode: payload.authMode,
    timeoutMs: payload.timeoutMs,
    extraHeaders: payload.extraHeaders
  }
}

async function loadChecks(): Promise<void> {
  checksLoading.value = true
  try {
    const res = await detectApi.checks()
    checks.value = res.items || []
    defaults.value = res.defaults || []
    presets.value = res.presets || []
    const ccMax = presets.value.find((p) => p.id === 'cc_max')
    selected.value = ccMax ? [...ccMax.checks] : [...defaults.value]
  } catch (e) {
    app.showError(t('detect.loadFailed') + '：' + errorMessage(e))
  } finally {
    checksLoading.value = false
  }
}

async function loadAccounts(): Promise<void> {
  accountsLoading.value = true
  accountsError.value = ''
  try {
    const res = await detectApi.accounts()
    accounts.value = res.items || []
  } catch (e) {
    // 线上库不可用不该挡住手填目标，这里只提示不报错
    accountsError.value = t('detect.accountUnavailable')
  } finally {
    accountsLoading.value = false
  }
}

async function loadBaseline(): Promise<void> {
  try { baseline.value = (await detectApi.baseline()).baseline } catch (e) { app.showError(t('detect.loadFailed') + '：' + errorMessage(e)) }
}

async function createBaseline(accountId: number): Promise<void> {
  if (!accountId) return
  baselineLoading.value = true
  try {
    baseline.value = await detectApi.createBaseline({
      account_id: accountId,
      model: runOptions.value.model,
      auth_mode: runOptions.value.authMode,
      extra_headers: runOptions.value.extraHeaders,
      timeout_ms: runOptions.value.timeoutMs
    })
    if (baseline.value.status === 'passed' && baseline.value.quality_ok) {
      app.showSuccess(t('detect.baselineCreated'))
    } else {
      const status = baseline.value.exchange?.status
      app.showError(t('detect.baselineFailed') + (status && status >= 400 ? `（HTTP ${status}）` : ''))
    }
  } catch (e) {
    app.showError(t('detect.runFailed') + '：' + errorMessage(e))
  } finally {
    baselineLoading.value = false
  }
}

function confirmAndStart(): void {
  if (!targets.value.length) {
    app.showError(t('detect.noTarget'))
    return
  }
  if (!selected.value.length) {
    app.showError(t('detect.noCheck'))
    return
  }
  if (highCostSelection(checks.value, effectiveChecks.value).length) {
    showHighCostConfirm.value = true
    return
  }
  void startRun()
}

async function startRun(): Promise<void> {
  showHighCostConfirm.value = false
  historyRun.value = null
  const applies = baselineApplies(baseline.value, runOptions.value.model)
  if (baseline.value && !sameBaselineModel(baseline.value.model, runOptions.value.model)) {
    app.showWarning(t('detect.baselineModelMismatch', {
      baseline: baseline.value.model,
      current: runOptions.value.model || t('detect.modelEmpty')
    }))
  }
  await job.start({
    targets: targets.value,
    model: runOptions.value.model,
    auth_mode: runOptions.value.authMode,
    checks: selected.value,
    baseline_id: applies ? baseline.value?.id : undefined,
    extra_headers: runOptions.value.extraHeaders,
    timeout_ms: runOptions.value.timeoutMs,
    concurrency: concurrency.value
  })
}

function openDetail(payload: { run: DetectTargetRun; check: DetectCheckResult }): void {
  detailCheck.value = payload.check
  detailTarget.value = payload.run.name
  detailTargetIndex.value = displayRuns.value.indexOf(payload.run)
  showDetail.value = true
}

function retryChecks(payload: DetectRetryPayload): void {
  showDetail.value = false
  void job.retry(payload)
}

function retryOne(payload: { targetIndex: number; checkId: string }): void {
  retryChecks({ target_index: payload.targetIndex, check_id: payload.checkId })
}

function retryDetail(): void {
  if (!detailCheck.value) return
  retryChecks({ target_index: detailTargetIndex.value, check_id: detailCheck.value.id })
}

function retryFailed(): void {
  retryChecks({})
}

/** 载入历史即认为用户在看旧报告，清掉当轮结果避免两个时间点混在一起。 */
function showHistoryReport(id: number, report: DetectTargetRun): void {
  job.job = null
  historyRun.value = report
  loadedHistoryId.value = id
  resultTab.value = 'result'
}

function historyDeleteLabel(item: DetectHistoryItem): string {
  const name = item.target_name || item.account_name || item.model
  return name ? `${name} · ${item.created_at}` : item.created_at
}

function askDeleteHistory(item: DetectHistoryItem): void {
  pendingHistoryDelete.value = { id: item.id, label: historyDeleteLabel(item) }
  showHistoryDelete.value = true
}

async function confirmDeleteHistory(): Promise<void> {
  const pending = pendingHistoryDelete.value
  showHistoryDelete.value = false
  if (!pending || deletingHistory.value) return
  deletingHistory.value = true
  try {
    await detectApi.deleteHistory(pending.id)
    if (loadedHistoryId.value === pending.id) {
      loadedHistoryId.value = null
      historyRun.value = null
    }
    await history.load(history.page)
    if (!history.items.length && history.page > 1) {
      await history.load(history.page - 1)
    }
    app.showSuccess(t('common.success'))
  } catch (e) {
    app.showError(t('common.failed') + '：' + errorMessage(e))
  } finally {
    deletingHistory.value = false
    pendingHistoryDelete.value = null
  }
}

async function openHistory(item: DetectHistoryItem): Promise<void> {
  try {
    showHistoryReport(item.id, (await detectApi.historyDetail(item.id)).report)
  } catch (e) {
    app.showError(t('detect.loadFailed') + '：' + errorMessage(e))
  }
}

function exportReport(): void {
  const payload = {
    format: 'midstream-ops-model-detect',
    version: 1,
    generated_at: new Date().toISOString(),
    note: '报告不含 API Key；请求头与响应原文均已脱敏。',
    checks: activeCheckIds.value,
    targets: displayRuns.value
  }
  const blob = new Blob([JSON.stringify(payload, null, 2)], { type: 'application/json' })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = `model-detect-${new Date().toISOString().split(':').join('-')}.json`
  link.click()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
  app.showSuccess(t('detect.exported'))
}

onMounted(() => {
  void loadChecks()
  void loadAccounts()
  void loadBaseline()
  void history.load(1)
})
</script>
