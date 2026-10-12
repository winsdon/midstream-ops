<template>
  <BaseDialog :show="detail !== null" :title="detail ? `${detail.group_name} · ${detail.model}` : ''" width="full" @close="emit('close')">
    <div v-if="detail" class="grid items-start gap-4 lg:grid-cols-[minmax(0,1.6fr)_minmax(16rem,22rem)]">
      <div class="min-w-0">
        <div v-if="admin && detail.has_document" class="mb-2 flex flex-wrap rounded-lg bg-gray-100 p-0.5 text-xs dark:bg-dark-800">
          <button
            v-for="mode in TABS"
            :key="mode"
            type="button"
            class="rounded-md px-2.5 py-1 font-medium transition-colors"
            :class="view === mode ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-700 dark:text-white' : 'text-gray-500 hover:text-gray-700 dark:text-dark-400'"
            @click="view = mode"
          >{{ t(`pelican.tab.${mode}`) }}</button>
        </div>
        <div v-if="showPreview" class="h-[55vh] min-h-[20rem] overflow-hidden rounded-lg border border-gray-100 dark:border-dark-700">
          <PelicanPreview :key="detail.id" :id="detail.id" :title="detail.group_name" :fetch-result="fetchResult" interactive />
        </div>
        <pre v-else-if="view === 'source'" class="h-[55vh] min-h-[20rem] overflow-auto whitespace-pre-wrap break-all rounded-lg bg-gray-50 p-3 text-xs dark:bg-dark-900">{{ detail.output?.document }}</pre>
        <pre v-else-if="view === 'reply'" class="h-[55vh] min-h-[20rem] overflow-auto whitespace-pre-wrap break-words rounded-lg bg-gray-50 p-3 text-xs dark:bg-dark-900">{{ detail.output?.text }}</pre>
        <pre v-else-if="view === 'parameters'" class="h-[55vh] min-h-[20rem] overflow-auto whitespace-pre-wrap break-words rounded-lg bg-gray-50 p-3 text-xs dark:bg-dark-900">{{ pretty(detail.output?.request) }}</pre>
        <pre v-else-if="view === 'usage'" class="h-[55vh] min-h-[20rem] overflow-auto whitespace-pre-wrap break-words rounded-lg bg-gray-50 p-3 text-xs dark:bg-dark-900">{{ pretty(detail.output?.usage) }}</pre>
        <div v-else class="flex h-[24rem] items-center justify-center rounded-lg bg-gray-50 text-sm text-gray-500 dark:bg-dark-900">{{ t(`pelican.statuses.${detail.status}`) }}</div>
      </div>

      <aside class="space-y-3 text-sm">
        <div class="flex flex-wrap gap-2">
          <Badge :variant="pelicanStatusVariant(detail.status)">{{ t(`pelican.statuses.${detail.status}`) }}</Badge>
          <Badge v-if="admin" :variant="detail.published_at ? 'success' : 'gray'">{{ t(detail.published_at ? 'pelican.published' : 'pelican.draft') }}</Badge>
        </div>
        <p class="text-gray-500">{{ t('pelican.testedAt') }}: <time :datetime="detail.tested_at">{{ fullDate(detail.tested_at) }}</time></p>
        <p class="text-gray-500">{{ t('pelican.duration') }}: {{ fmtMs(detail.duration_ms) }}</p>
        <p v-if="detail.published_at" class="text-gray-500">{{ t('pelican.publishedAt') }}: {{ fullDate(detail.published_at) }}</p>
        <p class="text-gray-500">{{ t('pelican.parameters') }}: {{ pelicanProtocolLabel(detail.protocol) }} · max_tokens={{ detail.max_tokens }}<span v-if="detail.source"> · {{ detail.source === 'manual' ? t('pelican.sourceManual') : t('pelican.sourceAccount') }}</span></p>
        <p v-if="admin && detail.error" class="text-red-600">{{ t(detail.error) }}</p>
        <h4 class="font-semibold text-gray-900 dark:text-white">{{ t('pelican.prompt') }}</h4>
        <pre class="max-h-48 overflow-auto whitespace-pre-wrap break-words rounded-lg bg-gray-50 p-3 text-xs dark:bg-dark-900">{{ detail.prompt }}</pre>
        <div v-if="admin && !pelicanPending(detail.status)" class="space-y-2">
          <template v-if="detail.source === 'account'">
            <Select v-model="retryAccountId" class="w-full" :options="retryAccountOptions" :placeholder="t('pelican.selectAccount')" searchable :aria-label="t('pelican.account')" />
            <button type="button" class="btn btn-primary w-full" :disabled="busy || !retryAccountId" @click="emit('retry')">{{ t('pelican.retry') }}</button>
          </template>
          <template v-else-if="detail.source === 'manual'">
            <label class="block text-sm">{{ t('pelican.apiKey') }}
              <input v-model.trim="retryKey" class="input mt-1 w-full" type="password" autocomplete="off" :placeholder="t('pelican.retryKeyHint')" />
            </label>
            <button type="button" class="btn btn-primary w-full" :disabled="busy || !retryKey.trim()" @click="emit('retry')">{{ t('pelican.retry') }}</button>
          </template>
          <p v-else class="text-xs text-gray-500">{{ t('pelican.legacyRetryHint') }}</p>
        </div>
      </aside>
    </div>
    <template v-if="detail" #footer>
      <button v-if="admin" type="button" class="btn btn-danger mr-auto" :disabled="busy || !pelicanDeletable(detail)" :title="deleteHint" @click="emit('delete')">{{ t('pelican.delete') }}</button>
      <button v-if="admin && pelicanPending(detail.status) && detail.batch_id" type="button" class="btn btn-secondary" :disabled="busy" @click="emit('cancel', detail.batch_id)">{{ t('pelican.cancel') }}</button>
      <button type="button" class="btn btn-secondary" @click="emit('close')">{{ t('common.close') }}</button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Badge from '@/components/common/Badge.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import { fmtMs } from '@/utils/format'
