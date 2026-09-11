<template>
  <div class="space-y-3">
    <StabilityToolbar
      v-model:grouping="grouping"
      v-model:provider="providerFilter"
      v-model:group="groupFilter"
      v-model:keyword="keyword"
      v-model:minutes="minutes"
      :provider-opts="providerOpts"
      :group-opts="groupOpts"
    >
      <template #after-windows>
        <StabilitySortBar :sort="listSort" @update:sort="listSort = $event" />
      </template>
      <template #end>
        <p v-if="generatedAt" class="whitespace-nowrap text-[11px] text-gray-400">
          {{ t('stability.updatedAt', { t: formatUpdatedAt(generatedAt) }) }}
        </p>
        <button class="btn btn-secondary text-sm" :title="t('common.refresh')" @click="load">
          <Icon name="refresh" size="sm" :class="passiveLoading && 'animate-spin'" />
        </button>
      </template>
    </StabilityToolbar>

    <StabilityKpiCards :kpis="kpis" />

    <div class="card overflow-hidden">
      <LoadingState v-if="passiveLoading && !passiveSections.length" />
      <EmptyState v-else-if="!passiveSections.length" icon="chart" />
      <div v-else class="overflow-x-auto py-3">
        <div
          class="grid items-center gap-x-3 border-b border-gray-100 px-3 pb-2 text-[11px] font-medium uppercase tracking-wider text-gray-400 dark:border-dark-800"
          :style="HEATMAP_GRID"
        >
          <span>{{ t('stability.channelDim') }}</span>
          <span class="text-right">{{ t('stability.successRate') }}</span>
          <span class="text-right">{{ t('stability.requests') }}</span>
          <span class="text-right">{{ t('stability.ftP50') }}</span>
          <span class="text-right">{{ t('stability.tokenPerSec') }}</span>
          <span class="text-right">{{ t('stability.cacheRate') }}</span>
          <span class="flex justify-between font-normal normal-case tracking-normal">
            <span>{{ heatmapStartLabel }}</span>
            <span>{{ heatmapEndLabel }}</span>
          </span>
        </div>
        <div class="divide-y divide-gray-50 dark:divide-dark-800">
          <StabilityHeatmapRow
            v-for="row in heatmapRows"
            :key="row.model.key || '__empty__'"
            :model="row.model"
            :minutes="minutes"
            :generated-at="generatedAt"
            @open-row="openPassiveSection(row.sec)"
            @open-cell="openPassiveCell(row.sec, $event)"
          />
        </div>
        <div class="mt-3 flex flex-wrap items-center gap-3 px-4 text-[11px] text-gray-400">
          <span class="inline-flex items-center gap-1">
            <span class="h-2 w-2 rounded-full bg-emerald-500" />{{ t('stability.tone.healthy') }} ≥{{ HEATMAP_HEALTH }}
          </span>
          <span class="inline-flex items-center gap-1">
            <span class="h-2 w-2 rounded-full bg-amber-400" />{{ t('stability.tone.watch') }} {{ HEATMAP_WATCH }}–{{ HEATMAP_HEALTH - 1 }}
          </span>
          <span class="inline-flex items-center gap-1">
            <span class="h-2 w-2 rounded-full bg-red-500" />{{ t('stability.tone.bad') }} &lt;{{ HEATMAP_WATCH }}
          </span>
          <span class="inline-flex items-center gap-1">
            <span class="h-2 w-2 rounded-full bg-gray-300" />{{ t('stability.live.empty') }}
          </span>
          <span class="ml-auto">{{ t('stability.cellGranularity', { n: cellGranularity }) }}</span>
        </div>
      </div>
    </div>

    <PassiveDetailDialog
      :show="showPassiveDetail"
      :title="passiveDialogTitle"
      :bucket-hint="passiveDialogHint"
      :models="passiveDialogModels"
      :minutes="minutes"
      :generated-at="generatedAt"
      :highlight="passiveDialogHighlight"
      @close="showPassiveDetail = false"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { stabilityApi } from '@/api'
