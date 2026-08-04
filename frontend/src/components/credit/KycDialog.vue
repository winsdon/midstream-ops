<template>
  <BaseDialog
    :show="show"
    :title="customer ? `${customerLabel} · ${t('credit.kyc.title')}` : t('credit.kyc.title')"
    width="extra-wide"
    @close="emit('close')"
  >
    <div v-if="loading" class="py-10 text-center text-sm text-gray-500">{{ t('common.loading') }}</div>

    <div v-else class="space-y-4">
      <!-- 状态与审核轨迹：审核人先看结论，再决定要不要往下翻 -->
      <div class="flex flex-wrap items-center gap-x-4 gap-y-2 rounded-lg bg-gray-50 p-3 dark:bg-dark-800/50">
        <span class="badge" :class="kycStatusClass(status)">{{ t(`credit.kyc.status.${status}`) }}</span>
        <span v-if="profile?.submitted_at" class="text-xs text-gray-500">
          {{ t('credit.kyc.submittedAt') }}: {{ fmtDateTime(profile.submitted_at) }}
        </span>
        <span v-if="profile?.reviewed_at" class="text-xs text-gray-500">
          {{ t('credit.kyc.reviewedAt') }}: {{ fmtDateTime(profile.reviewed_at) }}
          <template v-if="profile.reviewed_by">（{{ profile.reviewed_by }}）</template>
        </span>
      </div>

      <p
        v-if="profile?.review_note"
        class="rounded-lg px-3 py-2 text-sm"
        :class="status === 'rejected'
          ? 'bg-red-50 text-red-600 dark:bg-red-900/30 dark:text-red-400'
          : 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/20 dark:text-emerald-400'"
      >
        {{ t('credit.kyc.reviewNote') }}: {{ profile.review_note }}
      </p>

      <KycForm v-model="form" />

      <p v-if="formError" class="rounded-lg bg-red-50 px-3 py-2 text-sm text-red-600 dark:bg-red-900/30 dark:text-red-400">
        {{ formError }}
      </p>

      <!-- 审核区：仅 pending 可审，避免把已结案的档案重复改判 -->
      <div v-if="status === 'pending'" class="space-y-2 rounded-lg border border-amber-200 p-3 dark:border-amber-900/40">
        <h4 class="text-sm font-semibold text-gray-700 dark:text-dark-200">{{ t('credit.kyc.reviewTitle') }}</h4>
        <input v-model.trim="reviewNote" class="input" :placeholder="t('credit.kyc.reviewNotePlaceholder')" />
        <div class="flex justify-end gap-2">
          <button class="btn btn-secondary text-sm" :disabled="busy" @click="doReview('rejected')">
            {{ t('credit.kyc.reject') }}
          </button>
          <button class="btn btn-primary text-sm" :disabled="busy" @click="doReview('approved')">
            {{ t('credit.kyc.approve') }}
          </button>
        </div>
      </div>
    </div>

    <template #footer>
      <button class="btn btn-secondary" @click="emit('close')">{{ t('common.cancel') }}</button>
      <button class="btn btn-secondary" :disabled="busy || loading" @click="save(false)">
        {{ t('credit.kyc.saveDraft') }}
      </button>
      <button class="btn btn-primary" :disabled="busy || loading" @click="save(true)">
        {{ busy ? t('common.loading') : t('credit.kyc.submit') }}
      </button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
/**
 * KYC 管理端弹窗：查看、代客录入、审核三合一。
 *
 * 【含完整 PII】表单里是解密后的身份证号、银行账号。组件卸载时主动清空，
 * 不要把 form 提升到 store —— 那会让 PII 在整个 SPA 生命周期里驻留内存。
 */
import { ref, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { creditApi } from '@/api/credit'
import { errorMessage } from '@/api/client'
import { fmtDateTime } from '@/utils/format'
import { emptyKycForm, toKycForm, missingRequired, kycStatusClass } from '@/utils/creditModel'
import { useAppStore } from '@/stores/app'
import BaseDialog from '@/components/common/BaseDialog.vue'
import KycForm from './KycForm.vue'
import type { CreditCustomer, KycFormData, KycProfile, KycStatus } from '@/types/credit'

const props = defineProps<{
  show: boolean
  customer: CreditCustomer | null
}>()

const emit = defineEmits<{ (e: 'close'): void }>()

const { t } = useI18n()
const app = useAppStore()

const profile = ref<KycProfile | null>(null)
const form = ref<KycFormData>(emptyKycForm())
const loading = ref(false)
const busy = ref(false)
const formError = ref('')
const reviewNote = ref('')

const status = computed<KycStatus>(() => profile.value?.status ?? 'draft')

const customerLabel = computed(() => {
  const c = props.customer
  return c ? c.display_name || c.sub2api_user_id : ''
})

async function load() {
  if (!props.customer) return
  loading.value = true
  try {
    const p = await creditApi.getKyc(props.customer.id)
    profile.value = p
    form.value = toKycForm(p)
  } catch (e) {
    app.showError(errorMessage(e))
    profile.value = null
    form.value = emptyKycForm()
  } finally {
    loading.value = false
  }
}

/** submit=true 时先做本地必填校验，省一次往返；后端仍会再校验一遍 */
async function save(submit: boolean) {
  if (!props.customer) return
  formError.value = ''
  if (submit) {
    const missing = missingRequired(form.value)
    if (missing.length) {
      formError.value = t('credit.kyc.missingRequired', { fields: missing.map((k) => t(k)).join('、') })
      return
    }
  }
  busy.value = true
  try {
    profile.value = await creditApi.saveKyc(props.customer.id, { ...form.value, submit })
    app.showSuccess(t(submit ? 'credit.kyc.submitted' : 'credit.kyc.draftSaved'))
  } catch (e) {
    formError.value = errorMessage(e)
  } finally {
    busy.value = false
  }
}

async function doReview(next: 'approved' | 'rejected') {
  if (!props.customer) return
  formError.value = ''
  // 驳回必须说明理由，否则客户无从修正
  if (next === 'rejected' && !reviewNote.value) {
    formError.value = t('credit.kyc.rejectNeedsNote')
    return
  }
  busy.value = true
  try {
    profile.value = await creditApi.reviewKyc(props.customer.id, { status: next, note: reviewNote.value })
    reviewNote.value = ''
    app.showSuccess(t(next === 'approved' ? 'credit.kyc.approved' : 'credit.kyc.rejected'))
  } catch (e) {
    formError.value = errorMessage(e)
  } finally {
    busy.value = false
  }
}

// 关闭时清空表单：PII 不在内存里多留一秒，也避免下次打开闪现上一个客户的资料
watch(
  () => [props.show, props.customer?.id],
  ([show]) => {
    profile.value = null
    form.value = emptyKycForm()
    formError.value = ''
    reviewNote.value = ''
    if (show) load()
  }
)
</script>
