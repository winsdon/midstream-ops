<template>
  <BaseDialog :show="show" :title="t('provider.viewGroups')" width="wide" @close="emit('close')">
    <p class="mb-3 text-sm text-gray-500 dark:text-dark-400">{{ provider?.name }}</p>
    <p v-if="matchHint" class="mb-3 text-xs" :class="matchHintTone">{{ matchHint }}</p>
    <LoadingState v-if="loading" />
    <EmptyState v-else-if="!upstreamGroups.length" icon="sort" :title="t('provider.noGroups')" />
    <div v-else class="max-h-[60vh] space-y-5 overflow-y-auto">
      <div v-for="sec in groupedGroups" :key="sec.platform" class="space-y-2">
        <h4
          v-if="showPlatformSections"
          class="flex items-center gap-2 text-xs font-semibold uppercase tracking-wider text-gray-400 dark:text-dark-400"
        >
          <span class="h-1.5 w-1.5 rounded-full bg-primary-500"></span>
          {{ sec.label }}
          <span class="font-normal normal-case tracking-normal">({{ sec.groups.length }})</span>
        </h4>
        <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div
            v-for="g in sec.groups" :key="g.id"
            class="rounded-xl border p-3 text-left dark:border-dark-700"
            :class="cardClass(g)"
          >
            <div class="flex items-start gap-2">
              <input
                type="checkbox"
                class="checkbox mt-1"
                :checked="selected.has(g.entity_name)"
                :disabled="!canSelect(g)"
                @change="toggle(g)"
              />
              <div class="min-w-0 flex-1">
                <p class="truncate text-sm font-medium text-gray-900 dark:text-white" :title="g.entity_name">
                  {{ g.entity_name }}
                </p>
                <p class="mt-1 inline-block rounded-md bg-primary-50 px-2 py-0.5 text-xs font-semibold text-primary-700 dark:bg-primary-900/30 dark:text-primary-300">
                  ×{{ formatRate(g.rate) }}
                </p>
                <button
                  type="button"
                  class="ml-2 text-xs"
                  :class="matched(g).length ? 'text-primary-600 hover:underline dark:text-primary-400' : 'text-gray-400'"
                  @click="toggleExpand(g.entity_name)"
                >
                  {{ matched(g).length
                    ? t('provider.groupAccounts', { n: matched(g).length })
                    : t('provider.groupNoAccounts') }}
                </button>
              </div>
            </div>
            <ul
              v-if="expanded === g.entity_name"
              class="mt-2 space-y-1 border-t border-gray-100 pt-2 dark:border-dark-800"
            >
              <li v-if="!matched(g).length" class="text-xs text-gray-400">{{ t('provider.noMatchedAccounts') }}</li>
              <li
                v-for="a in matched(g)" :key="a.id"
                class="flex items-center justify-between gap-2 text-xs"
              >
                <span class="truncate text-gray-800 dark:text-dark-200">{{ a.name }}</span>
                <span class="shrink-0 text-gray-400">
                  {{ a.status || '-' }}
                  <span v-if="a.rate_multiplier != null"> · ×{{ formatRate(a.rate_multiplier) }}</span>
                  <span v-if="a.groups?.length"> · {{ a.groups.join(', ') }}</span>
                </span>
              </li>
            </ul>
          </div>
        </div>
      </div>
    </div>

    <template #footer>
      <span class="mr-auto text-xs text-gray-400">{{ t('provider.selectedCount', { n: selected.size }) }}</span>
      <button type="button" class="btn btn-secondary" @click="emit('close')">{{ t('common.close') }}</button>
      <button
        type="button"
        class="btn btn-primary"
        :disabled="!selected.size || !canCreate"
        :title="createTitle"
        @click="openProvision"
      >
        {{ t('provider.createSelected') }}
      </button>
    </template>
  </BaseDialog>

  <ProviderProvisionDialog
    :show="showProvision"
    :provider="provider"
    :groups="selectedGroups"
    :accounts="accounts"
    @close="showProvision = false"
    @done="onProvisionDone"
  />
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { providerApi, provisionApi, rateApi } from '@/api'
import { errorMessage } from '@/api/client'
import { useAppStore } from '@/stores/app'
import BaseDialog from '@/components/common/BaseDialog.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import LoadingState from '@/components/common/LoadingState.vue'
import ProviderProvisionDialog from '@/components/provider/ProviderProvisionDialog.vue'
import { platformLabel } from '@/utils/plazaModel'
import { formatRate, matchAccountsToGroup } from '@/utils/provisionModel'
import type { GroupLinkedAccount, Provider, ProviderAccount, RateSnapshotItem, UpstreamConnection } from '@/types'

const props = defineProps<{
  show: boolean
  provider: Provider | null
}>()

const emit = defineEmits<{
  close: []
  created: []
}>()

const { t } = useI18n()
const app = useAppStore()

