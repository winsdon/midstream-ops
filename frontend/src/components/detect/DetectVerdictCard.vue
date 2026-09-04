<template>
  <div class="card">
    <div class="card-body space-y-4">
      <!-- 标题与判定 -->
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div class="min-w-0">
          <h3 class="truncate text-sm font-semibold text-gray-900 dark:text-white">{{ run.name }}</h3>
          <p class="mt-0.5 truncate text-xs text-gray-400">{{ run.base_url }} · {{ run.model }}</p>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <Badge v-if="verdict" :variant="labelVariant(verdict.label)">
            {{ t(`detect.labels.${verdict.label}`) }}
          </Badge>
          <span v-if="verdict" class="text-xs text-gray-500 dark:text-dark-400">
            {{ t('detect.confidence') }}：{{ t(`detect.confidence${capitalize(verdict.confidence)}`) }}
          </span>
          <LoadingSpinner v-else size="sm" />
        </div>
      </div>

      <p v-if="verdict" class="text-xs leading-relaxed text-gray-600 dark:text-dark-300">{{ verdict.title }}</p>
      <p v-if="run.error" class="text-xs text-amber-600 dark:text-amber-400">{{ run.error }}</p>

      <!-- 真实性评分 -->
      <div v-if="verdict" class="rounded-xl border border-gray-100 p-3 dark:border-dark-800">
        <div class="flex items-center justify-between gap-3">
          <span class="text-xs font-medium text-gray-500 dark:text-dark-400" :title="t('detect.authenticityHint')">
            {{ t('detect.authenticity') }}
          </span>
          <span class="flex items-center gap-2">
            <span class="text-sm font-semibold text-gray-900 dark:text-white">
              {{ verdict.authenticity.score }}<span class="text-xs text-gray-400">/100</span>
            </span>
            <Badge :variant="gradeVariant(verdict.authenticity.grade)">
              {{ t(`detect.grades.${verdict.authenticity.grade}`) }}
            </Badge>
          </span>
        </div>
        <div class="mt-2 h-1.5 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-800">
          <div
            class="h-full rounded-full transition-all duration-500"
            :class="scoreBarClass(verdict.authenticity.grade)"
            :style="{ width: verdict.authenticity.score + '%' }"
          ></div>
        </div>
        <ul
          v-if="verdict.authenticity.capped && verdict.authenticity.cap_reason?.length"
          class="mt-2 space-y-1"
        >
          <li class="text-xs font-medium text-red-600 dark:text-red-400">{{ t('detect.capped') }}</li>
          <li
            v-for="(reason, i) in verdict.authenticity.cap_reason"
            :key="i"
            class="text-xs leading-relaxed text-red-500 dark:text-red-400"
          >
            · {{ reason }}
          </li>
        </ul>
      </div>

      <!-- 分类打分 -->
      <div v-if="verdict" class="space-y-1.5">
        <p class="text-xs font-medium text-gray-500 dark:text-dark-400">{{ t('detect.scores') }}</p>
        <div v-for="cls in SCORE_CLASSES" :key="cls" class="flex items-center gap-2">
          <span class="w-24 flex-shrink-0 truncate text-xs text-gray-500 dark:text-dark-400">
            {{ t(`detect.labels.${cls}`) }}
          </span>
          <span class="h-1.5 flex-1 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-800">
            <span
              class="block h-full rounded-full transition-all duration-500"
              :class="cls === verdict.label ? topBarClass : 'bg-gray-300 dark:bg-dark-600'"
              :style="{ width: barWidth(verdict.scores[cls] ?? 0) }"
            ></span>
          </span>
          <span class="w-6 flex-shrink-0 text-right text-xs tabular-nums text-gray-500 dark:text-dark-400">
            {{ verdict.scores[cls] ?? 0 }}
          </span>
        </div>
      </div>

      <!-- 判定依据 -->
      <div v-if="verdict" class="space-y-2">
        <p class="text-xs font-medium text-gray-500 dark:text-dark-400">{{ t('detect.primaryReasons') }}</p>
        <ul v-if="primary.length" class="space-y-1.5">
          <li v-for="(ev, i) in primary" :key="ev.key + i" class="flex items-start gap-2">
            <span class="mt-1.5 h-1.5 w-1.5 flex-shrink-0 rounded-full bg-primary-500"></span>
            <span class="min-w-0 text-xs leading-relaxed text-gray-700 dark:text-dark-200">
              {{ ev.label }}
              <span v-if="ev.detail" class="text-gray-400">（{{ ev.detail }}）</span>
              <span class="ml-1 text-gray-400">+{{ ev.weight }}</span>
            </span>
          </li>
        </ul>
        <p v-else class="text-xs text-gray-400">{{ t('detect.noEvidence') }}</p>

        <details v-if="others.length" class="pt-1">
          <summary class="cursor-pointer text-xs text-gray-500 dark:text-dark-400">
            {{ t('detect.otherSignals') }}（{{ others.length }}）
          </summary>
          <ul class="mt-2 space-y-1.5">
            <li v-for="(ev, i) in others" :key="ev.key + i" class="flex items-start gap-2">
              <span class="mt-1.5 h-1.5 w-1.5 flex-shrink-0 rounded-full bg-gray-300 dark:bg-dark-600"></span>
              <span class="min-w-0 text-xs leading-relaxed text-gray-500 dark:text-dark-400">
                {{ ev.label }}
                <span v-if="ev.detail">（{{ ev.detail }}）</span>
                <span v-if="ev.weight > 0" class="ml-1">+{{ ev.weight }}</span>
              </span>
            </li>
          </ul>
        </details>
      </div>

      <p class="border-t border-gray-100 pt-3 text-xs text-gray-400 dark:border-dark-800">
        {{
          t('detect.statusCounts', {
            passed: counts.passed,
            failed: counts.failed,
            suspicious: counts.suspicious,
            inconclusive: counts.inconclusive,
            unsupported: counts.unsupported
          })
        }}
      </p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Badge from '@/components/common/Badge.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import {
  SCORE_CLASSES,
  countStatuses,
  gradeVariant,
  labelVariant,
  scoreBarClass,
  splitReasons
} from '@/utils/detectModel'
import type { DetectTargetRun } from '@/types/detect'

const { t } = useI18n()

const props = defineProps<{ run: DetectTargetRun }>()

const verdict = computed(() => props.run.verdict)
const counts = computed(() => countStatuses(props.run))

const split = computed(() =>
  verdict.value
    ? splitReasons(verdict.value.reasons ?? [], verdict.value.label)
    : { primary: [], others: [] }
)
const primary = computed(() => split.value.primary)
const others = computed(() => split.value.others)

/** 分数条按 10 分满格换算——单类别拿到 10 分已经是压倒性证据。 */
function barWidth(score: number): string {
  return Math.min(100, score * 10) + '%'
}

/**
 * 领先类别的分数条跟随判定语义着色。
 *
 * 统一用主题色会让「包装 / 伪装」也显示成一条漂亮的绿条，读起来像好消息，
 * 与徽章传达的警示正好相反。
 */
const topBarClass = computed(() => {
  switch (verdict.value ? labelVariant(verdict.value.label) : 'gray') {
    case 'danger':
      return 'bg-red-500'
    case 'warning':
      return 'bg-amber-500'
    case 'purple':
      return 'bg-violet-500'
    case 'success':
      return 'bg-emerald-500'
    case 'primary':
      return 'bg-primary-500'
    default:
      return 'bg-gray-400'
  }
})

function capitalize(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1)
}
</script>
