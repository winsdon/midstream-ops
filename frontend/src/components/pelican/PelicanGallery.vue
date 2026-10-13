<template>
  <section class="space-y-3">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('pelican.results') }}</h2>
      <slot name="actions" />
    </div>
    <div class="-mx-1 px-1 py-2" :class="admin ? 'sticky top-16 z-20 bg-gray-50/95 backdrop-blur dark:bg-dark-950/95' : ''">
      <div v-if="admin && selected.length" class="card flex flex-wrap items-center gap-2 p-3">
        <span class="text-sm font-medium">{{ t('pelican.selected', { count: selected.length }) }}</span>
        <button type="button" class="btn btn-secondary btn-sm" @click="togglePage">{{ t('pelican.selectPage') }}</button>
        <button type="button" class="btn btn-primary btn-sm" :disabled="busy || !canPublish" @click="mutate('publish')">{{ t('pelican.publish') }}</button>
        <button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="mutate('unpublish')">{{ t('pelican.unpublish') }}</button>
        <button type="button" class="btn btn-secondary btn-sm text-red-600" :disabled="busy || !canDelete" @click="mutate('delete')">{{ t('pelican.delete') }}</button>
        <button type="button" class="btn btn-ghost btn-sm ml-auto" @click="clearSelection">{{ t('pelican.clearSelection') }}</button>
      </div>
      <div v-else class="card flex flex-wrap items-end gap-2 p-3">
        <label class="min-w-36 flex-1 text-sm">{{ t('pelican.group') }}
          <Select v-model="groupID" class="mt-1 w-full" :options="groupSelectOptions" @change="applyFilters" />
        </label>
        <label class="min-w-36 flex-1 text-sm">{{ t('pelican.model') }}
          <input v-model="model" list="pelican-filter-models" class="input mt-1 w-full" :placeholder="t('pelican.allModels')" @input="queueTextFilter" />
          <datalist id="pelican-filter-models"><option v-for="name in modelOptions" :key="name" :value="name" /></datalist>
        </label>
        <label class="text-sm">{{ t('pelican.from') }}<input v-model="from" type="date" class="input mt-1 block" @change="applyFilters" /></label>
        <label class="text-sm">{{ t('pelican.to') }}<input v-model="to" type="date" :min="from" class="input mt-1 block" @change="applyFilters" /></label>
        <div v-if="!admin" class="flex rounded-lg bg-gray-100 p-0.5 text-xs dark:bg-dark-800" role="group" :aria-label="t('pelican.history')">
          <button type="button" class="rounded-md px-2.5 py-1.5 font-medium" :class="history ? idleTab : activeTab" @click="setHistory(false)">{{ t('pelican.latest') }}</button>
          <button type="button" class="rounded-md px-2.5 py-1.5 font-medium" :class="history ? activeTab : idleTab" @click="setHistory(true)">{{ t('pelican.history') }}</button>
        </div>
        <button type="button" class="btn btn-secondary btn-sm" :disabled="loading" @click="refresh">
          <Icon name="refresh" size="sm" />
          {{ t('pelican.reload') }}
        </button>
      </div>
    </div>

    <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950 dark:text-red-300">{{ error }}</p>
    <EmptyState v-else-if="!items.length" icon="sparkles" :title="loading ? t('common.loading') : t('pelican.empty')" :description="!loading && !admin ? t('pelican.emptyHint') : undefined" />
    <div v-else class="grid gap-3 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">
      <article
        v-for="item in items"
        :key="item.id"
        class="card flex cursor-pointer flex-col overflow-hidden outline-none transition hover:border-primary-200 focus-visible:border-primary-400 dark:hover:border-primary-800"
        tabindex="0"
        @click="openDetail(item.id)"
        @keydown.enter.prevent="openDetail(item.id)"
      >
        <header class="flex items-start justify-between gap-2 px-2.5 pt-2">
          <div class="min-w-0">
            <h3 class="truncate text-sm font-semibold text-gray-900 dark:text-white" :title="item.model">{{ item.model }}</h3>
            <p class="truncate text-xs text-gray-500" :title="ownerTitle(item)">
              {{ item.group_name }}<template v-if="ownerLabel(item)"> · {{ ownerLabel(item) }}</template>
            </p>
          </div>
          <input
            v-if="admin"
            v-model="selected"
            type="checkbox"
            class="mt-0.5"
            :value="item.id"
            :aria-label="`${t('pelican.select')} ${item.group_name} ${item.model}`"
            @click.stop
          />
        </header>
        <div class="relative mx-2 mt-1.5 aspect-[16/10] overflow-hidden rounded-lg border border-gray-100 bg-white dark:border-dark-700">
          <PelicanPreview v-if="item.has_document" :id="item.id" :title="item.group_name" :fetch-result="api.detail" />
          <div v-else class="flex h-full flex-col items-center justify-center gap-2 bg-gray-50 p-3 text-center text-xs text-gray-500 dark:bg-dark-900">
            <LoadingSpinner v-if="pelicanPending(item.status)" />
            <span>{{ t(`pelican.statuses.${item.status}`) }}</span>
          </div>
        </div>
        <div class="flex flex-wrap items-center gap-x-1.5 gap-y-1 px-2.5 py-1.5 text-xs text-gray-500">
          <time :datetime="item.tested_at" :title="fullDate(item.tested_at)">{{ date(item.tested_at) }}</time>
          <span v-if="item.duration_ms > 0">· {{ fmtMs(item.duration_ms) }}</span>
          <Badge v-if="admin" :variant="pelicanStatusVariant(item.status)">{{ t(`pelican.statuses.${item.status}`) }}</Badge>
          <Badge v-if="admin" :variant="item.published_at ? 'success' : 'gray'">{{ t(item.published_at ? 'pelican.published' : 'pelican.draft') }}</Badge>
        </div>
        <p v-if="admin && item.error" class="line-clamp-2 break-words px-2.5 pb-2 text-xs text-red-600">{{ t(item.error) }}</p>
      </article>
    </div>
    <div class="flex flex-wrap items-center justify-between gap-3 text-sm">
      <span>{{ t('pelican.page', { page, pages, total }) }}</span>
      <div class="flex gap-2">
        <button type="button" class="btn btn-secondary btn-sm" :disabled="page <= 1 || loading" @click="page--; refresh()">{{ t('pelican.previous') }}</button>
        <button type="button" class="btn btn-secondary btn-sm" :disabled="page >= pages || loading" @click="page++; refresh()">{{ t('pelican.next') }}</button>
      </div>
    </div>
    <PelicanDetail
      v-model:retry-account-id="retryAccountID"
      v-model:retry-key="retryKey"
      :detail="detail"
      :admin="admin"
      :accounts="accounts"
      :busy="busy"
      :fetch-result="api.detail"
      @close="closeDetail"
      @delete="deleteOpen"
      @retry="retry"
      @cancel="cancel"
    />
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { embedPelicanAPI, pelicanAPI } from '@/api/pelican'
import { errorMessage } from '@/api/client'
import Badge from '@/components/common/Badge.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import { fmtDateTime, fmtMs } from '@/utils/format'
import { pelicanAccountsForGroup, pelicanDateRange, pelicanDeletable, pelicanHost, pelicanPending, pelicanRequestID, pelicanStatusVariant } from '@/utils/pelican'
import type { PelicanAccount, PelicanFacet, PelicanGroup, PelicanResult } from '@/types/pelican'
import PelicanDetail from './PelicanDetail.vue'
import PelicanPreview from './PelicanPreview.vue'

