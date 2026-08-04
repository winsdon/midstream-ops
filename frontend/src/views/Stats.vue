<template>
  <div class="space-y-5">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <h1 class="text-xl font-bold text-gray-900 dark:text-white">{{ t('nav.stats') }}</h1>
      <div class="flex flex-wrap items-center gap-2">
        <!-- 维度切换 -->
        <div class="flex rounded-lg bg-gray-100 p-0.5 dark:bg-dark-800">
          <button
            class="rounded-md px-3 py-1.5 text-sm font-medium transition-colors"
            :class="dim === 'provider' ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-700 dark:text-primary-300' : 'text-gray-500'"
            @click="setDim('provider')"
          >{{ t('stats.byProvider') }}</button>
          <button
            class="rounded-md px-3 py-1.5 text-sm font-medium transition-colors"
            :class="dim === 'group' ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-700 dark:text-primary-300' : 'text-gray-500'"
            @click="setDim('group')"
          >{{ t('stats.byGroup') }}</button>
        </div>
        <!-- 快捷范围 -->
        <div class="flex rounded-lg bg-gray-100 p-0.5 dark:bg-dark-800">
          <button v-for="r in ranges" :key="r.key"
            class="rounded-md px-3 py-1.5 text-sm font-medium transition-colors"
            :class="rangeKey === r.key ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-700 dark:text-primary-300' : 'text-gray-500'"
            @click="setRange(r.key)"
          >{{ t(r.label) }}</button>
        </div>
        <!-- 自定义日期 -->
        <input v-model="startDate" type="date" class="input !w-auto !py-1.5 text-sm" @change="onCustom" />
        <span class="text-gray-400">~</span>
        <input v-model="endDate" type="date" class="input !w-auto !py-1.5 text-sm" @change="onCustom" />
      </div>
    </div>

    <!-- 汇总条 -->
    <div class="grid grid-cols-3 gap-4">
      <div class="card p-5">
        <p class="text-xs text-gray-500">{{ t('stats.revenue') }}</p>
        <p class="mt-1 text-xl font-bold text-gray-900 dark:text-white">{{ displayMoney(totals.revenue) }}</p>
      </div>
      <div class="card p-5">
        <p class="flex items-center gap-1 text-xs text-gray-500">
          {{ t('stats.cost') }}
          <span class="cursor-help text-gray-400" :title="t('cost.actualHint')">ⓘ</span>
        </p>
        <p class="mt-1 text-xl font-bold text-gray-900 dark:text-white">{{ displayMoney(totals.cost) }}</p>
        <!-- 运营成本单列而非并入实扣：两者口径不同（变动 vs 固定成本），合并会掩盖构成 -->
        <p
          v-if="totals.operatingCost > 0"
          class="mt-1 text-xs text-gray-500"
          :title="t('opcost.statHint')"
        >
          + {{ t('opcost.title') }} {{ displayMoney(totals.operatingCost) }}
        </p>
      </div>
      <div class="card p-5">
        <p class="flex items-center gap-1 text-xs text-gray-500">
          {{ t('stats.profit') }}
          <span v-if="!privacyMode && !costComplete" class="text-amber-500" :title="t('cost.profitOverstated')">⚠</span>
        </p>
        <p class="mt-1 text-xl font-bold" :class="displayMoneyClass(totals.profit)">{{ displayMoney(totals.profit) }}</p>
        <!-- 隐私模式下整体隐藏，避免标签孤零零挂在 **** 旁边 -->
        <p v-if="!privacyMode && totalMargin !== null" class="mt-1 text-xs text-gray-500">
          {{ t('stats.margin') }} {{ fmtPct(totalMargin) }}
        </p>
      </div>
    </div>

    <!-- 成本来源与新鲜度 -->
    <CostSyncBar v-if="!privacyMode" :sync="costSync" :complete="costComplete" :accounts-missing="accountsMissing" />
    <!-- 分组成本是分摊值，口径须显式说明 -->
    <p v-if="!isProvider && !privacyMode" class="rounded-lg bg-gray-50 px-3 py-2 text-xs text-gray-500 dark:bg-dark-800/50 dark:text-dark-400">
      ⓘ {{ t('cost.groupCostApportioned') }}
    </p>

    <!-- 图表：分类数据用柱状图，收益/成本/利润三系列并排 -->
    <div class="card p-5">
      <div v-if="loading" class="flex h-[320px] items-center justify-center"><LoadingState /></div>
      <div v-else-if="privacyMode" class="flex h-[320px] items-center justify-center text-sm text-gray-400">
        {{ t('privacy.hidden') }}
      </div>
      <BarChart
        v-else
        :labels="chartLabels"
        :datasets="chartDatasets"
        :horizontal="false"
        :height="320"
        :value-formatter="fmtMoney"
      />
    </div>

    <!-- 明细表：两维度同构，共用一套渲染 -->
    <div class="card overflow-hidden">
      <div class="table-wrapper">
        <table class="table">
          <thead>
            <tr>
              <SortableTh
                sort-key="name" :active-key="sortKey" :order="sortOrder" @sort="sortBy"
                :label="isProvider ? t('stats.provider') : t('stats.groupName')"
              />
              <SortableTh
                v-if="!isProvider"
                sort-key="rate" :active-key="sortKey" :order="sortOrder" @sort="sortBy"
                :label="t('stats.rateMultiplier')"
              />
              <SortableTh
                sort-key="accounts" :active-key="sortKey" :order="sortOrder" @sort="sortBy"
                :label="t('stats.accounts')"
              />
              <SortableTh
                class="text-right" align="right"
                sort-key="revenue" :active-key="sortKey" :order="sortOrder" @sort="sortBy"
                :label="t('stats.revenue')"
              />
              <SortableTh
                class="text-right" align="right"
                sort-key="cost" :active-key="sortKey" :order="sortOrder" @sort="sortBy"
                :label="t('cost.actual')"
              />
              <SortableTh
                class="text-right" align="right"
                sort-key="profit" :active-key="sortKey" :order="sortOrder" @sort="sortBy"
                :label="t('stats.profit')"
              />
              <SortableTh
                class="text-right" align="right"
                sort-key="margin" :active-key="sortKey" :order="sortOrder" @sort="sortBy"
              >
                {{ t('stats.margin') }}
                <span class="cursor-help text-gray-400" :title="t('stats.marginHint')">ⓘ</span>
              </SortableTh>
              <SortableTh
                class="text-right" align="right"
                sort-key="requests" :active-key="sortKey" :order="sortOrder" @sort="sortBy"
                :label="t('stats.requests')"
              />
            </tr>
          </thead>
          <tbody>
            <TableState :loading="loading" :empty="!viewRows.length" :colspan="colCount" icon="chartBar" />

            <template v-for="r in sortedRows" :key="r.key">
              <tr class="cursor-pointer" @click="toggle(r.key)">
                <td class="font-medium text-gray-900 dark:text-white">
                  <span class="mr-1 inline-block w-3 text-gray-400">{{ expanded.has(r.key) ? '▾' : '▸' }}</span>
                  {{ r.name }}
                  <!-- 自营站显示身份标签而非 ⚠：其成本取运营成本，没有上游实扣是设计如此 -->
                  <span
                    v-if="r.selfOperated"
                    class="badge badge-success ml-1 !text-[10px]"
                    :title="t('provider.selfOperatedHint')"
                  >{{ t('provider.selfOperated') }}</span>
                  <span v-else-if="!r.cost_complete" class="ml-1 text-amber-500" :title="t('cost.incomplete', { n: r.accounts_missing })">⚠</span>
                </td>
                <td v-if="!isProvider">×{{ r.rateMultiplier }}</td>
                <td>{{ r.accounts.length }}</td>
                <td class="text-right">{{ displayMoney(r.revenue) }}</td>
                <td class="text-right">
                  {{ displayMoney(r.cost) }}
                  <span
                    v-if="r.operatingCost > 0"
                    class="block text-xs text-gray-500"
                    :title="t('opcost.statHint')"
                  >+{{ displayMoney(r.operatingCost) }}</span>
                </td>
                <td class="text-right font-semibold" :class="displayMoneyClass(r.profit)">{{ displayMoney(r.profit) }}</td>
                <td><MarginCell :pct="r.margin" /></td>
                <td class="text-right">{{ fmtNum(r.requests) }}</td>
              </tr>
              <tr v-for="a in (expanded.has(r.key) ? r.accounts : [])" :key="r.key + 'a' + a.account_id"
                class="bg-gray-50/60 dark:bg-dark-800/25">
                <td class="pl-9 text-gray-600 dark:text-dark-300">
                  {{ a.account_name }}
                  <span v-if="!a.cost_matched" class="ml-1 text-xs text-amber-600 dark:text-amber-400" :title="t('cost.keyUnmatchedHint')">
                    {{ t('cost.keyUnmatched') }}
                  </span>
                </td>
                <td v-if="!isProvider"></td>
                <td class="font-mono text-xs text-gray-400">#{{ a.account_id }}</td>
                <td class="text-right">{{ displayMoney(a.revenue) }}</td>
                <td class="text-right">{{ a.cost_matched ? displayMoney(a.cost) : '-' }}</td>
                <td class="text-right" :class="a.cost_matched && !privacyMode ? moneyClass(a.profit) : 'text-gray-400'">
                  {{ a.cost_matched ? displayMoney(a.profit) : '-' }}
                </td>
                <!-- 成本未匹配 ⇒ 利润未知 ⇒ 利润率未知，与相邻利润格同进同退 -->
                <td><MarginCell :pct="a.cost_matched ? profitMargin(a.revenue, a.profit) : null" /></td>
                <td class="text-right">{{ fmtNum(a.requests) }}</td>
              </tr>
            </template>
          </tbody>
        </table>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { statsApi } from '@/api'
