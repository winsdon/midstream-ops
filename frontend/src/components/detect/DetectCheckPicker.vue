<template>
  <div class="card">
    <div class="card-header">
      <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('detect.scenario') }}</h2>
      <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">{{ t('detect.scenarioHint') }}</p>
    </div>

    <div class="card-body space-y-4">
      <LoadingState v-if="loading" :label="t('common.loading')" size="sm" />
      <template v-else>
        <div class="grid gap-2 sm:grid-cols-2">
          <button
            v-for="preset in presets"
            :key="preset.id"
            type="button"
            class="rounded-xl border p-3 text-left transition-colors"
            :class="presetCardClass(preset.id)"
            @click="applyPreset(preset)"
          >
            <span class="flex items-center justify-between gap-2">
              <span class="text-sm font-semibold text-gray-900 dark:text-white">{{ preset.title }}</span>
              <Badge v-if="activePreset === preset.id" variant="primary">{{ t('detect.presetCurrent') }}</Badge>
              <Badge v-else-if="tweaked && lastPresetId === preset.id" variant="warning">
                {{ t('detect.presetTweaked') }}
              </Badge>
            </span>
            <span class="mt-1 block text-xs leading-relaxed text-gray-500 dark:text-dark-400">
              {{ t(`detect.presetHint.${preset.id}`) }}
            </span>
            <span class="mt-2 block text-xs text-gray-400">×{{ presetRequestCount(preset) }}</span>
          </button>
        </div>

        <button
          type="button"
          class="flex w-full items-center justify-between rounded-xl border border-gray-200 px-3 py-2 text-xs text-gray-600 dark:border-dark-700 dark:text-dark-300"
          @click="customOpen = !customOpen"
        >
          {{ t('detect.customChecks') }}
          <Icon :name="customOpen ? 'chevronDown' : 'chevronRight'" size="sm" />
        </button>

        <div v-if="customOpen" class="space-y-5">
          <div class="flex items-center gap-1 rounded-xl border border-gray-200 p-1 dark:border-dark-700">
            <button type="button" class="btn btn-ghost btn-sm" @click="applyDefault">
              {{ t('detect.selectDefault') }}
            </button>
            <button type="button" class="btn btn-ghost btn-sm" @click="selectAll">
              {{ t('detect.selectAll') }}
            </button>
            <button type="button" class="btn btn-ghost btn-sm" @click="selectNone">
              {{ t('detect.selectNone') }}
            </button>
          </div>

          <section v-for="group in grouped" :key="group.group" class="space-y-2">
            <h3 class="flex items-center gap-2 text-xs font-semibold uppercase tracking-wider text-gray-400">
              {{ t(`detect.groups.${group.group}`) }}
              <span class="h-px flex-1 bg-gray-100 dark:bg-dark-800"></span>
            </h3>
            <div class="grid gap-2">
              <label
                v-for="check in group.items"
                :key="check.id"
                class="flex cursor-pointer items-start gap-2.5 rounded-xl border p-3 transition-colors"
                :class="cardClass(check.id)"
              >
                <input
                  type="checkbox"
                  class="mt-0.5 h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
                  :checked="effective.has(check.id)"
                  :disabled="isDependencyOnly(check.id)"
                  @change="toggle(check.id)"
                />
                <span class="min-w-0 flex-1">
                  <span class="flex flex-wrap items-center gap-1.5">
                    <span class="text-sm font-medium text-gray-900 dark:text-white">{{ check.title }}</span>
                    <Badge :variant="costVariant(check.cost)">{{ t(`detect.cost${capitalize(check.cost)}`) }}</Badge>
                    <span class="text-xs text-gray-400">×{{ check.requests }}</span>
                  </span>
                  <span v-if="check.note" class="mt-0.5 block text-xs leading-relaxed text-gray-500 dark:text-dark-400">
                    {{ check.note }}
                  </span>
                  <span
                    v-if="isDependencyOnly(check.id)"
                    class="mt-0.5 block text-xs text-primary-600 dark:text-primary-400"
                  >
                    {{ t('detect.autoIncluded') }}
                  </span>
                </span>
              </label>
            </div>
          </section>
        </div>

        <p class="text-xs text-gray-500 dark:text-dark-400">
          {{ t('detect.requestsEstimate', { n: requestEstimate }) }}
        </p>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Badge from '@/components/common/Badge.vue'
import LoadingState from '@/components/common/LoadingState.vue'
import Icon from '@/components/icons/Icon.vue'
import { estimateRequests, groupChecks, matchingPresetId, withDependencies } from '@/utils/detectModel'
import type { DetectCheckMeta, DetectCost, DetectPreset } from '@/types/detect'

const { t } = useI18n()

const props = defineProps<{
  checks: DetectCheckMeta[]
  defaults: string[]
  presets: DetectPreset[]
  loading: boolean
  selected: string[]
  targetCount: number
}>()

const emit = defineEmits<{ (e: 'update:selected', ids: string[]): void }>()

const customOpen = ref(false)
const lastPresetId = ref<string | null>(null)

const selectedSet = computed(() => new Set(props.selected))
const effective = computed(() => withDependencies(props.checks, selectedSet.value))
const grouped = computed(() => groupChecks(props.checks))
const requestEstimate = computed(() =>
  estimateRequests(props.checks, effective.value, props.targetCount)
)
const activePreset = computed(() => matchingPresetId(props.selected, props.presets))
const tweaked = computed(() => !activePreset.value && props.selected.length > 0)

function isDependencyOnly(id: string): boolean {
  return effective.value.has(id) && !selectedSet.value.has(id)
}

function toggle(id: string): void {
  if (isDependencyOnly(id)) return
  const next = new Set(selectedSet.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  emit('update:selected', [...next])
}

function applyPreset(preset: DetectPreset): void {
  lastPresetId.value = preset.id
  customOpen.value = false
  emit('update:selected', [...preset.checks])
}

function applyDefault(): void {
  lastPresetId.value = null
  emit('update:selected', [...props.defaults])
}

function selectAll(): void {
  lastPresetId.value = null
  emit('update:selected', props.checks.map((c) => c.id))
}

function selectNone(): void {
  lastPresetId.value = null
  emit('update:selected', [])
}

function presetRequestCount(preset: DetectPreset): number {
  return estimateRequests(props.checks, withDependencies(props.checks, new Set(preset.checks)), props.targetCount)
}

function presetCardClass(id: string): string {
  if (activePreset.value === id) {
    return 'border-primary-300 bg-primary-50/60 dark:border-primary-700 dark:bg-primary-900/20'
  }
  if (tweaked.value && lastPresetId.value === id) {
    return 'border-amber-300 bg-amber-50/50 dark:border-amber-700 dark:bg-amber-900/20'
  }
  return 'border-gray-200 hover:border-gray-300 dark:border-dark-700 dark:hover:border-dark-600'
}

function cardClass(id: string): string {
  if (selectedSet.value.has(id)) {
    return 'border-primary-300 bg-primary-50/60 dark:border-primary-700 dark:bg-primary-900/20'
  }
  if (effective.value.has(id)) {
    return 'border-primary-200 bg-primary-50/30 dark:border-primary-800 dark:bg-primary-900/10'
  }
  return 'border-gray-200 hover:border-gray-300 dark:border-dark-700 dark:hover:border-dark-600'
}

function costVariant(cost: DetectCost): 'gray' | 'warning' | 'danger' {
  if (cost === 'high') return 'danger'
  if (cost === 'medium') return 'warning'
  return 'gray'
}

function capitalize(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1)
}
</script>