const props = defineProps<{ admin?: boolean; groups?: PelicanGroup[]; accounts?: PelicanAccount[] }>()
const { t, locale } = useI18n()
const api = props.admin ? pelicanAPI : embedPelicanAPI
const activeTab = 'bg-white text-gray-900 shadow-sm dark:bg-dark-700 dark:text-white'
const idleTab = 'text-gray-500 hover:text-gray-700 dark:text-dark-400'
const items = ref<PelicanResult[]>([])
const facets = ref<PelicanFacet[]>([])
const selected = ref<number[]>([])
const detail = ref<PelicanResult | null>(null)
const groupID = ref('')
const model = ref('')
const from = ref('')
const to = ref('')
const history = ref(false)
const page = ref(1)
const pages = ref(1)
const total = ref(0)
const loading = ref(false)
const busy = ref(false)
const error = ref('')
const retryAccountID = ref(0)
const retryKey = ref('')
let version = 0
let detailVersion = 0
let timer: ReturnType<typeof setTimeout> | undefined
let textTimer: ReturnType<typeof setTimeout> | undefined
let disposed = false
let retryRequest: { fingerprint: string; request: string } | undefined
let appliedFilters = ''

const groupOptions = computed(() => props.admin ? (props.groups ?? []) : Array.from(new Map(facets.value.map(facet => [facet.group_id, { id: facet.group_id, name: facet.group_name }])).values()))
const groupSelectOptions = computed(() => [{ value: '', label: t('pelican.allGroups') }, ...groupOptions.value.map(group => ({ value: String(group.id), label: group.name }))])
const modelOptions = computed(() => [...new Set(props.admin ? (props.groups ?? []).filter(group => !groupID.value || String(group.id) === groupID.value).flatMap(group => group.models) : facets.value.filter(facet => !groupID.value || String(facet.group_id) === groupID.value).map(facet => facet.model))])
const chosen = computed(() => items.value.filter(item => selected.value.includes(item.id)))
const canPublish = computed(() => chosen.value.every(item => item.status === 'completed' && item.has_document))
const canDelete = computed(() => chosen.value.every(pelicanDeletable))
const pageIds = computed(() => items.value.map(item => item.id))

