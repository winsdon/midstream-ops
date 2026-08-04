<template>
  <div class="space-y-5">
    <!-- 本站连接状态条 -->
    <div class="card flex flex-wrap items-center justify-between gap-3 p-4">
      <div class="flex items-center gap-3">
        <span class="inline-block h-2.5 w-2.5 rounded-full" :class="self?.configured ? 'bg-emerald-500' : 'bg-gray-300 dark:bg-dark-600'"></span>
        <div>
          <p class="text-sm font-medium text-gray-900 dark:text-white">
            {{ self?.configured ? t('pricing.selfConnected') : t('pricing.selfNotConfigured') }}
          </p>
          <p v-if="self?.configured" class="text-xs text-gray-400">{{ self.base_url }} · {{ self.login_email }}</p>
          <p v-else class="text-xs text-gray-400">{{ t('pricing.selfHint') }}</p>
        </div>
      </div>
      <div class="flex items-center gap-2">
        <button class="btn btn-secondary text-sm" @click="openSelf">{{ t('pricing.configureSelf') }}</button>
        <router-link :to="{ name: 'rates' }" class="btn btn-primary text-sm">{{ t('pricing.goConnect') }}</router-link>
      </div>
    </div>

    <!-- 规则列表 -->
    <div class="card overflow-hidden">
      <div class="table-wrapper">
        <table class="table">
          <thead>
            <tr>
              <th>{{ t('pricing.localGroup') }}</th>
              <th>{{ t('rates.dataSources') }}</th>
              <th>{{ t('pricing.rule') }}</th>
              <th class="text-right">{{ t('rates.referenceRate') }}</th>
              <th class="text-right">{{ t('pricing.currentRate') }}</th>
              <th class="text-right">{{ t('pricing.targetRate') }}</th>
              <th>{{ t('common.status') }}</th>
              <th>{{ t('common.actions') }}</th>
            </tr>
          </thead>
          <tbody>
            <TableState :loading="loading" :empty="!rows.length" :colspan="8" icon="sort" />
            <tr v-for="r in rows" :key="r.pricing.id">
              <td class="font-medium text-gray-900 dark:text-white">
                {{ r.pricing.local_group_name || '#' + r.pricing.local_group_id }}
                <div v-if="r.pricing.auto_enabled" class="mt-0.5">
                  <span class="badge badge-primary !text-[10px]">{{ t('pricing.autoOn') }}</span>
                </div>
              </td>
              <td class="text-xs text-gray-500">
                <div v-for="s in r.pricing.sources" :key="s.provider_id + ':' + s.upstream_group">
                  {{ providerName(s.provider_id) }} / {{ s.upstream_group }}
                  <span v-if="isPrimary(r.pricing, s)" class="text-primary-600 dark:text-primary-400">★</span>
                </div>
              </td>
              <td class="text-xs text-gray-500">
                <div>{{ sourceLabel(r.pricing.price_source) }}</div>
                <div>
                  {{ r.pricing.markup_mode === 'fixed' ? '+' + r.pricing.markup_value : '+' + r.pricing.markup_value + '%' }}
                  <span v-if="r.pricing.min_rate != null || r.pricing.max_rate != null" class="text-gray-400">
                    [{{ r.pricing.min_rate ?? '-' }}, {{ r.pricing.max_rate ?? '-' }}]
                  </span>
                </div>
                <div class="text-gray-400">{{ t('rates.followThreshold') }} {{ r.pricing.follow_threshold }}%</div>
              </td>
              <td class="text-right">{{ r.reference_rate != null ? '×' + fmtRate(r.reference_rate) : '—' }}</td>
              <td class="text-right">{{ r.current_rate != null ? '×' + fmtRate(r.current_rate) : '—' }}</td>
              <td class="text-right font-semibold" :class="r.needs_apply ? 'text-amber-600 dark:text-amber-400' : ''">
                {{ r.target_rate != null ? '×' + fmtRate(r.target_rate) : '—' }}
              </td>
              <td>
                <span v-if="r.pricing.conflict" class="badge badge-danger">{{ t('pricing.conflict') }}</span>
                <span v-else-if="r.needs_apply" class="badge badge-warning">{{ t('pricing.pendingApply') }}</span>
                <span v-else-if="r.target_rate != null" class="badge badge-success">{{ t('pricing.inSync') }}</span>
                <span v-else class="badge badge-gray">—</span>
              </td>
              <td>
                <div class="flex items-center gap-1">
                  <button
                    v-if="r.pricing.conflict"
                    class="btn btn-secondary !px-2 !py-1 text-xs"
                    :title="t('pricing.resolveHint')"
                    @click="resolveConflict(r)"
                  >{{ t('pricing.resolve') }}</button>
                  <button
                    v-else-if="r.needs_apply"
                    class="btn btn-primary !px-2 !py-1 text-xs"
                    :disabled="applying === r.pricing.id"
                    @click="applyOne(r)"
                  >{{ applying === r.pricing.id ? t('common.loading') : t('pricing.apply') }}</button>
                  <button class="p-1 text-gray-400 hover:text-primary-600" :title="t('pricing.history')" @click="openActions(r)">
                    <Icon name="clock" size="sm" />
                  </button>
                  <button class="p-1 text-gray-400 hover:text-red-600" :title="t('common.delete')" @click="onDelete(r)">
                    <Icon name="trash" size="sm" />
                  </button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- 本站连接弹窗 -->
    <BaseDialog :show="showSelf" :title="t('pricing.configureSelf')" @close="showSelf = false">
      <form id="self-form" class="space-y-4" @submit.prevent="saveSelf">
        <p class="rounded-lg bg-amber-50 px-3 py-2 text-xs text-amber-700 dark:bg-amber-900/20 dark:text-amber-300">
          ⚠ {{ t('pricing.selfWarning') }}
        </p>
        <div>
          <label class="input-label">{{ t('provider.baseUrl') }}</label>
          <input v-model.trim="selfForm.base_url" class="input" required placeholder="https://my-sub2api.com" />
        </div>
        <div class="grid grid-cols-2 gap-4">
          <div>
            <label class="input-label">{{ t('provider.loginEmail') }}</label>
            <input v-model.trim="selfForm.email" class="input" required autocomplete="off" />
          </div>
          <div>
            <label class="input-label">{{ t('provider.loginPassword') }}</label>
            <input v-model="selfForm.password" type="password" class="input"
              :placeholder="self?.configured ? t('provider.passwordPlaceholder') : ''" autocomplete="new-password" />
          </div>
        </div>
        <p v-if="selfError" class="rounded-lg bg-red-50 px-3 py-2 text-sm text-red-600 dark:bg-red-900/30 dark:text-red-400">{{ selfError }}</p>
      </form>
      <template #footer>
        <button type="button" class="btn btn-secondary" @click="showSelf = false">{{ t('common.cancel') }}</button>
        <button type="submit" form="self-form" class="btn btn-primary" :disabled="savingSelf">
          {{ savingSelf ? t('common.loading') : t('pricing.saveAndVerify') }}
        </button>
      </template>
    </BaseDialog>

    <!-- 调价历史 -->
    <BaseDialog :show="showActions" :title="t('pricing.history')" width="wide" @close="showActions = false">
      <LoadingState v-if="actionsLoading" />
      <EmptyState v-else-if="!actions.length" icon="clock" />
      <div v-else class="table-wrapper max-h-96 overflow-y-auto">
        <table class="table">
          <thead>
            <tr>
              <th>{{ t('stability.time') }}</th>
              <th>{{ t('pricing.trigger') }}</th>
              <th class="text-right">{{ t('rate.oldRate') }}</th>
              <th class="text-right">{{ t('rate.newRate') }}</th>
              <th>{{ t('common.status') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="a in actions" :key="a.id">
              <td class="text-xs">{{ a.created_at }}</td>
              <td class="text-xs">{{ a.trigger_by === 'auto' ? t('pricing.triggerAuto') : t('pricing.triggerManual') }}</td>
              <td class="text-right text-gray-400">{{ a.old_rate != null ? '×' + fmtRate(a.old_rate) : '—' }}</td>
              <td class="text-right font-semibold">×{{ fmtRate(a.new_rate) }}</td>
              <td>
                <span class="badge" :class="statusClass(a.status)">{{ statusLabel(a.status) }}</span>
                <div v-if="a.error" class="mt-0.5 max-w-xs truncate text-xs text-gray-400" :title="a.error">{{ a.error }}</div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </BaseDialog>

    <ConfirmDialog
      :show="showDeleteConfirm"
      :title="t('common.delete')"
      :message="pendingDelete ? t('common.confirmDelete', { name: pendingDelete.pricing.local_group_name }) : ''"
      danger
      @confirm="confirmDelete"
      @cancel="showDeleteConfirm = false"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { pricingApi, providerApi } from '@/api'
import { errorMessage } from '@/api/client'
import { useAppStore } from '@/stores/app'
import type { LocalGroupPricing, PricingPreviewRow, PricingSourceRef, Provider, RateActionItem, SelfInfo } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import TableState from '@/components/common/TableState.vue'
import LoadingState from '@/components/common/LoadingState.vue'
import EmptyState from '@/components/common/EmptyState.vue'

const { t } = useI18n()
const app = useAppStore()

const rows = ref<PricingPreviewRow[]>([])
const providers = ref<Provider[]>([])
const loading = ref(false)
const applying = ref<number | null>(null)

function providerName(id: number): string {
  return providers.value.find((p) => p.id === id)?.name || '#' + id
}
function fmtRate(v: number): string {
  return Number.isInteger(v) ? String(v) : v.toFixed(4).replace(/\.?0+$/, '')
}
function isPrimary(p: LocalGroupPricing, s: PricingSourceRef): boolean {
  return p.price_source === 'primary' && p.primary_provider_id === s.provider_id && p.primary_group === s.upstream_group
}
const SOURCE_LABELS: Record<string, string> = {
  primary: 'rates.sourcePrimary',
  lowest: 'rates.sourceLowest',
  highest: 'rates.sourceHighest',
  average: 'rates.sourceAverage'
}
function sourceLabel(s: string): string {
  return t(SOURCE_LABELS[s] || s)
}
function statusClass(s: string): string {
  if (s === 'applied') return 'badge-success'
  if (s === 'failed') return 'badge-danger'
  if (s === 'pending') return 'badge-warning'
  return 'badge-gray'
}
function statusLabel(s: string): string {
  const map: Record<string, string> = {
    applied: 'pricing.statusApplied',
    failed: 'pricing.statusFailed',
    pending: 'pricing.statusPending',
    skipped_conflict: 'pricing.statusConflict',
    skipped_threshold: 'pricing.statusThreshold'
  }
  return t(map[s] || s)
}

async function load() {
  loading.value = true
  try {
    const [r, p] = await Promise.all([pricingApi.rules(), providerApi.list(1, 100)])
    rows.value = r.items || []
    providers.value = p.items
  } catch (e) {
    app.showError(errorMessage(e))
  } finally {
    loading.value = false
  }
}

async function applyOne(r: PricingPreviewRow) {
  applying.value = r.pricing.id
  try {
    const res = await pricingApi.applyRule(r.pricing.id)
    if (res.ok) {
      app.showSuccess(t('pricing.applied'))
    } else {
      app.showError(res.error || t('common.failed'))
    }
    await load()
  } catch (e) {
    app.showError(errorMessage(e))
  } finally {
    applying.value = null
  }
}

async function resolveConflict(r: PricingPreviewRow) {
  try {
    await pricingApi.resolveConflict(r.pricing.id)
    app.showSuccess(t('pricing.resolved'))
    await load()
  } catch (e) {
    app.showError(errorMessage(e))
  }
}

// ---- 本站连接 ----
const self = ref<SelfInfo | null>(null)
const showSelf = ref(false)
const savingSelf = ref(false)
const selfError = ref('')
const selfForm = ref({ base_url: '', email: '', password: '' })

async function loadSelf() {
  try {
    self.value = await pricingApi.getSelf()
  } catch {
    self.value = null
  }
}
function openSelf() {
  selfForm.value = { base_url: self.value?.base_url || '', email: self.value?.login_email || '', password: '' }
  selfError.value = ''
  showSelf.value = true
}
async function saveSelf() {
  savingSelf.value = true
  selfError.value = ''
  try {
    const res = await pricingApi.saveSelf(
      selfForm.value.base_url,
      selfForm.value.email,
      selfForm.value.password || null
    )
    if (!res.ok) {
      selfError.value = res.error || t('common.failed')
      return
    }
    showSelf.value = false
    app.showSuccess(t('pricing.selfSaved'))
    await loadSelf()
  } catch (e) {
    selfError.value = errorMessage(e)
  } finally {
    savingSelf.value = false
  }
}

// ---- 调价历史 ----
const showActions = ref(false)
const actionsLoading = ref(false)
const actions = ref<RateActionItem[]>([])

async function openActions(r: PricingPreviewRow) {
  showActions.value = true
  actionsLoading.value = true
  actions.value = []
  try {
    const res = await pricingApi.actions(r.pricing.id)
    actions.value = res.items || []
  } catch (e) {
    app.showError(errorMessage(e))
  } finally {
    actionsLoading.value = false
  }
}

// ---- 删除 ----
const showDeleteConfirm = ref(false)
const pendingDelete = ref<PricingPreviewRow | null>(null)

function onDelete(r: PricingPreviewRow) {
  pendingDelete.value = r
  showDeleteConfirm.value = true
}
async function confirmDelete() {
  const r = pendingDelete.value
  showDeleteConfirm.value = false
  if (!r) return
  try {
    await pricingApi.deleteRule(r.pricing.id)
    app.showSuccess(t('common.success'))
    await load()
  } catch (e) {
    app.showError(errorMessage(e))
  } finally {
    pendingDelete.value = null
  }
}

onMounted(async () => {
  await Promise.all([load(), loadSelf()])
})
</script>
