<template>
  <div class="card overflow-hidden">
    <!-- 短窗口提示：探测每 15 分钟一轮，选 5 分钟时多数账号还没有新样本。
         不提示的话空表会被误读成「账号全挂了」。 -->
    <div
      v-if="sparse"
      class="border-b border-gray-200 bg-amber-50 px-4 py-2 text-xs text-amber-700 dark:border-dark-800 dark:bg-amber-900/20 dark:text-amber-400"
    >
      {{ t('stability.probeSparseHint', { n: PROBE_INTERVAL_MINUTES }) }}
    </div>
    <div v-if="budgetUsed > 0" class="border-b border-gray-200 bg-gray-50 px-4 py-2 text-xs text-gray-500 dark:border-dark-800 dark:bg-dark-800/50">
      {{ t('health.budgetUsed', { n: budgetUsed }) }}
    </div>
    <div class="table-wrapper">
      <table class="table">
        <thead>
          <tr>
            <SortableTh
              sort-key="grade" :active-key="sortKey" :order="sortOrder" @sort="toggle"
              :label="t('stability.account')"
            />
            <SortableTh
              sort-key="platform" :active-key="sortKey" :order="sortOrder" @sort="toggle"
              :label="t('common.platform')"
            />
            <SortableTh
              sort-key="state" :active-key="sortKey" :order="sortOrder" @sort="toggle"
              :label="t('health.state')"
            />
            <SortableTh
              class="text-right" align="right"
              sort-key="successRate" :active-key="sortKey" :order="sortOrder" @sort="toggle"
              :label="t('stability.successRate')"
            />
            <SortableTh
              class="text-right" align="right"
              sort-key="ttft" :active-key="sortKey" :order="sortOrder" @sort="toggle"
              :label="t('stability.avgTtft')"
            />
            <SortableTh
              class="text-right" align="right"
              sort-key="total" :active-key="sortKey" :order="sortOrder" @sort="toggle"
              :label="t('stability.avgTotal')"
            />
            <SortableTh
              sort-key="lastResult" :active-key="sortKey" :order="sortOrder" @sort="toggle"
              :label="t('stability.lastResult')"
            />
            <SortableTh
              sort-key="lastAt" :active-key="sortKey" :order="sortOrder" @sort="toggle"
              :label="t('stability.lastAt')"
            />
            <th>{{ t('common.actions') }}</th>
          </tr>
        </thead>
        <tbody>
          <TableState :loading="loading" :empty="!rows.length" :colspan="9" icon="beaker" />
          <tr v-for="r in sorted" :key="r.account_id">
            <td class="font-medium text-gray-900 dark:text-white">
              <GradeDot :grade="gradeOf(r)">
                <span class="truncate">{{ r.account_name }}</span>
              </GradeDot>
              <div v-if="r.provider_name" class="ml-4 text-xs text-gray-400">{{ r.provider_name }}</div>
            </td>
            <td><span class="rounded bg-gray-100 px-1.5 py-0.5 text-xs dark:bg-dark-800">{{ r.platform }}</span></td>
            <td>
              <button
                class="badge cursor-pointer"
                :class="healthBadge(healthOf(r.account_id)?.state)"
                :title="healthTip(healthOf(r.account_id))"
                @click="emit('timeline', r)"
              >{{ healthLabel(healthOf(r.account_id)?.state) }}</button>
            </td>
            <td class="text-right">
              <span :class="rateClass(r.success_rate)">{{ fmtPct(r.success_rate) }}</span>
              <span class="ml-1 text-xs text-gray-400">({{ r.success_count }}/{{ r.total }})</span>
            </td>
            <td><LatencyCell :ms="r.avg_ttft_ms" kind="ttft" primary /></td>
            <td><LatencyCell :ms="r.avg_total_ms" kind="total" /></td>
            <td>
              <span v-if="r.last_success === true" class="text-emerald-600">✓</span>
              <span v-else-if="r.last_success === false" class="text-red-500">✗</span>
              <span v-else class="text-gray-400">-</span>
            </td>
            <td class="text-xs text-gray-500">{{ r.last_at || '-' }}</td>
            <td>
              <div class="flex items-center gap-1">
                <button
                  @click="emit('probe', r)"
                  :disabled="probingId === r.account_id"
                  class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-emerald-50 hover:text-emerald-600 disabled:cursor-not-allowed disabled:opacity-50 dark:hover:bg-emerald-900/20 dark:hover:text-emerald-400"
                >
                  <Icon :name="probingId === r.account_id ? 'refresh' : 'play'" size="sm" :class="probingId === r.account_id ? 'animate-spin' : ''" />
                  <span class="text-xs">{{ t('stability.runProbe') }}</span>
                </button>
                <button
                  @click="emit('trend', r)"
                  class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700 dark:hover:text-primary-400"
                >
                  <Icon name="trendingUp" size="sm" />
                  <span class="text-xs">{{ t('stability.viewTrend') }}</span>
                </button>
                <button
                  @click="emit('toggle-disabled', r)"
                  :title="isDisabled(r) ? t('health.enable') : t('health.disable')"
                  class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-amber-600 dark:hover:bg-dark-700 dark:hover:text-amber-400"
                >
                  <Icon :name="isDisabled(r) ? 'play' : 'x'" size="sm" />
                  <span class="text-xs">{{ isDisabled(r) ? t('health.enable') : t('health.disable') }}</span>
                </button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { fmtPct } from '@/utils/format'
