<template>
  <section class="space-y-3">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <h2 class="text-base font-semibold">{{ t('pelican.results') }}</h2>
      <button class="btn btn-secondary btn-sm" :disabled="loading" @click="refresh">{{ t('pelican.reload') }}</button>
    </div>
    <form class="card flex flex-wrap items-end gap-2 p-3" @submit.prevent="page = 1; refresh()">
      <label class="min-w-36 flex-1 text-sm">{{ t('pelican.group') }}
        <Select v-model="groupID" class="mt-1 w-full" :options="groupSelectOptions" />
      </label>
      <label class="min-w-36 flex-1 text-sm">{{ t('pelican.model') }}<input v-model="model" list="pelican-filter-models" class="input mt-1 w-full" :placeholder="t('pelican.allModels')" /><datalist id="pelican-filter-models"><option v-for="m in modelOptions" :key="m" :value="m" /></datalist></label>
      <label class="text-sm">{{ t('pelican.from') }}<input v-model="from" type="date" class="input mt-1 block" /></label>
      <label class="text-sm">{{ t('pelican.to') }}<input v-model="to" type="date" :min="from" class="input mt-1 block" /></label>
      <label v-if="admin" class="text-sm">{{ t('pelican.batchFilter') }}<input v-model="batchID" type="number" min="1" class="input mt-1 block w-24" /></label>
      <Select v-else v-model="history" class="min-w-36" :options="historyOptions" :searchable="false" :aria-label="t('pelican.history')" />
      <button class="btn btn-primary btn-sm" :disabled="loading">{{ t('pelican.apply') }}</button>
    </form>
    <div v-if="admin && selected.length" class="flex flex-wrap items-center gap-2">
      <span class="mr-2 text-sm">{{ t('pelican.selected', { count: selected.length }) }}</span>
      <button class="btn btn-primary btn-sm" :disabled="busy || !canPublish" @click="mutate('publish')">{{ t('pelican.publish') }}</button>
      <button class="btn btn-secondary btn-sm" :disabled="busy" @click="mutate('unpublish')">{{ t('pelican.unpublish') }}</button>
      <button class="btn btn-secondary btn-sm text-red-600" :disabled="busy || !canDelete" @click="mutate('delete')">{{ t('pelican.delete') }}</button>
    </div>
    <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950 dark:text-red-300">{{ error }}</p>
    <div v-if="!error && !items.length" class="card p-10 text-center text-gray-500">
      <p>{{ loading ? t('common.loading') : t('pelican.empty') }}</p><p v-if="!admin" class="mt-2 text-sm">{{ t('pelican.emptyHint') }}</p>
    </div>
    <div v-if="!error" class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3 2xl:grid-cols-4">
      <article v-for="item in items" :key="item.id" class="card flex flex-col overflow-hidden">
        <header class="flex items-start justify-between gap-2 px-3 pt-2.5">
          <div class="min-w-0">
            <h3 class="truncate text-sm font-semibold" :title="item.group_name">{{ item.group_name }}</h3>
            <p class="truncate text-xs text-gray-500" :title="item.model">{{ item.model }}</p>
          </div>
          <input v-if="admin" v-model="selected" type="checkbox" class="mt-0.5" :value="item.id" :aria-label="`${t('pelican.select')} ${item.group_name} ${item.model}`" />
        </header>
        <div class="relative mx-3 mt-2 aspect-[16/10] overflow-hidden rounded-lg border border-gray-100 bg-white dark:border-dark-700">
          <PelicanPreview v-if="item.has_document" :id="item.id" :title="item.group_name" :fetch-result="api.detail" />
          <div v-else class="flex h-full items-center justify-center bg-gray-50 p-3 text-center text-xs text-gray-500 dark:bg-dark-900">{{ t(`pelican.statuses.${item.status}`) }}</div>
        </div>
        <div class="flex flex-wrap items-center gap-x-2 gap-y-1 px-3 py-2 text-xs text-gray-500">
          <time :datetime="item.tested_at" :title="fullDate(item.tested_at)">{{ date(item.tested_at) }}</time>
          <span v-if="item.duration_ms > 0">· {{ fmtMs(item.duration_ms) }}</span>
          <span class="badge">{{ t(`pelican.statuses.${item.status}`) }}</span>
          <span v-if="admin" class="badge" :class="item.published_at ? 'text-emerald-600' : 'text-gray-500'">{{ t(item.published_at ? 'pelican.published' : 'pelican.draft') }}</span>
        </div>
        <p v-if="admin && item.error" class="break-words px-3 pb-1 text-xs text-red-600">{{ t(item.error) }}</p>
        <div class="mt-auto flex flex-wrap gap-2 border-t border-gray-100 px-3 py-2 dark:border-dark-700">
          <button class="btn btn-secondary btn-sm" @click="openDetail(item.id)">{{ t('pelican.detail') }}</button>
          <button v-if="admin" class="btn btn-secondary btn-sm text-red-600" :disabled="busy || !deletable(item)" :title="deleteHint(item)" @click="remove(item)">{{ t('pelican.delete') }}</button>
          <button v-if="admin && pelicanPending(item.status) && item.batch_id" class="btn btn-secondary btn-sm" :disabled="busy" @click="cancel(item.batch_id)">{{ t('pelican.cancel') }}</button>
        </div>
      </article>
    </div>
    <div class="flex flex-wrap items-center justify-between gap-3 text-sm">
      <span>{{ t('pelican.page', { page, pages, total }) }}</span>
      <div class="flex gap-2"><button class="btn btn-secondary btn-sm" :disabled="page <= 1 || loading" @click="page--; refresh()">{{ t('pelican.previous') }}</button><button class="btn btn-secondary btn-sm" :disabled="page >= pages || loading" @click="page++; refresh()">{{ t('pelican.next') }}</button></div>
    </div>
    <BaseDialog :show="detail !== null" :title="detail ? `${detail.group_name} · ${detail.model}` : ''" width="full" @close="closeDetail">
      <template v-if="detail">
        <div v-if="detail.has_document">
          <div v-if="admin" class="mb-2 flex rounded-lg bg-gray-100 p-0.5 text-xs dark:bg-dark-800">
            <button v-for="mode in VIEW_MODES" :key="mode" type="button" class="rounded-md px-2.5 py-1 font-medium transition-colors"
              :class="view === mode ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-700 dark:text-white' : 'text-gray-500 hover:text-gray-700 dark:text-dark-400'"
              @click="view = mode">{{ mode === 'preview' ? t('pelican.previewMode') : t('pelican.sourceMode') }}</button>
          </div>
          <!-- 作品渲染在固定虚拟视口里等比缩放：整幅可见、没有 iframe 自己的滚动条 -->
          <div v-if="view === 'preview'" class="aspect-[16/10] max-h-[60vh] w-full overflow-hidden rounded-lg border border-gray-100 dark:border-dark-700">
            <PelicanPreview :key="detail.id" :id="detail.id" :title="detail.group_name" :fetch-result="api.detail" interactive />
          </div>
          <pre v-else class="max-h-[60vh] overflow-auto whitespace-pre-wrap break-all rounded-lg bg-gray-50 p-3 text-xs dark:bg-dark-900">{{ detail.output?.document }}</pre>
        </div>
        <div class="mt-3 space-y-3 text-sm">
          <p class="text-gray-500">{{ t('pelican.testedAt') }}: <time :datetime="detail.tested_at">{{ fullDate(detail.tested_at) }}</time> · {{ t('pelican.duration') }}: {{ fmtMs(detail.duration_ms) }}</p>
          <p v-if="detail.published_at" class="text-gray-500">{{ t('pelican.publishedAt') }}: {{ fullDate(detail.published_at) }}</p>
          <p class="text-gray-500">{{ t('pelican.parameters') }}: {{ protocolLabel(detail.protocol) }} · max_tokens={{ detail.max_tokens }}<span v-if="detail.source"> · {{ detail.source === 'manual' ? t('pelican.sourceManual') : t('pelican.sourceAccount') }}</span></p>
          <h4 class="font-semibold">{{ t('pelican.prompt') }}</h4><pre class="whitespace-pre-wrap break-words rounded-lg bg-gray-50 p-3 dark:bg-dark-900">{{ detail.prompt }}</pre>
          <p v-if="admin && detail.error" class="text-red-600">{{ t(detail.error) }}</p>
          <template v-if="admin">
            <details><summary class="cursor-pointer">{{ t('pelican.parameters') }}</summary><pre class="mt-2 max-h-80 overflow-auto whitespace-pre-wrap break-words">{{ JSON.stringify(detail.output?.request, null, 2) }}</pre></details>
            <details><summary class="cursor-pointer">{{ t('pelican.reply') }}</summary><pre class="mt-2 max-h-80 overflow-auto whitespace-pre-wrap break-words">{{ detail.output?.text }}</pre></details>
            <details><summary class="cursor-pointer">{{ t('pelican.usage') }}</summary><pre class="mt-2 overflow-auto">{{ JSON.stringify(detail.output?.usage, null, 2) }}</pre></details>
            <div class="flex flex-wrap items-end gap-2">
              <button class="btn btn-secondary text-red-600" :disabled="busy || !deletable(detail)" :title="deleteHint(detail)" @click="remove(detail)">{{ t('pelican.delete') }}</button>
              <template v-if="!pelicanPending(detail.status) && detail.source === 'account'">
                <Select v-model="retryAccountID" class="min-w-0 w-full sm:w-72" :options="retryAccountOptions" :placeholder="t('pelican.selectAccount')" searchable :aria-label="t('pelican.account')" />
                <button class="btn btn-primary" :disabled="busy || !retryAccountID" @click="retry">{{ t('pelican.retry') }}</button>
              </template>
              <template v-else-if="!pelicanPending(detail.status) && detail.source === 'manual'">
                <label class="min-w-0 w-full text-sm sm:w-72">{{ t('pelican.apiKey') }}
                  <input v-model.trim="retryKey" class="input mt-1 w-full" type="password" autocomplete="off" :placeholder="t('pelican.retryKeyHint')" />
                </label>
                <button class="btn btn-primary" :disabled="busy || !retryKey" @click="retry">{{ t('pelican.retry') }}</button>
              </template>
              <p v-else-if="!pelicanPending(detail.status)" class="text-xs text-gray-500">{{ t('pelican.legacyRetryHint') }}</p>
            </div>
          </template>
        </div>
      </template>
    </BaseDialog>
  </section>
