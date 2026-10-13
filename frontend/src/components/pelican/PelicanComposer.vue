<template>
  <BaseDialog :show="show" :title="t('pelican.startTest')" width="wide" @close="emit('close')">
    <form class="space-y-5" @submit.prevent="submit">
      <div class="space-y-2">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <label for="pelican-prompt" class="text-sm font-medium text-gray-900 dark:text-white">{{ t('pelican.prompt') }}</label>
          <button type="button" class="text-sm text-primary-600 dark:text-primary-400" @click="patch({ prompt: DEFAULT_PELICAN_PROMPT })">{{ t('pelican.resetPrompt') }}</button>
        </div>
        <textarea id="pelican-prompt" v-model="promptText" rows="2" class="input w-full resize-y" required :disabled="submitting" />
        <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('pelican.deliveryHint') }}</p>
      </div>

      <div class="grid gap-4 sm:grid-cols-2">
        <div class="min-w-0 text-sm">
          <span class="mb-1 block text-xs font-medium text-gray-500 dark:text-dark-400">{{ t('pelican.group') }}</span>
          <Select :model-value="draft.group_id || null" class="w-full" :options="groupOptions" :placeholder="t('pelican.selectGroup')" :disabled="submitting" @update:model-value="onGroup" />
          <p v-if="!groups.length" class="mt-1 text-xs text-amber-600 dark:text-amber-400">{{ t('pelican.groupEmpty') }}</p>
        </div>
        <div class="min-w-0">
          <span class="mb-1 block text-xs font-medium text-gray-500 dark:text-dark-400">{{ t('pelican.source') }}</span>
          <div class="flex rounded-lg bg-gray-100 p-0.5 text-xs dark:bg-dark-900" role="group" :aria-label="t('pelican.source')">
            <button
              v-for="option in sourceOptions"
              :key="option.value"
              type="button"
              class="flex-1 rounded-md px-2 py-2 font-medium transition-colors"
              :class="draft.source === option.value ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-700 dark:text-white' : 'text-gray-500 hover:text-gray-700 dark:text-dark-400'"
              :aria-pressed="draft.source === option.value"
              :disabled="submitting"
              @click="onSource(option.value)"
            >{{ option.label }}</button>
          </div>
        </div>
      </div>

      <div class="space-y-2">
        <span class="block text-xs font-medium text-gray-500 dark:text-dark-400">{{ draft.source === 'manual' ? t('pelican.address') : t('pelican.account') }}</span>
        <template v-if="draft.source === 'manual'">
          <input :value="draft.base_url" class="input w-full" type="url" placeholder="https://api.example.com" :disabled="submitting" :aria-label="t('pelican.address')" @input="patch({ base_url: ($event.target as HTMLInputElement).value.trim() })" />
          <input :value="draft.api_key" class="input w-full" type="password" autocomplete="off" :placeholder="t('pelican.apiKey')" :disabled="submitting" :aria-label="t('pelican.apiKey')" @input="patch({ api_key: ($event.target as HTMLInputElement).value.trim() })" />
          <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('pelican.apiKeyHint') }}</p>
        </template>
        <template v-else>
          <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('pelican.accountFilterHint') }}</p>
          <PelicanAccountPicker
            :accounts="available"
            :selected="draft.account_ids"
            :disabled="submitting"
            @toggle="toggleAccount"
            @select="selectAccounts"
            @clear="patch({ account_ids: [] })"
          />
          <p v-if="available.length > PELICAN_MAX_TARGETS" class="text-xs text-amber-600 dark:text-amber-400">{{ t('pelican.accountLimit', { max: PELICAN_MAX_TARGETS }) }}</p>
        </template>
      </div>

      <div class="grid gap-4 sm:grid-cols-2">
        <label class="min-w-0 text-sm">
          <span class="mb-1 block text-xs font-medium text-gray-500 dark:text-dark-400">{{ t('pelican.model') }}</span>
          <input :value="draft.model" class="input w-full" list="pelican-models" :placeholder="t('pelican.modelHint')" required :disabled="submitting" :aria-label="t('pelican.model')" @input="patch({ model: ($event.target as HTMLInputElement).value })" />
          <datalist id="pelican-models"><option v-for="model in modelOptions" :key="model" :value="model" /></datalist>
        </label>
        <div class="min-w-0 text-sm">
          <span class="mb-1 block text-xs font-medium text-gray-500 dark:text-dark-400">{{ t('pelican.protocol') }}</span>
          <template v-if="draft.source === 'manual'">
            <Select :model-value="draft.protocol || null" class="w-full" :options="manualProtocolOptions" :placeholder="t('pelican.selectProtocol')" :searchable="false" :disabled="submitting" @update:model-value="value => patch({ protocol: (value || '') as PelicanProtocol | '' })" />
          </template>
          <template v-else-if="protocolChoices.length > 1">
            <Select :model-value="accountProtocol" class="w-full" :options="accountProtocolOptions" :placeholder="t('pelican.selectProtocol')" :searchable="false" :disabled="submitting" @update:model-value="value => patch({ protocol: (value || '') as PelicanProtocol | '' })" />
            <p v-if="mixedPlatforms" class="mt-1 text-xs text-gray-500 dark:text-dark-400">{{ t('pelican.mixedProtocolHint') }}</p>
          </template>
          <p v-else class="flex h-[42px] items-center text-sm text-gray-700 dark:text-gray-200">
            {{ protocolChoices.length ? t('pelican.accountFixedProtocol') : '—' }}
          </p>
        </div>
      </div>

      <div>
        <button type="button" class="text-sm text-primary-600 dark:text-primary-400" :aria-expanded="advanced" @click="advanced = !advanced">
          {{ t('pelican.advanced') }}
        </button>
        <div v-if="advanced" class="mt-2 grid gap-3 sm:grid-cols-3">
          <label class="text-xs text-gray-500 dark:text-dark-400">{{ t('pelican.concurrency') }}
            <input :value="draft.concurrency" class="input mt-1 w-full !py-1.5 text-center" type="number" min="1" max="10" :disabled="submitting" @input="onNumber('concurrency', $event, 10)" @blur="syncNumber('concurrency', $event)" />
          </label>
          <label class="text-xs text-gray-500 dark:text-dark-400">{{ t('pelican.maxTokens') }}
            <input :value="draft.max_tokens" class="input mt-1 w-full !py-1.5 text-center" type="number" min="1" max="262144" :disabled="submitting" @input="onNumber('max_tokens', $event, 262144)" @blur="syncNumber('max_tokens', $event)" />
          </label>
          <label class="text-xs text-gray-500 dark:text-dark-400">{{ t('pelican.timeout') }}
            <input :value="draft.timeout_seconds" class="input mt-1 w-full !py-1.5 text-center" type="number" min="1" max="3600" :disabled="submitting" @input="onNumber('timeout_seconds', $event, 3600)" @blur="syncNumber('timeout_seconds', $event)" />
          </label>
        </div>
      </div>

      <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950 dark:text-red-300">{{ error }}</p>
      <p v-if="pending && !submitting" class="text-xs text-amber-700 dark:text-amber-300">{{ t('pelican.submissionHint') }}</p>
      <p v-if="pending && replayed && !submitting" class="text-xs text-gray-500 dark:text-dark-400">{{ t('pelican.replayHint', { count: runCount }) }}</p>
    </form>

    <template #footer>
      <span class="mr-auto text-sm text-gray-600 dark:text-dark-300">{{ t('pelican.willRun', { count: runCount }) }}</span>
      <button v-if="pending && !submitting" type="button" class="btn btn-secondary btn-sm" @click="emit('discard')">{{ t('pelican.discardSubmission') }}</button>
      <button type="button" class="btn btn-secondary btn-sm" :disabled="submitting" @click="emit('close')">{{ t('common.close') }}</button>
      <button type="button" class="btn btn-primary btn-sm" :disabled="submitting || !runCount" @click="submit">
        <Icon name="play" size="sm" />
        {{ t(submitting ? 'pelican.starting' : pending ? 'pelican.retrySubmission' : 'pelican.start') }}
      </button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import type { PelicanAccount, PelicanGroup, PelicanProtocol, PelicanSource } from '@/types/pelican'
