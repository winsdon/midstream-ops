<template>
  <main class="min-h-dvh w-full bg-gray-50 px-3 py-4 dark:bg-dark-950 sm:px-5 sm:py-6 lg:px-8">
    <!-- 参数缺失 / 会话失败 -->
    <div v-if="fatalError" class="mx-auto max-w-lg pt-16">
      <EmptyState icon="exclamationTriangle" :title="t(fatalError)" :description="t('plaza.errors.openFromMenu')" />
    </div>

    <!-- 首屏加载 -->
    <div v-else-if="loading" class="pt-24">
      <LoadingState :label="t('common.loading')" size="lg" />
    </div>

    <div v-else class="mx-auto max-w-3xl space-y-4">
      <header class="space-y-1">
        <div class="flex flex-wrap items-center gap-3">
          <h1 class="text-lg font-semibold text-gray-800 dark:text-dark-100">{{ t('credit.kyc.embed.title') }}</h1>
          <span class="badge" :class="kycStatusClass(status)">{{ t(`credit.kyc.status.${status}`) }}</span>
        </div>
        <p class="text-sm text-gray-500 dark:text-dark-400">{{ t('credit.kyc.embed.intro') }}</p>
      </header>

      <!-- 状态提示：客户最需要知道的是「现在轮到谁动」 -->
      <p v-if="statusHint" class="rounded-lg px-3 py-2 text-sm" :class="statusHintClass">
        {{ t(statusHint) }}
      </p>

      <!-- 审核意见：驳回时是客户唯一的修正依据，必须显眼 -->
      <p
        v-if="profile?.review_note"
        class="rounded-lg px-3 py-2 text-sm"
        :class="status === 'rejected'
          ? 'bg-red-50 text-red-600 dark:bg-red-900/30 dark:text-red-400'
          : 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/20 dark:text-emerald-400'"
      >
        {{ t('credit.kyc.reviewNote') }}: {{ profile.review_note }}
      </p>

      <div class="card p-4 sm:p-5">
        <KycForm v-model="form" :readonly="locked" />
      </div>

      <p v-if="formError" class="rounded-lg bg-red-50 px-3 py-2 text-sm text-red-600 dark:bg-red-900/30 dark:text-red-400">
        {{ formError }}
      </p>

      <div v-if="!locked" class="flex justify-end gap-2 pb-4">
        <button class="btn btn-secondary" :disabled="busy" @click="save(false)">
          {{ t('credit.kyc.saveDraft') }}
        </button>
        <button class="btn btn-primary" :disabled="busy" @click="save(true)">
          {{ busy ? t('common.loading') : t('credit.kyc.submit') }}
        </button>
      </div>
    </div>

    <Toast />
  </main>
</template>

<script setup lang="ts">
/**
 * KYC 自助嵌入页：客户在 sub2api 站内以 iframe 打开，自行填报实名资料。
 *
 * 【身份不在本页】客户身份全程由后端从会话上下文取。URL 上的 user_id 只在
 * 换会话时供后端与 token claims 比对，改了它换不出会话，改不出别人的档案。
 *
 * 【含完整 PII】表单里是解密后的身份证号、银行账号。不写 localStorage、
 * 不进 URL、不打日志 —— 页面关闭即随组件销毁。
 */
import { computed, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { createSession, fetchProfile, saveProfile } from '@/api/embedKyc'
import { applyTheme, queryString, resolveLocale, stripTokenFromUrl } from '@/utils/embedQuery'
import { emptyKycForm, toKycForm, missingRequired, kycStatusClass } from '@/utils/creditModel'
import Toast from '@/components/common/Toast.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import LoadingState from '@/components/common/LoadingState.vue'
import KycForm from '@/components/credit/KycForm.vue'
import { useAppStore } from '@/stores/app'
import type { CustomerKycProfile, KycFormData, KycStatus } from '@/types/credit'

const route = useRoute()
const { t, locale } = useI18n()
const app = useAppStore()

const loading = ref(true)
const busy = ref(false)
const fatalError = ref('')
const formError = ref('')
const profile = ref<CustomerKycProfile | null>(null)
const form = ref<KycFormData>(emptyKycForm())

const status = computed<KycStatus>(() => profile.value?.status ?? 'draft')

/**
 * 通过审核后锁定表单。与后端 SaveProfile 的 approved 拦截是同一条规则 ——
 * 这里只是提前告知，后端才是权威。
 */
const locked = computed(() => status.value === 'approved')

const statusHint = computed(() => {
  switch (status.value) {
    case 'pending':
      return 'credit.kyc.embed.pendingHint'
    case 'approved':
      return 'credit.kyc.embed.approvedHint'
    case 'rejected':
      return 'credit.kyc.embed.rejectedHint'
    default:
      return ''
  }
})

// 必须写完整类名字面量：Tailwind 扫的是源码文本，拼接出来的类名会被漏掉
const statusHintClass = computed(() => {
  switch (status.value) {
    case 'approved':
      return 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/20 dark:text-emerald-400'
    case 'rejected':
      return 'bg-red-50 text-red-600 dark:bg-red-900/30 dark:text-red-400'
    default:
      return 'bg-blue-50 text-blue-700 dark:bg-blue-900/20 dark:text-blue-400'
  }
})

/** submit=true 时先做本地必填校验，省一次往返；后端仍会再校验一遍 */
async function save(submit: boolean) {
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
    const saved = await saveProfile({ ...form.value, submit })
    profile.value = saved
    form.value = toKycForm(saved)
    app.showSuccess(t(submit ? 'credit.kyc.submitted' : 'credit.kyc.draftSaved'))
  } catch (e) {
    // 后端返回的 message 即 i18n key。
    formError.value = t(e instanceof Error ? e.message : 'plaza.errors.loadFailed')
  } finally {
    busy.value = false
  }
}

onMounted(async () => {
  // 主题与语言必须先于任何网络请求应用，保证错误态也是正确外观。
  applyTheme(queryString(route.query.theme))
  locale.value = resolveLocale(queryString(route.query.lang))

  const token = queryString(route.query.token)
  const userId = queryString(route.query.user_id)
  // 拿到 token 后立刻从地址栏抹掉：请求失败或用户分享/收藏地址都会泄露明文 token。
  if (token) stripTokenFromUrl()

  if (!token) {
    fatalError.value = 'plaza.errors.missingParams'
    loading.value = false
    return
  }

  try {
    await createSession(token, userId)
    const p = await fetchProfile()
    profile.value = p
    form.value = toKycForm(p)
  } catch (e) {
    fatalError.value = e instanceof Error ? e.message : 'plaza.errors.loadFailed'
  } finally {
    loading.value = false
  }
})
</script>
