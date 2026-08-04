<template>
  <Teleport to="body">
    <div
      class="pointer-events-none fixed right-4 top-4 z-[9999] space-y-3"
      aria-live="polite"
      aria-atomic="true"
    >
      <TransitionGroup
        enter-active-class="transition ease-out duration-300"
        enter-from-class="opacity-0 translate-x-full"
        enter-to-class="opacity-100 translate-x-0"
        leave-active-class="transition ease-in duration-200"
        leave-from-class="opacity-100 translate-x-0"
        leave-to-class="opacity-0 translate-x-full"
      >
        <div
          v-for="toast in toasts"
          :key="toast.id"
          :class="[
            'pointer-events-auto min-w-[320px] max-w-md overflow-hidden rounded-lg shadow-lg',
            'bg-white dark:bg-dark-800',
            'border-l-4',
            STYLES[toast.type].border
          ]"
        >
          <div class="p-4">
            <div class="flex items-start gap-3">
              <!-- 类型图标 -->
              <div class="mt-0.5 flex-shrink-0">
                <Icon
                  :name="ICON_NAME[toast.type]"
                  size="md"
                  :class="STYLES[toast.type].icon"
                  aria-hidden="true"
                />
              </div>

              <!-- 正文 -->
              <div class="min-w-0 flex-1">
                <p class="text-sm leading-relaxed text-gray-900 dark:text-white">
                  {{ toast.message }}
                </p>
              </div>

              <!-- 关闭 -->
              <button
                @click="appStore.hideToast(toast.id)"
                class="-m-1 flex-shrink-0 rounded p-1 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-600 dark:text-gray-500 dark:hover:bg-dark-700 dark:hover:text-gray-300"
                aria-label="关闭通知"
              >
                <Icon name="x" size="sm" />
              </button>
            </div>
          </div>

          <!-- 倒计时进度条 -->
          <div v-if="toast.duration" class="h-1 bg-gray-100 dark:bg-dark-700">
            <div
              :class="['toast-progress h-full', STYLES[toast.type].progress]"
              :style="{ animationDuration: `${toast.duration}ms` }"
            ></div>
          </div>
        </div>
      </TransitionGroup>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { storeToRefs } from 'pinia'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore, type ToastType } from '@/stores/app'

const appStore = useAppStore()
const { toasts } = storeToRefs(appStore)

const ICON_NAME: Record<ToastType, 'checkCircle' | 'xCircle' | 'exclamationTriangle' | 'infoCircle'> = {
  success: 'checkCircle',
  error: 'xCircle',
  warning: 'exclamationTriangle',
  info: 'infoCircle'
}

// 必须写完整字面量：Tailwind 扫描源码文本提取类名，`text-${x}` 这类拼接会被漏掉
const STYLES: Record<ToastType, { icon: string; border: string; progress: string }> = {
  success: { icon: 'text-green-500', border: 'border-green-500', progress: 'bg-green-500' },
  error: { icon: 'text-red-500', border: 'border-red-500', progress: 'bg-red-500' },
  warning: { icon: 'text-yellow-500', border: 'border-yellow-500', progress: 'bg-yellow-500' },
  info: { icon: 'text-blue-500', border: 'border-blue-500', progress: 'bg-blue-500' }
}
</script>

<style scoped>
.toast-progress {
  width: 100%;
  animation-name: toast-progress-shrink;
  animation-timing-function: linear;
  animation-fill-mode: forwards;
}

@keyframes toast-progress-shrink {
  from {
    width: 100%;
  }
  to {
    width: 0%;
  }
}
</style>
