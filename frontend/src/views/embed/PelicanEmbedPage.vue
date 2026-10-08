<template>
  <main class="min-h-dvh bg-gray-50 px-3 py-5 text-gray-800 dark:bg-dark-950 dark:text-dark-100 sm:px-6">
    <div class="mx-auto max-w-screen-2xl space-y-6">
      <header><h1 class="text-xl font-semibold">{{ t('pelican.title') }}</h1><p class="mt-2 text-sm text-gray-500 dark:text-dark-400">{{ t('pelican.intro') }}</p></header>
      <div v-if="error" class="card p-8 text-center"><p role="alert">{{ t(error) }}</p><p class="mt-2 text-sm text-gray-500">{{ t('plaza.errors.openFromMenu') }}</p></div>
      <p v-else-if="!ready" class="p-12 text-center">{{ t('common.loading') }}</p>
      <PelicanGallery v-else />
    </div>
  </main>
</template>
<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { embedPelicanAPI } from '@/api/pelican'
import { applyTheme, queryString, resolveLocale, stripTokenFromUrl } from '@/utils/embedQuery'
import PelicanGallery from '@/components/pelican/PelicanGallery.vue'
const route = useRoute(), { t, locale } = useI18n()
const ready = ref(false), error = ref('')
onMounted(async () => {
  applyTheme(queryString(route.query.theme)); locale.value = resolveLocale(queryString(route.query.lang))
  const token = queryString(route.query.token), userID = queryString(route.query.user_id)
  if (token) stripTokenFromUrl()
  if (!token) { error.value = 'plaza.errors.missingParams'; return }
  try { await embedPelicanAPI.createSession(token, userID); ready.value = true }
  catch (e) { error.value = e instanceof Error ? e.message : 'plaza.errors.loadFailed' }
})
</script>
