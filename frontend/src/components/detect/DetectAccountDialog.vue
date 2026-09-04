<template>
  <BaseDialog :show="show" :title="t('detect.pickAccounts')" width="wide" @close="emit('close')">
    <div class="space-y-3">
      <div class="flex rounded-xl bg-gray-100 p-1 dark:bg-dark-800">
        <button
          v-for="tab in TABS"
          :key="tab"
          type="button"
          class="flex-1 rounded-lg px-3 py-1.5 text-xs font-medium transition-colors"
          :class="
            mode === tab
              ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-700 dark:text-white'
              : 'text-gray-500 hover:text-gray-700 dark:text-dark-400 dark:hover:text-dark-200'
          "
          @click="emit('update:mode', tab)"
        >
          {{ tab === 'account' ? t('detect.tabAccount') : t('detect.tabManual') }}
        </button>
      </div>

      <template v-if="mode === 'account'">
        <div class="relative">
          <Icon
            name="search"
            size="sm"
            class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400"
          />
          <input
            v-model.trim="query"
            class="input !w-full !py-2 !pl-9 text-sm"
            :placeholder="t('detect.searchAccount')"
          />
        </div>

        <LoadingState v-if="accountsLoading" :label="t('common.loading')" size="sm" />
        <p v-else-if="accountsError" class="text-xs text-amber-600 dark:text-amber-400">
          {{ accountsError }}
        </p>
        <EmptyState v-else-if="!accounts.length" icon="server" :title="t('detect.accountEmpty')" />
        <EmptyState
          v-else-if="!visibleGroups.length"
          icon="search"
          :title="t('detect.noMatch')"
          :description="t('detect.noMatchHint')"
        />
        <div v-else class="max-h-[26rem] space-y-3 overflow-y-auto">
          <div
            v-for="g in visibleGroups"
            :key="g.name"
            class="rounded-lg border border-gray-200 p-2 dark:border-dark-700"
          >
            <p class="truncate px-1 text-xs font-medium text-gray-600 dark:text-dark-300">
              {{ g.ungrouped ? t('detect.ungrouped') : g.name }}
            </p>
            <label class="mt-1 flex cursor-pointer items-center gap-2 px-1 text-xs text-gray-500">
              <input
                type="checkbox"
                class="checkbox"
                :checked="isDetectGroupAllSelected(g, selectedIds)"
                @change="onToggleGroup(g)"
              />
              {{ t('detect.selectAllGroup', { n: g.accounts.length }) }}
            </label>
            <div class="mt-1 space-y-1">
              <label
                v-for="a in g.accounts"
                :key="`${g.name}-${a.account_id}`"
                class="flex cursor-pointer items-center justify-between gap-2 rounded px-2 py-1 text-sm hover:bg-gray-50 dark:hover:bg-dark-800"
                :class="atCap && !selectedIds.includes(a.account_id) ? 'opacity-50' : ''"
              >
                <span class="flex min-w-0 items-center gap-2">
                  <input
                    type="checkbox"
                    class="checkbox"
                    :checked="selectedIds.includes(a.account_id)"
                    :disabled="atCap && !selectedIds.includes(a.account_id)"
                    @change="toggleOne(a.account_id)"
                  />
                  <span class="truncate">{{ a.account_name }}</span>
                </span>
                <span class="flex shrink-0 items-center gap-1.5 text-xs text-gray-400">
                  <template v-if="a.provider_name">{{ a.provider_name }}</template>
                  <template v-else>{{ a.base_url || '—' }}</template>
                </span>
              </label>
            </div>
          </div>
        </div>
      </template>

      <div v-else class="space-y-3">
        <slot name="manual" />
      </div>
    </div>

    <template #footer>
      <span
        class="mr-auto text-xs"
        :class="atCap ? 'text-amber-600 dark:text-amber-400' : 'text-gray-400'"
      >
        {{ countText }}
      </span>
      <button type="button" class="btn btn-primary" @click="emit('close')">
        {{ t('detect.confirmPick') }}
      </button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import LoadingState from '@/components/common/LoadingState.vue'
import Icon from '@/components/icons/Icon.vue'
import {
  groupDetectAccounts,
  isDetectGroupAllSelected,
  searchDetectAccounts,
  toggleDetectGroup,
  type DetectAccountGroup
} from '@/utils/detectAccounts'
import { MAX_DETECT_TARGETS } from '@/utils/detectModel'
import type { DetectAccount } from '@/types/detect'

const TABS = ['account', 'manual'] as const

const props = defineProps<{
  show: boolean
  accounts: DetectAccount[]
  accountsLoading: boolean
  accountsError: string
  selectedIds: number[]
  mode: 'account' | 'manual'
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'update:mode', mode: 'account' | 'manual'): void
  (e: 'update:selectedIds', ids: number[]): void
}>()

const { t } = useI18n()
const query = ref('')

watch(
  () => props.show,
  (open) => {
    if (open) query.value = ''
  }
)

const grouped = computed(() => groupDetectAccounts(props.accounts))
const visibleGroups = computed(() => searchDetectAccounts(grouped.value, query.value))
const atCap = computed(() => props.selectedIds.length >= MAX_DETECT_TARGETS)

const visibleIdSet = computed(() => {
  const ids = new Set<number>()
  for (const g of visibleGroups.value) {
    for (const a of g.accounts) ids.add(a.account_id)
  }
  return ids
})

const hiddenCount = computed(
  () => props.selectedIds.filter((id) => !visibleIdSet.value.has(id)).length
)

const countText = computed(() => {
  if (atCap.value) return t('detect.atMaxTargets', { n: MAX_DETECT_TARGETS })
  if (props.mode === 'account' && hiddenCount.value > 0) {
    return t('detect.selectedHidden', {
      total: props.selectedIds.length,
      visible: props.selectedIds.length - hiddenCount.value,
      hidden: hiddenCount.value
    })
  }
  return t('detect.selectedTargets', { n: props.selectedIds.length })
})

function onToggleGroup(g: DetectAccountGroup): void {
  emit('update:selectedIds', toggleDetectGroup(g, props.selectedIds, MAX_DETECT_TARGETS))
}

function toggleOne(id: number): void {
  if (props.selectedIds.includes(id)) {
    emit(
      'update:selectedIds',
      props.selectedIds.filter((x) => x !== id)
    )
    return
  }
  if (atCap.value) return
  emit('update:selectedIds', [...props.selectedIds, id])
}
</script>
