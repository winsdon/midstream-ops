<template>
  <div class="space-y-6">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <h1 class="text-xl font-bold text-gray-900 dark:text-white">{{ t('nav.dashboard') }}</h1>
      <div class="flex flex-wrap items-center gap-2">
        <DateRangePicker v-model="range" @change="onRangeChange" />
        <button class="btn btn-secondary text-sm" @click="load" :disabled="loading">
          <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
          {{ loading ? t('common.loading') : t('common.refresh') }}
        </button>
      </div>
    </div>

    <!-- 汇总卡片 -->
    <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
      <StatCard
        :title="t('dash.revenue')"
        :value="displayMoney(summary?.revenue)"
        icon="dollar"
        icon-variant="success"
        class="animate-fade-in"
      >
        <template #footer>
          <DeltaHint v-if="!privacyMode" :delta="revenueDelta" :formatter="fmtMoney" />
        </template>
      </StatCard>
      <StatCard
        :title="t('dash.cost')"
        :value="displayMoney(summary?.cost)"
        icon="creditCard"
        icon-variant="warning"
        class="animate-fade-in"
      >
        <template #title-suffix>
          <span class="cursor-help text-gray-400" :title="t('cost.actualHint')">ⓘ</span>
        </template>
        <template #footer>
          <!-- 运营成本单列：与上游实扣口径不同（固定 vs 变动成本），合并会掩盖构成 -->
          <p
            v-if="(summary?.operating_cost || 0) > 0"
            class="text-xs text-gray-500"
            :title="t('opcost.statHint')"
          >
            + {{ t('opcost.title') }} {{ displayMoney(summary?.operating_cost) }}
          </p>
          <!-- 成本上涨是坏消息，配色反转 -->
          <DeltaHint v-if="!privacyMode" :delta="costDelta" :formatter="fmtMoney" negative-when-up />
        </template>
      </StatCard>
      <StatCard
        :title="t('dash.profit')"
        :value="displayMoney(summary?.profit)"
        :value-class="displayMoneyClass(summary?.profit)"
        icon="trendingUp"
        :icon-variant="!privacyMode && !costComplete ? 'danger' : 'primary'"
        class="animate-fade-in"
      >
        <template #title-suffix>
          <span v-if="!privacyMode && !costComplete" class="text-amber-500" :title="t('cost.profitOverstated')">⚠</span>
        </template>
        <template #footer>
          <p v-if="!privacyMode && !costComplete" class="mt-0.5 text-xs text-amber-600 dark:text-amber-400">
            {{ t('cost.upperBound') }}
          </p>
          <div v-else-if="!privacyMode" class="flex items-center gap-2">
            <DeltaHint :delta="profitDelta" :formatter="fmtMoney" />
            <span v-if="margin !== null" class="text-xs text-gray-400">
              {{ t('dash.margin') }} {{ fmtPct(margin) }}
            </span>
          </div>
        </template>
      </StatCard>
      <StatCard :title="t('dash.requests')" :value="fmtNum(summary?.requests)" icon="bolt" class="animate-fade-in" />
      <StatCard :title="t('dash.providers')" :value="fmtNum(summary?.provider_count)" icon="server" class="animate-fade-in" />
      <StatCard :title="t('dash.accounts')" :value="fmtNum(summary?.account_count)" icon="users" class="animate-fade-in" />
    </div>

    <!-- 成本来源与新鲜度 -->
    <CostSyncBar v-if="!privacyMode" :sync="summary?.cost_sync" :complete="costComplete" :accounts-missing="summary?.accounts_missing || 0" />

    <div class="grid gap-6 xl:grid-cols-5">
      <!-- 趋势图 -->
      <div class="card p-5 xl:col-span-3">
        <div class="mb-4 flex items-center justify-between">
          <h2 class="text-sm font-semibold text-gray-700 dark:text-dark-300">{{ t('dash.trendTitle2') }}</h2>
          <span class="text-xs text-gray-400">{{ range.start }} ~ {{ range.end }}</span>
        </div>
        <div v-if="trendLoading" class="flex h-[300px] items-center justify-center"><LoadingState /></div>
        <div v-else-if="privacyMode" class="flex h-[300px] items-center justify-center text-sm text-gray-400">
          {{ t('privacy.hidden') }}
        </div>
        <LineChart v-else :labels="trendLabels" :datasets="trendDatasets" :height="300" />
        <p v-if="!privacyMode && incompleteDays.length" class="mt-3 text-xs text-amber-600 dark:text-amber-400">
          ⚠ {{ t('cost.trendIncomplete', { days: incompleteDays.join('、') }) }}
        </p>
      </div>

      <!-- 分组贡献 -->
      <div class="card p-5 xl:col-span-2">
        <div class="mb-4 flex items-center justify-between">
          <h2 class="text-sm font-semibold text-gray-700 dark:text-dark-300">{{ t('dash.groupContribution') }}</h2>
          <span v-if="!privacyMode && concentration !== null" class="text-xs text-gray-400" :title="t('dash.concentrationHint')">
            {{ t('dash.top3', { pct: concentration.toFixed(0) }) }}
          </span>
        </div>
        <div v-if="loading" class="flex h-[240px] items-center justify-center"><LoadingState /></div>
        <div v-else-if="privacyMode" class="flex h-[240px] items-center justify-center text-sm text-gray-400">
          {{ t('privacy.hidden') }}
        </div>
        <EmptyState v-else-if="!groups.length" icon="chartBar" />
        <BarChart
          v-else
          :labels="groups.map((g) => g.group_name)"
          :data="groups.map((g) => g.revenue)"
          :height="240"
          :value-formatter="fmtMoney"
        />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { dashboardApi } from '@/api'
