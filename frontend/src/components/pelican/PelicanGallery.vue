<template>
  <section class="space-y-4">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <h2 class="text-lg font-semibold">{{ t('pelican.results') }}</h2>
      <button class="btn btn-secondary" :disabled="loading" @click="refresh">{{ t('pelican.reload') }}</button>
    </div>
    <form class="card flex flex-wrap items-end gap-3 p-4" @submit.prevent="page = 1; refresh()">
      <label class="min-w-40 flex-1 text-sm">{{ t('pelican.group') }}
        <Select v-model="groupID" class="mt-1 w-full" :options="groupSelectOptions" />
      </label>
      <label class="min-w-40 flex-1 text-sm">{{ t('pelican.model') }}<input v-model="model" list="pelican-filter-models" class="input mt-1 w-full" :placeholder="t('pelican.allModels')" /><datalist id="pelican-filter-models"><option v-for="m in modelOptions" :key="m" :value="m" /></datalist></label>
      <label class="text-sm">{{ t('pelican.from') }}<input v-model="from" type="date" class="input mt-1 block" /></label>
      <label class="text-sm">{{ t('pelican.to') }}<input v-model="to" type="date" :min="from" class="input mt-1 block" /></label>
      <label v-if="admin" class="text-sm">{{ t('pelican.batchFilter') }}<input v-model="batchID" type="number" min="1" class="input mt-1 block w-28" /></label>
      <Select v-else v-model="history" class="min-w-40" :options="historyOptions" :searchable="false" :aria-label="t('pelican.history')" />
      <button class="btn btn-primary" :disabled="loading">{{ t('pelican.apply') }}</button>
    </form>
    <div v-if="admin && selected.length" class="flex flex-wrap items-center gap-2">
      <span class="mr-2 text-sm">{{ t('pelican.selected', { count: selected.length }) }}</span>
      <button class="btn btn-primary" :disabled="busy || !canPublish" @click="mutate('publish')">{{ t('pelican.publish') }}</button>
      <button class="btn btn-secondary" :disabled="busy" @click="mutate('unpublish')">{{ t('pelican.unpublish') }}</button>
      <button class="btn btn-secondary text-red-600" :disabled="busy || !canDelete" @click="mutate('delete')">{{ t('pelican.delete') }}</button>
    </div>
    <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950 dark:text-red-300">{{ error }}</p>
    <div v-if="!error && !items.length" class="card p-12 text-center text-gray-500">
      <p>{{ loading ? t('common.loading') : t('pelican.empty') }}</p><p v-if="!admin" class="mt-2 text-sm">{{ t('pelican.emptyHint') }}</p>
    </div>
    <div v-if="!error" class="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
      <article v-for="item in items" :key="item.id" class="card overflow-hidden">
        <div class="flex items-start justify-between gap-2 p-4">
          <div class="min-w-0"><h3 class="break-words font-semibold">{{ item.group_name }}</h3><p class="mt-1 break-all text-sm text-gray-500">{{ item.model }}</p></div>
          <input v-if="admin" v-model="selected" type="checkbox" :value="item.id" :aria-label="`${t('pelican.select')} ${item.group_name} ${item.model}`" />
        </div>
        <div class="relative h-64 border-y border-gray-100 dark:border-dark-700">
          <PelicanPreview v-if="item.has_document" :id="item.id" :title="item.group_name" :fetch-result="api.detail" />
          <div v-else class="flex h-full items-center justify-center bg-gray-50 p-4 text-sm text-gray-500 dark:bg-dark-900">{{ t(`pelican.statuses.${item.status}`) }}</div>
        </div>
        <div class="space-y-2 p-4 text-sm">
          <p>{{ t('pelican.testedAt') }}: <time :datetime="item.tested_at">{{ date(item.tested_at) }}</time></p>
          <p class="text-gray-500">{{ t('pelican.duration') }}: {{ (item.duration_ms / 1000).toFixed(1) }} s</p>
          <div v-if="admin" class="flex flex-wrap gap-2"><span class="badge">{{ t(`pelican.statuses.${item.status}`) }}</span><span class="badge" :class="item.published_at ? 'text-emerald-600' : 'text-gray-500'">{{ t(item.published_at ? 'pelican.published' : 'pelican.draft') }}</span></div>
          <p v-if="admin && item.error" class="break-words text-red-600">{{ t(item.error) }}</p>
          <div class="flex flex-wrap gap-2 pt-1">
            <button class="btn btn-secondary" @click="openDetail(item.id)">{{ t('pelican.detail') }}</button>
            <button v-if="admin && pelicanPending(item.status) && item.batch_id" class="btn btn-secondary" :disabled="busy" @click="cancel(item.batch_id)">{{ t('pelican.cancel') }}</button>
          </div>
        </div>
      </article>
    </div>
    <div class="flex flex-wrap items-center justify-between gap-3 text-sm">
      <span>{{ t('pelican.page', { page, pages, total }) }}</span>
      <div class="flex gap-2"><button class="btn btn-secondary" :disabled="page <= 1 || loading" @click="page--; refresh()">{{ t('pelican.previous') }}</button><button class="btn btn-secondary" :disabled="page >= pages || loading" @click="page++; refresh()">{{ t('pelican.next') }}</button></div>
    </div>
    <BaseDialog :show="detail !== null" :title="detail ? `${detail.group_name} · ${detail.model}` : ''" width="full" @close="closeDetail">
      <template v-if="detail">
        <div v-if="detail.has_document" class="h-[65vh] min-h-80 overflow-hidden rounded-lg border"><PelicanPreview :key="detail.id" :id="detail.id" :title="detail.group_name" :fetch-result="api.detail" interactive /></div>
        <div class="mt-4 space-y-3 text-sm">
          <p>{{ t('pelican.testedAt') }}: {{ date(detail.tested_at) }} · {{ t('pelican.duration') }}: {{ (detail.duration_ms / 1000).toFixed(1) }} s</p>
          <p v-if="detail.published_at">{{ t('pelican.publishedAt') }}: {{ date(detail.published_at) }}</p>
          <p>{{ t('pelican.parameters') }}: {{ detail.protocol }} · max_tokens={{ detail.max_tokens }}</p>
          <h4 class="font-semibold">{{ t('pelican.prompt') }}</h4><pre class="whitespace-pre-wrap break-words rounded-lg bg-gray-50 p-3 dark:bg-dark-900">{{ detail.prompt }}</pre>
          <p v-if="admin && detail.error" class="text-red-600">{{ t(detail.error) }}</p>
          <template v-if="admin">
            <details><summary class="cursor-pointer">{{ t('pelican.parameters') }}</summary><pre class="mt-2 max-h-80 overflow-auto whitespace-pre-wrap break-words">{{ JSON.stringify(detail.output?.request, null, 2) }}</pre></details>
            <details><summary class="cursor-pointer">{{ t('pelican.reply') }}</summary><pre class="mt-2 max-h-80 overflow-auto whitespace-pre-wrap break-words">{{ detail.output?.text }}</pre></details>
            <details><summary class="cursor-pointer">{{ t('pelican.usage') }}</summary><pre class="mt-2 overflow-auto">{{ JSON.stringify(detail.output?.usage, null, 2) }}</pre></details>
            <div v-if="!pelicanPending(detail.status)" class="flex flex-wrap gap-2">
              <Select v-model="retryKey" class="min-w-0 w-full sm:w-80" :options="retryKeyOptions" :placeholder="t('pelican.selectKey')" :aria-label="t('pelican.key')" />
              <button class="btn btn-primary" :disabled="busy || !retryKey" @click="retry">{{ t('pelican.retry') }}</button>
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
import { pelicanDateRange, pelicanPending, pelicanRequestID } from '@/utils/pelican'
import type { PelicanFacet, PelicanGroup, PelicanResult } from '@/types/pelican'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import PelicanPreview from './PelicanPreview.vue'
const props = defineProps<{ admin?: boolean; groups?: PelicanGroup[] }>()
const emit = defineEmits<{ submitted: [id: number] }>()
const { t, locale } = useI18n()
const api = props.admin ? pelicanAPI : embedPelicanAPI
const items = ref<PelicanResult[]>([]), facets = ref<PelicanFacet[]>([])
const selected = ref<number[]>([]), detail = ref<PelicanResult | null>(null)
const groupID = ref(''), model = ref(''), from = ref(''), to = ref(''), batchID = ref('')
const history = ref(false), page = ref(1), pages = ref(1), total = ref(0)
const loading = ref(false), busy = ref(false), error = ref(''), retryKey = ref(0)
let version = 0, detailVersion = 0, timer: ReturnType<typeof setTimeout> | undefined, disposed = false
let retryRequest: { id: number; key: number; request: string } | undefined
const groupOptions = computed(() => props.admin ? (props.groups ?? []) : Array.from(new Map(facets.value.map(f => [f.group_id, { id: f.group_id, name: f.group_name }])).values()))
const groupSelectOptions = computed(() => [{ value: '', label: t('pelican.allGroups') }, ...groupOptions.value.map(g => ({ value: String(g.id), label: g.name }))])
const historyOptions = computed(() => [{ value: false, label: t('pelican.latest') }, { value: true, label: t('pelican.history') }])
const modelOptions = computed(() => [...new Set(props.admin ? (props.groups ?? []).filter(g => !groupID.value || String(g.id) === groupID.value).flatMap(g => g.models) : facets.value.filter(f => !groupID.value || String(f.group_id) === groupID.value).map(f => f.model))])
const chosen = computed(() => items.value.filter(v => selected.value.includes(v.id)))
const canPublish = computed(() => chosen.value.every(v => v.status === 'completed' && v.has_document))
const canDelete = computed(() => chosen.value.every(v => !v.published_at && !pelicanPending(v.status)))
const retryKeys = computed(() => props.groups?.find(g => g.id === detail.value?.group_id)?.keys ?? [])
const retryKeyOptions = computed(() => [{ value: 0, label: t('pelican.selectKey') }, ...retryKeys.value.map(key => ({ value: key.id, label: `${key.name} ${key.masked}` }))])
function date(value: string) { return new Date(value).toLocaleString(locale.value, { timeZoneName: 'short' }) }
function closeDetail() { detailVersion++; detail.value = null }
async function openDetail(id: number) {
  const own = ++detailVersion
  try { const value = await api.detail(id); if (own !== detailVersion || disposed) return; detail.value = value; retryKey.value = retryKeys.value.some(k => k.id === value.key_id) ? value.key_id! : (retryKeys.value.length === 1 ? retryKeys.value[0].id : 0) }
  catch (e) { if (own === detailVersion) error.value = t(errorMessage(e)) }
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
async function cancel(id: number) {
  busy.value = true
  try { await pelicanAPI.cancel(id); await refresh() } catch (e) { error.value = t(errorMessage(e)) } finally { busy.value = false }
}
async function retry() {
  if (!detail.value) return
  const id = detail.value.id, key = retryKey.value
  if (!retryRequest || retryRequest.id !== id || retryRequest.key !== key) retryRequest = { id, key, request: pelicanRequestID() }
  busy.value = true
  try { const batch = await pelicanAPI.retry(id, retryRequest.request, key); retryRequest = undefined; closeDetail(); emit('submitted', batch.id); await refresh() }
  catch (e) { error.value = t(errorMessage(e)) } finally { busy.value = false }
}
onMounted(refresh)
onBeforeUnmount(() => { disposed = true; version++; detailVersion++; clearTimeout(timer) })
defineExpose({ refresh })
</script>
