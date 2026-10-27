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
              <td>
                <button class="text-xs text-gray-500 hover:underline dark:text-dark-400" @click="openHistory(r)">
                  {{ t('rates.viewHistory') }}
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

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
import { providerApi, rateApi } from '@/api'
import { errorMessage } from '@/api/client'
import { useAppStore } from '@/stores/app'
import { useTableSort } from '@/composables/useTableSort'
import type { Provider, RateSnapshotItem } from '@/types'
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
  return list
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
 * 表头排序接在 filtered（搜索）之后。
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
    lastSeen: (r) => r.last_seen_at
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
  await load()
})
</script>
