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
          :job="job"
          :running="running"
          :disabled="!targets.length || !selected.length"
          :request-estimate="requestEstimate"
          :concurrency="concurrency"
          @update:concurrency="concurrency = $event"
          @start="confirmAndStart"
          @cancel="cancelRun"
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
              :allow-retry="!!job && !running"
              @select="openDetail"
              @export="exportReport"
              @retry="retryOne"
            />
          </template>
        </template>

        <DetectHistoryTable
          v-else
          :items="history"
          :loading="historyLoading"
          :page="historyPage"
          :pages="historyPages"
          :total="historyTotal"
          @open="openHistory"
          @update:page="loadHistory"
        />
      </div>
    </div>

    <DetectCheckDrawer
      :show="showDetail"
      :check="detailCheck"
      :target-name="detailTarget"
      :allow-retry="!!job && !running"
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
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { detectApi } from '@/api'
import { errorMessage } from '@/api/client'
import { useAppStore } from '@/stores/app'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import DetectTargetPanel from '@/components/detect/DetectTargetPanel.vue'
import DetectCheckPicker from '@/components/detect/DetectCheckPicker.vue'
import DetectRunBar from '@/components/detect/DetectRunBar.vue'
import DetectVerdictCard from '@/components/detect/DetectVerdictCard.vue'
import DetectCheckMatrix from '@/components/detect/DetectCheckMatrix.vue'
import DetectCheckDrawer from '@/components/detect/DetectCheckDrawer.vue'
import DetectHistoryTable from '@/components/detect/DetectHistoryTable.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import { DEFAULT_DETECT_CONCURRENCY, estimateRequests, highCostSelection, withDependencies } from '@/utils/detectModel'
import type {
  DetectAccount,
  DetectCheckMeta,
  DetectCheckResult,
  DetectHistoryItem,
  DetectJob,
  DetectPreset,
  DetectTargetInput,
  DetectTargetRun
} from '@/types/detect'

const { t } = useI18n()
const app = useAppStore()

/** 作业轮询间隔：要跟上「执行中」格子，间隔不宜超过 1s。 */
const POLL_INTERVAL_MS = 800
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

const targets = ref<DetectTargetInput[]>([])
const runOptions = ref({ model: '', authMode: 'both', timeoutMs: 90000, extraHeaders: {} as Record<string, string> })

const job = ref<DetectJob | null>(null)
const running = ref(false)
let pollTimer: ReturnType<typeof setInterval> | null = null

/** 从历史里载入的报告：与当轮结果共用同一套展示组件。 */
const historyRun = ref<DetectTargetRun | null>(null)

const history = ref<DetectHistoryItem[]>([])
const historyLoading = ref(false)
const historyPage = ref(1)
const historyPages = ref(1)
const historyTotal = ref(0)

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
  if (job.value?.targets?.length) return job.value.targets
  return historyRun.value ? [historyRun.value] : []
})

/** 矩阵行：本轮作业用后端返回的 checks，历史报告用它自己跑过的项。 */
const activeCheckIds = computed<string[]>(() => {
  if (job.value?.checks?.length) return job.value.checks
  return (historyRun.value?.checks ?? []).map((c) => c.id)
})

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

async function loadHistory(page = 1): Promise<void> {
  historyLoading.value = true
  try {
    const res = await detectApi.history({ page, page_size: HISTORY_PAGE_SIZE })
    history.value = res.items || []
    historyPage.value = res.page
    historyPages.value = res.pages
    historyTotal.value = res.total
  } catch (e) {
    app.showError(t('detect.loadFailed') + '：' + errorMessage(e))
  } finally {
    historyLoading.value = false
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
  running.value = true
  historyRun.value = null
  job.value = null
  try {
    const res = await detectApi.run({
      targets: targets.value,
      model: runOptions.value.model,
      auth_mode: runOptions.value.authMode,
      checks: selected.value,
      extra_headers: runOptions.value.extraHeaders,
      timeout_ms: runOptions.value.timeoutMs,
      concurrency: concurrency.value
    })
    startPolling(res.job_id)
  } catch (e) {
    running.value = false
    app.showError(t('detect.runFailed') + '：' + errorMessage(e))
  }
}

function startPolling(jobId: string): void {
  stopPolling()
  const tick = async (): Promise<void> => {
    try {
      const snap = await detectApi.job(jobId)
      job.value = snap
      if (snap.status !== 'running') {
        stopPolling()
        running.value = false
        app.showSuccess(snap.status === 'cancelled' ? t('detect.cancelled') : t('detect.completed'))
        void loadHistory(1)
      }
    } catch (e) {
      // 单次轮询失败不终止：可能只是网络抖动，下一拍会补上
      if (errorMessage(e).includes('404')) {
        stopPolling()
        running.value = false
      }
    }
  }
  void tick()
  pollTimer = setInterval(() => void tick(), POLL_INTERVAL_MS)
}

function stopPolling(): void {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

async function cancelRun(): Promise<void> {
  if (!job.value) return
  try {
    await detectApi.cancel(job.value.id)
  } catch (e) {
    app.showError(errorMessage(e))
  }
}

function openDetail(payload: { run: DetectTargetRun; check: DetectCheckResult }): void {
  detailCheck.value = payload.check
  detailTarget.value = payload.run.name
  detailTargetIndex.value = displayRuns.value.indexOf(payload.run)
  showDetail.value = true
}

async function retryOne(payload: { targetIndex: number; checkId: string }): Promise<void> {
  await retryChecks({ target_index: payload.targetIndex, check_id: payload.checkId })
}

function retryDetail(): void {
  if (!detailCheck.value) return
  void retryChecks({ target_index: detailTargetIndex.value, check_id: detailCheck.value.id })
}

function retryFailed(): void {
  void retryChecks({})
}

async function retryChecks(payload: { target_index?: number; check_id?: string }): Promise<void> {
  if (!job.value) return
  showDetail.value = false
  running.value = true
  try {
    await detectApi.retry(job.value.id, payload)
    startPolling(job.value.id)
  } catch (e) {
    running.value = false
    app.showError(t('detect.retryFailedStart') + '：' + errorMessage(e))
  }
}

async function openHistory(item: DetectHistoryItem): Promise<void> {
  try {
    const detail = await detectApi.historyDetail(item.id)
    // 载入历史即认为用户在看旧报告，清掉当轮结果避免两个时间点混在一起
    job.value = null
    historyRun.value = detail.report
    resultTab.value = 'result'
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
  void loadHistory(1)
})

onUnmounted(stopPolling)
</script>
