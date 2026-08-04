<template>
  <tr v-if="loading" class="!bg-transparent">
    <td :colspan="colspan">
      <LoadingState />
    </td>
  </tr>
  <tr v-else-if="empty" class="!bg-transparent">
    <td :colspan="colspan">
      <EmptyState :icon="icon" :title="title" :description="description" />
    </td>
  </tr>
</template>

<script setup lang="ts">
import LoadingState from './LoadingState.vue'
import EmptyState from './EmptyState.vue'
import type { IconName } from '@/components/icons/paths'

// 表格里的加载 / 空态占位行：原本每张表都手写两行 <td colspan>，收拢到一处
// !bg-transparent 用于压掉 .table tbody tr 的 hover 高亮 —— 占位行不是数据行，不该有悬停反馈
defineProps<{
  loading: boolean
  empty: boolean
  colspan: number
  icon?: IconName
  title?: string
  description?: string
}>()
</script>
