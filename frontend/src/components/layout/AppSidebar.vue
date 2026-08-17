<template>
  <aside
    class="sidebar"
    :class="[collapsed ? 'w-[72px]' : 'w-64', { '-translate-x-full lg:translate-x-0': !mobileOpen }]"
  >
    <!-- 品牌区 -->
    <div class="sidebar-header" :class="{ 'sidebar-header-collapsed': collapsed }">
      <div
        class="sidebar-logo flex h-9 w-9 items-center justify-center rounded-xl bg-gradient-primary text-lg font-bold text-white shadow-glow"
      >
        M
      </div>
      <div
        class="sidebar-brand"
        :class="{ 'sidebar-brand-collapsed': collapsed }"
        :aria-hidden="collapsed"
      >
        <span class="sidebar-brand-title text-base font-bold text-gray-900 dark:text-white">
          {{ t('app.title') }}
        </span>
      </div>
    </div>

    <!-- 导航 -->
    <nav class="sidebar-nav scrollbar-hide">
      <div class="sidebar-section">
        <router-link
          v-for="item in navItems"
          :key="item.name"
          :to="{ name: item.name }"
          class="sidebar-link mb-1"
          :class="{ 'sidebar-link-active': isActive(item.name), 'sidebar-link-collapsed': collapsed }"
          :title="collapsed ? t(item.label) : undefined"
          @click="closeMobile"
        >
          <Icon :name="item.icon" size="md" class="flex-shrink-0" />
          <span class="sidebar-label" :class="{ 'sidebar-label-collapsed': collapsed }" :aria-hidden="collapsed">
            {{ t(item.label) }}
          </span>
        </router-link>
      </div>
    </nav>

    <!-- 底部：主题 / 折叠 / 登出 -->
    <div class="mt-auto border-t border-gray-100 p-3 dark:border-dark-800">
      <button
        type="button"
        class="sidebar-link mb-1 w-full"
        :class="{ 'sidebar-link-collapsed': collapsed }"
        :title="collapsed ? themeLabel : undefined"
        @click="app.toggleTheme()"
      >
        <Icon :name="isDark ? 'sun' : 'moon'" size="md" class="flex-shrink-0" :class="{ 'text-amber-500': isDark }" />
        <span class="sidebar-label" :class="{ 'sidebar-label-collapsed': collapsed }" :aria-hidden="collapsed">
          {{ themeLabel }}
        </span>
      </button>

      <button
        type="button"
        class="sidebar-link mb-1 w-full"
        :class="{ 'sidebar-link-collapsed': collapsed }"
        :title="collapsed ? t('nav.expand') : t('nav.collapse')"
        @click="app.toggleSidebar()"
      >
        <Icon :name="collapsed ? 'chevronRight' : 'chevronLeft'" size="md" class="flex-shrink-0" />
        <span class="sidebar-label" :class="{ 'sidebar-label-collapsed': collapsed }" :aria-hidden="collapsed">
          {{ t('nav.collapse') }}
        </span>
      </button>

      <button
        type="button"
        class="sidebar-link w-full text-red-600 hover:bg-red-50 hover:text-red-700 dark:text-red-400 dark:hover:bg-red-900/20 dark:hover:text-red-300"
        :class="{ 'sidebar-link-collapsed': collapsed }"
        :title="collapsed ? t('nav.logout') : undefined"
        @click="onLogout"
      >
        <Icon name="login" size="md" class="flex-shrink-0" />
        <span class="sidebar-label" :class="{ 'sidebar-label-collapsed': collapsed }" :aria-hidden="collapsed">
          {{ t('nav.logout') }}
        </span>
      </button>
    </div>
  </aside>

  <!-- 移动端遮罩 -->
  <transition name="fade">
    <div v-if="mobileOpen" class="fixed inset-0 z-30 bg-black/50 lg:hidden" @click="app.setMobileOpen(false)"></div>
  </transition>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { storeToRefs } from 'pinia'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import Icon from '@/components/icons/Icon.vue'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const app = useAppStore()
const auth = useAuthStore()

const { sidebarCollapsed: collapsed, mobileOpen, isDark } = storeToRefs(app)

const navItems = [
  { name: 'dashboard', label: 'nav.dashboard', icon: 'chart' },
  { name: 'providers', label: 'nav.providers', icon: 'server' },
  { name: 'stats', label: 'nav.stats', icon: 'dollar' },
  { name: 'rates', label: 'nav.rates', icon: 'sort' },
  { name: 'pricing', label: 'nav.pricing', icon: 'arrowsUpDown' },
  { name: 'stability', label: 'nav.stability', icon: 'trendingUp' },
  { name: 'credit', label: 'nav.credit', icon: 'creditCard' },
  { name: 'embed-hub', label: 'nav.embedHub', icon: 'grid' },
  { name: 'settings', label: 'nav.settings', icon: 'cog' }
] as const

const themeLabel = computed(() => (isDark.value ? t('nav.lightMode') : t('nav.darkMode')))

// 仪表盘挂在根路径 '/'，无法用 route.name 前缀匹配，统一取路径首段判断
const currentRoot = computed(() => route.path.split('/')[1] || 'dashboard')
function isActive(name: string): boolean {
  return currentRoot.value === name
}

// 移动端点击导航后收起抽屉；延迟让路由过渡先跑起来，避免遮罩瞬间闪动
function closeMobile(): void {
  if (mobileOpen.value) {
    setTimeout(() => app.setMobileOpen(false), 150)
  }
}

function onLogout(): void {
  auth.logout()
  router.push({ name: 'login' })
}
</script>

<style scoped>
.sidebar-logo {
  flex: 0 0 2.25rem;
  min-width: 2.25rem;
}

.sidebar-header-collapsed {
  gap: 0;
  padding-left: 1.125rem;
  padding-right: 1.125rem;
}

/* 折叠时用 max-width 收缩而非 display:none，保证文字有平滑的收放动画 */
.sidebar-brand {
  min-width: 0;
  flex: 1 1 auto;
  max-width: 12rem;
  white-space: nowrap;
  transition:
    max-width 0.22s ease,
    opacity 0.14s ease,
    transform 0.14s ease;
}

.sidebar-brand-collapsed {
  max-width: 0;
  overflow: hidden;
  opacity: 0;
  transform: translateX(-4px);
  pointer-events: none;
}

.sidebar-brand-title {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.sidebar-link-collapsed {
  gap: 0;
  padding-left: 0.875rem;
  padding-right: 0.875rem;
}

.sidebar-label {
  display: block;
  min-width: 0;
  max-width: 12rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  transition:
    max-width 0.2s ease,
    opacity 0.12s ease,
    transform 0.12s ease;
}

.sidebar-label-collapsed {
  max-width: 0;
  opacity: 0;
  transform: translateX(-4px);
  pointer-events: none;
}

.fade-enter-active,
.fade-leave-active {
  transition: opacity 0.2s ease;
}

.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}
</style>
