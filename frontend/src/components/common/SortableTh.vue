<template>
  <th :aria-sort="ariaSort">
    <button
      type="button"
      class="group flex w-full items-center gap-1"
      :class="align === 'right' ? 'justify-end' : 'justify-start'"
      @click="$emit('sort', sortKey)"
    >
      <span><slot>{{ label }}</slot></span>
      <!-- 只用一个上箭头：desc 靠 rotate-180 翻转，省掉第二套 path。
           非当前列显示淡色下箭头暗示「这列可以排」，hover 时加深 -->
      <Icon
        v-if="active"
        name="arrowUp"
        size="xs"
        class="shrink-0 text-primary-600 transition-transform dark:text-primary-400"
        :class="order === 'desc' && 'rotate-180'"
        :stroke-width="2.5"
      />
      <Icon
        v-else
        name="arrowDown"
        size="xs"
        class="shrink-0 text-gray-300 transition-colors group-hover:text-gray-500 dark:text-dark-600 dark:group-hover:text-dark-400"
      />
    </button>
  </th>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import Icon from '@/components/icons/Icon.vue'
import type { SortOrder } from '@/utils/tableSort'

/**
 * 可排序表头。原本每张表都手写字面量 <th>，排序交互收拢到这里。
 *
 * 外部写的 class（如 text-right）由 Vue 自动合并到根 <th>，无需 props 转发；
 * 但 text-align 不作用于 flex 子项，所以箭头靠边要另给 align —— 只写
 * class="text-right" 会得到「文字靠右、箭头贴左」的错位。
 */
const props = withDefaults(
  defineProps<{
    /** 列标识，与 useTableSort 的 accessors key 一致 */
    sortKey: string
    /** 当前排序列（由 useTableSort 提供） */
    activeKey: string
    order: SortOrder
    /** 表头文字；也可用默认插槽传富内容 */
    label?: string
    align?: 'left' | 'right'
  }>(),
  { align: 'left' }
)

defineEmits<{ sort: [key: string] }>()

const active = computed(() => props.activeKey === props.sortKey)

// 让屏幕阅读器能播报当前列的排序状态
const ariaSort = computed(() =>
  active.value ? (props.order === 'asc' ? 'ascending' : 'descending') : 'none'
)
</script>
