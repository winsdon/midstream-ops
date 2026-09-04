<template>
  <div class="card">
    <div class="card-header">
      <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('detect.title') }}</h2>
      <p class="mt-1 text-xs leading-relaxed text-gray-500 dark:text-dark-400">{{ t('detect.intro') }}</p>
    </div>

    <div class="card-body space-y-3">
      <button type="button" class="btn btn-secondary w-full justify-between" @click="showPicker = true">
        <span class="truncate text-left">
          {{ pickerLabel }}
        </span>
        <Icon name="chevronRight" size="sm" />
      </button>

      <div v-if="selectedAccounts.length" class="flex flex-wrap gap-1.5">
        <span
          v-for="acc in selectedAccounts"
          :key="acc.account_id"
          class="inline-flex max-w-full items-center gap-1 rounded-lg bg-primary-50 px-2 py-0.5 text-xs text-primary-700 dark:bg-primary-900/30 dark:text-primary-300"
        >
          <span class="truncate">{{ acc.account_name }}</span>
          <button type="button" class="shrink-0" :title="t('detect.removeTarget')" @click="removeAccount(acc.account_id)">
            <Icon name="x" size="sm" />
          </button>
        </span>
      </div>
      <p v-else-if="mode === 'manual'" class="text-xs text-gray-500 dark:text-dark-400">
        {{ t('detect.selectedTargets', { n: targets.length }) }}
      </p>

      <details class="rounded-xl border border-gray-100 px-3 py-2 dark:border-dark-800">
        <summary class="cursor-pointer text-xs text-gray-500 dark:text-dark-400">
          {{ t('detect.model') }} / {{ t('detect.authMode') }}
        </summary>
        <div class="mt-3 grid gap-3 sm:grid-cols-2">
          <div>
            <span class="input-label">{{ t('detect.model') }}</span>
            <input
              v-model="modelInput"
              type="text"
              class="input"
              list="detect-model-options"
              :placeholder="t('detect.modelPlaceholder')"
            />
            <datalist id="detect-model-options">
              <option v-for="m in MODEL_OPTIONS" :key="m" :value="m" />
            </datalist>
            <div class="mt-2 flex flex-wrap gap-1.5">
              <button
                v-for="m in MODEL_OPTIONS"
                :key="m"
                type="button"
                class="rounded-lg px-2 py-1 text-xs transition-colors"
                :class="
                  modelInput === m
                    ? 'bg-primary-100 text-primary-700 dark:bg-primary-900/40 dark:text-primary-300'
                    : 'bg-gray-100 text-gray-600 hover:bg-gray-200 dark:bg-dark-800 dark:text-dark-300 dark:hover:bg-dark-700'
                "
                @click="modelInput = m"
              >
                {{ m }}
              </button>
            </div>
            <span class="input-hint">{{ t('detect.modelHint') }}</span>
          </div>
          <div>
            <span class="input-label">{{ t('detect.authMode') }}</span>
            <Select v-model="authModeInput" :options="authOptions" />
            <details class="mt-3">
              <summary class="cursor-pointer text-xs text-gray-500 dark:text-dark-400">
                {{ t('detect.advanced') }}
              </summary>
              <div class="mt-3 space-y-3">
                <label class="block">
                  <span class="input-label">{{ t('detect.timeoutMs') }}</span>
                  <input v-model.number="timeoutInput" type="number" min="5000" max="300000" step="1000" class="input" />
                </label>
                <label class="block">
                  <span class="input-label">{{ t('detect.extraHeaders') }}</span>
                  <textarea
                    v-model="extraHeadersInput"
                    rows="2"
                    class="input font-mono text-xs"
                    placeholder='{"anthropic-beta":"…"}'
                  ></textarea>
                  <span v-if="extraHeadersError" class="input-error-text">{{ t('detect.extraHeadersInvalid') }}</span>
                </label>
              </div>
            </details>
          </div>
        </div>
      </details>

      <p class="flex items-start gap-2 rounded-xl bg-emerald-50 px-3 py-2 text-xs text-emerald-700 dark:bg-emerald-900/20 dark:text-emerald-400">
        <Icon name="shield" size="sm" class="mt-0.5 flex-shrink-0" />
        <span>{{ t('detect.keyNotStored') }}</span>
      </p>
    </div>

    <DetectAccountDialog
      :show="showPicker"
      :accounts="accounts"
      :accounts-loading="accountsLoading"
      :accounts-error="accountsError"
      :selected-ids="selectedIdList"
      :mode="mode"
      @update:mode="mode = $event"
      @update:selected-ids="setSelectedIds"
      @close="showPicker = false"
    >
      <template #manual>
        <div class="space-y-3">
          <div
            v-for="(target, idx) in manualTargets"
            :key="target.uid"
            class="space-y-3 rounded-xl border border-gray-200 p-3 dark:border-dark-700"
          >
            <div class="flex items-center justify-between gap-2">
              <input
                v-model="target.name"
                type="text"
                class="input max-w-xs"
                :placeholder="t('detect.targetNamePlaceholder')"
              />
              <button
                v-if="manualTargets.length > 1"
                type="button"
                class="btn btn-ghost btn-sm"
                :title="t('detect.removeTarget')"
                @click="removeManual(idx)"
              >
                <Icon name="x" size="sm" />
              </button>
            </div>
            <div class="grid gap-3 sm:grid-cols-2">
              <label class="block">
                <span class="input-label">{{ t('detect.baseUrl') }}</span>
                <input
                  v-model="target.baseUrl"
                  type="url"
                  class="input"
                  :placeholder="t('detect.baseUrlPlaceholder')"
                />
                <span class="input-hint">{{ t('detect.baseUrlHint') }}</span>
              </label>
              <label class="block">
                <span class="input-label">{{ t('detect.apiKey') }}</span>
                <div class="relative">
                  <input
                    v-model="target.apiKey"
                    :type="target.reveal ? 'text' : 'password'"
                    class="input pr-16"
                    autocomplete="new-password"
                    :placeholder="t('detect.apiKeyPlaceholder')"
                  />
                  <button
                    type="button"
                    class="absolute right-2 top-1/2 -translate-y-1/2 rounded-lg px-2 py-1 text-xs text-gray-500 hover:bg-gray-100 dark:text-dark-400 dark:hover:bg-dark-700"
                    @click="target.reveal = !target.reveal"
                  >
                    {{ target.reveal ? t('detect.hideKey') : t('detect.showKey') }}
                  </button>
                </div>
              </label>
            </div>
          </div>
          <button type="button" class="btn btn-secondary btn-sm" @click="addManual">
            <Icon name="plus" size="sm" />
            {{ t('detect.addManual') }}
          </button>
        </div>
      </template>
    </DetectAccountDialog>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import Select from '@/components/common/Select.vue'