function date(value: string) { return fmtDateTime(value) }
function fullDate(value: string) { return new Date(value).toLocaleString(locale.value, { timeZoneName: 'short' }) }
/** 所属账号：优先用记录里的名称快照，旧记录回落到账号列表；只给管理端看。 */
function ownerLabel(item: PelicanResult): string {
  if (!props.admin) return ''
  if (item.account_name) return item.account_name
  if (item.account_id) return (props.accounts ?? []).find(account => account.id === item.account_id)?.name ?? ''
  if (item.source === 'manual') return item.base_url ? `${t('pelican.sourceManualShort')} ${pelicanHost(item.base_url)}` : ''
  return ''
}
function ownerTitle(item: PelicanResult) {
  const owner = ownerLabel(item)
  return owner ? `${item.group_name} · ${owner}` : item.group_name
}
function closeDetail() { detailVersion++; detail.value = null; retryKey.value = '' }
function clearSelection() { selected.value = [] }
function togglePage() {
  if (pageIds.value.length && pageIds.value.every(id => selected.value.includes(id))) {
    const drop = new Set(pageIds.value)
    selected.value = selected.value.filter(id => !drop.has(id))
    return
  }
  selected.value = [...new Set([...selected.value, ...pageIds.value])]
}
function deleteOpen() { if (detail.value) void remove(detail.value) }
function filterSignature() {
  return [groupID.value, model.value, from.value, to.value, history.value ? '1' : '0'].join('\u0000')
}
function applyFilters() {
  const next = filterSignature()
  if (next === appliedFilters) return
  appliedFilters = next
  page.value = 1
  clearTimeout(textTimer)
  void refresh()
}
function queueTextFilter() {
  clearTimeout(textTimer)
  textTimer = setTimeout(applyFilters, 300)
}
function setHistory(value: boolean) {
  if (history.value === value) return
  history.value = value
  applyFilters()
}

