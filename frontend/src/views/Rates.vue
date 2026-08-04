<template>
  <div class="space-y-5">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <!-- scope + 供应商筛选 -->
      <div class="flex flex-wrap items-center gap-2">
        <div class="flex rounded-lg bg-gray-100 p-0.5 dark:bg-dark-800">
          <button
            v-for="s in scopes" :key="s.key"
            class="rounded-md px-3 py-1.5 text-sm font-medium transition-colors"
            :class="scope === s.key ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-700 dark:text-primary-300' : 'text-gray-500'"
            @click="setScope(s.key)"
          >{{ t(s.label) }}</button>
        </div>
        <Select
          v-if="scope === 'upstream'"
          v-model="providerId"
          class="!w-44"
          :options="providerOptions"
          @change="load"
        />
        <label class="flex items-center gap-1.5 text-sm text-gray-500">
          <input v-model="includeDeleted" type="checkbox" class="checkbox" @change="load" />
          {{ t('rates.showDeleted') }}
        </label>
      </div>
      <div class="flex items-center gap-2">
        <input v-model.trim="search" class="input !w-52 !py-1.5 text-sm" :placeholder="t('common.search')" />
        <button class="btn btn-secondary text-sm" @click="load">{{ t('common.refresh') }}</button>
      </div>
    </div>

    <!-- 对接状态 Tab（仅上游 scope 有对接概念） -->
    <div v-if="scope === 'upstream'" class="flex rounded-lg bg-gray-100 p-0.5 dark:bg-dark-800" role="tablist">
      <button
        v-for="tab in statusTabs" :key="tab.key"
        type="button" role="tab" :aria-selected="statusFilter === tab.key"
        class="flex items-center gap-1.5 rounded-md px-3 py-1.5 text-sm font-medium transition-colors"
        :class="statusFilter === tab.key ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-700 dark:text-primary-300' : 'text-gray-500'"
        @click="statusFilter = tab.key"
      >
        {{ t(tab.label) }}
        <span class="rounded bg-gray-200 px-1.5 text-[10px] tabular-nums dark:bg-dark-600">{{ statusCounts[tab.key] }}</span>
      </button>
    </div>

    <!-- 当前倍率列表 -->
    <div class="card overflow-hidden">
      <div class="table-wrapper">
        <table class="table">
          <thead>
            <tr>
              <SortableTh
                v-if="scope === 'upstream'"
                sort-key="provider" :active-key="sortKey" :order="sortOrder" @sort="sortBy"
                :label="t('stats.provider')"
              />
              <SortableTh
                v-else
                sort-key="entityType" :active-key="sortKey" :order="sortOrder" @sort="sortBy"
                :label="t('rate.entityType')"
              />
              <SortableTh
                sort-key="entityName" :active-key="sortKey" :order="sortOrder" @sort="sortBy"
                :label="t('rate.entityName')"
              />
              <SortableTh
                class="text-right" align="right"
                sort-key="rate" :active-key="sortKey" :order="sortOrder" @sort="sortBy"
                :label="t('rates.currentRate')"
              />
              <SortableTh
                class="text-right" align="right"
                sort-key="prevRate" :active-key="sortKey" :order="sortOrder" @sort="sortBy"
                :label="t('rates.prevRate')"
              />
              <SortableTh
                class="text-right" align="right"
                sort-key="delta" :active-key="sortKey" :order="sortOrder" @sort="sortBy"
                :label="t('rates.delta')"
              />
              <SortableTh
                sort-key="since" :active-key="sortKey" :order="sortOrder" @sort="sortBy"
                :label="t('rates.effectiveSince')"
              />
              <SortableTh
                sort-key="lastSeen" :active-key="sortKey" :order="sortOrder" @sort="sortBy"
                :label="t('rates.lastConfirmed')"
              />
              <SortableTh
                v-if="scope === 'upstream'"
                sort-key="mapped" :active-key="sortKey" :order="sortOrder" @sort="sortBy"
                :label="t('rates.connectStatus')"
              />
              <th>{{ t('common.actions') }}</th>
            </tr>
          </thead>
          <tbody>
            <TableState :loading="loading" :empty="!filtered.length" :colspan="8" icon="trendingUp" />
            <tr v-for="r in sortedRows" :key="r.id" :class="{ 'opacity-50': r.deleted }">
              <td v-if="scope === 'upstream'" class="text-gray-600 dark:text-dark-300">{{ providerName(r.provider_id) }}</td>
              <td v-else>
                <Badge :variant="r.entity_type === 'group' ? 'primary' : 'purple'">
                  {{ r.entity_type === 'group' ? t('rate.group') : t('rate.account') }}
                </Badge>
              </td>
              <td class="font-medium text-gray-900 dark:text-white">
                {{ r.entity_name }}
                <span v-if="r.deleted" class="ml-1 badge badge-gray !text-[10px]">{{ t('rates.gone') }}</span>
              </td>
              <td class="text-right font-semibold">×{{ fmtRate(r.rate) }}</td>
              <td class="text-right text-gray-400">
                {{ r.prev_rate !== undefined && r.prev_rate !== null ? '×' + fmtRate(r.prev_rate) : '-' }}
              </td>
              <td class="text-right">
                <span v-if="delta(r) !== null" :class="delta(r)! > 0 ? 'text-red-600 dark:text-red-400' : 'text-emerald-600 dark:text-emerald-400'">
                  {{ delta(r)! > 0 ? '↑' : '↓' }} {{ Math.abs(delta(r)!).toFixed(1) }}%
                </span>
                <span v-else class="text-gray-300 dark:text-dark-600">—</span>
              </td>
              <td class="text-xs text-gray-500">
                {{ r.first_seen_at }}
                <div class="text-gray-400">{{ t('rates.duration', { d: durationDays(r) }) }}</div>
              </td>
              <td class="text-xs text-gray-400">{{ r.last_seen_at }}</td>
              <td v-if="scope === 'upstream'">
                <span v-if="isMapped(r)" class="badge badge-success !text-[10px]">{{ t('rates.mapped') }}</span>
                <span v-else class="badge badge-gray !text-[10px]">{{ t('rates.unmapped') }}</span>
              </td>
              <td>
                <div class="flex items-center gap-2">
                  <button
                    v-if="scope === 'upstream' && !r.deleted"
                    class="text-xs font-medium text-primary-600 hover:underline dark:text-primary-400"
                    @click="openConnect(r)"
                  >{{ isMapped(r) ? t('rates.viewRule') : t('rates.connect') }}</button>
                  <button class="text-xs text-gray-500 hover:underline dark:text-dark-400" @click="openHistory(r)">
                    {{ t('rates.viewHistory') }}
                  </button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- 配置对接弹窗 -->
    <BaseDialog :show="showConnect" :title="t('rates.connectTitle')" width="wide" @close="showConnect = false">
      <form id="connect-form" class="space-y-4" @submit.prevent="saveRule">
        <!-- 上游只读信息 -->
        <div class="grid grid-cols-2 gap-3 rounded-lg bg-gray-50 p-3 text-sm dark:bg-dark-800/50">
          <div>
            <span class="text-gray-400">{{ t('stats.provider') }}：</span>
            <span class="font-medium text-gray-900 dark:text-white">{{ providerName(connectSource?.provider_id || 0) }}</span>
          </div>
          <div>
            <span class="text-gray-400">{{ t('rate.entityName') }}：</span>
            <span class="font-medium text-gray-900 dark:text-white">{{ connectSource?.entity_name }}</span>
          </div>
          <div>
            <span class="text-gray-400">{{ t('rates.currentRate') }}：</span>
            <span class="font-semibold text-gray-900 dark:text-white">×{{ fmtRate(connectSource?.rate || 0) }}</span>
          </div>
        </div>

        <!-- 本站分组 -->
        <div>
          <label class="input-label">{{ t('pricing.localGroup') }} <span class="text-red-500">*</span></label>
          <Select
            v-model="ruleForm.local_group_id"
            :options="localGroupOptions"
            :placeholder="t('rates.selectLocalGroup')"
            :error="!ruleForm.local_group_id"
            @change="onLocalGroupChange"
          />
          <p v-if="existingRuleHint" class="mt-1 text-xs text-amber-600 dark:text-amber-400">{{ existingRuleHint }}</p>
        </div>

        <!-- 数据源（多上游聚合） -->
        <div>
          <label class="input-label">{{ t('rates.dataSources') }} <span class="text-red-500">*</span></label>
          <p class="mb-1.5 text-xs text-gray-400">{{ t('rates.dataSourcesHint') }}</p>
          <div class="max-h-40 space-y-1 overflow-y-auto rounded-lg border border-gray-200 p-2 dark:border-dark-700">
            <label
              v-for="u in upstreamOptions" :key="u.key"
              class="flex cursor-pointer items-center justify-between rounded px-2 py-1.5 text-sm hover:bg-gray-50 dark:hover:bg-dark-800"
            >
              <span class="flex items-center gap-2">
                <input type="checkbox" :value="u.key" v-model="ruleForm.sourceKeys" class="checkbox" />
                <span class="text-gray-700 dark:text-dark-300">{{ u.label }}</span>
              </span>
              <span class="text-xs text-gray-400">×{{ fmtRate(u.rate) }}</span>
            </label>
          </div>
        </div>

        <!-- 参考价来源 -->
        <div class="grid grid-cols-2 gap-4">
          <div>
            <label class="input-label">{{ t('rates.priceSource') }}</label>
            <Select v-model="ruleForm.price_source" :options="priceSourceOptions" :searchable="false" />
          </div>
          <div v-if="ruleForm.price_source === 'primary'">
            <label class="input-label">{{ t('rates.primarySource') }}</label>
            <Select v-model="ruleForm.primaryKey" :options="primarySourceOptions" />
          </div>
        </div>

        <!-- 加价 -->
        <div class="grid grid-cols-3 gap-4">
          <div>
            <label class="input-label">{{ t('rates.markupMode') }}</label>
            <Select v-model="ruleForm.markup_mode" :options="markupModeOptions" :searchable="false" />
          </div>
          <div>
            <label class="input-label">{{ t('rates.markupValue') }}</label>
            <div class="relative">
              <input v-model.number="ruleForm.markup_value" type="number" step="0.01" class="input !pr-7" />
              <span class="absolute right-3 top-1/2 -translate-y-1/2 text-sm text-gray-400">
                {{ ruleForm.markup_mode === 'percentage' ? '%' : '×' }}
              </span>
            </div>
          </div>
          <div>
            <label class="input-label">{{ t('rates.followThreshold') }}</label>
            <div class="relative">
              <input v-model.number="ruleForm.follow_threshold" type="number" min="0" step="1" class="input !pr-7" />
              <span class="absolute right-3 top-1/2 -translate-y-1/2 text-sm text-gray-400">%</span>
            </div>
          </div>
        </div>

        <div class="grid grid-cols-2 gap-4">
          <div>
            <label class="input-label">{{ t('pricing.minRate') }}</label>
            <input v-model="ruleForm.min_rate" type="number" step="0.01" class="input" :placeholder="t('pricing.noLimit')" />
          </div>
          <div>
            <label class="input-label">{{ t('pricing.maxRate') }}</label>
            <input v-model="ruleForm.max_rate" type="number" step="0.01" class="input" :placeholder="t('pricing.noLimit')" />
          </div>
        </div>

        <!-- 实时预估 -->
        <div class="rounded-lg border border-primary-200 bg-primary-50/50 p-3 dark:border-primary-900/40 dark:bg-primary-900/10">
          <div class="flex items-center justify-between">
            <span class="text-sm text-gray-600 dark:text-dark-300">{{ t('rates.estimated') }}</span>
            <span v-if="estimated !== null" class="text-lg font-bold text-primary-700 dark:text-primary-300">
              ×{{ estimated.toFixed(4) }}
            </span>
            <span v-else class="text-sm text-amber-600 dark:text-amber-400">{{ t('rates.noEstimate') }}</span>
          </div>
          <p v-if="estimateFormula" class="mt-1 text-[10px] text-gray-400">{{ estimateFormula }}</p>
        </div>

        <!-- 阈值语义说明（极易理解反，必须写清） -->
        <p class="rounded-lg bg-amber-50 px-3 py-2 text-xs text-amber-700 dark:bg-amber-900/20 dark:text-amber-300">
          ⓘ {{ t('rates.thresholdExplain') }}
        </p>

        <label class="flex items-center gap-2 rounded-lg bg-gray-50 p-3 text-sm dark:bg-dark-800/50">
          <input v-model="ruleForm.auto_enabled" type="checkbox" class="checkbox" />
          <span>
            {{ t('pricing.autoEnable') }}
            <span class="text-xs text-gray-400">{{ t('pricing.autoHint') }}</span>
          </span>
        </label>

        <p v-if="ruleError" class="rounded-lg bg-red-50 px-3 py-2 text-sm text-red-600 dark:bg-red-900/30 dark:text-red-400">{{ ruleError }}</p>
      </form>
      <template #footer>
        <button type="button" class="btn btn-secondary" @click="showConnect = false">{{ t('common.cancel') }}</button>
        <button type="submit" form="connect-form" class="btn btn-primary" :disabled="savingRule">
          {{ savingRule ? t('common.loading') : t('common.save') }}
        </button>
      </template>
    </BaseDialog>

    <!-- 变更历史弹窗 -->
    <BaseDialog :show="showHistory" :title="historyTitle" width="wide" @close="showHistory = false">
      <LoadingState v-if="historyLoading" />
      <EmptyState v-else-if="!historyItems.length" icon="clock" />
      <div v-else class="table-wrapper max-h-96 overflow-y-auto">
        <table class="table">
          <thead>
            <tr>
              <th>{{ t('rates.effectiveSince') }}</th>
              <th class="text-right">{{ t('rate.oldRate') }}</th>
              <th class="text-right">{{ t('rate.newRate') }}</th>
              <th class="text-right">{{ t('rates.delta') }}</th>
              <th>{{ t('rates.lastConfirmed') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="h in historyItems" :key="h.id">
              <td class="text-xs">{{ h.first_seen_at }}</td>
              <td class="text-right text-gray-400">{{ h.prev_rate !== undefined && h.prev_rate !== null ? '×' + fmtRate(h.prev_rate) : '-' }}</td>
              <td class="text-right font-semibold">×{{ fmtRate(h.rate) }}</td>
              <td class="text-right">
                <span v-if="delta(h) !== null" :class="delta(h)! > 0 ? 'text-red-600' : 'text-emerald-600'">
                  {{ delta(h)! > 0 ? '↑' : '↓' }} {{ Math.abs(delta(h)!).toFixed(1) }}%
                </span>
                <span v-else class="text-gray-300">{{ t('rates.initial') }}</span>
              </td>
              <td class="text-xs text-gray-400">{{ h.last_seen_at }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </BaseDialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { providerApi, pricingApi, rateApi } from '@/api'
import { errorMessage } from '@/api/client'
import { useAppStore } from '@/stores/app'
import { useTableSort } from '@/composables/useTableSort'
import type { LocalGroupOption, PricingPreviewRow, Provider, RateSnapshotItem } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Badge from '@/components/common/Badge.vue'
import TableState from '@/components/common/TableState.vue'
import SortableTh from '@/components/common/SortableTh.vue'
import LoadingState from '@/components/common/LoadingState.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Select from '@/components/common/Select.vue'

const { t } = useI18n()
const app = useAppStore()

const scopes = [
  { key: 'upstream', label: 'rates.scopeUpstream' },
  { key: 'local', label: 'rates.scopeLocal' }
] as const

const scope = ref<'upstream' | 'local'>('upstream')
const providerId = ref(0)
const includeDeleted = ref(false)
const search = ref('')
const loading = ref(false)
const items = ref<RateSnapshotItem[]>([])
const providers = ref<Provider[]>([])

const providerOptions = computed(() => [
  { value: 0, label: t('common.all') },
  ...providers.value.map((p) => ({ value: p.id, label: p.name }))
])

const filtered = computed(() => {
  let list = items.value
  const q = search.value.toLowerCase()
  if (q) list = list.filter((r) => r.entity_name.toLowerCase().includes(q))
  if (scope.value === 'upstream' && statusFilter.value !== 'all') {
    list = list.filter((r) => (statusFilter.value === 'mapped' ? isMapped(r) : !isMapped(r)))
  }
  return list
})

// ---- 对接状态 ----
const statusTabs = [
  { key: 'all' as const, label: 'common.all' },
  { key: 'mapped' as const, label: 'rates.mapped' },
  { key: 'unmapped' as const, label: 'rates.unmapped' }
]
const statusFilter = ref<'all' | 'mapped' | 'unmapped'>('all')
const mappedKeys = ref<Set<string>>(new Set())

function sourceKey(providerId: number, group: string): string {
  return `${providerId}:${group}`
}
function isMapped(r: RateSnapshotItem): boolean {
  return mappedKeys.value.has(sourceKey(r.provider_id, r.entity_id))
}

const statusCounts = computed(() => {
  const base = search.value
    ? items.value.filter((r) => r.entity_name.toLowerCase().includes(search.value.toLowerCase()))
    : items.value
  const mapped = base.filter(isMapped).length
  return { all: base.length, mapped, unmapped: base.length - mapped }
})

function providerName(id: number): string {
  return providers.value.find((p) => p.id === id)?.name || '#' + id
}

function fmtRate(v: number): string {
  return Number.isInteger(v) ? String(v) : v.toFixed(4).replace(/\.?0+$/, '')
}

// 涨跌幅（%）：持续展示到下一次变化
function delta(r: RateSnapshotItem): number | null {
  if (r.prev_rate === undefined || r.prev_rate === null || r.prev_rate === 0) return null
  return ((r.rate - r.prev_rate) / r.prev_rate) * 100
}

function durationDays(r: RateSnapshotItem): number {
  const first = new Date(r.first_seen_at).getTime()
  return Math.max(0, Math.floor((Date.now() - first) / 86400000))
}

/**
 * 表头排序接在 filtered（搜索 + 对接状态 tab）之后。
 *
 * prev_rate 在首次快照时为 null，delta 也随之为 null —— sortRows
 * 让这些行在升序降序下都排最后，而不是当 0 处理挤进有涨跌的行之间。
 */
const { sortKey, sortOrder, sorted: sortedRows, toggle: sortBy } = useTableSort<RateSnapshotItem>(
  filtered,
  {
    provider: (r) => providerName(r.provider_id),
    entityType: (r) => r.entity_type,
    entityName: (r) => r.entity_name,
    rate: (r) => r.rate,
    prevRate: (r) => r.prev_rate ?? null,
    delta: (r) => delta(r),
    since: (r) => r.first_seen_at,
    lastSeen: (r) => r.last_seen_at,
    mapped: (r) => (isMapped(r) ? 1 : 0)
  }
)

function setScope(s: 'upstream' | 'local') {
  scope.value = s
  providerId.value = 0
  load()
}

async function load() {
  loading.value = true
  try {
    const res = await rateApi.current({
      scope: scope.value,
      provider_id: scope.value === 'upstream' && providerId.value > 0 ? providerId.value : undefined,
      include_deleted: includeDeleted.value
    })
    items.value = res.items
  } catch (e) {
    app.showError(errorMessage(e))
  } finally {
    loading.value = false
  }
}

// 变更历史弹窗
const showHistory = ref(false)
const historyLoading = ref(false)
const historyItems = ref<RateSnapshotItem[]>([])
const historyTitle = ref('')

async function openHistory(r: RateSnapshotItem) {
  historyTitle.value = r.entity_name + ' · ' + t('rates.historyTitle')
  showHistory.value = true
  historyLoading.value = true
  historyItems.value = []
  try {
    const res = await rateApi.history({
      scope: r.scope,
      provider_id: r.provider_id > 0 ? r.provider_id : undefined,
      entity_type: r.entity_type,
      entity_id: r.entity_id,
      page: 1,
      page_size: 100
    })
    historyItems.value = res.items as unknown as RateSnapshotItem[]
  } catch (e) {
    app.showError(errorMessage(e))
  } finally {
    historyLoading.value = false
  }
}

onMounted(async () => {
  try {
    const res = await providerApi.list(1, 100)
    providers.value = res.items
  } catch {
    // 供应商列表仅用于筛选下拉，失败不阻断
  }
  await Promise.all([load(), loadMapped(), loadLocalGroups()])
})

// ---- 配置对接 ----
const showConnect = ref(false)
const savingRule = ref(false)
const ruleError = ref('')
const connectSource = ref<RateSnapshotItem | null>(null)
const localGroups = ref<LocalGroupOption[]>([])
const existingRules = ref<PricingPreviewRow[]>([])

const emptyRuleForm = () => ({
  local_group_id: 0,
  sourceKeys: [] as string[],
  primaryKey: '',
  price_source: 'primary',
  markup_mode: 'percentage',
  markup_value: 10,
  follow_threshold: 10,
  min_rate: '' as string | number,
  max_rate: '' as string | number,
  auto_enabled: false
})
const ruleForm = ref(emptyRuleForm())

// 所有上游分组作为可选数据源（含当前行，默认勾选）
const upstreamOptions = computed(() =>
  items.value
    .filter((r) => !r.deleted)
    .map((r) => ({
      key: sourceKey(r.provider_id, r.entity_id),
      label: `${providerName(r.provider_id)} / ${r.entity_name}`,
      rate: r.rate
    }))
)
function labelOfKey(key: string): string {
  return upstreamOptions.value.find((o) => o.key === key)?.label || key
}

// 主源只能取自已勾选的数据源
const primarySourceOptions = computed(() =>
  ruleForm.value.sourceKeys.map((key) => ({ value: key, label: labelOfKey(key) }))
)

const localGroupOptions = computed(() =>
  localGroups.value.map((g) => ({
    value: g.id,
    label: `${g.name}（当前 ×${fmtRate(g.rate)}）`
  }))
)

const PRICE_SOURCES = ['primary', 'lowest', 'highest', 'average']
const priceSourceOptions = computed(() =>
  PRICE_SOURCES.map((value) => ({
    value,
    label: t(`rates.source${value.charAt(0).toUpperCase()}${value.slice(1)}`)
  }))
)

const markupModeOptions = computed(() => [
  { value: 'percentage', label: t('rates.markupPercentage') },
  { value: 'fixed', label: t('rates.markupFixed') }
])

// 已有规则提示：选中的本站分组若已配过规则，说明是编辑而非新建
const existingRuleHint = computed(() => {
  if (!ruleForm.value.local_group_id) return ''
  const hit = existingRules.value.find((r) => r.pricing.local_group_id === ruleForm.value.local_group_id)
  return hit ? t('rates.ruleExists') : ''
})

// 实时预估：前端镜像后端公式，改任意字段立刻出结果
const referenceRate = computed<number | null>(() => {
  const keys = ruleForm.value.sourceKeys
  if (!keys.length) return null
  const rates = keys
    .map((k) => upstreamOptions.value.find((o) => o.key === k)?.rate)
    .filter((v): v is number => v !== undefined)
  if (!rates.length) return null
  switch (ruleForm.value.price_source) {
    case 'primary': {
      const p = upstreamOptions.value.find((o) => o.key === ruleForm.value.primaryKey)
      return p ? p.rate : null
    }
    case 'lowest':
      return Math.min(...rates)
    case 'highest':
      return Math.max(...rates)
    case 'average':
      return rates.reduce((a, b) => a + b, 0) / rates.length
    default:
      return null
  }
})

const estimated = computed<number | null>(() => {
  const ref = referenceRate.value
  if (ref === null) return null
  const f = ruleForm.value
  let v = f.markup_mode === 'fixed' ? ref + f.markup_value : ref * (1 + f.markup_value / 100)
  const min = typeof f.min_rate === 'number' ? f.min_rate : parseFloat(String(f.min_rate))
  const max = typeof f.max_rate === 'number' ? f.max_rate : parseFloat(String(f.max_rate))
  if (!Number.isNaN(min) && v < min) v = min
  if (!Number.isNaN(max) && v > max) v = max
  return Math.round(v * 10000) / 10000
})

const estimateFormula = computed(() => {
  const ref = referenceRate.value
  if (ref === null) return ''
  const f = ruleForm.value
  return f.markup_mode === 'fixed'
    ? `${ref.toFixed(4)} + ${f.markup_value}`
    : `${ref.toFixed(4)} × (1 + ${f.markup_value}%)`
})

function onLocalGroupChange() {
  // 切到已有规则的分组时回填其配置，避免误覆盖
  const hit = existingRules.value.find((r) => r.pricing.local_group_id === ruleForm.value.local_group_id)
  if (!hit) return
  const p = hit.pricing
  ruleForm.value = {
    ...ruleForm.value,
    sourceKeys: p.sources.map((s) => sourceKey(s.provider_id, s.upstream_group)),
    primaryKey:
      p.primary_provider_id && p.primary_group ? sourceKey(p.primary_provider_id, p.primary_group) : '',
    price_source: p.price_source,
    markup_mode: p.markup_mode,
    markup_value: p.markup_value,
    follow_threshold: p.follow_threshold,
    min_rate: p.min_rate ?? '',
    max_rate: p.max_rate ?? '',
    auto_enabled: p.auto_enabled
  }
}

function openConnect(r: RateSnapshotItem) {
  connectSource.value = r
  ruleError.value = ''
  const key = sourceKey(r.provider_id, r.entity_id)
  // 已对接：定位到引用了该上游的规则并回填
  const hit = existingRules.value.find((row) =>
    row.pricing.sources.some((s) => sourceKey(s.provider_id, s.upstream_group) === key)
  )
  if (hit) {
    ruleForm.value = { ...emptyRuleForm(), local_group_id: hit.pricing.local_group_id }
    onLocalGroupChange()
  } else {
    ruleForm.value = { ...emptyRuleForm(), sourceKeys: [key], primaryKey: key }
  }
  showConnect.value = true
}

async function loadMapped() {
  try {
    const res = await pricingApi.mapped()
    mappedKeys.value = new Set(res.keys || [])
  } catch {
    mappedKeys.value = new Set()
  }
}

async function loadLocalGroups() {
  try {
    const [g, rules] = await Promise.all([pricingApi.localGroups(), pricingApi.rules()])
    localGroups.value = g.items || []
    existingRules.value = rules.items || []
  } catch {
    // PG 不可用时分组下拉为空，弹窗内会提示
  }
}

async function saveRule() {
  const f = ruleForm.value
  ruleError.value = ''
  if (!f.local_group_id) {
    ruleError.value = t('rates.selectLocalGroup')
    return
  }
  if (!f.sourceKeys.length) {
    ruleError.value = t('rates.needSource')
    return
  }
  if (f.price_source === 'primary' && !f.primaryKey) {
    ruleError.value = t('rates.needPrimary')
    return
  }

  const sources = f.sourceKeys.map((k) => {
    const [pid, ...rest] = k.split(':')
    return { provider_id: Number(pid), upstream_group: rest.join(':') }
  })
  const primary =
    f.price_source === 'primary' && f.primaryKey
      ? (() => {
          const [pid, ...rest] = f.primaryKey.split(':')
          return { provider_id: Number(pid), group: rest.join(':') }
        })()
      : null

  savingRule.value = true
  try {
    await pricingApi.saveRule({
      local_group_id: f.local_group_id,
      local_group_name: localGroups.value.find((g) => g.id === f.local_group_id)?.name || '',
      auto_enabled: f.auto_enabled,
      price_source: f.price_source,
      primary_provider_id: primary?.provider_id ?? null,
      primary_group: primary?.group ?? null,
      markup_mode: f.markup_mode,
      markup_value: f.markup_value,
      follow_threshold: f.follow_threshold,
      min_rate: f.min_rate === '' ? null : Number(f.min_rate),
      max_rate: f.max_rate === '' ? null : Number(f.max_rate),
      sources
    })
    showConnect.value = false
    app.showSuccess(t('common.success'))
    await Promise.all([loadMapped(), loadLocalGroups()])
  } catch (e) {
    ruleError.value = errorMessage(e)
  } finally {
    savingRule.value = false
  }
}
</script>
