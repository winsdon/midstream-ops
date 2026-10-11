<template>
  <div class="space-y-4">
    <p class="text-sm text-gray-500">{{ t('pelican.adminIntro') }}</p>
    <form class="grid items-start gap-4 xl:grid-cols-[minmax(0,1fr)_minmax(20rem,23rem)]" @submit.prevent="submit">
      <!-- 主区：批次进度 + 作品列表；侧边栏在窄屏排到前面，打开页面就能直接发起测试 -->
      <div class="order-2 space-y-3 xl:order-1">
        <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950 dark:text-red-300">{{ error }}</p>
        <p v-if="notice" role="status" class="text-sm text-emerald-600">{{ notice }}</p>
        <div v-if="activeBatch" class="card flex flex-wrap items-center justify-between gap-3 p-3">
          <div><span class="font-medium">{{ t('pelican.batch', { id: activeBatch }) }}</span><p class="mt-1 text-sm text-gray-500">{{ t('pelican.progress', { done: batchItems.filter(v => !pelicanPending(v.status)).length, total: batchItems.length }) }}</p></div>
          <button v-if="batchItems.some(v => pelicanPending(v.status))" type="button" class="btn btn-secondary" @click="cancelBatch">{{ t('pelican.cancel') }}</button>
        </div>
        <PelicanGallery ref="gallery" admin :groups="groups" :accounts="accounts" @submitted="trackBatch" />
      </div>

      <aside class="order-1 space-y-3 xl:order-2 xl:sticky xl:top-4">
        <details class="card p-4">
          <summary class="cursor-pointer font-semibold">{{ t('pelican.config') }}</summary>
          <div class="mt-3 space-y-3">
            <div class="flex flex-wrap items-center justify-between gap-2">
              <label for="pelican-prompt" class="text-sm font-medium">{{ t('pelican.prompt') }}</label>
              <button type="button" class="text-sm text-primary-600" @click="prompt = DEFAULT_PELICAN_PROMPT">{{ t('pelican.resetPrompt') }}</button>
            </div>
            <textarea id="pelican-prompt" v-model="prompt" rows="4" class="input w-full" required />
            <p class="text-xs text-gray-500">{{ t('pelican.deliveryHint') }}</p>
            <details class="text-sm">
              <summary class="cursor-pointer">{{ t('pelican.advanced') }}</summary>
              <div class="mt-3 grid gap-3">
                <label class="text-sm">{{ t('pelican.concurrency') }}<input v-model.number="concurrency" class="input mt-1 w-full" type="number" min="1" max="10" required /></label>
                <label class="text-sm">{{ t('pelican.maxTokens') }}<input v-model.number="maxTokens" class="input mt-1 w-full" type="number" min="1" max="262144" required /></label>
                <label class="text-sm">{{ t('pelican.timeout') }}<input v-model.number="timeout" class="input mt-1 w-full" type="number" min="1" max="3600" required /></label>
              </div>
            </details>
          </div>
        </details>

        <details class="card p-4">
          <summary class="cursor-pointer font-semibold">{{ t('pelican.targets') }}</summary>
          <fieldset class="mt-3 space-y-3" :disabled="submitting">
            <div v-for="(row, index) in targets" :key="row.uid" class="space-y-2 rounded-lg border border-gray-200 p-3 dark:border-dark-700">
              <label class="block text-sm">{{ t('pelican.group') }}
                <Select v-model="row.group_id" class="mt-1 w-full" :options="groupOptions" :placeholder="t('pelican.selectGroup')" :disabled="submitting" @change="changeGroup(row)" />
              </label>
              <label class="block text-sm">{{ t('pelican.source') }}
                <Select v-model="row.source" class="mt-1 w-full" :options="sourceOptions" :placeholder="t('pelican.selectSource')" :searchable="false" :disabled="submitting" @change="changeSource(row)" />
              </label>
              <template v-if="row.source === 'account'">
                <label class="block text-sm">{{ t('pelican.account') }}
                  <Select v-model="row.account_id" class="mt-1 w-full" :options="accountOptions" :placeholder="t('pelican.selectAccount')" searchable :disabled="submitting" @change="syncProtocol(row)" />
                </label>
                <p v-if="!accounts.length" class="text-xs text-amber-600 dark:text-amber-400">{{ t('pelican.accountEmpty') }}</p>
              </template>
              <template v-else-if="row.source === 'manual'">
                <label class="block text-sm">{{ t('pelican.address') }}<input v-model.trim="row.base_url" class="input mt-1 w-full" type="url" placeholder="https://api.example.com" :disabled="submitting" /></label>
                <label class="block text-sm">{{ t('pelican.apiKey') }}<input v-model.trim="row.api_key" class="input mt-1 w-full" type="password" autocomplete="off" :disabled="submitting" /></label>
                <p class="text-xs text-gray-500">{{ t('pelican.apiKeyHint') }}</p>
              </template>
              <label class="block text-sm">{{ t('pelican.model') }}
                <input v-model="row.model" class="input mt-1 w-full" :list="`pelican-models-${row.uid}`" :placeholder="t('pelican.modelHint')" required />
                <datalist :id="`pelican-models-${row.uid}`"><option v-for="model in groupFor(row)?.models ?? []" :key="model" :value="model" /></datalist>
              </label>
              <label class="block text-sm">{{ t('pelican.protocol') }}
                <Select v-model="row.protocol" class="mt-1 w-full" :options="protocolOptions(row)" :placeholder="t('pelican.selectProtocol')" :searchable="false" :disabled="submitting" />
              </label>
              <button class="btn btn-secondary" type="button" :disabled="targets.length === 1" @click="targets.splice(index, 1)">{{ t('pelican.remove') }}</button>
            </div>
            <button class="btn btn-secondary w-full" type="button" :disabled="targets.length >= 100" @click="addTarget">{{ t('pelican.add') }}</button>
          </fieldset>
        </details>

        <div class="card space-y-2 p-4">
          <p class="text-sm text-gray-500">{{ t('pelican.targetSummary', { ready: readyCount, total: targets.length }) }}</p>
          <p v-if="pending && !submitting" class="text-sm text-amber-700 dark:text-amber-300">{{ t('pelican.submissionHint') }}</p>
          <div class="flex flex-wrap gap-2">
            <button class="btn btn-primary" :disabled="submitting || !ready">{{ t(submitting ? 'pelican.starting' : pending ? 'pelican.retrySubmission' : 'pelican.start') }}</button>
            <button v-if="pending && !submitting" type="button" class="btn btn-secondary" @click="clearPending">{{ t('pelican.discardSubmission') }}</button>
          </div>
        </div>
      </aside>
    </form>
  </div>
