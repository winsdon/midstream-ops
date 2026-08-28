<template>
  <div>
  <div v-if="loading && !sections.length" class="card overflow-hidden">
    <slot name="table" :rows="emptyRows" />
  </div>
  <div v-else-if="!sections.length" class="card overflow-hidden">
    <slot name="table" :rows="emptyRows" />
  </div>
  <div
    v-else
    :class="
      compact
        ? 'grid items-start gap-2 [grid-template-columns:repeat(auto-fill,minmax(18rem,1fr))]'
        : 'space-y-3'
    "
  >
    <StabilitySection
      v-for="sec in sections"
      :key="sec.key || '__empty__'"
      :title="titleOf(sec.label, grouping)"
      :sla="sec.sla"
      :account-count="sec.accountCount"
      :request-count="sec.requestCount"
      :grade="sec.grade"
      :default-open="shouldOpen(sec)"
      :show-grade="showGrade"
      :compact="compact"
      :expand-cmd="expandCmd"
    >
      <template v-if="sec.children.length">
        <div class="flex flex-col gap-1 px-1.5 pb-1.5">
          <div v-for="child in sec.children" :key="child.key || '__empty__'">
            <div
              class="flex items-center gap-1.5 px-1 py-0.5 text-[11px]"
              :class="compact ? '' : 'border-t border-gray-100 bg-gray-50/80 px-4 py-1.5 dark:border-dark-800 dark:bg-dark-800/40'"
            >
              <GradeDot v-if="showGrade" class="min-w-0 flex-1" :grade="child.grade">
                <span class="truncate font-medium text-gray-700 dark:text-dark-200">{{ titleOf(child.label, inner) }}</span>
              </GradeDot>
              <span v-else class="min-w-0 flex-1 truncate font-medium text-gray-700 dark:text-dark-200">{{ titleOf(child.label, inner) }}</span>
              <span class="flex shrink-0 items-center gap-x-1 whitespace-nowrap text-gray-400">
                <span :class="slaTone(child.sla)">{{ fmtPct(child.sla) }}</span>
                <span>{{ t('stability.sectionAccounts', { n: child.accountCount }) }}</span>
                <span>{{ t('stability.sectionRequests', { n: child.requestCount }) }}</span>
              </span>
            </div>
            <slot name="table" :rows="child.rows" />
          </div>
        </div>
      </template>
      <slot v-else name="table" :rows="sec.rows" />
    </StabilitySection>
  </div>
  </div>
</template>

<script setup lang="ts" generic="T extends FilterableRow">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { fmtPct } from '@/utils/format'
import { passiveRateClass, rateClass, type FilterableRow } from '@/utils/stabilityModel'
import type { StabilitySection as Section, GroupingMode } from '@/utils/stabilitySections'
import GradeDot from './GradeDot.vue'
import StabilitySection from './StabilitySection.vue'

const props = withDefaults(
  defineProps<{
    sections: Section<T>[]
    loading: boolean
    grouping: GroupingMode
    showGrade?: boolean
    autoCollapse?: boolean
    compact?: boolean
    expandCmd?: { open: boolean } | null
  }>(),
  { showGrade: true, autoCollapse: false, compact: false, expandCmd: null }
)

const { t } = useI18n()

const inner = computed<GroupingMode>(() => (props.grouping === 'provider' ? 'group' : 'provider'))
const emptyRows = computed<T[]>(() => [])

function titleOf(label: string, dim: GroupingMode): string {
  if (label) return label
  return dim === 'group' ? t('stability.ungrouped') : t('stability.unassigned')
}

function shouldOpen(sec: Section<T>): boolean {
  if (!props.autoCollapse) return true
  if (props.sections.length <= 1) return true
  return sec.grade === 'warn' || sec.grade === 'bad'
}

function slaTone(v: number | null): string {
  return props.showGrade ? rateClass(v) : passiveRateClass(v)
}
</script>
