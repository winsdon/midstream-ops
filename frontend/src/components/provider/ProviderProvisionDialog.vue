<template>
  <BaseDialog
    :show="show"
    :title="t('provider.provisionTitle')"
    width="extra-wide"
    @close="!submitting && emit('close')"
  >
    <div class="space-y-4">
      <p v-if="!selfConfigured" class="rounded-lg bg-amber-50 px-3 py-2 text-xs text-amber-700 dark:bg-amber-900/20 dark:text-amber-300">
        {{ t('provider.selfNotConfigured') }}
      </p>
      <p v-else class="text-xs text-gray-400">{{ t('provider.provisionHint') }}</p>

      <p v-if="isNewAPI" class="rounded-lg bg-amber-50 px-3 py-2 text-xs text-amber-700 dark:bg-amber-900/20 dark:text-amber-300">{{ t('provider.newApiDefaults') }}</p>

      <div>
        <label class="input-label">{{ t('provider.baseUrl') }}</label>
        <input v-model.trim="baseUrl" class="input font-mono text-sm" :disabled="submitting || rows.some((r) => !!r.operationId)" />
        <p v-if="urlCount > 1" class="mt-1 text-xs text-amber-600 dark:text-amber-400">
          {{ t('provider.provisionMultiUrl', { n: urlCount }) }}
        </p>
      </div>

      <div class="table-wrapper">
        <table class="table">
          <thead>
            <tr>
              <th>{{ t('stats.groupName') }}</th>
              <th>{{ t('stats.rateMultiplier') }}</th>
              <th v-if="isNewAPI">{{ t('provider.accountProtocol') }}</th>
              <th>{{ t('provider.keyName') }}</th>
              <th>{{ t('provider.accountNameLabel') }}</th>
              <th>{{ t('provider.relatedGroups') }}</th>
              <th class="w-28">{{ t('common.status') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in rows" :key="row.group">
              <td class="text-sm font-medium">{{ row.group }}</td>
              <td class="text-xs">×{{ formatRate(row.rate) }}</td>
              <td v-if="isNewAPI">
                <select v-model="row.platform" class="input !py-1.5 text-xs" :disabled="submitting || !!row.operationId" @change="changePlatform(row)">
                  <option value="" disabled>{{ t('provider.chooseProtocol') }}</option>
                  <option value="openai">OpenAI</option>
                  <option value="anthropic">Anthropic</option>
                  <option value="gemini">Gemini</option>
                </select>
              </td>
              <td>
                <input v-model.trim="row.keyName" class="input !py-1.5 text-xs" :disabled="submitting || !!row.operationId" />
              </td>
              <td>
                <input v-model.trim="row.accountName" class="input !py-1.5 text-xs" :disabled="submitting || !!row.operationId" />
              </td>
              <td>
                <div class="max-h-24 min-w-[10rem] space-y-0.5 overflow-y-auto">
                  <label
                    v-for="g in compatibleGroups(row)" :key="g.id"
                    class="flex cursor-pointer items-center gap-1.5 text-xs"
                  >
                    <input
                      type="checkbox"
                      class="checkbox"
                      :checked="row.localGroupIds.includes(g.id)"
                      :disabled="submitting || !!row.operationId"
                      @change="toggleLocal(row, g.id)"
                    />
                    <span class="truncate" :title="g.name">{{ g.name }}</span>
                    <span class="shrink-0 text-gray-400">×{{ formatRate(g.rate) }}</span>
                  </label>
                  <p v-if="!compatibleGroups(row).length" class="text-xs text-gray-400">{{ t('provider.noLocalGroups') }}</p>
                </div>
              </td>
              <td class="text-xs">
                <span v-if="row.status === 'pending'" class="text-gray-400">—</span>
                <span v-else-if="row.status === 'running'" class="text-primary-600">{{ t('provider.creating') }}</span>
                <span v-else-if="row.status === 'ok'" class="text-emerald-600">{{ t('provider.provisionRowOk') }}</span>
                <span v-else class="text-red-500" :title="row.error">{{ row.error || t('provider.provisionRowFail') }}</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <template #footer>
      <button type="button" class="btn btn-secondary" :disabled="submitting" @click="emit('close')">
        {{ t('common.cancel') }}
      </button>
      <button
        type="button"
        class="btn btn-primary"
        :disabled="submitting || !selfConfigured || !canSubmit"
        @click="submit"
      >
        {{ submitting ? t('common.loading') : t('provider.createSelected') + ' (' + pendingCount + ')' }}
      </button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { selfApi, provisionApi, type ProvisionConnectResult } from '@/api'
import { errorMessage } from '@/api/client'
import { useAppStore } from '@/stores/app'
import BaseDialog from '@/components/common/BaseDialog.vue'
import {
  defaultLocalGroupIds,
  formatRate,
  localAccountName,
  pickAccountBaseURL,
  uniqueAccountBaseURLs,
  upstreamKeyName
} from '@/utils/provisionModel'
import type { LocalGroupOption, Provider, ProviderAccount, RateSnapshotItem } from '@/types'

interface Row {
  group: string
  rate: number
  platform: string
  keyName: string
  accountName: string
  localGroupIds: number[]
  status: 'pending' | 'running' | 'ok' | 'fail'
  error: string
  operationId: string
}

const props = defineProps<{
  show: boolean
  provider: Provider | null
  groups: RateSnapshotItem[]
  accounts: ProviderAccount[]
}>()

const emit = defineEmits<{
  close: []
  done: []
}>()

const { t } = useI18n()
const app = useAppStore()

const selfConfigured = ref(true)
const localGroups = ref<LocalGroupOption[]>([])
const baseUrl = ref('')
const urlCount = ref(0)
const rows = ref<Row[]>([])
const submitting = ref(false)
const rowsProviderId = ref<number>()

const isNewAPI = computed(() => props.provider?.platform === 'new-api')

const pendingCount = computed(() => rows.value.filter((r) => r.status === 'pending' || r.status === 'fail').length)
const canSubmit = computed(() =>
  rows.value.some((r) => r.status === 'pending' || r.status === 'fail') &&
  rows.value.every((r) => r.status === 'ok' || (r.keyName && r.accountName && r.localGroupIds.length > 0 && (!isNewAPI.value || ['openai', 'anthropic', 'gemini'].includes(r.platform))))
)

function compatibleGroups(row: Row) {
  return localGroups.value.filter((g) => !!row.platform && g.platform === row.platform)
}

function changePlatform(row: Row) {
  row.localGroupIds = defaultLocalGroupIds(localGroups.value, row.group, row.rate, row.platform)
}

function toggleLocal(row: Row, id: number) {
  const has = row.localGroupIds.includes(id)
  const localGroupIds = has
    ? row.localGroupIds.filter((x) => x !== id)
    : [...row.localGroupIds, id]
  rows.value = rows.value.map((r) => (r.group === row.group ? { ...r, localGroupIds } : r))
}

function newOperationId(): string {
  return globalThis.crypto?.randomUUID?.() ?? `op-${Date.now()}-${Math.random().toString(36).slice(2)}`
}

function buildRows() {
  rowsProviderId.value = props.provider?.id
  const providerName = props.provider?.name || ''
  rows.value = props.groups.map((g) => ({
    group: g.entity_name,
    rate: g.rate,
    platform: isNewAPI.value ? '' : g.platform || '',
    keyName: upstreamKeyName(g.entity_name, g.rate),
    accountName: localAccountName(providerName, g.entity_name, g.rate),
    localGroupIds: isNewAPI.value ? [] : defaultLocalGroupIds(localGroups.value, g.entity_name, g.rate, g.platform),
    status: 'pending',
    error: '',
    operationId: ''
  }))
}

async function hydrate() {
  // Reopening an unfinished batch must not reset its operation IDs or successful rows.
  if (rowsProviderId.value === props.provider?.id &&
      rows.value.some((r) => r.operationId) && rows.value.some((r) => r.status !== 'ok') &&
      rows.value.length === props.groups.length &&
      rows.value.every((r) => props.groups.some((g) => g.entity_name === r.group))) return
  const urls = uniqueAccountBaseURLs(props.accounts.map((a) => a.base_url))
  urlCount.value = urls.length
  baseUrl.value = pickAccountBaseURL(urls) || props.provider?.base_url || ''
  try {
    const [self, locals] = await Promise.all([
      selfApi.get(),
      selfApi.localGroups()
    ])
    selfConfigured.value = !!self.configured
    localGroups.value = locals.items || []
  } catch (e) {
    app.showError(errorMessage(e))
    selfConfigured.value = false
    localGroups.value = []
  }
  buildRows()
}

async function submit() {
  if (!props.provider || submitting.value || !canSubmit.value) return
  submitting.value = true
  let anyOk = false
  try {
    for (let i = 0; i < rows.value.length; i++) {
      const row = rows.value[i]
      if (row.status === 'ok') continue
      row.operationId ||= newOperationId()
      rows.value = rows.value.map((r, idx) =>
        idx === i ? { ...r, status: 'running', error: '' } : r
      )
      try {
        const res: ProvisionConnectResult = await provisionApi.connect({
          provider_id: props.provider.id,
          upstream_group: row.group,
          local_group_ids: row.localGroupIds,
          key_name: row.keyName,
          account_name: row.accountName,
          base_url: baseUrl.value,
          operation_id: row.operationId,
          platform: isNewAPI.value ? row.platform : undefined
        })
        if (!res.ok) {
          rows.value = rows.value.map((r, idx) =>
            idx === i ? { ...r, status: 'fail', error: res.error || t('provider.provisionRowFail') } : r
          )
          continue
        }
        anyOk = true
        rows.value = rows.value.map((r, idx) =>
          idx === i ? { ...r, status: 'ok', error: '' } : r
        )
      } catch (e) {
        rows.value = rows.value.map((r, idx) =>
          idx === i ? { ...r, status: 'fail', error: errorMessage(e) } : r
        )
      }
    }
    const ok = rows.value.filter((r) => r.status === 'ok').length
    const fail = rows.value.filter((r) => r.status === 'fail').length
    if (fail === 0) {
      app.showSuccess(t('provider.provisionDone', { ok, fail }))
      emit('done')
    } else if (anyOk) {
      app.showError(t('provider.provisionDone', { ok, fail }))
    }
  } finally {
    submitting.value = false
  }
}

watch(
  () => props.show,
  (show) => {
    if (show) void hydrate()
  }
)
</script>
