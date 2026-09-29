<template>
  <div class="grid gap-3 sm:grid-cols-2 2xl:grid-cols-3">
    <article
      v-for="card in cards"
      :key="card.index"
      class="flex flex-col overflow-hidden rounded-xl border border-gray-100 dark:border-dark-800"
    >
      <header class="flex items-start justify-between gap-2 px-3 pt-3">
        <div class="min-w-0">
          <p class="truncate text-sm font-medium text-gray-900 dark:text-white" :title="card.run.name">{{ card.run.name }}</p>
          <p class="truncate text-xs text-gray-400">{{ card.run.model }}</p>
        </div>
        <Badge v-if="card.res && !card.running" :variant="iqStatusVariant(card.res.status)">
          {{ t(statusLabelKey(card.res)) }}
        </Badge>
      </header>

      <!-- 缩略图本身不接收点击（iframe 里是不可信内容），点击由外层接住去开详情 -->
      <div
        class="m-3 aspect-[4/3] overflow-hidden rounded-lg border bg-white transition-colors dark:border-dark-700"
        :class="card.openable ? 'cursor-pointer border-gray-100 hover:border-primary-300' : 'border-gray-100'"
        :role="card.openable ? 'button' : undefined"
        :tabindex="card.openable ? 0 : -1"
        :aria-label="card.openable ? t('detect.iqViewDetail') : undefined"
        @click="open(card)"
        @keydown.enter="open(card)"
      >
        <DetectArtifactFrame
          v-if="card.res?.output?.document"
          :document="card.res.output.document"
          :kind="card.res.output.document_kind"
          :title="`${card.run.name} · ${card.res.title}`"
        />
        <div
          v-else
          class="flex h-full flex-col items-center justify-center gap-2 bg-gray-50 px-4 text-center text-xs text-gray-400 dark:bg-dark-800/60 dark:text-dark-400"
        >
          <LoadingSpinner v-if="card.running" />
          <span>{{ placeholder(card) }}</span>
        </div>
      </div>

      <footer class="mt-auto flex items-center justify-between gap-2 px-3 pb-3 text-xs text-gray-500 dark:text-dark-400">
        <span class="truncate" :title="card.res?.summary">{{ card.running ? '' : card.res?.summary }}</span>
        <span class="flex flex-shrink-0 items-center gap-2">
          <span v-if="card.res && !card.running" class="tabular-nums">{{ fmtMs(card.res.duration_ms) }}</span>
          <button
            v-if="allowRetry && isRequestFailed(card.res)"
            type="button"
            class="inline-flex items-center gap-1 rounded-lg px-1.5 py-0.5 font-medium text-primary-600 hover:bg-primary-50 dark:text-primary-400 dark:hover:bg-primary-900/30"
            :title="t('detect.retryHint')"
            @click="emit('retry', { targetIndex: card.index, checkId })"
          >
            <Icon name="refresh" size="sm" />
            {{ t('detect.retry') }}
          </button>
        </span>
      </footer>
    </article>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Badge from '@/components/common/Badge.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import Icon from '@/components/icons/Icon.vue'
import DetectArtifactFrame from '@/components/detect/DetectArtifactFrame.vue'
import { findCheck, iqStatusVariant, isRequestFailed, statusLabelKey } from '@/utils/detectModel'
import { fmtMs } from '@/utils/format'
import type { DetectCheckResult, DetectTargetRun } from '@/types/detect'

const { t } = useI18n()

const props = defineProps<{
  runs: DetectTargetRun[]
  /** 本区展示的题目（grading = visual） */
  checkId: string
  allowRetry?: boolean
}>()

const emit = defineEmits<{
  (e: 'select', payload: { run: DetectTargetRun; check: DetectCheckResult }): void
  (e: 'retry', payload: { targetIndex: number; checkId: string }): void
}>()

interface Card {
  index: number
  run: DetectTargetRun
  res?: DetectCheckResult
  running: boolean
  openable: boolean
}

const cards = computed<Card[]>(() =>
  props.runs.map((run, index) => {
    const res = findCheck(run, props.checkId)
    const running = res?.status === 'running'
    return { index, run, res, running, openable: !!res && !running }
  })
)

function placeholder(card: Card): string {
  if (!card.res) return t('detect.iqPending')
  if (card.running) return t('detect.iqRunning')
  return t('detect.iqNoArtwork')
}

function open(card: Card): void {
  if (card.openable && card.res) emit('select', { run: card.run, check: card.res })
}
</script>