import { pelicanDeletable, pelicanHost, pelicanPending, pelicanProtocolLabel, pelicanStatusVariant } from '@/utils/pelican'
import type { PelicanAccount, PelicanResult } from '@/types/pelican'
import PelicanPreview from './PelicanPreview.vue'

const TABS = ['preview', 'source', 'reply', 'parameters', 'usage'] as const
type Tab = (typeof TABS)[number]

const props = defineProps<{
  detail: PelicanResult | null
  admin?: boolean
  accounts?: PelicanAccount[]
  busy?: boolean
  fetchResult: (id: number) => Promise<PelicanResult>
}>()
const retryAccountId = defineModel<number>('retryAccountId', { required: true })
const retryKey = defineModel<string>('retryKey', { required: true })
const emit = defineEmits<{ close: []; delete: []; retry: []; cancel: [id: number] }>()

const { t, locale } = useI18n()
const view = ref<Tab>('preview')
const retryAccountOptions = computed(() => (props.accounts ?? []).map(account => ({ value: account.id, label: `${account.name} · ${account.platform} · ${pelicanHost(account.base_url)}` })))
const showPreview = computed(() => !!props.detail?.has_document && (!props.admin || view.value === 'preview'))
const deleteHint = computed(() => {
  const item = props.detail
  if (!item) return ''
  if (item.published_at) return t('pelican.deleteBlockedPublished')
  if (pelicanPending(item.status)) return t('pelican.deleteBlockedRunning')
  return t('pelican.delete')
})

watch(() => props.detail?.id, () => { view.value = 'preview' })

function fullDate(value: string) { return new Date(value).toLocaleString(locale.value, { timeZoneName: 'short' }) }
function pretty(value: unknown) { return JSON.stringify(value, null, 2) }
</script>
