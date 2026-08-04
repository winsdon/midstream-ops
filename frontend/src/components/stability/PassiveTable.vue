<template>
  <div class="card overflow-hidden">
    <div class="border-b border-gray-200 bg-gray-50 px-4 py-2.5 text-xs text-gray-500 dark:border-dark-800 dark:bg-dark-800/50">
      {{ t('stability.passive') }} · {{ t('stability.passiveHint') }}
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
            <!-- 首字列前移到耗时之前：它是判断「卡不卡」的主指标 -->
            <SortableTh
              class="text-right" align="right"
              sort-key="ftP50" :active-key="sortKey" :order="sortOrder" @sort="toggle"
              :label="t('stability.firstToken')"
            />
            <SortableTh
              class="text-right" align="right"
              sort-key="durP50" :active-key="sortKey" :order="sortOrder" @sort="toggle"
              :label="t('stability.duration')"
            />
            <!-- 抖动：唯一回答「稳不稳」而非「快不快」的列 -->
            <SortableTh
              class="text-right" align="right"
              sort-key="jitter" :active-key="sortKey" :order="sortOrder" @sort="toggle"
            >
              {{ t('stability.jitter') }}
              <span class="cursor-help text-gray-400" :title="t('stability.jitterHint', { n: JITTER_MIN_SAMPLES })">ⓘ</span>
            </SortableTh>
            <SortableTh
              class="text-right" align="right"
              sort-key="requests" :active-key="sortKey" :order="sortOrder" @sort="toggle"
              :label="t('stability.requests')"
            />
          </tr>
        </thead>
        <tbody>
          <TableState :loading="loading" :empty="!rows.length" :colspan="6" icon="chart" />
          <tr v-for="r in sorted" :key="r.account_id">
            <td class="font-medium text-gray-900 dark:text-white">
              <GradeDot :grade="gradeOf(r)">
                <span class="truncate">{{ r.account_name }}</span>
              </GradeDot>
              <div v-if="r.provider_name" class="ml-4 text-xs text-gray-400">{{ r.provider_name }}</div>
            </td>
            <td><span class="rounded bg-gray-100 px-1.5 py-0.5 text-xs dark:bg-dark-800">{{ r.platform }}</span></td>
            <td><LatencyCell :ms="r.first_token_p50" :p95="r.first_token_p95" kind="ttft" primary /></td>
            <td><LatencyCell :ms="r.duration_p50" :p95="r.duration_p95" kind="total" /></td>
            <td class="text-right" :class="jitterClass(jitterOf(r))">{{ fmtJitter(jitterOf(r)) }}</td>
            <td class="text-right">{{ fmtNum(r.requests) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { fmtNum } from '@/utils/format'
import { useTableSort } from '@/composables/useTableSort'
import {
  GRADE_RANK,
  JITTER_MIN_SAMPLES,
  jitterRatio,
  type RowGrade
} from '@/utils/stabilityModel'
import TableState from '@/components/common/TableState.vue'
import SortableTh from '@/components/common/SortableTh.vue'
import GradeDot from './GradeDot.vue'
import LatencyCell from './LatencyCell.vue'
import type { PassiveRow } from '@/types'

const props = defineProps<{
  /** 已由父组件筛选过；排序在筛选之后 */
  rows: PassiveRow[]
  loading: boolean
  gradeOf: (r: PassiveRow) => RowGrade
}>()

const { t } = useI18n()

const rowsRef = computed(() => props.rows)

/** 取首字的 P50/P95 —— 首字是本页声明的主指标，抖动跟着主指标走 */
function jitterOf(r: PassiveRow): number | null {
  return jitterRatio(r.first_token_p50, r.first_token_p95, r.requests)
}

function fmtJitter(v: number | null): string {
  return v === null ? '-' : v.toFixed(1) + '×'
}

/**
 * 抖动着色。沿用 LatencyCell 的反绿色通胀规则：正常区间给中性灰，
 * 只有长尾明显（>3×，即 P95 是 P50 的三倍以上）才点亮琥珀。
 *
 * 不设红档、也不接入 rowGrade：抖动大未必是故障（长短回复混跑天然拉开分位数），
 * 给它红色或让它拖黑评级点会制造假警报。
 * 必须写完整字面量供 Tailwind 扫描。
 */
const JITTER_WARN = 3

function jitterClass(v: number | null): string {
  if (v === null) return 'text-gray-400 dark:text-dark-500'
  return v >= JITTER_WARN ? 'text-amber-600 dark:text-amber-400' : 'text-gray-700 dark:text-dark-300'
}

/**
 * 排序 accessors 只有本表关心，故 useTableSort 放在子组件内部。
 * 缺值（无样本 / 样本不足）由 sortRows 排到末尾，升序降序都一样。
 */
const { sortKey, sortOrder, sorted, toggle } = useTableSort<PassiveRow>(rowsRef, {
  // 账号列的排序键是评级而非名字：这一列首格就是评级点，
  // 用户点它想要的是「把有问题的排上来」
  grade: (r) => GRADE_RANK[props.gradeOf(r)],
  platform: (r) => r.platform,
  requests: (r) => r.requests,
  durP50: (r) => r.duration_p50,
  ftP50: (r) => r.first_token_p50,
  jitter: (r) => jitterOf(r)
})
</script>
