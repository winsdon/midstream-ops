<template>
  <div class="space-y-4 pb-56 lg:pb-0">
    <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950 dark:text-red-300">{{ error }}</p>
    <p v-if="notice" role="status" class="text-sm text-emerald-600">{{ notice }}</p>
    <PelicanComposer
      v-model:prompt="prompt"
      v-model:concurrency="concurrency"
      v-model:max-tokens="maxTokens"
      v-model:timeout="timeout"
      v-model:targets="targets"
      :groups="groups"
      :accounts="accounts"
      :submitting="submitting"
      :pending="pending !== null"
      :batch-id="activeBatch"
      :batch-items="batchItems"
      @submit="submit"
      @discard="clearPending"
      @cancel="cancelBatch"
    />
    <PelicanGallery ref="gallery" admin :groups="groups" :accounts="accounts" :focus-batch="activeBatch" @submitted="trackBatch" />
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { pelicanAPI } from '@/api/pelican'
import { errorMessage } from '@/api/client'
import PelicanComposer from '@/components/pelican/PelicanComposer.vue'
import PelicanGallery from '@/components/pelican/PelicanGallery.vue'
import type { PelicanAccount, PelicanGroup, PelicanProtocol, PelicanRequest, PelicanResult, PelicanTarget } from '@/types/pelican'
import { DEFAULT_PELICAN_PROMPT, pelicanAccountPlatform, pelicanBlankRow, pelicanNormalizeRow, pelicanPending, pelicanRequestID, pelicanTargetReady, type PelicanTargetRow } from '@/utils/pelican'

const { t } = useI18n()
const groups = ref<PelicanGroup[]>([])
const accounts = ref<PelicanAccount[]>([])
const error = ref('')
const notice = ref('')
const prompt = ref(DEFAULT_PELICAN_PROMPT)
const concurrency = ref(3)
const maxTokens = ref(32000)
const timeout = ref(900)
const submitting = ref(false)
const pending = ref<PelicanRequest | null>(null)
const targets = ref<PelicanTargetRow[]>([])
const gallery = ref<InstanceType<typeof PelicanGallery>>()
const activeBatch = ref(storedBatch())
const batchItems = ref<PelicanResult[]>([])
let nextUID = 1
let disposed = false
let batchTimer: ReturnType<typeof setTimeout> | undefined

function storedBatch() {
  try { return Number(sessionStorage.getItem('pelican.batch')) || 0 } catch { return 0 }
}
function toTarget(row: PelicanTargetRow): PelicanTarget {
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
function persistPending() {
  if (pending.value) sessionStorage.setItem('pelican.pending', JSON.stringify(sanitize(pending.value)))
  else sessionStorage.removeItem('pelican.pending')
}
function clearPending() { pending.value = null; persistPending() }
/** 参数没变时沿用同一个 request_id：重试不会重复执行，改了参数则算新测试。 */
function samePending(next: PelicanRequest) {
  if (!pending.value) return false
  const strip = (request: PelicanRequest) => JSON.stringify({ ...request, request_id: '' })
  return strip(pending.value) === strip(next)
}
function ready() { return targets.value.length > 0 && targets.value.every(pelicanTargetReady) }
async function submit() {
  if (!ready()) return
  submitting.value = true
  error.value = ''
  notice.value = ''
  const draft = buildRequest('')
  const payload = { ...draft, request_id: samePending(draft) ? pending.value!.request_id : pelicanRequestID() }
  pending.value = payload
  persistPending()
  try {
    const batch = await pelicanAPI.start(payload)
    clearPending()
    trackBatch(batch.id)
    notice.value = t('pelican.submitted')
  } catch (e) { error.value = t(errorMessage(e)) } finally { submitting.value = false }
}
function trackBatch(id: number) {
  activeBatch.value = id
  sessionStorage.setItem('pelican.batch', String(id))
  void pollBatch()
}
async function pollBatch() {
  clearTimeout(batchTimer)
  const id = activeBatch.value
  try {
    const data = await pelicanAPI.batch(id)
    if (disposed || id !== activeBatch.value) return
    batchItems.value = data.items
    if (data.items.some(item => pelicanPending(item.status))) batchTimer = setTimeout(pollBatch, 2500)
  } catch (e) { if (!disposed) error.value = t(errorMessage(e)) }
}
async function cancelBatch() {
  try { await pelicanAPI.cancel(activeBatch.value); await pollBatch(); await gallery.value?.refresh() }
  catch (e) { error.value = t(errorMessage(e)) }
}
/** 账号加载前创建的空行改成本站账号；已填写或从待确认提交恢复的行保持原样。 */
function hydrate(row: PelicanTargetRow): PelicanTargetRow {
  const blank = !row.group_id && !row.model.trim() && !row.base_url.trim() && !row.api_key && !row.account_id
  let next = row
  if (blank && next.source !== 'account' && accounts.value.length) {
    next = { ...next, source: 'account', account_id: accounts.value.length === 1 ? accounts.value[0].id : 0 }
  }
  if (!next.source) next = { ...next, source: accounts.value.length ? 'account' : 'manual' }
  if (next.source === 'account' && !next.account_id && accounts.value.length === 1) {
    next = { ...next, account_id: accounts.value[0].id }
  }
  return pelicanNormalizeRow(next, pelicanAccountPlatform(accounts.value, next))
}
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
        uid: nextUID++,
        group_id: target.group_id ?? 0,
        source: target.source ?? '',
        account_id: target.account_id ?? 0,
        base_url: target.base_url ?? '',
        api_key: '',
        model: target.model ?? '',
        protocol: target.protocol ?? ''
      }))
    }
  } catch { clearPending() }
}
onMounted(async () => {
  restorePending()
  if (!targets.value.length) targets.value = [pelicanBlankRow(nextUID++, [])]
  try {
    const [groupData, accountData] = await Promise.all([pelicanAPI.groups(), pelicanAPI.accounts()])
    groups.value = groupData.items
    accounts.value = accountData.items
    targets.value = targets.value.map(hydrate)
    if (activeBatch.value > 0) trackBatch(activeBatch.value)
  } catch (e) { error.value = t(errorMessage(e)) }
})
onBeforeUnmount(() => { disposed = true; clearTimeout(batchTimer) })
</script>
