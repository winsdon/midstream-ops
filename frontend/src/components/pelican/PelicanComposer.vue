<template>
  <form class="card" @submit.prevent="ready && !submitting && emit('submit')">
    <fieldset class="space-y-4 p-4 sm:p-5" :disabled="submitting">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <label for="pelican-prompt" class="text-sm font-medium text-gray-900 dark:text-white">{{ t('pelican.prompt') }}</label>
        <button type="button" class="text-sm text-primary-600 dark:text-primary-400" @click="prompt = DEFAULT_PELICAN_PROMPT">{{ t('pelican.resetPrompt') }}</button>
      </div>
      <textarea id="pelican-prompt" v-model="prompt" rows="2" class="input w-full resize-y" required />
      <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('pelican.deliveryHint') }}</p>

      <div class="space-y-2">
        <h2 class="text-sm font-medium text-gray-900 dark:text-white">{{ t('pelican.targets') }}</h2>
        <div class="hidden gap-2 px-1 text-xs font-medium text-gray-500 dark:text-dark-400 lg:grid lg:grid-cols-[minmax(9rem,1.1fr)_9.5rem_minmax(11rem,1.35fr)_minmax(8rem,1fr)_8.5rem_2.25rem]">
          <span>{{ t('pelican.group') }}</span>
          <span>{{ t('pelican.source') }}</span>
          <span>{{ t('pelican.credential') }}</span>
          <span>{{ t('pelican.model') }}</span>
          <span>{{ t('pelican.protocol') }}</span>
          <span class="sr-only">{{ t('pelican.remove') }}</span>
        </div>
        <div
          v-for="row in targets"
          :key="row.uid"
          class="grid gap-3 rounded-xl border border-gray-200 p-3 dark:border-dark-700 lg:grid-cols-[minmax(9rem,1.1fr)_9.5rem_minmax(11rem,1.35fr)_minmax(8rem,1fr)_8.5rem_2.25rem] lg:items-start lg:rounded-none lg:border-0 lg:border-b lg:px-1 lg:py-3"
        >
          <label class="min-w-0 text-sm">
            <span class="mb-1 block text-xs font-medium text-gray-500 dark:text-dark-400 lg:sr-only">{{ t('pelican.group') }}</span>
            <Select :model-value="row.group_id || null" class="w-full" :options="groupOptions" :placeholder="t('pelican.selectGroup')" :disabled="submitting" @update:model-value="onGroup(row, $event)" />
          </label>
          <div class="min-w-0">
            <span class="mb-1 block text-xs font-medium text-gray-500 dark:text-dark-400 lg:sr-only">{{ t('pelican.source') }}</span>
            <div class="flex rounded-lg bg-gray-100 p-0.5 text-xs dark:bg-dark-900" role="group" :aria-label="t('pelican.source')">
              <button
                v-for="option in sourceOptions"
                :key="option.value"
                type="button"
                class="flex-1 rounded-md px-2 py-1.5 font-medium transition-colors"
                :class="row.source === option.value ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-700 dark:text-white' : 'text-gray-500 hover:text-gray-700 dark:text-dark-400'"
                :aria-pressed="row.source === option.value"
                @click="onSource(row, option.value)"
              >{{ option.label }}</button>
            </div>
          </div>
          <div class="min-w-0 text-sm">
            <span class="mb-1 block text-xs font-medium text-gray-500 dark:text-dark-400 lg:sr-only">{{ row.source === 'manual' ? t('pelican.address') : t('pelican.account') }}</span>
            <template v-if="row.source === 'manual'">
              <input :value="row.base_url" class="input w-full" type="url" placeholder="https://api.example.com" :disabled="submitting" :aria-label="t('pelican.address')" @input="onText(row, 'base_url', ($event.target as HTMLInputElement).value.trim())" />
              <input :value="row.api_key" class="input mt-1.5 w-full" type="password" autocomplete="off" :placeholder="t('pelican.apiKey')" :disabled="submitting" :aria-label="t('pelican.apiKey')" @input="onText(row, 'api_key', ($event.target as HTMLInputElement).value.trim())" />
            </template>
            <template v-else>
              <Select :model-value="row.account_id || null" class="w-full" :options="accountOptions" :placeholder="t('pelican.selectAccount')" searchable :disabled="submitting" @update:model-value="onAccount(row, $event)" />
              <p v-if="!accounts.length" class="mt-1 text-xs text-amber-600 dark:text-amber-400">{{ t('pelican.accountEmpty') }}</p>
            </template>
          </div>
          <label class="min-w-0 text-sm">
            <span class="mb-1 block text-xs font-medium text-gray-500 dark:text-dark-400 lg:sr-only">{{ t('pelican.model') }}</span>
            <input :value="row.model" class="input w-full" :list="`pelican-models-${row.uid}`" :placeholder="t('pelican.modelHint')" required :disabled="submitting" :aria-label="t('pelican.model')" @input="onText(row, 'model', ($event.target as HTMLInputElement).value)" />
            <datalist :id="`pelican-models-${row.uid}`"><option v-for="model in modelsFor(row)" :key="model" :value="model" /></datalist>
          </label>
          <div class="min-w-0 text-sm">
            <span class="mb-1 block text-xs font-medium text-gray-500 dark:text-dark-400 lg:sr-only">{{ t('pelican.protocol') }}</span>
            <p v-if="protocolChoices(row).length <= 1" class="flex h-[42px] items-center text-sm text-gray-700 dark:text-gray-200">{{ protocolChoices(row)[0]?.label || '—' }}</p>
            <Select v-else :model-value="row.protocol || null" class="w-full" :options="protocolChoices(row)" :placeholder="t('pelican.selectProtocol')" :searchable="false" :disabled="submitting" @update:model-value="onProtocol(row, $event)" />
          </div>
          <button type="button" class="btn btn-ghost btn-sm mt-5 justify-self-start !px-2 lg:mt-1" :disabled="targets.length === 1" :aria-label="t('pelican.remove')" @click="removeRow(row.uid)">
            <Icon name="x" size="sm" />
          </button>
        </div>
        <p v-if="targets.some(row => row.source === 'manual')" class="text-xs text-gray-500 dark:text-dark-400">{{ t('pelican.apiKeyHint') }}</p>
        <button class="btn btn-secondary btn-sm" type="button" :disabled="targets.length >= 100" @click="addRow">
          <Icon name="plus" size="sm" />
          {{ t('pelican.add') }}
        </button>
      </div>
    </fieldset>

    <div class="fixed inset-x-0 bottom-0 z-20 flex flex-wrap items-end justify-between gap-3 border-t border-gray-200 bg-white/95 px-4 py-3 shadow-[0_-8px_20px_rgba(15,23,42,0.06)] backdrop-blur dark:border-dark-700 dark:bg-dark-900/95 sm:px-5 lg:static lg:inset-auto lg:rounded-b-2xl lg:border-gray-100 lg:bg-white lg:shadow-none lg:backdrop-blur-none dark:lg:bg-dark-900">
      <div class="min-w-[12rem] flex-1">
        <p class="text-sm text-gray-600 dark:text-dark-300">{{ t('pelican.targetSummary', { ready: readyCount, total: targets.length }) }}</p>
        <p v-if="pending && !submitting" class="mt-1 text-xs text-amber-700 dark:text-amber-300">{{ t('pelican.submissionHint') }}</p>
      </div>
      <div class="flex flex-wrap items-end gap-2">
        <label class="text-xs text-gray-500 dark:text-dark-400">{{ t('pelican.concurrency') }}
          <input v-model.number="concurrency" class="input mt-1 w-20 !px-2 !py-1.5 text-center" type="number" min="1" max="10" required :disabled="submitting" />
        </label>
        <label class="text-xs text-gray-500 dark:text-dark-400">{{ t('pelican.maxTokens') }}
          <input v-model.number="maxTokens" class="input mt-1 w-28 !px-2 !py-1.5 text-center" type="number" min="1" max="262144" required :disabled="submitting" />
        </label>
        <label class="text-xs text-gray-500 dark:text-dark-400">{{ t('pelican.timeout') }}
          <input v-model.number="timeout" class="input mt-1 w-24 !px-2 !py-1.5 text-center" type="number" min="1" max="3600" required :disabled="submitting" />
        </label>
      </div>
      <div class="flex flex-wrap gap-2">
        <button v-if="pending && !submitting" type="button" class="btn btn-secondary btn-sm" @click="emit('discard')">{{ t('pelican.discardSubmission') }}</button>
        <button type="button" class="btn btn-primary btn-sm" :disabled="submitting || !ready" @click="ready && !submitting && emit('submit')">
          <Icon name="play" size="sm" />
          {{ t(submitting ? 'pelican.starting' : pending ? 'pelican.retrySubmission' : 'pelican.start') }}
        </button>
      </div>
    </div>
  </form>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import type { PelicanAccount, PelicanGroup, PelicanProtocol, PelicanSource } from '@/types/pelican'
