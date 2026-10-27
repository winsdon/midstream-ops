<template>
  <!-- 本站连接：自动建号经由本站 sub2api admin API，凭据在此配置并当场校验 -->
  <div class="card flex flex-wrap items-center justify-between gap-4 p-5">
    <div class="flex items-start gap-3">
      <div class="mt-0.5 flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-xl bg-primary-50 text-primary-600 dark:bg-primary-900/30 dark:text-primary-300">
        <Icon name="server" size="md" />
      </div>
      <div>
        <p class="flex items-center gap-2 font-medium text-gray-900 dark:text-white">
          {{ t('settings.selfTitle') }}
          <span class="inline-flex items-center gap-1 text-xs font-normal" :class="info?.configured ? 'text-emerald-600 dark:text-emerald-400' : 'text-gray-400'">
            <span class="inline-block h-2 w-2 rounded-full" :class="info?.configured ? 'bg-emerald-500' : 'bg-gray-300 dark:bg-dark-600'"></span>
            {{ info?.configured ? t('settings.selfConnected') : t('settings.selfNotConfigured') }}
          </span>
        </p>
        <p v-if="info?.configured" class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">{{ info.base_url }} · {{ info.login_email }}</p>
        <p v-else class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">{{ t('settings.selfHint') }}</p>
      </div>
    </div>
    <button class="btn btn-secondary text-sm" @click="open">{{ t('settings.configureSelf') }}</button>

    <BaseDialog :show="showDialog" :title="t('settings.selfTitle')" @close="showDialog = false">
      <form id="self-form" class="space-y-4" @submit.prevent="save">
        <p class="rounded-lg bg-amber-50 px-3 py-2 text-xs text-amber-700 dark:bg-amber-900/20 dark:text-amber-300">
          ⚠ {{ t('settings.selfWarning') }}
        </p>
        <div>
          <label class="input-label">{{ t('provider.baseUrl') }}</label>
          <input v-model.trim="form.base_url" class="input" required placeholder="https://my-sub2api.com" />
        </div>
        <div class="grid grid-cols-2 gap-4">
          <div>
            <label class="input-label">{{ t('provider.loginEmail') }}</label>
            <input v-model.trim="form.email" class="input" required autocomplete="off" />
          </div>
          <div>
            <label class="input-label">{{ t('provider.loginPassword') }}</label>
            <input v-model="form.password" type="password" class="input"
              :placeholder="info?.configured ? t('provider.passwordPlaceholder') : ''" autocomplete="new-password" />
          </div>
        </div>
        <p v-if="error" class="rounded-lg bg-red-50 px-3 py-2 text-sm text-red-600 dark:bg-red-900/30 dark:text-red-400">{{ error }}</p>
      </form>
      <template #footer>
        <button type="button" class="btn btn-secondary" @click="showDialog = false">{{ t('common.cancel') }}</button>
        <button type="submit" form="self-form" class="btn btn-primary" :disabled="saving">
          {{ saving ? t('common.loading') : t('settings.saveAndVerify') }}
        </button>
      </template>
    </BaseDialog>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { selfApi } from '@/api'
import { errorMessage } from '@/api/client'
import { useAppStore } from '@/stores/app'
import type { SelfInfo } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'

const { t } = useI18n()
const app = useAppStore()

const info = ref<SelfInfo | null>(null)
const showDialog = ref(false)
const saving = ref(false)
const error = ref('')
const form = ref({ base_url: '', email: '', password: '' })

async function load() {
  try {
    info.value = await selfApi.get()
  } catch {
    info.value = null
  }
}

function open() {
  form.value = { base_url: info.value?.base_url || '', email: info.value?.login_email || '', password: '' }
  error.value = ''
  showDialog.value = true
}

async function save() {
  saving.value = true
  error.value = ''
  try {
    // 密码留空 = 不修改（后端 nil 语义）
    const res = await selfApi.save(form.value.base_url, form.value.email, form.value.password || null)
    if (!res.ok) {
      error.value = res.error || t('common.failed')
      return
    }
    showDialog.value = false
    app.showSuccess(t('settings.selfSaved'))
    await load()
  } catch (e) {
    error.value = errorMessage(e)
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>
