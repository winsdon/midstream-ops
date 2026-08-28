<template>
  <LoadingState v-if="loading && !rows.length" />
  <EmptyState v-else-if="!rows.length" icon="chart" />
  <div v-else class="flex flex-col gap-1.5 px-1.5 pb-1.5">
    <PassiveCard
      v-for="r in sorted"
      :key="r.account_id"
      :row="r"
      :minutes="minutes"
      :secondary="secondary"
      @click="emit('select', r)"
    />
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { sortPassiveRows, type RowGrade } from '@/utils/stabilityModel'
import LoadingState from '@/components/common/LoadingState.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import PassiveCard from './PassiveCard.vue'
import type { PassiveRow } from '@/types'

const props = defineProps<{
  rows: PassiveRow[]
  loading: boolean
  minutes: number
  secondary?: 'groups' | 'provider'
  gradeOf: (r: PassiveRow) => RowGrade
}>()

const emit = defineEmits<{ select: [row: PassiveRow] }>()

const sorted = computed(() => sortPassiveRows(props.rows, props.gradeOf))
</script>
