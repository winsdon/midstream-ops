<template>
  <header class="glass sticky top-0 z-30 border-b border-gray-200/50 dark:border-dark-700/50">
    <div class="flex h-16 items-center justify-between px-4 md:px-6">
      <!-- 左侧：移动端菜单 + 页面标题 -->
      <div class="flex items-center gap-4">
        <button
          type="button"
          class="btn-ghost btn-icon lg:hidden"
          :aria-label="t('nav.menu')"
          @click="app.toggleMobileSidebar()"
        >
          <Icon name="menu" size="md" />
        </button>

        <div>
          <h1 class="text-lg font-semibold text-gray-900 dark:text-white">{{ pageTitle }}</h1>
          <p v-if="pageDescription" class="hidden text-xs text-gray-500 dark:text-dark-400 lg:block">
            {{ pageDescription }}
          </p>
        </div>
      </div>

      <!-- 右侧：语言切换 + 用户下拉 -->
      <div class="flex items-center gap-3">
        <LocaleSwitcher />
        <button
          type="button"
          class="btn-ghost btn-icon"
          :aria-label="privacyMode ? t('privacy.showValues') : t('privacy.hideValues')"
          :title="privacyMode ? t('privacy.showValues') : t('privacy.hideValues')"
          :aria-pressed="privacyMode"
          @click="app.togglePrivacyMode()"
        >
          <Icon :name="privacyMode ? 'eyeOff' : 'eye'" size="md" />
        </button>

        <div class="relative" ref="dropdownRef">
          <button
            type="button"
            class="flex items-center gap-2 rounded-xl p-1.5 transition-colors hover:bg-gray-100 dark:hover:bg-dark-800"
            :aria-label="auth.username"
            @click="dropdownOpen = !dropdownOpen"
          >
            <div
              class="flex h-8 w-8 items-center justify-center rounded-xl bg-gradient-to-br from-primary-500 to-primary-600 text-sm font-medium text-white shadow-sm"
            >
              {{ userInitials }}
            </div>
            <span class="hidden text-sm font-medium text-gray-900 dark:text-white md:block">
              {{ auth.username }}
            </span>
            <Icon name="chevronDown" size="sm" class="hidden text-gray-400 md:block" />
          </button>

          <transition name="dropdown">
            <div v-if="dropdownOpen" class="dropdown right-0 mt-2 w-48">
              <div class="border-b border-gray-100 px-4 py-3 dark:border-dark-700">
                <div class="text-sm font-medium text-gray-900 dark:text-white">{{ auth.username }}</div>
              </div>
              <div class="py-1">
                <button
                  type="button"
                  class="dropdown-item w-full text-red-600 hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-900/20"
                  @click="onLogout"
                >
                  <Icon name="login" size="sm" />
                  {{ t('nav.logout') }}
                </button>
              </div>
            </div>
          </transition>
        </div>
      </div>
    </div>
  </header>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import Icon from '@/components/icons/Icon.vue'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const app = useAppStore()
const auth = useAuthStore()
const privacyMode = computed(() => app.privacyMode)

const dropdownOpen = ref(false)
const dropdownRef = ref<HTMLElement | null>(null)

const userInitials = computed(() => auth.username.slice(0, 2).toUpperCase() || '?')

// 标题走 route.meta 的 i18n key，切语言时自动跟随
const pageTitle = computed(() => {
  const key = route.meta.titleKey as string | undefined
  return key ? t(key) : ''
})

const pageDescription = computed(() => {
  const key = route.meta.descriptionKey as string | undefined
  return key ? t(key) : ''
})

function onLogout(): void {
  dropdownOpen.value = false
  auth.logout()
  router.push({ name: 'login' })
}

function handleClickOutside(event: MouseEvent): void {
  if (dropdownRef.value && !dropdownRef.value.contains(event.target as Node)) {
    dropdownOpen.value = false
  }
}

onMounted(() => document.addEventListener('click', handleClickOutside))
onBeforeUnmount(() => document.removeEventListener('click', handleClickOutside))
</script>

<style scoped>
.dropdown-enter-active,
.dropdown-leave-active {
  transition: all 0.2s ease;
}

.dropdown-enter-from,
.dropdown-leave-to {
  opacity: 0;
  transform: scale(0.95) translateY(-4px);
}
</style>
