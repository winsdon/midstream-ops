<template>
  <div v-if="pages > 1" class="flex items-center justify-between border-t border-gray-200 px-4 py-3 dark:border-dark-800">
    <span class="text-sm text-gray-500">{{ t('common.total', { n: total }) }}</span>
    <div class="flex items-center gap-2">
      <button class="btn btn-secondary !px-2.5 !py-1 text-xs" :disabled="page <= 1" @click="go(page - 1)">
        {{ t('common.prev') }}
      </button>
      <span class="text-sm text-gray-600 dark:text-dark-400">{{ page }} / {{ pages }}</span>
      <button class="btn btn-secondary !px-2.5 !py-1 text-xs" :disabled="page >= pages" @click="go(page + 1)">
        {{ t('common.next') }}
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
const { t } = useI18n()
const props = defineProps<{ page: number; pages: number; total: number }>()
const emit = defineEmits<{ (e: 'change', page: number): void }>()
function go(p: number) {
  if (p >= 1 && p <= props.pages) emit('change', p)
}
</script>