import {
  DEFAULT_PELICAN_PROMPT,
  PELICAN_MAX_TARGETS,
  pelicanAccountProtocolChoices,
  pelicanAccountsForGroup,
  pelicanPruneAccountIDs,
  pelicanProtocolLabel,
  pelicanProtocolOptions,
  type PelicanDraft
} from '@/utils/pelican'
import PelicanAccountPicker from './PelicanAccountPicker.vue'

const props = defineProps<{
  show: boolean
  groups: PelicanGroup[]
  accounts: PelicanAccount[]
  submitting: boolean
  /** 上一次提交尚未确认（参数没改时复用同一个请求标识）。 */
  pending: boolean
  /** 本次提交会跑多少个目标，由页面算：重放待确认提交时按存下来的那份。 */
  runCount: number
  /** 参数没改动，本次提交是把待确认的那份原样重放。 */
  replayed: boolean
  error?: string
}>()
const draft = defineModel<PelicanDraft>('draft', { required: true })
const emit = defineEmits<{ submit: []; discard: []; close: [] }>()

const { t } = useI18n()
const advanced = ref(false)
const sourceOptions = computed(() => [
  { value: 'account' as const, label: t('pelican.sourceAccount') },
  { value: 'manual' as const, label: t('pelican.sourceManualShort') }
])
const groupOptions = computed(() => props.groups.map(group => ({ value: group.id, label: group.name, description: group.platform || undefined })))
const available = computed(() => pelicanAccountsForGroup(props.accounts, draft.value.group_id))
const selectedAccounts = computed(() => available.value.filter(account => draft.value.account_ids.includes(account.id)))
const modelOptions = computed(() => props.groups.find(group => group.id === draft.value.group_id)?.models ?? [])
const protocolChoices = computed(() => pelicanAccountProtocolChoices(selectedAccounts.value))
/** 展示用的 OpenAI 协议：draft 里存的是选择，展示时收敛到实际会提交的那个。 */
const accountProtocol = computed<PelicanProtocol>(() => (draft.value.protocol === 'chat' && protocolChoices.value.includes('chat') ? 'chat' : 'responses'))
const mixedPlatforms = computed(() => protocolChoices.value.length > 1 && selectedAccounts.value.some(account => account.platform === 'anthropic'))
const manualProtocolOptions = computed(() => pelicanProtocolOptions().map(value => ({ value, label: pelicanProtocolLabel(value) })))
const accountProtocolOptions = computed(() => protocolChoices.value.map(value => ({ value, label: pelicanProtocolLabel(value) })))
const promptText = computed({ get: () => draft.value.prompt, set: (value: string) => patch({ prompt: value }) })

