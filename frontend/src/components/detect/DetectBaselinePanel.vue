<template>
  <div class="card">
    <div class="card-header">
      <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('detect.baselineTitle') }}</h2>
      <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">{{ t('detect.baselineHint') }}</p>
    </div>
    <div class="card-body space-y-3">
      <button type="button" class="btn btn-secondary w-full justify-between" :disabled="loading" @click="showPicker = true">
        <span class="truncate text-left">{{ pickerLabel }}</span>
        <Icon name="chevronRight" size="sm" />
      </button>
      <div v-if="selectedAccount" class="flex flex-wrap gap-1.5">
        <span
          class="inline-flex max-w-full items-center gap-1 rounded-lg bg-primary-50 px-2 py-0.5 text-xs text-primary-700 dark:bg-primary-900/30 dark:text-primary-300"
        >
          <span class="truncate">{{ selectedAccount.account_name }}</span>
          <button type="button" class="shrink-0" :title="t('detect.removeTarget')" @click="selectedAccountId = 0">
            <Icon name="x" size="sm" />
          </button>
        </span>
      </div>
      <p v-if="accountsError" class="text-xs text-amber-600 dark:text-amber-400">{{ accountsError }}</p>
      <p v-else-if="!accountsLoading && !accounts.length" class="text-xs text-amber-600 dark:text-amber-400">
        {{ t('detect.accountEmpty') }}
      </p>
      <p class="text-xs text-gray-500 dark:text-dark-400">
        {{ t('detect.baselineUsesModel', { model: testModel || t('detect.modelEmpty') }) }}
      </p>
      <button
        type="button"
        class="btn btn-secondary w-full"
        :disabled="!canGenerate"
        @click="emit('generate', selectedAccountId)"
      >
        {{ loading ? t('detect.baselineGenerating') : t('detect.generateBaseline') }}
      </button>

      <div v-if="baseline" class="space-y-2 rounded-xl bg-gray-50 p-3 text-xs dark:bg-dark-800/60">
        <div class="font-medium text-gray-900 dark:text-white">{{ baseline.target_name }} · {{ baseline.model }}</div>
        <div class="text-gray-500 dark:text-dark-400">
          {{ t('detect.baselineStats', { output: baseline.output_tokens, thinking: baseline.thinking_tokens || baseline.thinking_chars }) }}
        </div>
        <div :class="baseline.quality_ok ? 'text-emerald-600' : 'text-red-600'">
          {{ baseline.quality_ok ? t('detect.baselineReady') : baselineError }}
        </div>
        <p v-if="modelMismatch" class="text-amber-600 dark:text-amber-400">
          {{ t('detect.baselineModelMismatch', { baseline: baseline.model, current: testModel || t('detect.modelEmpty') }) }}
        </p>
        <button
          type="button"
          class="btn btn-ghost btn-sm"
          :disabled="!baseline.exchange"
          :title="baseline.exchange ? '' : t('detect.baselineNoExchange')"
          @click="showExchange = true"
        >
          {{ t('detect.baselineViewExchange') }}
        </button>
      </div>
      <p v-else class="text-xs text-amber-600 dark:text-amber-400">{{ t('detect.baselineMissing') }}</p>
    </div>

    <DetectAccountDialog
      :show="showPicker"
      :title="t('detect.baselinePickAccount')"
      :accounts="accounts"
      :accounts-loading="accountsLoading"
      :accounts-error="accountsError"
      :selected-ids="selectedIds"
      mode="account"
      :multiple="false"
      @update:selected-ids="onPickIds"
      @close="showPicker = false"
    />

    <BaseDialog
      :show="showExchange"
      :title="t('detect.baselineViewExchange')"
      width="extra-wide"
      @close="showExchange = false"
    >
      <DetectExchangeList v-if="baseline?.exchange" :exchanges="[baseline.exchange]" />
      <p v-else class="text-xs text-gray-500">{{ t('detect.baselineNoExchange') }}</p>
    </BaseDialog>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import DetectAccountDialog from '@/components/detect/DetectAccountDialog.vue'
import DetectExchangeList from '@/components/detect/DetectExchangeList.vue'
import { sameBaselineModel } from '@/utils/detectBaseline'
import type { DetectAccount, DetectBaseline } from '@/types/detect'

const props = defineProps<{
  accounts: DetectAccount[]
  accountsLoading: boolean
  accountsError: string
  baseline: DetectBaseline | null
  loading: boolean
  testModel: string
}>()

const emit = defineEmits<{
  (e: 'generate', accountId: number): void
}>()

const { t } = useI18n()
const selectedAccountId = ref(0)
const showExchange = ref(false)
const showPicker = ref(false)

const selectedAccount = computed(() => props.accounts.find((a) => a.account_id === selectedAccountId.value) ?? null)
const selectedIds = computed(() => (selectedAccountId.value > 0 ? [selectedAccountId.value] : []))
const pickerLabel = computed(() =>
  selectedAccount.value ? selectedAccount.value.account_name : t('detect.baselinePickAccount')
)
const canGenerate = computed(() => selectedAccountId.value > 0 && !props.loading && props.accounts.length > 0)

function onPickIds(ids: number[]): void {
  selectedAccountId.value = ids[0] ?? 0
}

const modelMismatch = computed(() => {
  if (!props.baseline) return false
  return !sameBaselineModel(props.baseline.model, props.testModel)
})

const baselineError = computed(() => {
  const raw = (props.baseline?.error || '').replace(/<[^>]+>/g, ' ').replace(/\s+/g, ' ').trim()
  if (!raw) return t('detect.baselineFailed')
  return raw.length > 180 ? `${raw.slice(0, 180)}…` : raw
})

watch(
  () => props.baseline?.account_id,
  (id) => {
    if (id && id > 0) selectedAccountId.value = id
  },
  { immediate: true }
)
</script>
