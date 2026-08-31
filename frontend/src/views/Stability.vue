<template>
  <div class="space-y-5">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <h1 class="text-xl font-bold text-gray-900 dark:text-white">{{ t('nav.stability') }}</h1>
    </div>

    <StabilityToolbar
      v-model:tab="tab"
      v-model:grouping="grouping"
      v-model:provider="providerFilter"
      v-model:health="healthFilter"
      v-model:group="groupFilter"
      v-model:keyword="keyword"
      v-model:minutes="minutes"
      :provider-opts="providerOpts"
      :health-opts="healthOpts"
      :group-opts="groupOpts"
      :loading="passiveLoading || activeLoading"
      @refresh="load"
    />

    <StabilityListHeader
      :all-open="allOpen"
      :hint="tab === 'passive' ? t('stability.passiveHint') : undefined"
      :sort="listSort"
      @toggle-all="toggleAll"
      @update:sort="listSort = $event"
    />

    <div
      v-if="tab === 'active' && minutes < PROBE_INTERVAL_MINUTES"
      class="rounded-lg bg-amber-50 px-4 py-2 text-xs text-amber-700 dark:bg-amber-900/20 dark:text-amber-400"
    >
      {{ t('stability.probeSparseHint', { n: PROBE_INTERVAL_MINUTES }) }}
    </div>
    <div
      v-if="tab === 'active' && budgetUsed > 0"
      class="rounded-lg bg-gray-50 px-4 py-2 text-xs text-gray-500 dark:bg-dark-800/50 dark:text-dark-400"
    >
      {{ t('health.budgetUsed', { n: budgetUsed }) }}
    </div>

    <StabilityBlocks
      v-if="tab === 'passive'"
      :sections="passiveSections"
      :loading="passiveLoading"
      :grouping="grouping"
      :show-grade="false"
      :compact="true"
      :selectable="true"
      :expand-cmd="expandCmd"
      :open-detail="openPassiveSection"
      @update:all-open="allOpen = $event"
    >
      <template #table="{ rows }">
        <PassiveCards
          :rows="rows"
          :loading="passiveLoading && !passiveSections.length"
          :minutes="minutes"
          :secondary="rowSecondary"
          :sort-key="listSort.key"
          :sort-order="listSort.order"
          @select="openPassiveDetail"
        />
      </template>
    </StabilityBlocks>

    <StabilityBlocks
      v-if="tab === 'active'"
      :sections="activeSections"
      :loading="activeLoading"
      :grouping="grouping"
      :expand-cmd="expandCmd"
      @update:all-open="allOpen = $event"
    >
      <template #table="{ rows }">
        <ActiveTable
          :rows="rows"
          :loading="activeLoading && !activeSections.length"
          :probing-id="probingId"
          :secondary="rowSecondary"
          :health-of="healthOf"
          :grade-of="activeGrade"
          @probe="runProbe"
          @trend="openTrend"
          @timeline="openTimeline"
          @toggle-disabled="toggleDisabled"
        />
      </template>
    </StabilityBlocks>

    <PassiveDetailDialog
      :show="showPassiveDetail"
      :detail="passiveDetail"
      :minutes="minutes"
      @close="showPassiveDetail = false"
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
  groupOptions,
  rowGrade,
  PASSIVE_RATE_BANDS,
  DEFAULT_WINDOW_MINUTES,
  PROBE_INTERVAL_MINUTES,
  type FilterableRow,
  type SearchableRow,
  type RowGrade,
  type WindowMinutes
} from '@/utils/stabilityModel'
import {
  applySectionSort,
  buildSections,
  passiveCounts,
  activeCounts,
  DEFAULT_STABILITY_SORT,
  type GroupingMode,
  type StabilitySection,
  type StabilitySort
} from '@/utils/stabilitySections'
import {
  aggregatePassiveRows,
  passiveDetailFromRow,
  type PassiveDetail
} from '@/utils/stabilityMetrics'
import BaseDialog from '@/components/common/BaseDialog.vue'
import LineChart from '@/components/LineChart.vue'
import LoadingState from '@/components/common/LoadingState.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import StabilityToolbar from '@/components/stability/StabilityToolbar.vue'
import StabilityListHeader from '@/components/stability/StabilityListHeader.vue'
import StabilityBlocks from '@/components/stability/StabilityBlocks.vue'
import PassiveCards from '@/components/stability/PassiveCards.vue'
import PassiveDetailDialog from '@/components/stability/PassiveDetailDialog.vue'
import ActiveTable from '@/components/stability/ActiveTable.vue'
import type { HealthEventItem, HealthStateItem, PassiveRow, ProbeSummaryRow, ProbeResult } from '@/types'

const { t } = useI18n()
const app = useAppStore()

/**
 * 默认落在被动统计：它查的是真实流量，任何时候都有数据；
 * 主动探测每 15 分钟才一轮，短窗口下样本稀疏，不适合当首屏。
 */
const tab = ref<'passive' | 'active'>('passive')
const grouping = ref<GroupingMode>('provider')
const minutes = ref<WindowMinutes>(DEFAULT_WINDOW_MINUTES)

