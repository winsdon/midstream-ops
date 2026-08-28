<template>
  <div class="card min-w-0 w-full overflow-hidden">
    <button
      type="button"
      class="flex w-full items-center gap-1.5 px-2.5 py-1.5 text-left transition-colors hover:bg-gray-50 dark:hover:bg-dark-800/60"
      :aria-expanded="open"
      @click="open = !open"
    >
      <Icon
        :name="open ? 'chevronDown' : 'chevronRight'"
        size="sm"
        class="shrink-0 text-gray-400"
      />
      <GradeDot v-if="showGrade" class="min-w-0 flex-1" :grade="grade">
        <span class="block truncate font-semibold text-gray-900 dark:text-white">{{ title }}</span>
      </GradeDot>
      <span v-else class="min-w-0 flex-1 truncate font-semibold text-gray-900 dark:text-white">{{ title }}</span>
      <span class="flex shrink-0 items-center gap-x-1 whitespace-nowrap text-[11px] text-gray-500 dark:text-dark-400">
        <span :class="slaClass">{{ fmtPct(sla) }}</span>
        <span>{{ t('stability.sectionAccounts', { n: accountCount }) }}</span>
        <span>{{ t('stability.sectionRequests', { n: requestCount }) }}</span>
      </span>
    </button>
    <div v-show="open">
      <slot />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { fmtPct } from '@/utils/format'
import { passiveRateClass, rateClass, type RowGrade } from '@/utils/stabilityModel'
import Icon from '@/components/icons/Icon.vue'
import GradeDot from './GradeDot.vue'

const props = withDefaults(
  defineProps<{
    title: string
    sla: number | null
    accountCount: number
    requestCount: number
    grade: RowGrade
    defaultOpen?: boolean
    showGrade?: boolean
    compact?: boolean
    /** 父级「全部展开 / 全部收起」：每次点都换新对象，保证已展开后再点展开仍能对齐 */
    expandCmd?: { open: boolean } | null
  }>(),
  { defaultOpen: true, showGrade: true, compact: false, expandCmd: null }
)

const { t } = useI18n()
const open = ref(props.expandCmd?.open ?? props.defaultOpen)
const slaClass = computed(() => (props.showGrade ? rateClass(props.sla) : passiveRateClass(props.sla)))

watch(
  () => props.defaultOpen,
  (v) => {
    if (v) open.value = true
  }
)

watch(
  () => props.expandCmd,
  (cmd) => {
    if (cmd) open.value = cmd.open
  }
)
</script>