function patch(partial: Partial<PelicanDraft>) {
  draft.value = { ...draft.value, ...partial }
}
/** 账号勾选：每次都基于当前草稿算下一个状态，勾选顺序即提交顺序；总数封顶后端上限。 */
function toggleAccount(id: number) {
  const current = draft.value.account_ids
  if (current.includes(id)) {
    patch({ account_ids: current.filter(kept => kept !== id) })
    return
  }
  if (current.length >= PELICAN_MAX_TARGETS) return
  patch({ account_ids: [...current, id] })
}
function selectAccounts(ids: number[]) {
  patch({ account_ids: pelicanPruneAccountIDs([...draft.value.account_ids, ...ids], available.value).slice(0, PELICAN_MAX_TARGETS) })
}
type PelicanNumberKey = 'concurrency' | 'max_tokens' | 'timeout_seconds'
/** 高级选项只接受正整数：空值或非法输入保持原值，超上限按上限收敛。 */
function onNumber(key: PelicanNumberKey, event: Event, max: number) {
  const parsed = Math.floor(Number((event.target as HTMLInputElement).value))
  if (!Number.isFinite(parsed) || parsed < 1) return
  const value = Math.min(parsed, max)
  if (key === 'concurrency') patch({ concurrency: value })
  else if (key === 'max_tokens') patch({ max_tokens: value })
  else patch({ timeout_seconds: value })
}
/** 失焦时把输入框拉回真实提交值：编辑途中允许空着，离开就必须和提交值一致。 */
function syncNumber(key: PelicanNumberKey, event: Event) {
  (event.target as HTMLInputElement).value = String(draft.value[key])
}
/** 换分组时收敛账号勾选：保留仍可选的，空了就在唯一账号时默认选中；模型随分组清空。 */
function accountsForGroup(groupID: number) {
  const list = pelicanAccountsForGroup(props.accounts, groupID)
  const kept = pelicanPruneAccountIDs(draft.value.account_ids, list)
  return kept.length ? kept : (list.length === 1 ? [list[0].id] : [])
}
function onGroup(value: unknown) {
  const group_id = Number(value) || 0
  patch({ group_id, model: '', account_ids: accountsForGroup(group_id) })
}
function onSource(source: PelicanSource) {
  if (draft.value.source === source) return
  const account_ids = source === 'account' && !draft.value.account_ids.length && available.value.length === 1 ? [available.value[0].id] : draft.value.account_ids
  patch({ source, account_ids })
}
function submit() {
  if (props.submitting || !props.runCount) return
  emit('submit')
}
</script>
