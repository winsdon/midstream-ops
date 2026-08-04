<template>
  <div class="flex min-h-screen items-center justify-center bg-gradient-to-br from-primary-50 via-gray-50 to-primary-100 px-4 dark:from-dark-950 dark:via-dark-900 dark:to-dark-950">
    <div class="w-full max-w-md animate-slide-up">
      <div class="card p-8">
        <div class="mb-8 text-center">
          <div class="mx-auto mb-4 flex h-14 w-14 items-center justify-center rounded-2xl bg-primary-600 text-2xl font-bold text-white shadow-lg">
            M
          </div>
          <h1 class="text-xl font-bold text-gray-900 dark:text-white">{{ t('app.title') }}</h1>
          <p class="mt-1 text-sm text-gray-500">{{ t('app.subtitle') }}</p>
        </div>

        <form @submit.prevent="onSubmit" class="space-y-5">
          <div>
            <label class="input-label">{{ t('login.username') }}</label>
            <input v-model.trim="username" type="text" class="input" autocomplete="username" required autofocus />
          </div>
          <div>
            <label class="input-label">{{ t('login.password') }}</label>
            <input v-model="password" type="password" class="input" autocomplete="current-password" required />
          </div>

          <p v-if="error" class="rounded-lg bg-red-50 px-3 py-2 text-sm text-red-600 dark:bg-red-900/30 dark:text-red-400">
            {{ error }}
          </p>

          <button type="submit" class="btn btn-primary w-full !py-2.5" :disabled="loading">
            <span v-if="loading">{{ t('login.logging') }}</span>
            <span v-else>{{ t('login.submit') }}</span>
          </button>
        </form>
      </div>
      <p class="mt-6 text-center text-xs text-gray-400">Sub2API Account Monitor</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { errorMessage } from '@/api/client'

const { t } = useI18n()
const router = useRouter()
const route = useRoute()
const auth = useAuthStore()

const username = ref('')
const password = ref('')
const loading = ref(false)
const error = ref('')

async function onSubmit() {
  if (loading.value) return
  loading.value = true
  error.value = ''
  try {
    await auth.login(username.value, password.value)
    const redirect = (route.query.redirect as string) || '/'
    router.push(redirect)
  } catch (e) {
    error.value = errorMessage(e)
  } finally {
    loading.value = false
  }
}
</script>
