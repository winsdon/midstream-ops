/**
 * 全局 UI 状态 store
 * 管理侧边栏、主题、Toast 通知三块与业务无关的界面状态
 */

import { defineStore } from 'pinia'
import { ref, computed } from 'vue'

export type ToastType = 'success' | 'error' | 'info' | 'warning'

export interface Toast {
  id: string
  type: ToastType
  message: string
  duration?: number
  startTime?: number
}

/** 主题存储键。与 main.ts 的防闪烁逻辑共用，改名会让既有用户的主题偏好失效 */
const THEME_KEY = 'monitor_theme'
/** 侧边栏折叠状态存储键 */
const SIDEBAR_KEY = 'monitor_sidebar_collapsed'
const PRIVACY_KEY = 'monitor_privacy_mode'

export const useAppStore = defineStore('app', () => {
  // ==================== 侧边栏 ====================

  const sidebarCollapsed = ref<boolean>(localStorage.getItem(SIDEBAR_KEY) === '1')
  const mobileOpen = ref<boolean>(false)

  function setSidebarCollapsed(collapsed: boolean): void {
    sidebarCollapsed.value = collapsed
    localStorage.setItem(SIDEBAR_KEY, collapsed ? '1' : '0')
  }

  function toggleSidebar(): void {
    setSidebarCollapsed(!sidebarCollapsed.value)
  }

  function setMobileOpen(open: boolean): void {
    mobileOpen.value = open
  }

  function toggleMobileSidebar(): void {
    mobileOpen.value = !mobileOpen.value
  }

  // ==================== 主题 ====================

  // main.ts 在 mount 前已按 localStorage 打好 class，这里读取即为真实状态
  const isDark = ref<boolean>(document.documentElement.classList.contains('dark'))

  function setTheme(dark: boolean): void {
    isDark.value = dark
    document.documentElement.classList.toggle('dark', dark)
    localStorage.setItem(THEME_KEY, dark ? 'dark' : 'light')
  }

  function toggleTheme(): void {
    setTheme(!isDark.value)
  }

  const privacyMode = ref<boolean>(localStorage.getItem(PRIVACY_KEY) === '1')

  function setPrivacyMode(enabled: boolean): void {
    privacyMode.value = enabled
    localStorage.setItem(PRIVACY_KEY, enabled ? '1' : '0')
  }

  function togglePrivacyMode(): void {
    setPrivacyMode(!privacyMode.value)
  }

  // ==================== Toast ====================

  const toasts = ref<Toast[]>([])
  const hasActiveToasts = computed(() => toasts.value.length > 0)

  let toastIdCounter = 0

  function hideToast(id: string): void {
    const index = toasts.value.findIndex((t) => t.id === id)
    if (index !== -1) {
      toasts.value.splice(index, 1)
    }
  }

  /**
   * 弹出一条通知
   * @param duration 自动消失毫秒数，不传则常驻直到手动关闭
   * @returns toast id，可用于手动关闭
   */
  function showToast(type: ToastType, message: string, duration?: number): string {
    const id = `toast-${++toastIdCounter}`
    toasts.value.push({
      id,
      type,
      message,
      duration,
      startTime: duration !== undefined ? Date.now() : undefined
    })

    if (duration !== undefined) {
      setTimeout(() => hideToast(id), duration)
    }

    return id
  }

  function showSuccess(message: string, duration = 3000): string {
    return showToast('success', message, duration)
  }

  function showError(message: string, duration = 5000): string {
    return showToast('error', message, duration)
  }

  function showInfo(message: string, duration = 3000): string {
    return showToast('info', message, duration)
  }

  function showWarning(message: string, duration = 4000): string {
    return showToast('warning', message, duration)
  }

  function clearAllToasts(): void {
    toasts.value = []
  }

  return {
    // 侧边栏
    sidebarCollapsed,
    mobileOpen,
    setSidebarCollapsed,
    toggleSidebar,
    setMobileOpen,
    toggleMobileSidebar,

    // 主题
    isDark,
    setTheme,
    toggleTheme,

    privacyMode,
    setPrivacyMode,
    togglePrivacyMode,

    // Toast
    toasts,
    hasActiveToasts,
    showToast,
    showSuccess,
    showError,
    showInfo,
    showWarning,
    hideToast,
    clearAllToasts
  }
})
