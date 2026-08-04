<template>
  <div class="space-y-5">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <h1 class="text-xl font-bold text-gray-900 dark:text-white">{{ t('nav.stability') }}</h1>
    </div>

    <StabilityToolbar
      v-model:tab="tab"
      v-model:provider="providerFilter"
      v-model:health="healthFilter"
      v-model:keyword="keyword"
      v-model:minutes="minutes"
      :provider-opts="providerOpts"
      :health-opts="healthOpts"
      :loading="passiveLoading || activeLoading"
      @refresh="load"
    />

    <PassiveTable
      v-show="tab === 'passive'"
      :rows="passiveRows"
      :loading="passiveLoading"
      :grade-of="passiveGrade"
    />

    <ActiveTable
      v-show="tab === 'active'"
      :rows="activeRows"
      :loading="activeLoading"
      :budget-used="budgetUsed"
      :probing-id="probingId"
      :minutes="minutes"
      :health-of="healthOf"
      :grade-of="activeGrade"
      @probe="runProbe"
      @trend="openTrend"
      @timeline="openTimeline"
      @toggle-disabled="toggleDisabled"
    />

    <!-- 状态时间线弹窗 -->
    <BaseDialog :show="showTimeline" :title="timelineAccount?.account_name + ' · ' + t('health.timeline')" width="wide" @close="showTimeline = false">
      <LoadingState v-if="timelineLoading" />
      <EmptyState v-else-if="!timelineItems.length" icon="clock" :title="t('health.noEvents')" />
      <div v-else class="max-h-96 space-y-2 overflow-y-auto">
        <div v-for="e in timelineItems" :key="e.id" class="flex items-start gap-3 rounded-lg border border-gray-100 p-3 dark:border-dark-800">
          <div class="mt-1 flex flex-col items-center">
            <span class="badge" :class="healthBadge(e.to_state)">{{ healthLabel(e.to_state) }}</span>
          </div>
          <div class="min-w-0 flex-1">
            <p class="text-sm text-gray-900 dark:text-white">
              {{ healthLabel(e.from_state) }} → {{ healthLabel(e.to_state) }}
              <span class="ml-1 text-xs text-gray-400">{{ e.reason }}</span>
            </p>
            <p v-if="e.detail" class="mt-0.5 truncate text-xs text-red-500" :title="e.detail">{{ e.detail }}</p>
            <p class="mt-0.5 text-xs text-gray-400">{{ e.created_at }}</p>
          </div>
        </div>
      </div>
    </BaseDialog>

    <!-- 趋势弹窗 -->
    <BaseDialog :show="showTrend" :title="trendAccount?.account_name + ' · ' + t('stability.viewTrend')" width="extra-wide" @close="showTrend = false">
      <LoadingState v-if="trendLoading" />
      <div v-else>
        <div v-if="trendItems.length" class="mb-4">
          <LineChart :labels="trendLabels" :datasets="trendDatasets" :height="240" />
        </div>
        <div class="table-wrapper">
          <table class="table">
            <thead>
              <tr>
                <th>{{ t('stability.time') }}</th><th>{{ t('stability.result') }}</th>
                <th class="text-right">{{ t('stability.ttft') }}</th><th class="text-right">{{ t('stability.totalTime') }}</th>
                <th>{{ t('stability.source') }}</th><th>Error</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="it in trendItems" :key="it.id">
                <td class="text-xs">{{ it.created_at }}</td>
                <td><span :class="it.success ? 'text-emerald-600' : 'text-red-500'">{{ it.success ? '✓' : '✗' }}</span></td>
                <td class="text-right">{{ fmtMs(it.ttft_ms) }}</td>
                <td class="text-right">{{ fmtMs(it.total_ms) }}</td>
                <td class="text-xs text-gray-500">{{ it.source }}</td>
                <td><span class="block max-w-[200px] truncate text-xs text-red-500" :title="it.error || ''">{{ it.error || '-' }}</span></td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </BaseDialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { stabilityApi } from '@/api'
