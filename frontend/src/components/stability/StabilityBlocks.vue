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
      :request-count="sec.requestCount"
      :grade="sec.grade"
      :open="isOpen(sec.key)"
      :show-grade="showGrade"
      :selectable="selectable"
      @update:open="setOpen(sec.key, $event)"
      @select="openDetail?.(sec)"
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
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { fmtPct } from '@/utils/format'
import { passiveRateClass, rateClass, type FilterableRow } from '@/utils/stabilityModel'
import {
  allSectionsOpen,
  type StabilitySection as Section,
  type GroupingMode
} from '@/utils/stabilitySections'
import GradeDot from './GradeDot.vue'
import StabilitySection from './StabilitySection.vue'

const props = withDefaults(
  defineProps<{
    sections: Section<T>[]
    loading: boolean
    grouping: GroupingMode
    showGrade?: boolean
    compact?: boolean
    /** 标题点击打开详情（被动统计）；主动探测表头仍只展开 */
    selectable?: boolean
    /** 父级「全部展开 / 全部收起」：每次点都换新对象，保证已展开后再点展开仍能对齐 */
    expandCmd?: { open: boolean } | null
    /** 泛型块上 emit 不可靠，详情用回调 */
    openDetail?: (section: Section<T>) => void
  }>(),
  { showGrade: true, compact: false, selectable: false, expandCmd: null }
)

const emit = defineEmits<{
  'update:allOpen': [value: boolean]
}>()

const { t } = useI18n()

const inner = computed<GroupingMode>(() => (props.grouping === 'provider' ? 'group' : 'provider'))
const emptyRows = computed<T[]>(() => [])
const openMap = ref<Record<string, boolean>>({})

function titleOf(label: string, dim: GroupingMode): string {
  if (label) return label
  return dim === 'group' ? t('stability.ungrouped') : t('stability.unassigned')
}

function isOpen(key: string): boolean {
  return openMap.value[key] === true
}

function setOpen(key: string, open: boolean) {
  openMap.value = { ...openMap.value, [key]: open }
}

const sectionKeys = computed(() => props.sections.map((s) => s.key))
const allOpen = computed(() => allSectionsOpen(openMap.value, sectionKeys.value))

watch(allOpen, (v) => emit('update:allOpen', v), { immediate: true })

watch(
  () => props.expandCmd,
  (cmd) => {
    if (!cmd) return
    const next: Record<string, boolean> = { ...openMap.value }
    for (const key of sectionKeys.value) {
      next[key] = cmd.open
    }
    openMap.value = next
  }
)

function slaTone(v: number | null): string {
  return props.showGrade ? rateClass(v) : passiveRateClass(v)
}
</script>
