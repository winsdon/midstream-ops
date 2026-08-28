<template>
  <div class="card min-w-0 w-full overflow-hidden">
    <div class="flex items-stretch">
      <button
        type="button"
        class="flex shrink-0 items-center px-2.5 py-1.5 transition-colors hover:bg-gray-50 dark:hover:bg-dark-800/60"
        :aria-expanded="open"
        @click="emit('update:open', !open)"
      >
        <Icon
          :name="open ? 'chevronDown' : 'chevronRight'"
          size="sm"
          class="text-gray-400"
        />
      </button>
      <button
        type="button"
        class="flex min-w-0 flex-1 items-center gap-1.5 py-1.5 pr-2.5 text-left transition-colors hover:bg-gray-50 dark:hover:bg-dark-800/60"
        :aria-expanded="selectable ? undefined : open"
        :aria-haspopup="selectable ? 'dialog' : undefined"
        @click="onTitleClick"
      >
        <GradeDot v-if="showGrade" class="min-w-0 flex-1" :grade="grade">
          <span class="block truncate font-semibold text-gray-900 dark:text-white">{{ title }}</span>
        </GradeDot>
        <span v-else class="min-w-0 flex-1 truncate font-semibold text-gray-900 dark:text-white">{{ title }}</span>
        <span class="flex shrink-0 items-center gap-x-1 whitespace-nowrap text-[11px] text-gray-500 dark:text-dark-400">
          <span :class="slaClass">{{ fmtPct(sla) }}</span>
          <span>{{ t('stability.sectionRequests', { n: requestCount }) }}</span>
        </span>
      </button>
    </div>
    <div v-show="open">
      <slot />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { fmtPct } from '@/utils/format'
import { passiveRateClass, rateClass, type RowGrade } from '@/utils/stabilityModel'
import Icon from '@/components/icons/Icon.vue'
import GradeDot from './GradeDot.vue'

const props = withDefaults(
  defineProps<{
    title: string
    sla: number | null
    requestCount: number
    grade: RowGrade
    /** 由父级控制；缺省收起 */
    open?: boolean
    showGrade?: boolean
    /** 标题区域点击打开详情；关闭时标题点击仍只展开/收起 */
    selectable?: boolean
  }>(),
  { open: false, showGrade: true, selectable: false }
)

const emit = defineEmits<{
  'update:open': [value: boolean]
  select: []
}>()

const { t } = useI18n()
const slaClass = computed(() => (props.showGrade ? rateClass(props.sla) : passiveRateClass(props.sla)))

function onTitleClick() {
  if (props.selectable) {
    emit('select')
    return
  }
  emit('update:open', !props.open)
}
</script>
