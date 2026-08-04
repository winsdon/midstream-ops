<template>
  <div class="min-h-screen bg-gray-50 dark:bg-dark-950">
    <!-- 背景装饰 -->
    <div class="pointer-events-none fixed inset-0 bg-mesh-gradient"></div>

    <AppSidebar />

    <div
      class="relative min-h-screen transition-all duration-300"
      :class="collapsed ? 'lg:ml-[72px]' : 'lg:ml-64'"
    >
      <AppHeader />

      <main class="p-4 md:p-6 lg:p-8">
        <slot />
      </main>
    </div>
  </div>
</template>

<script setup lang="ts">
import { storeToRefs } from 'pinia'
import { useAppStore } from '@/stores/app'
import AppSidebar from './AppSidebar.vue'
import AppHeader from './AppHeader.vue'

const { sidebarCollapsed: collapsed } = storeToRefs(useAppStore())
</script>
