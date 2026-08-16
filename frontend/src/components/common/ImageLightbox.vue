<template>
  <Teleport to="body">
    <Transition name="lightbox-fade">
      <div
        v-if="open"
        class="fixed inset-0 z-[100] flex items-center justify-center bg-black/85 p-4 backdrop-blur-sm"
        role="dialog"
        aria-modal="true"
        @click.self="close"
      >
        <!-- 关闭 -->
        <button
          type="button"
          class="lightbox-btn absolute right-4 top-4"
          :aria-label="t('common.close')"
          @click="close"
        >
          <Icon name="x" size="lg" />
        </button>

        <!-- 上一张 / 下一张：只有多张时才出现 -->
        <button
          v-if="images.length > 1"
          type="button"
          class="lightbox-btn absolute left-4 top-1/2 -translate-y-1/2"
          :aria-label="t('media.preview.prev')"
          @click.stop="step(-1)"
        >
          <Icon name="chevronLeft" size="lg" />
        </button>
        <button
          v-if="images.length > 1"
          type="button"
          class="lightbox-btn absolute right-4 top-1/2 -translate-y-1/2"
          :aria-label="t('media.preview.next')"
          @click.stop="step(1)"
        >
          <Icon name="chevronRight" size="lg" />
        </button>

        <figure class="flex max-h-full max-w-full flex-col items-center gap-3">
          <img
            :src="current"
            :alt="caption || t('media.preview.title')"
            class="max-h-[82vh] max-w-[92vw] rounded-xl object-contain shadow-2xl"
          />
          <figcaption
            v-if="caption || images.length > 1"
            class="flex max-w-[92vw] items-center gap-3 text-xs text-white/70"
          >
            <span v-if="images.length > 1" class="shrink-0 tabular-nums">
              {{ index + 1 }} / {{ images.length }}
            </span>
            <span v-if="caption" class="line-clamp-2">{{ caption }}</span>
          </figcaption>
        </figure>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
/**
 * 图片大图预览。
 *
 * 独立成通用组件而非塞进媒体页：它与业务无耦合，任何要看大图的地方都能用。
 *
 * 【为什么用 Teleport】遮罩必须脱离父级的 stacking context。媒体页的左栏是
 * sticky + overflow-auto 容器，在里面渲染 fixed 遮罩会被裁在栏内。
 */
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'

const props = defineProps<{
  /** 待预览的图片地址列表（data URI 或 http URL 皆可）。 */
  images: string[]
  /** 打开时定位到第几张。 */
  startIndex?: number
  /** 图注，通常是提示词。 */
  caption?: string
}>()

const emit = defineEmits<{ (e: 'close'): void }>()

const index = ref(props.startIndex ?? 0)

const open = computed(() => props.images.length > 0)
const current = computed(() => props.images[index.value] ?? props.images[0] ?? '')

// 外部换了图片集或起始位就重新定位，避免残留上一次的下标越界
watch(
  () => [props.images, props.startIndex] as const,
  () => {
    index.value = Math.min(props.startIndex ?? 0, Math.max(0, props.images.length - 1))
  }
)

/** 循环切换：到头绕回另一端，比禁用按钮少一次「点了没反应」的困惑。 */
function step(delta: number) {
  const n = props.images.length
  if (n < 2) return
  index.value = (index.value + delta + n) % n
}

function close() {
  emit('close')
}

function onKeydown(e: KeyboardEvent) {
  if (!open.value) return
  if (e.key === 'Escape') close()
  else if (e.key === 'ArrowLeft') step(-1)
  else if (e.key === 'ArrowRight') step(1)
}

const { t } = useI18n()

// 键盘监听只在打开期间存在：常驻监听会在页面其他地方按 Esc 时做无谓的判断，
// 也容易在组件复用时漏掉解绑。
watch(
  open,
  (isOpen) => {
    if (isOpen) {
      window.addEventListener('keydown', onKeydown)
      // 背后的页面不该跟着滚
      document.body.style.overflow = 'hidden'
    } else {
      window.removeEventListener('keydown', onKeydown)
      document.body.style.overflow = ''
    }
  },
  { immediate: true }
)

onUnmounted(() => {
  window.removeEventListener('keydown', onKeydown)
  document.body.style.overflow = ''
})
</script>

<style scoped>
.lightbox-btn {
  @apply rounded-full bg-white/10 p-2.5 text-white/90 backdrop-blur transition-colors hover:bg-white/25 hover:text-white;
}

.lightbox-fade-enter-active,
.lightbox-fade-leave-active {
  transition: opacity 0.18s ease;
}
.lightbox-fade-enter-from,
.lightbox-fade-leave-to {
  opacity: 0;
}
</style>
