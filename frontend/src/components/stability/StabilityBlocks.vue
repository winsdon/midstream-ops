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
      <slot name="table" :rows="sec.rows" />
    </StabilitySection>
  </div>
  </div>
</template>

<script setup lang="ts" generic="T extends FilterableRow">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { type FilterableRow } from '@/utils/stabilityModel'
import {
  allSectionsOpen,
  type StabilitySection as Section,
  type GroupingMode
} from '@/utils/stabilitySections'
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
</script>
