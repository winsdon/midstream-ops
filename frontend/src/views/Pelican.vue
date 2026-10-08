<template>
  <div class="space-y-6">
    <p class="text-sm text-gray-500">{{ t('pelican.adminIntro') }}</p>
    <details class="card p-4" :open="!config.gateway_url || !config.user_id">
      <summary class="cursor-pointer font-semibold">{{ t('pelican.config') }}</summary>
      <form class="mt-4 grid gap-4 md:grid-cols-2" @submit.prevent="saveConfig">
        <label class="text-sm">{{ t('pelican.gateway') }}<input v-model="config.gateway_url" class="input mt-1 w-full" type="url" required placeholder="https://api.example.com" /></label>
        <div class="space-y-2"><label class="block text-sm">{{ t('pelican.userSearch') }}<input v-model="userSearch" class="input mt-1 w-full" /></label>
          <label class="block text-sm">{{ t('pelican.user') }}
            <Select v-model="config.user_id" class="mt-1 w-full" :options="userOptions" :placeholder="t('pelican.selectUser')" searchable />
          </label>
        </div>
        <p class="text-sm text-gray-500 md:col-span-2">{{ t('pelican.userHint') }}</p>
        <button class="btn btn-secondary justify-self-start" :disabled="saving || !config.user_id">{{ t('pelican.save') }}</button>
      </form>
    </details>
    <form class="card space-y-4 p-4 sm:p-5" @submit.prevent="submit">
      <h2 class="font-semibold">{{ t('pelican.targets') }}</h2>
      <fieldset class="space-y-3" :disabled="submitting || !!pending">
        <div v-for="(row, index) in targets" :key="row.uid" class="grid items-end gap-3 rounded-lg border border-gray-200 p-3 dark:border-dark-700 sm:grid-cols-2 xl:grid-cols-[1fr_1fr_1fr_10rem_auto]">
          <label class="min-w-0 text-sm">{{ t('pelican.group') }}
            <Select v-model="row.group_id" class="mt-1 w-full" :options="groupOptions" :placeholder="t('pelican.selectGroup')" :disabled="submitting || !!pending" @change="changeGroup(row)" />
          </label>
          <label class="min-w-0 text-sm">{{ t('pelican.key') }}
            <Select v-model="row.key_id" class="mt-1 w-full" :options="keyOptions(row)" :placeholder="t('pelican.selectKey')" :disabled="submitting || !!pending" />
          </label>
          <label class="min-w-0 text-sm">{{ t('pelican.model') }}<input v-model="row.model" class="input mt-1 w-full" :list="`pelican-models-${row.uid}`" :placeholder="t('pelican.modelHint')" required /><datalist :id="`pelican-models-${row.uid}`"><option v-for="model in groupFor(row)?.models ?? []" :key="model" :value="model" /></datalist></label>
          <label class="min-w-0 text-sm">{{ t('pelican.protocol') }}
            <Select v-model="row.protocol" class="mt-1 w-full" :options="protocolOptions(row)" :placeholder="t('pelican.selectProtocol')" :searchable="false" :disabled="submitting || !!pending" />
          </label>
          <button class="btn btn-secondary" type="button" :disabled="targets.length === 1" @click="targets.splice(index, 1)">{{ t('pelican.remove') }}</button>
        </div>
        <button class="btn btn-secondary" type="button" :disabled="targets.length >= 100" @click="addTarget">{{ t('pelican.add') }}</button>
        <div class="flex flex-wrap justify-between gap-2"><label for="pelican-prompt" class="text-sm font-medium">{{ t('pelican.prompt') }}</label><button type="button" class="text-sm text-primary-600" @click="prompt = DEFAULT_PELICAN_PROMPT">{{ t('pelican.resetPrompt') }}</button></div>
        <textarea id="pelican-prompt" v-model="prompt" rows="3" class="input w-full" required />
        <p class="text-xs text-gray-500">{{ t('pelican.deliveryHint') }}</p>
        <details><summary class="cursor-pointer text-sm">{{ t('pelican.advanced') }}</summary><div class="mt-3 grid gap-3 sm:grid-cols-3">
          <label class="text-sm">{{ t('pelican.concurrency') }}<input v-model.number="concurrency" class="input mt-1 w-full" type="number" min="1" max="10" required /></label>
          <label class="text-sm">{{ t('pelican.maxTokens') }}<input v-model.number="maxTokens" class="input mt-1 w-full" type="number" min="1" max="262144" required /></label>
          <label class="text-sm">{{ t('pelican.timeout') }}<input v-model.number="timeout" class="input mt-1 w-full" type="number" min="1" max="3600" required /></label>
        </div></details>
      </fieldset>
      <p v-if="pending && !submitting" class="text-sm text-amber-700 dark:text-amber-300">{{ t('pelican.submissionHint') }}</p>
      <div class="flex flex-wrap gap-2">
        <button class="btn btn-primary" :disabled="submitting || !ready">{{ t(submitting ? 'pelican.starting' : pending ? 'pelican.retrySubmission' : 'pelican.start') }}</button>
        <button v-if="pending && !submitting" type="button" class="btn btn-secondary" @click="clearPending">{{ t('pelican.discardSubmission') }}</button>
      </div>
    </form>
    <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950 dark:text-red-300">{{ error }}</p>
    <p v-if="notice" role="status" class="text-sm text-emerald-600">{{ notice }}</p>
    <div v-if="activeBatch" class="card flex flex-wrap items-center justify-between gap-3 p-4">
      <div><span class="font-medium">{{ t('pelican.batch', { id: activeBatch }) }}</span><p class="mt-1 text-sm text-gray-500">{{ t('pelican.progress', { done: batchItems.filter(v => !pelicanPending(v.status)).length, total: batchItems.length }) }}</p></div>
      <button v-if="batchItems.some(v => pelicanPending(v.status))" class="btn btn-secondary" @click="cancelBatch">{{ t('pelican.cancel') }}</button>
    </div>
    <PelicanGallery ref="gallery" admin :groups="groups" @submitted="trackBatch" />
  </div>