import { useTableSort } from '@/composables/useTableSort'
import {
  GRADE_RANK,
  PROBE_INTERVAL_MINUTES,
  healthRank,
  rateBand,
  type RateBand,
  type RowGrade,
  type WindowMinutes
} from '@/utils/stabilityModel'
import TableState from '@/components/common/TableState.vue'
import SortableTh from '@/components/common/SortableTh.vue'
import Icon from '@/components/icons/Icon.vue'
import GradeDot from './GradeDot.vue'
import LatencyCell from './LatencyCell.vue'
import type { HealthStateItem, ProbeSummaryRow } from '@/types'

const props = defineProps<{
  /** 已由父组件筛选过 */
  rows: ProbeSummaryRow[]
  loading: boolean
  budgetUsed: number
  probingId: number | null
  minutes: WindowMinutes
  healthOf: (accountId: number) => HealthStateItem | undefined
  gradeOf: (r: ProbeSummaryRow) => RowGrade
}>()

const emit = defineEmits<{
  (e: 'probe' | 'trend' | 'timeline' | 'toggle-disabled', r: ProbeSummaryRow): void
}>()

const { t } = useI18n()

const sparse = computed(() => props.minutes < PROBE_INTERVAL_MINUTES)

function isDisabled(r: ProbeSummaryRow): boolean {
  return props.healthOf(r.account_id)?.state === 'disabled'
}

function healthBadge(state?: string): string {
  switch (state) {
    case 'healthy': return 'badge-success'
    case 'degraded': return 'badge-warning'
    case 'suspended': return 'badge-danger'
    case 'observing': return 'badge-purple'
    case 'recovering': return 'badge-primary'
    case 'disabled': return 'badge-gray'
    default: return 'badge-success' // 无记录 = 未观测，默认视作正常
  }
}

function healthLabel(state?: string): string {
  if (!state) return t('health.states.healthy')
  return t('health.states.' + state)
}

function healthTip(st?: HealthStateItem): string {
  if (!st) return t('health.neverProbed')
  const parts = [t('health.weight', { n: st.weight_percent })]
  if (st.consecutive_failures > 0) parts.push(t('provider.syncFailing', { n: st.consecutive_failures }))
  // 连续成功数只在恢复过程中有意义：稳定健康的账号看这个数没有信息量，
  // 而 recovering 状态下它正是「还差几次能回到 healthy」的唯一线索
  if (st.consecutive_successes > 0) parts.push(t('health.consecutiveSuccesses', { n: st.consecutive_successes }))
  if (st.cooldown_until) parts.push(t('health.cooldownUntil', { time: st.cooldown_until }))
  return parts.join(' · ')
}

/**
 * 成功率着色。分档走 rateBand（与评级点共用一份阈值），
 * 但缺值在这里显示为灰色而非弃权 —— 主动表的成功率恒有值，
 * 缺值意味着「窗口内没探测过」，与评级点弃权的语境不同。
 *
 * 必须写完整字面量：Tailwind 扫描源码文本提取类名，拼接出来的会被漏掉。
 */
const RATE_TONE: Record<RateBand, string> = {
  good: 'font-semibold text-emerald-600',
  warn: 'font-semibold text-amber-600',
  bad: 'font-semibold text-red-600',
  unknown: 'text-gray-400'
}

function rateClass(v?: number | null): string {
  return RATE_TONE[rateBand(v)]
}

const rowsRef = computed(() => props.rows)

const { sortKey, sortOrder, sorted, toggle } = useTableSort<ProbeSummaryRow>(rowsRef, {
  // 账号列排评级：首格就是评级点，点它是想把有问题的排上来
  grade: (r) => GRADE_RANK[props.gradeOf(r)],
  platform: (r) => r.platform,
  state: (r) => healthRank(props.healthOf(r.account_id)?.state),
  successRate: (r) => r.success_rate,
  ttft: (r) => r.avg_ttft_ms ?? null,
  total: (r) => r.avg_total_ms ?? null,
  lastResult: (r) => (r.last_success == null ? null : r.last_success ? 1 : 0),
  lastAt: (r) => r.last_at ?? null
})
</script>