import { errorMessage } from '@/api/client'
import { useAppStore } from '@/stores/app'
import {
  filterRows,
  searchStabilityRows,
  providerOptions,
  groupOptions,
  rowGrade,
  PASSIVE_RATE_BANDS,
  DEFAULT_WINDOW_MINUTES,
  type FilterableRow,
  type SearchableRow,
  type WindowMinutes
} from '@/utils/stabilityModel'
import {
  buildSections,
  passiveCounts,
  DEFAULT_GROUPING,
  type GroupingMode,
  type StabilitySection
} from '@/utils/stabilitySections'
import {
  cardFromRow,
  cardFromSection,
  pageKpis,
  sortHeatmapModels,
  DEFAULT_HEATMAP_SORT,
  type HeatmapSort,
  type StatusCardModel
} from '@/utils/stabilityMetrics'
import { slaPercent } from '@/utils/stabilityModel'
import {
  HEATMAP_GRID,
  HEATMAP_HEALTH,
  HEATMAP_WATCH,
  cellEndIso,
  displayCellCount,
  displayCells
} from '@/utils/stabilityTimeline'
import LoadingState from '@/components/common/LoadingState.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Icon from '@/components/icons/Icon.vue'
import StabilityToolbar from '@/components/stability/StabilityToolbar.vue'
import StabilitySortBar from '@/components/stability/StabilitySortBar.vue'
import StabilityKpiCards from '@/components/stability/StabilityKpiCards.vue'
import StabilityHeatmapRow from '@/components/stability/StabilityHeatmapRow.vue'
import PassiveDetailDialog from '@/components/stability/PassiveDetailDialog.vue'
import type { PassiveRow } from '@/types'

const { t } = useI18n()
const app = useAppStore()

const grouping = ref<GroupingMode>(DEFAULT_GROUPING)
const minutes = ref<WindowMinutes>(DEFAULT_WINDOW_MINUTES)
const providerFilter = ref<string | null>(null)
const groupFilter = ref<string | null>(null)
const keyword = ref('')

const passive = ref<PassiveRow[]>([])
const passiveLoading = ref(false)
const generatedAt = ref('')
const showPassiveDetail = ref(false)
const passiveDialogTitle = ref('')
const passiveDialogHint = ref('')
const passiveDialogModels = ref<StatusCardModel[]>([])
const passiveDialogHighlight = ref<number | null>(null)
const listSort = ref<HeatmapSort>({ ...DEFAULT_HEATMAP_SORT })

const optionSource = computed<FilterableRow[]>(() => passive.value)
const providerOpts = computed(() => providerOptions(optionSource.value))
const groupOpts = computed(() => groupOptions(optionSource.value))

function visibleRows<T extends FilterableRow & SearchableRow>(rows: T[]): T[] {
  return searchStabilityRows(
    filterRows(rows, providerFilter.value, null, () => undefined, groupFilter.value),
    keyword.value
  )
}

const passiveRows = computed(() => visibleRows(passive.value))

function passiveGrade(r: PassiveRow) {
  return rowGrade({
    ttftMs: r.first_token_p50,
    successRate: r.sla,
    rateBands: PASSIVE_RATE_BANDS
  })
}

const passiveSections = computed(() =>
  buildSections(passiveRows.value, grouping.value, passiveGrade, passiveCounts)
)

const heatmapRows = computed(() => {
  const items = passiveSections.value.map((sec) => ({
    sec,
    model: sectionCard(sec)
  }))
  const sorted = sortHeatmapModels(
    items.map((it) => it.model),
    listSort.value.key,
    listSort.value.order
  )
  const byKey = new Map(items.map((it) => [it.model.key, it.sec]))
  return sorted.map((model) => ({ model, sec: byKey.get(model.key)! }))
})

const kpis = computed(() => pageKpis(passiveRows.value, minutes.value))

const cellGranularity = computed(() => {
  const mins = minutes.value / displayCellCount(minutes.value)
  if (mins < 1) return `${Math.round(mins * 60)}s`
  return `${mins}m`
})