</template>
<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { pelicanAPI } from '@/api/pelican'
import { errorMessage } from '@/api/client'
import type { PelicanConfig, PelicanGroup, PelicanTarget, PelicanRequest, PelicanResult, PelicanProtocol } from '@/types/pelican'
import { DEFAULT_PELICAN_PROMPT, pelicanPending, pelicanRequestID } from '@/utils/pelican'
import PelicanGallery from '@/components/pelican/PelicanGallery.vue'
import Select from '@/components/common/Select.vue'
const { t } = useI18n()
const config = ref<PelicanConfig>({ gateway_url: '', user_id: '' })
const groups = ref<PelicanGroup[]>([]), users = ref<{ id: string; email: string; status: string }[]>([])
const userSearch = ref(''), error = ref(''), notice = ref(''), prompt = ref(DEFAULT_PELICAN_PROMPT)
const concurrency = ref(3), maxTokens = ref(32000), timeout = ref(900)
const submitting = ref(false), saving = ref(false)
const pending = ref<PelicanRequest | null>(null)
const gallery = ref<InstanceType<typeof PelicanGallery>>()
type TargetRow = Omit<PelicanTarget, 'protocol'> & { uid: number; protocol: PelicanProtocol | '' }
let nextUID = 0, disposed = false, batchTimer: ReturnType<typeof setTimeout> | undefined
const targets = ref<TargetRow[]>([])
const activeBatch = ref(0), batchItems = ref<PelicanResult[]>([])
const filteredUsers = computed(() => users.value.filter(u => u.id === config.value.user_id || `${u.id} ${u.email}`.toLowerCase().includes(userSearch.value.toLowerCase())))
const userOptions = computed(() => filteredUsers.value.map(u => ({ value: u.id, label: `#${u.id} · ${u.email} · ${u.status}`, disabled: u.status !== 'active' })))
const groupOptions = computed(() => groups.value.map(group => ({ value: group.id, label: group.name, description: group.reason ? t(group.reason) : undefined, disabled: !!group.reason })))
const ready = computed(() => !!pending.value || (targets.value.length > 0 && targets.value.every(row => row.group_id > 0 && row.key_id > 0 && row.model.trim() && row.protocol)))
function addTarget() { targets.value.push({ uid: nextUID++, group_id: 0, key_id: 0, model: '', protocol: '' }) }
function groupFor(row: TargetRow) { return groups.value.find(g => g.id === row.group_id) }
function keyOptions(row: TargetRow) { return (groupFor(row)?.keys ?? []).map(key => ({ value: key.id, label: `${key.name} ${key.masked}` })) }
function protocolOptions(row: TargetRow) {
  const platform = groupFor(row)?.platform
  return [
    ...(platform !== 'openai' ? [{ value: 'messages', label: 'Messages' }] : []),
    ...(platform !== 'anthropic' ? [{ value: 'responses', label: 'Responses' }, { value: 'chat', label: 'Chat Completions' }] : [])
  ]
}
function changeGroup(row: TargetRow) {
  const group = groupFor(row)
  row.key_id = group?.keys.length === 1 ? group.keys[0].id : 0
  row.model = ''; row.protocol = group?.platform === 'anthropic' ? 'messages' : group?.platform === 'openai' ? 'responses' : ''
}
function persistPending() { if (pending.value) sessionStorage.setItem('pelican.pending', JSON.stringify(pending.value)); else sessionStorage.removeItem('pelican.pending') }
function clearPending() { pending.value = null; persistPending() }
async function loadGroups() { groups.value = (await pelicanAPI.groups()).items }
async function saveConfig() {
  saving.value = true; error.value = ''; notice.value = ''
  try { config.value = await pelicanAPI.saveConfig(config.value); await loadGroups(); notice.value = t('pelican.saved') }
  catch (e) { error.value = t(errorMessage(e)) } finally { saving.value = false }
}
async function submit() {
  if (!ready.value) return
  submitting.value = true; error.value = ''; notice.value = ''
  if (!pending.value) pending.value = { request_id: pelicanRequestID(), prompt: prompt.value, concurrency: concurrency.value, max_tokens: maxTokens.value, timeout_seconds: timeout.value, targets: targets.value.map(({ uid: _uid, ...row }) => ({ ...row, protocol: row.protocol as PelicanProtocol })) }
  persistPending()
  try { const batch = await pelicanAPI.start(pending.value); clearPending(); trackBatch(batch.id); notice.value = t('pelican.submitted'); await gallery.value?.refresh() }
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
onMounted(async () => {
  addTarget()
  try { const raw = sessionStorage.getItem('pelican.pending'); if (raw) pending.value = JSON.parse(raw) } catch { sessionStorage.removeItem('pelican.pending') }
  try {
    const [cfg, people] = await Promise.all([pelicanAPI.config(), pelicanAPI.users()])
    config.value = cfg; users.value = people.items; await loadGroups()
    const batch = Number(sessionStorage.getItem('pelican.batch')); if (batch > 0) trackBatch(batch)
  } catch (e) { error.value = t(errorMessage(e)) }
})
onBeforeUnmount(() => { disposed = true; clearTimeout(batchTimer) })
</script>