import { DEFAULT_PELICAN_PROMPT, pelicanAccountPlatform, pelicanBlankRow, pelicanHost, pelicanNormalizeRow, pelicanProtocolLabel, pelicanProtocolOptions, pelicanTargetReady, type PelicanTargetRow } from '@/utils/pelican'

const props = defineProps<{
  groups: PelicanGroup[]
  accounts: PelicanAccount[]
  submitting: boolean
  pending: boolean
}>()
const prompt = defineModel<string>('prompt', { required: true })
const concurrency = defineModel<number>('concurrency', { required: true })
const maxTokens = defineModel<number>('maxTokens', { required: true })
const timeout = defineModel<number>('timeout', { required: true })
const targets = defineModel<PelicanTargetRow[]>('targets', { required: true })
const emit = defineEmits<{ submit: []; discard: [] }>()

const { t } = useI18n()
const sourceOptions = computed(() => [
  { value: 'account' as const, label: t('pelican.sourceAccount') },
  { value: 'manual' as const, label: t('pelican.sourceManualShort') }
])
const groupOptions = computed(() => props.groups.map(group => ({ value: group.id, label: group.name, description: group.platform || undefined })))
const accountOptions = computed(() => props.accounts.map(account => ({ value: account.id, label: `${account.name} · ${account.platform} · ${pelicanHost(account.base_url)}` })))
const readyCount = computed(() => targets.value.filter(pelicanTargetReady).length)
const ready = computed(() => targets.value.length > 0 && targets.value.every(pelicanTargetReady))

