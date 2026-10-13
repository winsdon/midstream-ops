<template>
  <div class="min-w-0 rounded-xl border border-gray-200 dark:border-dark-600">
    <div class="flex flex-wrap items-center gap-2 border-b border-gray-100 px-3 py-2 dark:border-dark-700">
      <span class="text-xs text-gray-500 dark:text-dark-400">{{ t('pelican.selectedAccounts', { count: selected.length }) }}</span>
      <button type="button" class="btn btn-ghost btn-sm !px-2" :disabled="disabled || !accounts.length" @click="emit('select', visible.map(account => account.id))">{{ t('pelican.selectAll') }}</button>
      <button type="button" class="btn btn-ghost btn-sm !px-2" :disabled="disabled || !selected.length" @click="emit('clear')">{{ t('pelican.clearAll') }}</button>
    </div>
    <div v-if="accounts.length > 6" class="border-b border-gray-100 px-3 py-2 dark:border-dark-700">
      <input v-model="query" type="search" class="input !py-1.5 text-sm" :placeholder="t('pelican.searchAccount')" :disabled="disabled" :aria-label="t('pelican.searchAccount')" />
    </div>
    <ul v-if="visible.length" class="max-h-56 overflow-y-auto p-1" role="group" :aria-label="t('pelican.account')">
      <li v-for="account in visible" :key="account.id">
        <label class="flex cursor-pointer items-center gap-2 rounded-lg px-2 py-1.5 hover:bg-gray-50 dark:hover:bg-dark-700">
          <!--
            复选框：让浏览器自己先切换（原生行为，点哪儿都跟手），勾选态最终以 selected 为准
            回写一次 —— 到上限等父级不接受的情况把浏览器翻过去的状态拨回来，避免出现
            「界面勾着但不会提交」的假状态。
          -->
          <input
            type="checkbox"
            class="mt-0.5"
            :checked="selected.includes(account.id)"
            :disabled="disabled"
            @change="onChange(account.id, $event)"
          />
          <span class="min-w-0 flex-1">
            <span class="block truncate text-sm text-gray-900 dark:text-white" :title="account.name">{{ account.name }}</span>
            <span class="block truncate text-xs text-gray-500 dark:text-dark-400">{{ account.platform }} · {{ pelicanHost(account.base_url) }}</span>
          </span>
        </label>
      </li>
    </ul>
    <p v-else class="px-3 py-4 text-center text-xs text-gray-500 dark:text-dark-400">
      {{ accounts.length ? t('common.noOptionsFound') : t('pelican.noAccountsInGroup') }}
    </p>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PelicanAccount } from '@/types/pelican'
import { pelicanHost } from '@/utils/pelican'

const props = defineProps<{
  /** 已经按归属分组过滤好的可选账号。 */
  accounts: PelicanAccount[]
  /** 当前勾选的账号 id。 */
  selected: number[]
  disabled?: boolean
}>()
const emit = defineEmits<{ toggle: [id: number]; select: [ids: number[]]; clear: [] }>()
const { t } = useI18n()
const query = ref('')

function onChange(id: number, event: Event) {
  const input = event.target as HTMLInputElement
  emit('toggle', id)
  // nextTick 之后再按模型回写：父级同步更新状态，这一步只负责把 DOM 拉回一致。
  void nextTick(() => { input.checked = props.selected.includes(id) })
}

const visible = computed(() => {
  const keyword = query.value.trim().toLowerCase()
  if (!keyword) return props.accounts
  return props.accounts.filter(account => `${account.name} ${account.platform} ${pelicanHost(account.base_url)}`.toLowerCase().includes(keyword))
})
</script>
