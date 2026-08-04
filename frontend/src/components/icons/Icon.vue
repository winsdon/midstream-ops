<template>
  <svg
    :class="sizeClass"
    fill="none"
    viewBox="0 0 24 24"
    stroke="currentColor"
    :stroke-width="strokeWidth"
  >
    <path stroke-linecap="round" stroke-linejoin="round" :d="iconPath" />
  </svg>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { iconPaths, type IconName } from './paths'

const props = withDefaults(defineProps<{
  name: IconName
  size?: 'xs' | 'sm' | 'md' | 'lg' | 'xl'
  strokeWidth?: number
}>(), {
  size: 'md',
  strokeWidth: 1.5
})

const iconPath = computed(() => iconPaths[props.name])

const sizeClass = computed(() => ({
  xs: 'h-3 w-3',
  sm: 'h-4 w-4',
  md: 'h-5 w-5',
  lg: 'h-6 w-6',
  xl: 'h-8 w-8'
}[props.size]))
</script>
