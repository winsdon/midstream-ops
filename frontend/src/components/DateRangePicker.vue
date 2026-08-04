<template>
  <div class="flex flex-wrap items-center gap-2">
    <!-- 快捷范围 -->
    <div class="flex rounded-lg bg-gray-100 p-0.5 dark:bg-dark-800">
      <button
        v-for="p in presets" :key="p.key"
        type="button"
        class="rounded-md px-3 py-1.5 text-xs font-medium transition-colors sm:text-sm"
        :class="activePreset === p.key
          ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-700 dark:text-primary-300'
          : 'text-gray-500 hover:text-gray-700 dark:hover:text-dark-200'"
        @click="applyPreset(p.key)"
      >{{ t(p.label) }}</button>
    </div>

    <!-- 自定义区间 -->
    <div class="flex items-center gap-1.5">
      <input
        v-model="startDate" type="date" :max="endDate"
        class="input !w-auto !py-1.5 text-xs sm:text-sm"
        @change="applyCustom"
      />
      <span class="text-gray-400">~</span>
      <input
        v-model="endDate" type="date" :min="startDate" :max="today"
        class="input !w-auto !py-1.5 text-xs sm:text-sm"
        @change="applyCustom"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { todayStr, daysAgoStr } from '@/utils/format'

const { t } = useI18n()

export interface DateRange {
  start: string
  end: string
}

const props = withDefaults(
  defineProps<{ modelValue?: DateRange }>(),
  {}
)
const emit = defineEmits<{ 'update:modelValue': [value: DateRange]; change: [value: DateRange] }>()

type PresetKey = 'today' | 'yesterday' | 'last7' | 'last30' | 'custom'

const presets = [
  { key: 'today' as const, label: 'range.today' },
  { key: 'yesterday' as const, label: 'range.yesterday' },
  { key: 'last7' as const, label: 'range.last7' },
  { key: 'last30' as const, label: 'range.last30' }
]

const today = todayStr()
const activePreset = ref<PresetKey>('today')
const startDate = ref(props.modelValue?.start || today)
const endDate = ref(props.modelValue?.end || today)

// 快捷项 → 具体区间（闭区间，含首尾两天）
function rangeOf(key: PresetKey): DateRange {
  switch (key) {
    case 'yesterday': {
      const y = daysAgoStr(1)
      return { start: y, end: y }
    }
    case 'last7':
      return { start: daysAgoStr(6), end: today }
    case 'last30':
      return { start: daysAgoStr(29), end: today }
    default:
      return { start: today, end: today }
  }
}

function emitRange(): void {
  const r = { start: startDate.value, end: endDate.value }
  emit('update:modelValue', r)
  emit('change', r)
}

function applyPreset(key: PresetKey): void {
  activePreset.value = key
  const r = rangeOf(key)
  startDate.value = r.start
  endDate.value = r.end
  emitRange()
}

// 手改日期即切到自定义态；顺序颠倒时自动纠正
function applyCustom(): void {
  if (startDate.value > endDate.value) {
    const tmp = startDate.value
    startDate.value = endDate.value
    endDate.value = tmp
  }
  activePreset.value = 'custom'
  emitRange()
}

// 外部重置时同步（父组件受控场景）
watch(
  () => props.modelValue,
  (v) => {
    if (!v) return
    if (v.start !== startDate.value) startDate.value = v.start
    if (v.end !== endDate.value) endDate.value = v.end
  }
)
</script>