const heatmapStartLabel = computed(() => formatUpdatedAt(heatmapRangeStart()))
const heatmapEndLabel = computed(() => formatUpdatedAt(generatedAt.value))

function heatmapRangeStart(): string {
  const end = Date.parse(generatedAt.value)
  if (!Number.isFinite(end)) return ''
  return new Date(end - minutes.value * 60 * 1000).toISOString()
}

function formatUpdatedAt(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${pad(d.getMonth() + 1)}/${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

function sectionTitle(sec: StabilitySection<PassiveRow>): string {
  if (sec.label) return sec.label
  return grouping.value === 'group' ? t('stability.ungrouped') : t('stability.unassigned')
}

function uniqueJoined(values: readonly string[]): string {
  const seen = new Set<string>()
  const out: string[] = []
  for (const raw of values) {
    const v = raw.trim()
    if (!v || seen.has(v)) continue
    seen.add(v)
    out.push(v)
  }
  return out.join(' · ')
}

function rowSubtitle(row: PassiveRow): string {
  if (grouping.value === 'provider') return (row.groups ?? []).join(' · ')
  return row.provider_name || t('stability.unassigned')
}

function sectionCard(sec: StabilitySection<PassiveRow>): StatusCardModel {
  const model = cardFromSection(sec.rows, sectionTitle(sec))
  const subtitle =
    grouping.value === 'provider'
      ? uniqueJoined(sec.rows.flatMap((r) => r.groups ?? []))
      : uniqueJoined(sec.rows.map((r) => r.provider_name))
  return { ...model, subtitle }
}

function dialogModels(sec: StabilitySection<PassiveRow>): StatusCardModel[] {
  return sortHeatmapModels(
    sec.rows.map((r) => cardFromRow(r, rowSubtitle(r))),
    listSort.value.key,
    listSort.value.order
  )
}

function openPassiveSection(sec: StabilitySection<PassiveRow>) {
  passiveDialogTitle.value = sectionTitle(sec)
  passiveDialogHint.value = ''
  passiveDialogHighlight.value = null
  passiveDialogModels.value = dialogModels(sec)
  showPassiveDetail.value = true
}

function openPassiveCell(sec: StabilitySection<PassiveRow>, index: number) {
  const cells = displayCells(sectionCard(sec).timeline, minutes.value, generatedAt.value)
  const cell = cells[index]
  const title = sectionTitle(sec)
  passiveDialogTitle.value = title
  if (cell) {
    const start = formatUpdatedAt(cell.t)
    const end = formatUpdatedAt(cellEndIso(cell.t, minutes.value))
    const sla = slaPercent(cell.ok, cell.err)
    const slaText = sla == null ? t('stability.live.empty') : `${sla.toFixed(1)}%`
    passiveDialogHint.value = t('stability.bucketHint', {
      start,
      end,
      sla: slaText,
      ok: cell.ok,
      err: cell.err
    })
  } else {
    passiveDialogHint.value = ''
  }
  passiveDialogHighlight.value = index
  passiveDialogModels.value = dialogModels(sec)
  showPassiveDetail.value = true
}

async function loadPassive() {
  passiveLoading.value = true
  try {
    const res = await stabilityApi.passive(minutes.value)
    generatedAt.value = res.generated_at || new Date().toISOString()
    passive.value = (res.items || []).map((r) => {
      const success = r.success_count ?? r.requests ?? 0
      const error = r.error_count ?? 0
      return {
        ...r,
        groups: r.groups ?? [],
        success_count: success,
        error_count: error,
        requests: success + error,
        sla: r.sla ?? null,
        timeline: r.timeline ?? []
      }
    })
  } catch (e) {
    app.showError(errorMessage(e))
  } finally {
    passiveLoading.value = false
  }
}

async function load() {
  showPassiveDetail.value = false
  await loadPassive()
}

watch(minutes, () => {
  void load()
})

onMounted(load)
</script>