import DetectAccountDialog from '@/components/detect/DetectAccountDialog.vue'
import { MAX_DETECT_TARGETS } from '@/utils/detectModel'
import type { DetectAccount, DetectTargetInput } from '@/types/detect'

const { t } = useI18n()

const props = defineProps<{
  accounts: DetectAccount[]
  accountsLoading: boolean
  accountsError: string
}>()

const emit = defineEmits<{
  (
    e: 'change',
    payload: {
      targets: DetectTargetInput[]
      model: string
      authMode: string
      timeoutMs: number
      extraHeaders: Record<string, string>
    }
  ): void
}>()

const MODEL_OPTIONS = [
  'claude-opus-5',
  'claude-fable-5',
  'claude-opus-4-8',
  'claude-sonnet-5',
  'claude-sonnet-4-6',
  'claude-haiku-4-5'
]

const showPicker = ref(false)
const mode = ref<'account' | 'manual'>('account')
const selectedIds = ref<Set<number>>(new Set())
const modelInput = ref('claude-opus-5')
const authModeInput = ref('both')
const timeoutInput = ref(90000)
const extraHeadersInput = ref('')

interface ManualTarget {
  uid: number
  name: string
  baseUrl: string
  apiKey: string
  reveal: boolean
}

let manualSeq = 0
function blankManual(): ManualTarget {
  return { uid: ++manualSeq, name: '', baseUrl: '', apiKey: '', reveal: false }
}
const manualTargets = ref<ManualTarget[]>([blankManual()])

const authOptions = computed(() => [
  { value: 'both', label: t('detect.authBoth') },
  { value: 'x-api-key', label: 'x-api-key' },
  { value: 'bearer', label: 'Bearer Token' }
])

const extraHeadersError = ref(false)

const extraHeaders = computed<Record<string, string>>(() => {
  const text = extraHeadersInput.value.trim()
  if (!text) {
    extraHeadersError.value = false
    return {}
  }
  try {
    const parsed = JSON.parse(text)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      extraHeadersError.value = true
      return {}
    }
    extraHeadersError.value = false
    return parsed as Record<string, string>
  } catch {
    extraHeadersError.value = true
    return {}
  }
})

const selectedIdList = computed(() => [...selectedIds.value])

const selectedAccounts = computed(() =>
  props.accounts.filter((a) => selectedIds.value.has(a.account_id))
)

const pickerLabel = computed(() => {
  if (mode.value === 'manual') return t('detect.tabManual')
  if (!selectedAccounts.value.length) return t('detect.pickAccounts')
  return t('detect.selectedTargets', { n: selectedAccounts.value.length })
})

function setSelectedIds(ids: number[]): void {
  selectedIds.value = new Set(ids.slice(0, MAX_DETECT_TARGETS))
}

function removeAccount(id: number): void {
  const next = new Set(selectedIds.value)
  next.delete(id)
  selectedIds.value = next
}

function addManual(): void {
  manualTargets.value = [...manualTargets.value, blankManual()]
}

function removeManual(idx: number): void {
  manualTargets.value = manualTargets.value.filter((_, i) => i !== idx)
}

const targets = computed<DetectTargetInput[]>(() => {
  if (mode.value === 'account') {
    return selectedAccounts.value.map((a) => ({ account_id: a.account_id }))
  }
  return manualTargets.value
    .filter((m) => m.baseUrl.trim() && m.apiKey.trim())
    .map((m) => ({ name: m.name.trim(), base_url: m.baseUrl.trim(), api_key: m.apiKey.trim() }))
})

watch(
  [targets, modelInput, authModeInput, timeoutInput, extraHeaders],
  () => {
    emit('change', {
      targets: targets.value,
      model: modelInput.value.trim(),
      authMode: authModeInput.value,
      timeoutMs: timeoutInput.value,
      extraHeaders: extraHeaders.value
    })
  },
  { immediate: true, deep: true }
)
</script>