// null = 不限。provider / group 用 '' 表示空桶，故不能用 '' 表示不限。
const providerFilter = ref<string | null>(null)
const healthFilter = ref<string | null>(null)
const groupFilter = ref<string | null>(null)
const keyword = ref('')

const passive = ref<PassiveRow[]>([])
const passiveLoading = ref(false)
const summary = ref<ProbeSummaryRow[]>([])
const activeLoading = ref(false)
const probingId = ref<number | null>(null)
const showPassiveDetail = ref(false)
const passiveDetail = ref<PassiveDetail | null>(null)
const expandCmd = ref<{ open: boolean } | null>(null)
const allOpen = ref(false)
const listSort = ref<StabilitySort>({ ...DEFAULT_STABILITY_SORT })

function toggleAll() {
  expandCmd.value = { open: !allOpen.value }
}

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
const optionSource = computed<FilterableRow[]>(() =>
  tab.value === 'passive' ? passive.value : summary.value
)
const providerOpts = computed(() => providerOptions(optionSource.value))
const healthOpts = computed(() => healthOptions(optionSource.value, healthStateOf))
const groupOpts = computed(() => groupOptions(optionSource.value))

const rowSecondary = computed<'groups' | 'provider'>(() =>
  grouping.value === 'provider' ? 'groups' : 'provider'
)

/**
 * 筛选在排序之前；排序由各子表内部的 useTableSort 负责。
 * 下拉筛选与文本搜索都是行级谓词，先后顺序不影响结果。
 *
 * 注意 optionSource 刻意不跟随搜索（见上）—— 否则搜索会让 pill 消失，
 * 而 StabilityToolbar 在选项只剩一个时会整组丢弃，用户就失去了清除筛选的入口。
 */
function visibleRows<T extends FilterableRow & SearchableRow>(rows: T[]): T[] {
  return searchStabilityRows(
    filterRows(rows, providerFilter.value, healthFilter.value, healthStateOf, groupFilter.value),
    keyword.value
  )
}

const passiveRows = computed(() => visibleRows(passive.value))
const activeRows = computed(() => visibleRows(summary.value))

const passiveSections = computed(() =>
  applySectionSort(
    buildSections(passiveRows.value, grouping.value, passiveGrade, passiveCounts),
    listSort.value.key,
    listSort.value.order
  )
)
const activeSections = computed(() =>
  applySectionSort(
    buildSections(activeRows.value, grouping.value, activeGrade, activeCounts),
    listSort.value.key,
    listSort.value.order
  )
)

/** 被动卡用流量 SLA + 首字；不吃探测健康状态（那是主动表的事）。 */
function passiveGrade(r: PassiveRow): RowGrade {
  return rowGrade({
    ttftMs: r.first_token_p50,
    successRate: r.sla,
    rateBands: PASSIVE_RATE_BANDS
  })
}

function activeGrade(r: ProbeSummaryRow): RowGrade {
  return rowGrade({
    ttftMs: r.avg_ttft_ms,
    successRate: r.success_rate,
    healthState: healthStateOf(r.account_id)
  })
}

/**
 * 切 tab 时清掉筛选：两张表的账号集合不同（被动只有有流量的，
 * 主动只有开了探测的），沿用旧筛选很容易得到一张空表却看不出为什么。
 *
 * 搜索词刻意豁免：查询词通常是账号名，而同一账号两个 tab 都有 ——
 * 「看 X 的被动数据，再看它的探测结果」正是这页的真实工作流，清空会与之对抗。
 */
watch(tab, (next) => {
  providerFilter.value = null
  healthFilter.value = null
  groupFilter.value = null
  showPassiveDetail.value = false
  if (next === 'active' && !summary.value.length) void loadSummary()
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
    passive.value = (res.items || []).map((r) => {
      const success = r.success_count ?? r.requests ?? 0
      const error = r.error_count ?? 0
      return {
        ...r,
        groups: r.groups ?? [],
        success_count: success,
        error_count: error,
        requests: success + error,
        sla: r.sla ?? null
      }
    })
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
    summary.value = (res.items || []).map((r) => ({ ...r, groups: r.groups ?? [] }))
  } catch (e) {
    app.showError(errorMessage(e))
  } finally {
    activeLoading.value = false
  }
}

function sectionTitle(sec: StabilitySection<PassiveRow>): string {
  if (sec.label) return sec.label
  return grouping.value === 'group' ? t('stability.ungrouped') : t('stability.unassigned')
}

function openPassiveDetail(r: PassiveRow) {
  passiveDetail.value = passiveDetailFromRow(r)
  showPassiveDetail.value = true
}

function openPassiveSection(sec: StabilitySection<PassiveRow>) {
  passiveDetail.value = aggregatePassiveRows(sec.rows, sectionTitle(sec))
  showPassiveDetail.value = true
}

async function load() {
  if (tab.value === 'passive') {
    await Promise.all([loadPassive(), loadHealth()])
    return
  }
  await Promise.all([loadSummary(), loadHealth()])
}

// 窗口档位变更要重新取数（筛选是本地的，不必重新请求）
watch(minutes, () => {
  showPassiveDetail.value = false
  void load()
})

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