import { errorMessage } from '@/api/client'
import { fmtMoney, fmtNum, fmtPct, moneyClass, todayStr, daysAgoStr } from '@/utils/format'
import { profitMargin } from '@/utils/profitMargin'
import { useAppStore } from '@/stores/app'
import { usePrivacyMoney } from '@/composables/usePrivacyMoney'
import { useTableSort } from '@/composables/useTableSort'
import BarChart, { type BarSeries } from '@/components/BarChart.vue'
import CostSyncBar from '@/components/CostSyncBar.vue'
import MarginCell from '@/components/stats/MarginCell.vue'
import TableState from '@/components/common/TableState.vue'
import SortableTh from '@/components/common/SortableTh.vue'
import LoadingState from '@/components/common/LoadingState.vue'
import { SERIES } from '@/utils/chartTheme'
import type { CostSyncStatus, StatsAccountRow, StatsProviderRow, StatsGroupRow } from '@/types'

const { t } = useI18n()
const app = useAppStore()
const { privacyMode, displayMoney, displayMoneyClass } = usePrivacyMoney()
const dim = ref<'provider' | 'group'>('provider')
const rangeKey = ref<'today' | '7' | '30' | 'custom'>('today')
const startDate = ref(todayStr())
const endDate = ref(todayStr())
const rows = ref<(StatsProviderRow | StatsGroupRow)[]>([])
const costSync = ref<CostSyncStatus | null>(null)
const expanded = ref(new Set<string>())
const loading = ref(false)