import { errorMessage } from '@/api/client'
import { fmtMoney, fmtNum, fmtPct, todayStr } from '@/utils/format'
import { profitMargin } from '@/utils/profitMargin'
import { useAppStore } from '@/stores/app'
import { usePrivacyMoney } from '@/composables/usePrivacyMoney'
import Icon from '@/components/icons/Icon.vue'
import LineChart from '@/components/LineChart.vue'
import BarChart from '@/components/BarChart.vue'
import DateRangePicker, { type DateRange } from '@/components/DateRangePicker.vue'
import DeltaHint from '@/components/common/DeltaHint.vue'
import CostSyncBar from '@/components/CostSyncBar.vue'
import StatCard from '@/components/common/StatCard.vue'
import LoadingState from '@/components/common/LoadingState.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import type { DashboardSummary, TrendPoint } from '@/types'

const { t } = useI18n()
const app = useAppStore()
const { privacyMode, displayMoney, displayMoneyClass } = usePrivacyMoney()
const summary = ref<DashboardSummary | null>(null)
const trend = ref<TrendPoint[]>([])
const range = ref<DateRange>({ start: todayStr(), end: todayStr() })
const loading = ref(false)
const trendLoading = ref(false)

// 数据未加载时不显示警告，避免闪烁误报
const costComplete = computed(() => summary.value?.cost_complete !== false)

const groups = computed(() => summary.value?.groups || [])
const concentration = computed(() =>
  summary.value?.group_concentration != null && groups.value.length ? summary.value.group_concentration : null
)

const margin = computed(() => profitMargin(summary.value?.revenue, summary.value?.profit))

// 环比：趋势序列末两点之差（区间内最后一天 vs 前一天）
function deltaOf(pick: (p: TrendPoint) => number): number | null {
  const v = trend.value
  if (v.length < 2) return null
  return pick(v[v.length - 1]) - pick(v[v.length - 2])
}
const revenueDelta = computed(() => deltaOf((p) => p.revenue))
// 成本环比含运营成本：与趋势图的成本线同口径，也才和利润环比自洽
// （否则「成本涨了 10、利润跌了 60」会让人以为算错）
const costDelta = computed(() => deltaOf((p) => p.cost + (p.operating_cost || 0)))
const profitDelta = computed(() => deltaOf((p) => p.profit))

const trendLabels = computed(() => trend.value.map((p) => p.day.slice(5)))
// 成本线取「实扣 + 运营成本」而非单画实扣：利润线已扣掉运营成本，
// 若成本线只画实扣，图上「收益 − 成本」会对不上利润线，差额凭空消失。
// 指标卡把两者分行展示（构成清晰），图上则合并（关系自洽）—— 两处口径不同是有意的。
const trendDatasets = computed(() => [
  { label: t('stats.revenue'), data: trend.value.map((p) => p.revenue), borderColor: '#14b8a6' },
  { label: t('dash.cost'), data: trend.value.map((p) => p.cost + (p.operating_cost || 0)), borderColor: '#f59e0b' },
  { label: t('stats.profit'), data: trend.value.map((p) => p.profit), borderColor: '#8b5cf6' }
])

// 无上游成本记录的日期：这些点的利润等于收益，不可直接采信
const incompleteDays = computed(() =>
  trend.value.filter((p) => !p.cost_complete).map((p) => p.day.slice(5))
)

async function loadSummary() {
  try {
    summary.value = await dashboardApi.summary(range.value.start, range.value.end)
  } catch (e) {
    app.showError(errorMessage(e))
  }
}

async function loadTrend() {
  trendLoading.value = true
  try {
    // 单日区间的趋势图只有一个点，没有可读性：至少拉 7 天做背景
    const start = range.value.start === range.value.end ? minusDays(range.value.end, 6) : range.value.start
    const res = await dashboardApi.trendRange(start, range.value.end)
    trend.value = res.points || []
  } catch (e) {
    app.showError(errorMessage(e))
  } finally {
    trendLoading.value = false
  }
}

function minusDays(date: string, n: number): string {
  const d = new Date(date + 'T00:00:00')
  d.setDate(d.getDate() - n)
  return d.toISOString().slice(0, 10)
}

function onRangeChange() {
  load()
}

async function load() {
  loading.value = true
  await Promise.all([loadSummary(), loadTrend()])
  loading.value = false
}

onMounted(load)
</script>