function modelsFor(row: PelicanTargetRow) {
  return props.groups.find(group => group.id === row.group_id)?.models ?? []
}
function protocolChoices(row: PelicanTargetRow) {
  return pelicanProtocolOptions(pelicanAccountPlatform(props.accounts, row)).map(value => ({ value, label: pelicanProtocolLabel(value) }))
}
function patch(uid: number, partial: Partial<PelicanTargetRow>) {
  targets.value = targets.value.map(row => {
    if (row.uid !== uid) return row
    const merged = { ...row, ...partial }
    return pelicanNormalizeRow(merged, pelicanAccountPlatform(props.accounts, merged))
  })
}
function onGroup(row: PelicanTargetRow, value: unknown) {
  patch(row.uid, { group_id: Number(value) || 0, model: '' })
}
function onAccount(row: PelicanTargetRow, value: unknown) {
  patch(row.uid, { account_id: Number(value) || 0 })
}
function onProtocol(row: PelicanTargetRow, value: unknown) {
  patch(row.uid, { protocol: (value || '') as PelicanProtocol | '' })
}
function onSource(row: PelicanTargetRow, source: PelicanSource) {
  if (row.source === source) return
  const only = props.accounts.length === 1 ? props.accounts[0].id : 0
  patch(row.uid, { source, account_id: only })
}
function onText(row: PelicanTargetRow, key: 'base_url' | 'api_key' | 'model', value: string) {
  patch(row.uid, { [key]: value })
}
function removeRow(uid: number) {
  if (targets.value.length <= 1) return
  targets.value = targets.value.filter(row => row.uid !== uid)
}
function addRow() {
  if (targets.value.length >= 100) return
  const uid = Math.max(0, ...targets.value.map(row => row.uid)) + 1
  targets.value = [...targets.value, pelicanBlankRow(uid, props.accounts)]
}
</script>