/** 图表最多展示的分类数，超出部分只在表格里出现 */
const CHART_TOP = 15

const ranges = [
  { key: 'today' as const, label: 'stats.rangeToday' },
  { key: '7' as const, label: 'stats.range7' },
  { key: '30' as const, label: 'stats.range30' }
]

const isProvider = computed(() => dim.value === 'provider')

/** 归一化后的表格行：两个维度的差异收敛到 key/name/rateMultiplier 三个字段 */
interface ViewRow {
  key: string
  name: string
  rateMultiplier: number
  revenue: number
  cost: number
  /** 站点级运营成本；分组维度恒为 0 */
  operatingCost: number
  /** 自营站：显示身份标签而非成本不完整告警；分组维度恒为 false */
  selfOperated: boolean
  profit: number
  /**
   * 利润率（百分点）；null = 收益为 0 无法计算。
   *
   * 渲染条件与利润列完全一致：成本不完整的行照样出数值（它是上界，
   * 行上已有的 ⚠ 承担了这个说明）。留空会与紧邻的利润格自相矛盾，
   * 且 null 会让这些行在按利润率排序时沉底 —— 恰好把最该检查的行藏起来。
   */
  margin: number | null
  requests: number
  cost_complete: boolean
  accounts_missing: number
  accounts: StatsAccountRow[]
}