</template>
<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { pelicanAPI } from '@/api/pelican'
import { errorMessage } from '@/api/client'
import type { PelicanAccount, PelicanGroup, PelicanProtocol, PelicanRequest, PelicanResult, PelicanSource, PelicanTarget } from '@/types/pelican'
import { DEFAULT_PELICAN_PROMPT, pelicanHost, pelicanPending, pelicanProtocolOptions, pelicanRequestID, pelicanTargetReady } from '@/utils/pelican'
import PelicanGallery from '@/components/pelican/PelicanGallery.vue'
import Select from '@/components/common/Select.vue'
const { t } = useI18n()
const groups = ref<PelicanGroup[]>([]), accounts = ref<PelicanAccount[]>([])
const error = ref(''), notice = ref(''), prompt = ref(DEFAULT_PELICAN_PROMPT)
const concurrency = ref(3), maxTokens = ref(32000), timeout = ref(900)
const submitting = ref(false)
const pending = ref<PelicanRequest | null>(null)
const gallery = ref<InstanceType<typeof PelicanGallery>>()
type TargetRow = { uid: number; group_id: number; source: PelicanSource | ''; account_id: number; base_url: string; api_key: string; model: string; protocol: PelicanProtocol | '' }
let nextUID = 0, disposed = false, batchTimer: ReturnType<typeof setTimeout> | undefined
const targets = ref<TargetRow[]>([])
const activeBatch = ref(0), batchItems = ref<PelicanResult[]>([])
const groupOptions = computed(() => groups.value.map(group => ({ value: group.id, label: group.name, description: group.platform || undefined })))
const sourceOptions = computed(() => [{ value: 'account', label: t('pelican.sourceAccount') }, { value: 'manual', label: t('pelican.sourceManual') }])
const accountOptions = computed(() => accounts.value.map(a => ({ value: a.id, label: `${a.name} · ${a.platform} · ${pelicanHost(a.base_url)}` })))
const readyCount = computed(() => targets.value.filter(pelicanTargetReady).length)
const ready = computed(() => targets.value.length > 0 && targets.value.every(pelicanTargetReady))
function addTarget() {
  targets.value.push({ uid: nextUID++, group_id: 0, source: accounts.value.length ? 'account' : 'manual', account_id: accounts.value.length === 1 ? accounts.value[0].id : 0, base_url: '', api_key: '', model: '', protocol: '' })
  syncProtocol(targets.value[targets.value.length - 1])
}
function groupFor(row: TargetRow) { return groups.value.find(g => g.id === row.group_id) }
function accountFor(row: TargetRow) { return accounts.value.find(a => a.id === row.account_id) }
function protocolOptions(row: TargetRow) {
  const platform = row.source === 'account' ? accountFor(row)?.platform : undefined
  const labels: Record<PelicanProtocol, string> = { messages: 'Messages', responses: 'Responses', chat: 'Chat Completions' }
  return pelicanProtocolOptions(platform).map(value => ({ value, label: labels[value] }))
}
/** 来源/账号/分组变化后，协议必须落在当前可选范围内。 */
function syncProtocol(row: TargetRow) {
  const allowed = protocolOptions(row).map(option => option.value)
  if (!row.protocol || !allowed.includes(row.protocol)) row.protocol = allowed[0] ?? ''
}
function changeGroup(row: TargetRow) { row.model = ''; syncProtocol(row) }
function changeSource(row: TargetRow) { row.account_id = accounts.value.length === 1 ? accounts.value[0].id : 0; syncProtocol(row) }
function toTarget(row: TargetRow): PelicanTarget {
  const base = { group_id: row.group_id, model: row.model.trim(), protocol: row.protocol as PelicanProtocol }
  return row.source === 'manual'
    ? { ...base, source: 'manual', base_url: row.base_url.trim(), api_key: row.api_key }
    : { ...base, source: 'account', account_id: row.account_id }
}
function buildRequest(requestID: string): PelicanRequest {
  return { request_id: requestID, prompt: prompt.value, concurrency: concurrency.value, max_tokens: maxTokens.value, timeout_seconds: timeout.value, targets: targets.value.map(toTarget) }
}
/** 待确认提交里不带 Key，写 sessionStorage 前必须再剥一次。 */
function sanitize(request: PelicanRequest): PelicanRequest {
  return { ...request, targets: request.targets.map(({ api_key: _apiKey, ...target }) => target) }
}
function persistPending() { if (pending.value) sessionStorage.setItem('pelican.pending', JSON.stringify(sanitize(pending.value))); else sessionStorage.removeItem('pelican.pending') }
function clearPending() { pending.value = null; persistPending() }
/** 参数没变时沿用同一个 request_id：重试不会重复执行，改了参数则算新测试。 */
function samePending(next: PelicanRequest) {
  if (!pending.value) return false
  const strip = (request: PelicanRequest) => JSON.stringify({ ...request, request_id: '' })
  return strip(pending.value) === strip(next)
}
async function submit() {
  if (!ready.value) return
  submitting.value = true; error.value = ''; notice.value = ''
  const draft = buildRequest('')
  const payload = { ...draft, request_id: samePending(draft) ? pending.value!.request_id : pelicanRequestID() }
  pending.value = payload
  persistPending()
  try { const batch = await pelicanAPI.start(payload); clearPending(); trackBatch(batch.id); notice.value = t('pelican.submitted'); await gallery.value?.refresh() }
  catch (e) { error.value = t(errorMessage(e)) } finally { submitting.value = false }
}
function trackBatch(id: number) { activeBatch.value = id; sessionStorage.setItem('pelican.batch', String(id)); void pollBatch() }
async function pollBatch() {
  clearTimeout(batchTimer)
  const id = activeBatch.value
  try {
    const data = await pelicanAPI.batch(id)
    if (disposed || id !== activeBatch.value) return
    batchItems.value = data.items
    if (data.items.some(v => pelicanPending(v.status))) batchTimer = setTimeout(pollBatch, 2500)
  } catch (e) { if (!disposed) error.value = t(errorMessage(e)) }
}
async function cancelBatch() { try { await pelicanAPI.cancel(activeBatch.value); await pollBatch(); await gallery.value?.refresh() } catch (e) { error.value = t(errorMessage(e)) } }
/** 表单按待确认提交回填（手填 Key 除外），重新提交时 hash 才能与上次一致。 */
function restorePending() {
  let raw = ''
  try { raw = sessionStorage.getItem('pelican.pending') ?? '' } catch { raw = '' }
  if (!raw) return
  try {
    const stored: PelicanRequest = JSON.parse(raw)
    pending.value = stored
    if (typeof stored.prompt === 'string' && stored.prompt.trim()) prompt.value = stored.prompt
    if (stored.concurrency) concurrency.value = stored.concurrency
    if (stored.max_tokens) maxTokens.value = stored.max_tokens
    if (stored.timeout_seconds) timeout.value = stored.timeout_seconds
    if (Array.isArray(stored.targets) && stored.targets.length) {
      targets.value = stored.targets.map(target => ({
        uid: nextUID++, group_id: target.group_id ?? 0, source: target.source ?? '',
        account_id: target.account_id ?? 0, base_url: target.base_url ?? '', api_key: '', model: target.model ?? '', protocol: target.protocol ?? ''
      }))
      for (const row of targets.value) syncProtocol(row)
    }
  } catch { clearPending() }
}
onMounted(async () => {
  restorePending()
  if (!targets.value.length) addTarget()
  try {
    const [groupData, accountData] = await Promise.all([pelicanAPI.groups(), pelicanAPI.accounts()])
    groups.value = groupData.items; accounts.value = accountData.items
    for (const row of targets.value) {
      if (!row.source) row.source = accounts.value.length ? 'account' : 'manual'
      if (row.source === 'account' && !row.account_id && accounts.value.length === 1) row.account_id = accounts.value[0].id
      syncProtocol(row)
    }
    const batch = Number(sessionStorage.getItem('pelican.batch')); if (batch > 0) trackBatch(batch)
  } catch (e) { error.value = t(errorMessage(e)) }
})
onBeforeUnmount(() => { disposed = true; clearTimeout(batchTimer) })
</script>