</template>
<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { pelicanAPI, embedPelicanAPI } from '@/api/pelican'
import { errorMessage } from '@/api/client'
import { fmtDateTime, fmtMs } from '@/utils/format'
import { pelicanDateRange, pelicanHost, pelicanPending, pelicanRequestID } from '@/utils/pelican'
import type { PelicanAccount, PelicanFacet, PelicanGroup, PelicanProtocol, PelicanResult } from '@/types/pelican'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import PelicanPreview from './PelicanPreview.vue'
const props = defineProps<{ admin?: boolean; groups?: PelicanGroup[]; accounts?: PelicanAccount[] }>()
const emit = defineEmits<{ submitted: [id: number] }>()
const { t, locale } = useI18n()
const api = props.admin ? pelicanAPI : embedPelicanAPI
const VIEW_MODES = ['preview', 'source'] as const
const PROTOCOL_LABELS: Record<PelicanProtocol, string> = { messages: 'Messages', responses: 'Responses', chat: 'Chat Completions' }
const items = ref<PelicanResult[]>([]), facets = ref<PelicanFacet[]>([])
const selected = ref<number[]>([]), detail = ref<PelicanResult | null>(null)
const groupID = ref(''), model = ref(''), from = ref(''), to = ref(''), batchID = ref('')
const history = ref(false), page = ref(1), pages = ref(1), total = ref(0)
const loading = ref(false), busy = ref(false), error = ref(''), view = ref<(typeof VIEW_MODES)[number]>('preview')
const retryAccountID = ref(0), retryKey = ref('')
let version = 0, detailVersion = 0, timer: ReturnType<typeof setTimeout> | undefined, disposed = false
let retryRequest: { fingerprint: string; request: string } | undefined
const groupOptions = computed(() => props.admin ? (props.groups ?? []) : Array.from(new Map(facets.value.map(f => [f.group_id, { id: f.group_id, name: f.group_name }])).values()))
const groupSelectOptions = computed(() => [{ value: '', label: t('pelican.allGroups') }, ...groupOptions.value.map(g => ({ value: String(g.id), label: g.name }))])
const historyOptions = computed(() => [{ value: false, label: t('pelican.latest') }, { value: true, label: t('pelican.history') }])
const modelOptions = computed(() => [...new Set(props.admin ? (props.groups ?? []).filter(g => !groupID.value || String(g.id) === groupID.value).flatMap(g => g.models) : facets.value.filter(f => !groupID.value || String(f.group_id) === groupID.value).map(f => f.model))])
const chosen = computed(() => items.value.filter(v => selected.value.includes(v.id)))
const canPublish = computed(() => chosen.value.every(v => v.status === 'completed' && v.has_document))
const canDelete = computed(() => chosen.value.every(deletable))
const retryAccountOptions = computed(() => (props.accounts ?? []).map(a => ({ value: a.id, label: `${a.name} · ${a.platform} · ${pelicanHost(a.base_url)}` })))
function date(value: string) { return fmtDateTime(value) }
function fullDate(value: string) { return new Date(value).toLocaleString(locale.value, { timeZoneName: 'short' }) }
function protocolLabel(protocol: PelicanProtocol) { return PROTOCOL_LABELS[protocol] ?? protocol }
function deletable(item?: PelicanResult | null) { return !!item && !item.published_at && !pelicanPending(item.status) }
function deleteHint(item: PelicanResult) {
  if (item.published_at) return t('pelican.deleteBlockedPublished')
  if (pelicanPending(item.status)) return t('pelican.deleteBlockedRunning')
  return t('pelican.delete')
}
function closeDetail() { detailVersion++; detail.value = null; retryKey.value = '' }
async function openDetail(id: number) {
  const own = ++detailVersion
  try {
    const value = await api.detail(id)
    if (own !== detailVersion || disposed) return
    detail.value = value; view.value = 'preview'; retryKey.value = ''
    retryAccountID.value = value.account_id && (props.accounts ?? []).some(a => a.id === value.account_id) ? value.account_id : ((props.accounts ?? []).length === 1 ? props.accounts![0].id : 0)
  } catch (e) { if (own === detailVersion) error.value = t(errorMessage(e)) }
}
async function refresh() {
  const own = ++version
  clearTimeout(timer); loading.value = true
  try {
    const query = new URLSearchParams({ page: String(page.value), page_size: '12', history: String(history.value), ...pelicanDateRange(from.value, to.value) })
    if (groupID.value) query.set('group_id', groupID.value)
    if (model.value) query.set('model', model.value)
    if (batchID.value) query.set('batch_id', batchID.value)
    const [data, filters] = await Promise.all([api.list(query.toString()), props.admin ? Promise.resolve(null) : embedPelicanAPI.filters()])
    if (own !== version || disposed) return
    items.value = data.items; pages.value = data.pages; total.value = data.total; error.value = ''
    if (filters) facets.value = filters.items
    selected.value = selected.value.filter(id => items.value.some(v => v.id === id))
    if (detail.value) { const id = detail.value.id; try { const updated = await api.detail(id); if (detail.value?.id === id) detail.value = updated } catch { closeDetail() } }
  } catch (e) { if (own === version) { error.value = t(errorMessage(e)); items.value = []; closeDetail() } }
  finally { if (own === version && !disposed) { loading.value = false; timer = setTimeout(refresh, props.admin ? 5000 : 30000) } }
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
    const batch = await pelicanAPI.retry(record.id, { request_id: retryRequest.request, account_id: account || undefined, api_key: key || undefined })
    retryRequest = undefined; closeDetail(); emit('submitted', batch.id); await refresh()
  } catch (e) { error.value = t(errorMessage(e)) } finally { busy.value = false }
}
onMounted(refresh)
onBeforeUnmount(() => { disposed = true; version++; detailVersion++; clearTimeout(timer) })
defineExpose({ refresh })
</script>