const viewRows = computed<ViewRow[]>(() =>
  rows.value.map((r) => {
    const isG = 'group_id' in r
    return {
      key: isG ? 'g' + r.group_id : 'p' + r.provider,
      name: isG ? r.group_name : r.provider,
      rateMultiplier: isG ? r.rate_multiplier : 1,
      revenue: r.revenue,
      cost: r.cost,
      operatingCost: r.operating_cost || 0,
      selfOperated: !isG && r.self_operated,
      profit: r.profit,
      margin: profitMargin(r.revenue, r.profit),
      requests: r.requests,
      cost_complete: r.cost_complete,
      accounts_missing: r.accounts_missing,
      accounts: r.accounts || []
    }
  })
)

const colCount = computed(() => (isProvider.value ? 7 : 8))

/**
 * 表头排序只作用于父行：子账号明细挂在父行的 accounts 上，后端已按收益降序排好。
 *
 * 未点表头时原样返回后端序 —— 收益统计的「(未归属)」桶在后端恒排最后
 * （stats_service.go），这个默认序由此完整保留；用户主动点了某列才按真实值排，
 * 那时「未归属」按其真实收益插到中间是对的，藏在最后反而是信息损失。
 */
const { sortKey, sortOrder, sorted: sortedRows, toggle: sortBy } = useTableSort<ViewRow>(viewRows, {
  name: (r) => r.name,
  rate: (r) => r.rateMultiplier,
  accounts: (r) => r.accounts.length,
  revenue: (r) => r.revenue,
  cost: (r) => r.cost,
  profit: (r) => r.profit,
  margin: (r) => r.margin,
  requests: (r) => r.requests
})

const totals = computed(() =>
  viewRows.value.reduce(
    (acc, r) => ({
      revenue: acc.revenue + r.revenue,
      cost: acc.cost + r.cost,
      operatingCost: acc.operatingCost + r.operatingCost,
      profit: acc.profit + r.profit
    }),
    { revenue: 0, cost: 0, operatingCost: 0, profit: 0 }
  )
)

const costComplete = computed(() => viewRows.value.every((r) => r.cost_complete))
const accountsMissing = computed(() => viewRows.value.reduce((n, r) => n + r.accounts_missing, 0))

/**
 * 总体利润率是收益加权的 Σ利润 / Σ收益，刻意不是各行利润率的算术平均 ——
 * 平均会让一个 90% 毛利的小上游主导整个头部数字。
 */
const totalMargin = computed(() => profitMargin(totals.value.revenue, totals.value.profit))

const chartLabels = computed(() => viewRows.value.slice(0, CHART_TOP).map((r) => r.name))
// 用 SERIES 常量而非字面量：收益恒 teal、成本恒红，与仪表盘语义配色一致
// 成本柱取「实扣 + 运营成本」：利润柱已扣掉运营成本，成本柱若只画实扣，
// 三根柱子的「收益 − 成本 = 利润」关系会对不上。表格里两者分行展示看构成，图上合并看关系。
const chartDatasets = computed<BarSeries[]>(() => {
  const top = viewRows.value.slice(0, CHART_TOP)
  return [
    { label: t('stats.revenue'), data: top.map((r) => r.revenue), color: SERIES.revenue },
    { label: t('stats.cost'), data: top.map((r) => r.cost + r.operatingCost), color: SERIES.cost },
    { label: t('stats.profit'), data: top.map((r) => r.profit), color: SERIES.profit }
  ]
})

function toggle(key: string) {
  // Set 原地改动不触发响应式，故重建一个新 Set
  const next = new Set(expanded.value)
  if (next.has(key)) {
    next.delete(key)
  } else {
    next.add(key)
  }
  expanded.value = next
}

function setDim(d: 'provider' | 'group') {
  dim.value = d
  rows.value = []
  expanded.value = new Set()
  load()
}
function setRange(key: 'today' | '7' | '30') {
  rangeKey.value = key
  endDate.value = todayStr()
  startDate.value = key === 'today' ? todayStr() : daysAgoStr(key === '7' ? 6 : 29)
  load()
}
function onCustom() {
  rangeKey.value = 'custom'
  load()
}

async function load() {
  loading.value = true
  try {
    const s = startDate.value || undefined
    const e = endDate.value || undefined
    const res = isProvider.value ? await statsApi.byProvider(s, e) : await statsApi.byGroup(s, e)
    rows.value = res.items || []
    costSync.value = res.cost_sync ?? null
  } catch (err) {
    app.showError(errorMessage(err))
    rows.value = []
  } finally {
    loading.value = false
  }
}

onMounted(load)
</script>
