<template>
  <div class="card">
    <div class="card-body space-y-4">
      <!-- 标题与真实性等级 -->
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div class="min-w-0">
          <h3 class="truncate text-sm font-semibold text-gray-900 dark:text-white">{{ run.name }}</h3>
          <p class="mt-0.5 truncate text-xs text-gray-400">{{ run.base_url }} · {{ run.model }}</p>
        </div>
        <Badge v-if="verdict" :variant="gradeVariant(verdict.authenticity.grade)">
          {{ t(`detect.grades.${verdict.authenticity.grade}`) }}
        </Badge>
        <LoadingSpinner v-else-if="!run.finished_at" size="sm" />
      </div>

      <p v-if="!verdict && run.finished_at" class="text-xs leading-relaxed text-gray-500 dark:text-dark-400">
        {{ t('detect.noVerdict') }}
      </p>
      <p v-if="run.error" class="text-xs text-amber-600 dark:text-amber-400">{{ run.error }}</p>

      <!-- 真实性评分与得分证据链 -->
      <div v-if="verdict" class="rounded-xl border border-gray-100 p-3 dark:border-dark-800">
        <div class="flex items-center justify-between gap-3">
          <span class="text-xs font-medium text-gray-500 dark:text-dark-400" :title="t('detect.authenticityHint')">
            {{ t('detect.authenticity') }}
          </span>
          <span class="text-sm font-semibold text-gray-900 dark:text-white">
            {{ verdict.authenticity.score }}<span class="text-xs text-gray-400">/100</span>
          </span>
        </div>
        <div class="mt-2 h-1.5 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-800">
          <div
            class="h-full rounded-full transition-all duration-500"
            :class="scoreBarClass(verdict.authenticity.grade)"
            :style="{ width: verdict.authenticity.score + '%' }"
          ></div>
        </div>

        <p class="mt-3 text-xs font-medium text-gray-500 dark:text-dark-400">{{ t('detect.authChain') }}</p>
        <ul v-if="chain.items.length" class="mt-1.5 space-y-2">
          <li v-for="item in chain.items" :key="item.id" class="flex items-start gap-2">
            <span
              class="mt-px w-12 flex-shrink-0 rounded px-1 text-center text-xs font-semibold tabular-nums"
              :class="pointsClass(item.kind)"
            >
              {{ pointsText(item) }}
            </span>
            <span class="min-w-0 text-xs leading-relaxed">
              <span class="font-medium text-gray-800 dark:text-dark-100">{{ item.title }}</span>
              <span v-if="item.max" class="ml-1 text-gray-400">{{ t('detect.authOutOf', { max: item.max }) }}</span>
              <span v-if="item.cap" class="block text-red-500 dark:text-red-400">{{ item.cap }}</span>
              <span v-if="item.detail" class="line-clamp-2 text-gray-500 dark:text-dark-400" :title="item.detail">{{ item.detail }}</span>
            </span>
          </li>
        </ul>
        <p v-else class="mt-1.5 text-xs text-gray-400">{{ t('detect.authEmpty') }}</p>
        <p
          v-if="chain.items.length"
          class="mt-2 border-t border-gray-100 pt-2 text-xs text-gray-500 dark:border-dark-800 dark:text-dark-400"
        >
          {{ t('detect.authTotal', { raw: signed(chain.raw), score: verdict.authenticity.score }) }}
          <span v-if="totalNote">（{{ totalNote }}）</span>
        </p>
      </div>

      <!-- 渠道形态：只作参考，不计入真实性 -->
      <details v-if="verdict" class="rounded-xl border border-gray-100 p-3 dark:border-dark-800">
        <summary class="flex cursor-pointer flex-wrap items-center gap-2 text-xs text-gray-500 dark:text-dark-400">
          {{ t('detect.channelShape') }}
          <Badge :variant="labelVariant(verdict.label)">{{ t(`detect.labels.${verdict.label}`) }}</Badge>
          <span>{{ t('detect.confidence') }}：{{ t(`detect.confidence${capitalize(verdict.confidence)}`) }}</span>
        </summary>
        <div class="mt-3 space-y-3">
          <p class="text-xs leading-relaxed text-gray-600 dark:text-dark-300">{{ verdict.title }}</p>
          <div class="space-y-1.5">
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
          <div class="space-y-2">
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
            <ul v-if="others.length" class="space-y-1.5 pt-1">
              <li v-for="(ev, i) in others" :key="ev.key + i" class="flex items-start gap-2">
                <span class="mt-1.5 h-1.5 w-1.5 flex-shrink-0 rounded-full bg-gray-300 dark:bg-dark-600"></span>
                <span class="min-w-0 text-xs leading-relaxed text-gray-500 dark:text-dark-400">
                  {{ ev.label }}
                  <span v-if="ev.detail">（{{ ev.detail }}）</span>
                  <span v-if="ev.weight > 0" class="ml-1">+{{ ev.weight }}</span>
                </span>
              </li>
            </ul>
          </div>
        </div>
      </details>

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
  buildAuthChain,
  countStatuses,
  gradeVariant,
  labelVariant,
  scoreBarClass,
  splitReasons
} from '@/utils/detectModel'
import type { AuthChainItem } from '@/utils/detectModel'
import type { DetectCheckMeta, DetectTargetRun } from '@/types/detect'

const { t } = useI18n()

const props = defineProps<{ run: DetectTargetRun; checks?: DetectCheckMeta[] }>()

const verdict = computed(() => props.run.verdict)
const counts = computed(() => countStatuses(props.run))

const split = computed(() =>
  verdict.value
    ? splitReasons(verdict.value.reasons ?? [], verdict.value.label)
    : { primary: [], others: [] }
)
const primary = computed(() => split.value.primary)
const others = computed(() => split.value.others)

const chain = computed(() => buildAuthChain(props.run, props.checks ?? []))

/** 合计与最终分不一致时说明原因：封顶、满分 100、最低 0。 */
const totalNote = computed(() => {
  const auth = verdict.value?.authenticity
  if (!auth) return ''
  if (auth.capped) return t('detect.authNoteCapped')
  if (chain.value.raw > auth.score && auth.score === 100) return t('detect.authNoteMax')
  if (chain.value.raw < 0) return t('detect.authNoteMin')
  return ''
})

function signed(n: number): string {
  return n > 0 ? `+${n}` : n < 0 ? `−${-n}` : '0'
}

function pointsText(item: AuthChainItem): string {
  return item.kind === 'cap' ? t('detect.authCapTag') : signed(item.points)
}

function pointsClass(kind: AuthChainItem['kind']): string {
  switch (kind) {
    case 'cap':
      return 'bg-red-50 text-red-600 dark:bg-red-900/30 dark:text-red-400'
    case 'plus':
      return 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-400'
    case 'minus':
      return 'bg-amber-50 text-amber-700 dark:bg-amber-900/30 dark:text-amber-400'
    default:
      return 'bg-gray-100 text-gray-400 dark:bg-dark-800 dark:text-dark-500'
  }
}

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