async function openDetail(id: number) {
  const own = ++detailVersion
  try {
    const value = await api.detail(id)
    if (own !== detailVersion || disposed) return
    detail.value = value
    retryKey.value = ''
    const known = (props.accounts ?? []).some(account => account.id === value.account_id)
    // 记录的账号已失效时，优先预选同一归属分组下唯一可选账号，与详情的选项范围一致。
    const scoped = pelicanAccountsForGroup(props.accounts ?? [], value.group_id)
    const fallback = scoped.length ? scoped : (props.accounts ?? [])
    retryAccountID.value = value.account_id && known ? value.account_id : (fallback.length === 1 ? fallback[0].id : 0)
  } catch (e) { if (own === detailVersion) error.value = t(errorMessage(e)) }
}

async function refresh() {
  const own = ++version
  clearTimeout(timer)
  loading.value = true
  try {
    const query = new URLSearchParams({ page: String(page.value), page_size: '12', history: String(history.value), ...pelicanDateRange(from.value, to.value) })
    if (groupID.value) query.set('group_id', groupID.value)
    if (model.value) query.set('model', model.value)
    const [data, filters] = await Promise.all([api.list(query.toString()), props.admin ? Promise.resolve(null) : embedPelicanAPI.filters()])
    if (own !== version || disposed) return
    items.value = data.items
    pages.value = data.pages
    total.value = data.total
    error.value = ''
    if (filters) facets.value = filters.items
    selected.value = selected.value.filter(id => items.value.some(item => item.id === id))
    if (detail.value) {
      const id = detail.value.id
      try {
        const updated = await api.detail(id)
        if (detail.value?.id === id) detail.value = updated
      } catch { closeDetail() }
    }
  } catch (e) {
    if (own === version) { error.value = t(errorMessage(e)); items.value = []; closeDetail() }
  } finally {
    if (own === version && !disposed) { loading.value = false; timer = setTimeout(refresh, props.admin ? 5000 : 30000) }
  }
}

async function mutate(action: string) {
  if (action === 'delete' && !window.confirm(t('pelican.confirmDelete'))) return
  busy.value = true
  try { await pelicanAPI.mutate(action, selected.value); selected.value = []; await refresh() }
  catch (e) { error.value = t(errorMessage(e)) } finally { busy.value = false }
}

async function remove(item: PelicanResult) {
  if (!window.confirm(t('pelican.confirmDeleteOne'))) return
  busy.value = true
  try {
    await pelicanAPI.mutate('delete', [item.id])
    selected.value = selected.value.filter(id => id !== item.id)
    if (detail.value?.id === item.id) closeDetail()
    await refresh()
  } catch (e) { error.value = t(errorMessage(e)) } finally { busy.value = false }
}

async function cancel(id: number) {
  busy.value = true
  try { await pelicanAPI.cancel(id); await refresh() } catch (e) { error.value = t(errorMessage(e)) } finally { busy.value = false }
}

async function retry() {
  const record = detail.value
  if (!record || !record.source) return
  const account = record.source === 'account' ? retryAccountID.value : 0
  const key = record.source === 'manual' ? retryKey.value.trim() : ''
  if (record.source === 'account' && !account) return
  if (record.source === 'manual' && !key) return
  const fingerprint = `${record.id}|${account}|${key}`
  if (!retryRequest || retryRequest.fingerprint !== fingerprint) retryRequest = { fingerprint, request: pelicanRequestID() }
  busy.value = true
  try {
    await pelicanAPI.retry(record.id, { request_id: retryRequest.request, account_id: account || undefined, api_key: key || undefined })
    retryRequest = undefined
    closeDetail()
    await refresh()
  } catch (e) { error.value = t(errorMessage(e)) } finally { busy.value = false }
}

appliedFilters = filterSignature()

onMounted(refresh)
onBeforeUnmount(() => { disposed = true; version++; detailVersion++; clearTimeout(timer); clearTimeout(textTimer) })
defineExpose({ refresh })
</script>