import { errorMessage } from '@/api/client'
import { fmtMs } from '@/utils/format'
import { useAppStore } from '@/stores/app'
import {
  filterRows,
  searchStabilityRows,
  providerOptions,
  healthOptions,
  rowGrade,
  type FilterableRow,
  type SearchableRow,
  type RowGrade,
  type WindowMinutes
} from '@/utils/stabilityModel'
import BaseDialog from '@/components/common/BaseDialog.vue'
import LineChart from '@/components/LineChart.vue'
import LoadingState from '@/components/common/LoadingState.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import StabilityToolbar from '@/components/stability/StabilityToolbar.vue'
import PassiveTable from '@/components/stability/PassiveTable.vue'
import ActiveTable from '@/components/stability/ActiveTable.vue'
import type { HealthEventItem, HealthStateItem, PassiveRow, ProbeSummaryRow, ProbeResult } from '@/types'

const { t } = useI18n()
const app = useAppStore()

/**
 * 默认落在被动统计：它查的是真实流量，任何时候都有数据；
 * 主动探测每 15 分钟才一轮，短窗口下样本稀疏，不适合当首屏。
 */
const tab = ref<'passive' | 'active'>('passive')
const minutes = ref<WindowMinutes>(1440)

// null = 不限。provider 用 '' 表示「未归属」桶，故不能用 '' 表示不限。
const providerFilter = ref<string | null>(null)
const healthFilter = ref<string | null>(null)
const keyword = ref('')

const passive = ref<PassiveRow[]>([])
const passiveLoading = ref(false)
const summary = ref<ProbeSummaryRow[]>([])
const activeLoading = ref(false)
const probingId = ref<number | null>(null)

// 健康状态
const healthStates = ref<Map<number, HealthStateItem>>(new Map())
const budgetUsed = ref(0)

function healthOf(accountId: number): HealthStateItem | undefined {
  return healthStates.value.get(accountId)
}
function healthStateOf(accountId: number): string | undefined {
  return healthStates.value.get(accountId)?.state
}

// 时间线弹窗用；主动表自带一份（那里还要渲染 tooltip），此处只需 badge 与文案
function healthBadge(state?: string): string {
  switch (state) {
    case 'healthy': return 'badge-success'
    case 'degraded': return 'badge-warning'
    case 'suspended': return 'badge-danger'
    case 'observing': return 'badge-purple'
    case 'recovering': return 'badge-primary'
    case 'disabled': return 'badge-gray'
    default: return 'badge-success'
  }
}
function healthLabel(state?: string): string {
  if (!state) return t('health.states.healthy')
  return t('health.states.' + state)
}

/**
 * 筛选选项取自「当前 tab 的全量行」而非筛选后的行 ——
 * 否则选中某个供应商后其他供应商的 pill 会消失，用户无法切换回去。
 */
const optionSource = computed<{ account_id: number; provider_name: string }[]>(() =>
  tab.value === 'passive' ? passive.value : summary.value
)
const providerOpts = computed(() => providerOptions(optionSource.value))
const healthOpts = computed(() => healthOptions(optionSource.value, healthStateOf))

/**
 * 筛选在排序之前；排序由各子表内部的 useTableSort 负责。
 * pill 筛选与文本搜索都是行级谓词，先后顺序不影响结果。
 *
 * 注意 optionSource 刻意不跟随搜索（见上）—— 否则搜索会让 pill 消失，
 * 而 StabilityToolbar 在选项只剩一个时会整组丢弃，用户就失去了清除筛选的入口。
 */
function visibleRows<T extends FilterableRow & SearchableRow>(rows: T[]): T[] {
  return searchStabilityRows(
    filterRows(rows, providerFilter.value, healthFilter.value, healthStateOf),
    keyword.value
  )
}

const passiveRows = computed(() => visibleRows(passive.value))
const activeRows = computed(() => visibleRows(summary.value))

/**
 * 被动口径没有成功率（线上 usage_logs 只记成功请求），该维度弃权，
 * 评级由首字延迟与健康状态合成。
 */
function passiveGrade(r: PassiveRow): RowGrade {
  return rowGrade({ ttftMs: r.first_token_p50, healthState: healthStateOf(r.account_id) })
}

function activeGrade(r: ProbeSummaryRow): RowGrade {
  return rowGrade({
    ttftMs: r.avg_ttft_ms,
    successRate: r.success_rate,
    healthState: healthStateOf(r.account_id)
  })
}

