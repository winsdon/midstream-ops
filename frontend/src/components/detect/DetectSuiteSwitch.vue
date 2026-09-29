<template>
  <div class="flex flex-wrap items-center gap-x-4 gap-y-2">
    <div class="inline-flex rounded-xl bg-gray-100 p-1 dark:bg-dark-800" role="tablist" :aria-label="t('detect.suiteLabel')">
      <button
        v-for="suite in DETECT_SUITES"
        :key="suite"
        type="button"
        role="tab"
        :aria-selected="modelValue === suite"
        class="flex items-center gap-1.5 rounded-lg px-4 py-1.5 text-sm font-medium transition-colors"
        :class="
          modelValue === suite
            ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-700 dark:text-white'
            : 'text-gray-500 hover:text-gray-700 dark:text-dark-400 dark:hover:text-dark-200'
        "
        @click="emit('update:modelValue', suite)"
      >
        <Icon :name="SUITE_ICON[suite]" size="sm" />
        {{ t(`detect.suite.${suite}`) }}
      </button>
    </div>
    <p class="min-w-0 flex-1 text-xs leading-relaxed text-gray-500 dark:text-dark-400">
      {{ t(`detect.suiteHint.${modelValue}`) }}
    </p>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { IconName } from '@/components/icons/paths'
import { DETECT_SUITES } from '@/utils/detectModel'
import type { DetectSuite } from '@/types/detect'

const { t } = useI18n()

const SUITE_ICON: Record<DetectSuite, IconName> = {
  authenticity: 'shield',
  iq: 'brain'
}

defineProps<{ modelValue: DetectSuite }>()

const emit = defineEmits<{ (e: 'update:modelValue', suite: DetectSuite): void }>()
</script>