const loading = ref(false)
const upstreamGroups = ref<RateSnapshotItem[]>([])
const accounts = ref<ProviderAccount[]>([])
const connections = ref<UpstreamConnection[]>([])
const keyHitsByGroup = ref<Record<string, GroupLinkedAccount[]>>({})
const matchSource = ref('')
const matchError = ref('')
const selected = ref<Set<string>>(new Set())
const expanded = ref<string | null>(null)
const showProvision = ref(false)

const PLATFORM_ORDER = ['anthropic', 'openai', 'gemini', 'antigravity']

const canCreate = computed(() => props.provider?.platform !== 'new-api')
const createTitle = computed(() =>
  canCreate.value ? undefined : t('provider.createDisabledNewApi')
)

const groupedGroups = computed(() => {
  const buckets = upstreamGroups.value.reduce<Record<string, RateSnapshotItem[]>>((acc, g) => {
    const key = g.platform || ''
    ;(acc[key] ||= []).push(g)
    return acc
  }, {})
  return Object.keys(buckets)
    .sort((a, b) => {
      if (!a !== !b) return a ? -1 : 1
      const ia = PLATFORM_ORDER.indexOf(a)
      const ib = PLATFORM_ORDER.indexOf(b)
      if (ia !== ib) return (ia < 0 ? PLATFORM_ORDER.length : ia) - (ib < 0 ? PLATFORM_ORDER.length : ib)
      return a.localeCompare(b)
    })
    .map((platform) => ({
      platform,
      label: platform ? platformLabel(platform) : t('provider.uncategorizedPlatform'),
      groups: buckets[platform]
    }))
})

const showPlatformSections = computed(() => groupedGroups.value.some((s) => s.platform !== ''))

const selectedGroups = computed(() =>
  upstreamGroups.value.filter((g) => selected.value.has(g.entity_name) && canSelect(g))
)

const matchHint = computed(() => {
  if (matchSource.value === 'live_keys') return t('provider.groupMatchLive')
  if (matchSource.value === 'stored_map' && matchError.value) {
    return t('provider.groupMatchStored', { error: matchError.value })
  }
  if (matchSource.value === 'stored_map') return t('provider.groupMatchStoredOk')
  return ''
})
const matchHintTone = computed(() =>
  matchSource.value === 'live_keys'
    ? 'text-gray-400'
    : 'text-amber-600 dark:text-amber-400'
)

function matched(g: RateSnapshotItem) {
  return matchAccountsToGroup(
    accounts.value,
    g.entity_name,
    keyHitsByGroup.value[g.entity_name] || [],
    connections.value.map((c) => ({
      upstream_group: c.upstream_group,
      local_account_id: c.local_account_id
    }))
  )
}

function canSelect(g: RateSnapshotItem): boolean {
  return !g.deleted && canCreate.value
}

function cardClass(g: RateSnapshotItem): string {
  if (g.deleted) return 'border-gray-200 opacity-50'
  return selected.value.has(g.entity_name)
    ? 'border-primary-300 bg-primary-50/40 dark:border-primary-800 dark:bg-primary-900/10'
    : 'border-gray-200'
}

function toggle(g: RateSnapshotItem) {
  if (!canSelect(g)) return
  const next = new Set(selected.value)
  if (next.has(g.entity_name)) next.delete(g.entity_name)
  else next.add(g.entity_name)
  selected.value = next
}

function toggleExpand(name: string) {
  expanded.value = expanded.value === name ? null : name
}

function openProvision() {
  if (!selectedGroups.value.length || !canCreate.value) return
  showProvision.value = true
}

function onProvisionDone() {
  showProvision.value = false
  emit('created')
  void load()
}

async function load() {
  if (!props.provider) return
  loading.value = true
  upstreamGroups.value = []
  accounts.value = []
  connections.value = []
  keyHitsByGroup.value = {}
  matchSource.value = ''
  matchError.value = ''
  try {
    const [rates, accs, conns, grouped] = await Promise.allSettled([
      rateApi.current({ scope: 'upstream', provider_id: props.provider.id }),
      providerApi.accounts(props.provider.id),
      provisionApi.list(),
      providerApi.groupAccounts(props.provider.id)
    ])
    if (rates.status === 'fulfilled') {
      upstreamGroups.value = rates.value.items || []
    } else {
      app.showError(errorMessage(rates.reason))
    }
    if (accs.status === 'fulfilled') {
      accounts.value = accs.value.items || []
    }
    if (conns.status === 'fulfilled') {
      connections.value = (conns.value.items || []).filter((c) => c.provider_id === props.provider!.id)
    }
    if (grouped.status === 'fulfilled') {
      matchSource.value = grouped.value.source || ''
      matchError.value = grouped.value.error || ''
      const next: Record<string, GroupLinkedAccount[]> = {}
      for (const b of grouped.value.items || []) {
        next[b.group] = b.accounts || []
      }
      keyHitsByGroup.value = next
    } else {
      matchError.value = errorMessage(grouped.reason)
    }
  } finally {
    loading.value = false
  }
}

watch(
  () => [props.show, props.provider?.id] as const,
  ([show]) => {
    if (!show) {
      selected.value = new Set()
      expanded.value = null
      showProvision.value = false
      return
    }
    void load()
  }
)
</script>
