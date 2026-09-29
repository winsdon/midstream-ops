<template>
  <div class="space-y-4">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <p class="min-w-0 flex-1 text-xs leading-relaxed text-gray-500 dark:text-dark-400">{{ t('detect.iqBoardHint') }}</p>
      <button type="button" class="btn btn-secondary btn-sm" @click="emit('export')">
        <Icon name="download" size="sm" />
        {{ t('detect.exportJson') }}
      </button>
    </div>

    <section v-for="meta in sections" :key="meta.id" class="card">
      <div class="card-header">
        <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ meta.title }}</h2>
        <p v-if="meta.note" class="mt-1 text-xs text-gray-500 dark:text-dark-400">{{ meta.note }}</p>
      </div>
      <div class="card-body">
        <DetectIQGallery
          v-if="meta.grading === 'visual'"
          :runs="runs"
          :check-id="meta.id"
          :allow-retry="allowRetry"
          @select="emit('select', $event)"
          @retry="emit('retry', $event)"
        />
        <DetectIQAnswerTable
          v-else
          :runs="runs"
          :check-id="meta.id"
          :allow-retry="allowRetry"
          @select="emit('select', $event)"
          @retry="emit('retry', $event)"
        />
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import DetectIQGallery from '@/components/detect/DetectIQGallery.vue'
import DetectIQAnswerTable from '@/components/detect/DetectIQAnswerTable.vue'
import type { DetectCheckMeta, DetectCheckResult, DetectTargetRun } from '@/types/detect'

const { t } = useI18n()

const props = defineProps<{
  runs: DetectTargetRun[]
  /** 智商题目录（决定分区顺序、标题与判分方式） */
  checks: DetectCheckMeta[]
  /** 本轮实际跑的题目 */
  activeIds: string[]
  allowRetry?: boolean
}>()

const emit = defineEmits<{
  (e: 'select', payload: { run: DetectTargetRun; check: DetectCheckResult }): void
  (e: 'retry', payload: { targetIndex: number; checkId: string }): void
  (e: 'export'): void
}>()

/**
 * 一道题一个分区，按判分方式摆：交作品的进画廊，有标准答案的进答案表。
 * 分区由目录元数据驱动，以后加题不用改这里。
 */
const sections = computed(() => {
  const active = new Set(props.activeIds)
  return props.checks.filter((c) => active.has(c.id))
})
</script>