/**
 * 切 tab 时清掉两个 pill 筛选：两张表的账号集合不同（被动只有有流量的，
 * 主动只有开了探测的），沿用旧筛选很容易得到一张空表却看不出为什么。
 *
 * 搜索词刻意豁免：查询词通常是账号名，而同一账号两个 tab 都有 ——
 * 「看 X 的被动数据，再看它的探测结果」正是这页的真实工作流，清空会与之对抗。
 */
watch(tab, () => {
  providerFilter.value = null
  healthFilter.value = null
})

async function loadHealth() {
  try {
    const res = await stabilityApi.healthStates()
    const m = new Map<number, HealthStateItem>()
    for (const it of res.items) m.set(it.account_id, it)
    healthStates.value = m
    budgetUsed.value = res.budget_used
  } catch {
    // 健康状态是增强信息，失败不打断主列表
  }
}

async function toggleDisabled(r: ProbeSummaryRow) {
  const cur = healthOf(r.account_id)
  const target = cur?.state !== 'disabled'
  try {
    await stabilityApi.setHealthDisabled(r.account_id, target)
    app.showSuccess(target ? t('health.disabledOk') : t('health.enabledOk'))
    await loadHealth()
  } catch (e) {
    app.showError(errorMessage(e))
  }
}

// 状态时间线
const showTimeline = ref(false)
const timelineLoading = ref(false)
const timelineItems = ref<HealthEventItem[]>([])
const timelineAccount = ref<ProbeSummaryRow | null>(null)

async function openTimeline(r: ProbeSummaryRow) {
  timelineAccount.value = r
  showTimeline.value = true
  timelineLoading.value = true
  timelineItems.value = []
  try {
    const res = await stabilityApi.healthEvents(r.account_id)
    timelineItems.value = res.items
  } catch (e) {
    app.showError(errorMessage(e))
  } finally {
    timelineLoading.value = false
  }
}

const showTrend = ref(false)
const trendLoading = ref(false)
const trendItems = ref<ProbeResult[]>([])
const trendAccount = ref<ProbeSummaryRow | null>(null)

const trendLabels = computed(() => trendItems.value.map((i) => (i.created_at || '').slice(5, 16)))
const trendDatasets = computed(() => [
  { label: t('stability.ttft'), data: trendItems.value.map((i) => i.ttft_ms ?? 0), borderColor: '#14b8a6' },
  { label: t('stability.totalTime'), data: trendItems.value.map((i) => i.total_ms ?? 0), borderColor: '#8b5cf6' }
])

async function loadPassive() {
  passiveLoading.value = true
  try {
    const res = await stabilityApi.passive(minutes.value)
    passive.value = res.items || []
  } catch (e) {
    app.showError(errorMessage(e))
  } finally {
    passiveLoading.value = false
  }
}

async function loadSummary() {
  activeLoading.value = true
  try {
    const res = await stabilityApi.probeSummary(minutes.value)
    summary.value = res.items || []
  } catch (e) {
    app.showError(errorMessage(e))
  } finally {
    activeLoading.value = false
  }
}

async function load() {
  await Promise.all([loadPassive(), loadSummary(), loadHealth()])
}

// 窗口档位变更要重新取数（筛选是本地的，不必重新请求）
watch(minutes, load)

async function runProbe(r: ProbeSummaryRow) {
  probingId.value = r.account_id
  try {
    const res = await stabilityApi.runProbe({ account_id: r.account_id })
    const pr = res as ProbeResult
    const msg = `${r.account_name}: ${pr.success ? '✓' : '✗'} TTFT=${fmtMs(pr.ttft_ms)} 总耗时=${fmtMs(pr.total_ms)}${pr.error ? ' · ' + pr.error : ''}`
    // 探测本身完成即请求成功；探测结果失败（pr.success=false）属业务信息，用红色提醒
    if (pr.success) {
      app.showSuccess(msg)
    } else {
      app.showError(msg)
    }
    await Promise.all([loadSummary(), loadHealth()])
  } catch (e) {
    app.showError(errorMessage(e))
  } finally {
    probingId.value = null
  }
}

async function openTrend(r: ProbeSummaryRow) {
  trendAccount.value = r
  showTrend.value = true
  trendLoading.value = true
  trendItems.value = []
  try {
    const res = await stabilityApi.probeTrend(r.account_id, minutes.value)
    trendItems.value = res.items || []
  } catch (e) {
    app.showError(errorMessage(e))
  } finally {
    trendLoading.value = false
  }
}

onMounted(load)
</script>
