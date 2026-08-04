<template>
  <BaseDialog
    :show="show"
    :title="customer ? `${customerLabel} · ${t('credit.ledgerTitle')}` : t('credit.ledgerTitle')"
    width="extra-wide"
    @close="emit('close')"
  >
    <div class="space-y-4">
      <!-- 敞口概览：记完一笔立刻能看到额度变化，不必关弹窗回列表 -->
      <div v-if="customer" class="grid grid-cols-3 gap-3 rounded-lg bg-gray-50 p-3 dark:bg-dark-800/50">
        <div>
          <p class="text-xs text-gray-500">{{ t('credit.limit') }}</p>
          <p class="mt-0.5 font-semibold text-gray-900 dark:text-white">{{ displayMoney(customer.credit_limit) }}</p>
        </div>
        <div>
          <p class="text-xs text-gray-500">{{ t('credit.outstanding') }}</p>
          <p class="mt-0.5 font-semibold text-gray-900 dark:text-white">{{ displayMoney(customer.outstanding) }}</p>
        </div>
        <div>
          <p class="text-xs text-gray-500">{{ t('credit.available') }}</p>
          <p class="mt-0.5 font-semibold" :class="displayMoneyClass(customer.available)">
            {{ displayMoney(customer.available) }}
          </p>
        </div>
      </div>

      <!-- 记一笔：折叠收起，避免每次打开弹窗都被表单占掉半屏 -->
      <div class="rounded-lg border border-gray-200 dark:border-dark-700">
        <button
          type="button"
          class="flex w-full items-center justify-between px-3 py-2 text-sm font-medium text-gray-700 dark:text-dark-300"
          @click="showForm = !showForm"
        >
          <span class="flex items-center gap-1.5">
            <Icon name="plus" size="sm" />
            {{ t('credit.addEntry') }}
          </span>
          <Icon :name="showForm ? 'chevronUp' : 'chevronDown'" size="sm" class="text-gray-400" />
        </button>

        <form v-if="showForm" id="ledger-entry-form" class="space-y-3 border-t border-gray-200 p-3 dark:border-dark-700" @submit.prevent="submitEntry">
          <div class="grid grid-cols-1 gap-3 sm:grid-cols-3">
            <div>
              <label class="input-label">{{ t('credit.entryType') }}</label>
              <!-- 二选一用 segmented 而非下拉：少一次点击，且颜色直接传达方向 -->
              <div role="radiogroup" class="flex rounded-lg bg-gray-100 p-0.5 dark:bg-dark-800">
                <button
                  v-for="opt in ENTRY_TYPES"
                  :key="opt"
                  type="button"
                  role="radio"
                  :aria-checked="form.entry_type === opt"
                  :class="entryTypeClass(opt)"
                  @click="form.entry_type = opt"
                >
                  {{ t(opt === 'advance' ? 'credit.advance' : 'credit.repayment') }}
                </button>
              </div>
            </div>
            <div>
              <label class="input-label">{{ t('credit.amount') }}</label>
              <input v-model.number="form.amount" type="number" step="0.01" min="0.01" class="input" required />
            </div>
            <div>
              <label class="input-label">{{ t('credit.occurredAt') }}</label>
              <input v-model="form.occurredLocal" type="datetime-local" class="input" />
              <p class="mt-1 text-xs text-gray-400">{{ t('credit.occurredAtHint') }}</p>
            </div>
          </div>
          <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <div>
              <label class="input-label">{{ t('credit.note') }}</label>
              <input v-model.trim="form.note" class="input" />
            </div>
            <div>
              <label class="input-label">{{ t('credit.externalRef') }}</label>
              <input v-model.trim="form.external_ref" class="input" :placeholder="t('credit.externalRefHint')" />
            </div>
          </div>
          <p class="rounded-lg bg-amber-50 px-3 py-2 text-xs text-amber-700 dark:bg-amber-900/20 dark:text-amber-400">
            ⓘ {{ t('credit.manualHint') }}
          </p>
          <p v-if="formError" class="rounded-lg bg-red-50 px-3 py-2 text-sm text-red-600 dark:bg-red-900/30 dark:text-red-400">
            {{ formError }}
          </p>
          <div class="flex justify-end">
            <button type="submit" class="btn btn-primary text-sm" :disabled="saving">
              {{ saving ? t('common.loading') : t('credit.submitEntry') }}
            </button>
          </div>
        </form>
      </div>

      <!-- 台账明细：只追加不改删，记错走冲正 -->
      <div class="table-wrapper">
        <table class="table">
          <thead>
            <tr>
              <th>{{ t('credit.occurredAt') }}</th>
              <th>{{ t('credit.entryType') }}</th>
              <th class="text-right">{{ t('credit.amount') }}</th>
              <th>{{ t('credit.note') }}</th>
              <th>{{ t('credit.externalRef') }}</th>
              <th>{{ t('credit.operator') }}</th>
              <th>{{ t('common.actions') }}</th>
            </tr>
          </thead>
          <tbody>
            <TableState :loading="loading" :empty="!entries.length" :colspan="7" icon="clipboard" />
            <tr v-for="e in entries" :key="e.id" :class="e.reversed_of !== null ? 'bg-gray-50/60 dark:bg-dark-800/25' : ''">
              <td class="text-xs">{{ fmtDateTime(e.occurred_at) }}</td>
              <td>
                <span class="badge" :class="e.entry_type === 'advance' ? 'badge-warning' : 'badge-success'">
                  {{ t(e.entry_type === 'advance' ? 'credit.advance' : 'credit.repayment') }}
                </span>
              </td>
              <td class="text-right font-semibold" :class="signClass(e)">{{ signedAmount(e) }}</td>
              <td class="max-w-[180px] truncate text-sm" :title="e.note">{{ e.note || '-' }}</td>
              <td class="font-mono text-xs text-gray-500">{{ e.external_ref || '-' }}</td>
              <td class="text-xs text-gray-500">{{ e.operator || '-' }}</td>
              <td>
                <!-- 冲正分录本身不可再冲正（后端同样拒绝，这里只是不给入口） -->
                <button
                  v-if="e.reversed_of === null"
                  class="flex items-center gap-1 rounded-lg px-2 py-1 text-xs text-gray-500 transition-colors hover:bg-amber-50 hover:text-amber-600 disabled:cursor-not-allowed disabled:opacity-50 dark:hover:bg-amber-900/20 dark:hover:text-amber-400"
                  :disabled="reversingId === e.id"
                  @click="askReverse(e)"
                >
                  <Icon name="swap" size="xs" />
                  {{ t('credit.reverse') }}
                </button>
                <span v-else class="text-xs text-gray-400">{{ t('credit.isReversal', { id: e.reversed_of }) }}</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <Pagination :page="page" :pages="pages" :total="total" @change="goPage" />
    </div>

    <ConfirmDialog
      :show="!!pendingReverse"
      :title="t('credit.reverseTitle')"
      :message="t('credit.reverseConfirm', { amount: fmtMoney(pendingReverse?.amount), id: pendingReverse?.id })"
      danger
      @confirm="doReverse"
      @cancel="pendingReverse = null"
    />
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, reactive, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { creditApi } from '@/api/credit'
import { errorMessage } from '@/api/client'
import { fmtMoney, fmtDateTime } from '@/utils/format'
import { usePrivacyMoney } from '@/composables/usePrivacyMoney'
import { useAppStore } from '@/stores/app'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import TableState from '@/components/common/TableState.vue'
import Pagination from '@/components/Pagination.vue'
import Icon from '@/components/icons/Icon.vue'
import type { CreditCustomer, CreditLedgerEntry, LedgerEntryType } from '@/types/credit'

const props = defineProps<{
  show: boolean
  customer: CreditCustomer | null
}>()

const emit = defineEmits<{
  (e: 'close'): void
  /** 敞口变化后回传最新客户，由父组件就地更新列表行，省一次整表刷新 */
  (e: 'updated', customer: CreditCustomer): void
}>()

const { t } = useI18n()
const app = useAppStore()
const { displayMoney, displayMoneyClass } = usePrivacyMoney()

const PAGE_SIZE = 20

const entries = ref<CreditLedgerEntry[]>([])
const loading = ref(false)
const saving = ref(false)
const page = ref(1)
const pages = ref(1)
const total = ref(0)
const showForm = ref(false)
const formError = ref('')
const reversingId = ref<number | null>(null)
const pendingReverse = ref<CreditLedgerEntry | null>(null)

const customerLabel = computed(() => {
  const c = props.customer
  if (!c) return ''
  return c.display_name || c.sub2api_user_id
})

const ENTRY_TYPES: LedgerEntryType[] = ['advance', 'repayment']

const form = reactive({
  entry_type: 'advance' as LedgerEntryType,
  amount: null as number | null,
  /** datetime-local 的本地时间字符串；空 = 用后端当前时间 */
  occurredLocal: '',
  note: '',
  external_ref: ''
})

// 必须写完整字面量：Tailwind 扫描源码文本提取类名，拼接出来的类名会被漏掉
const SEG_BASE = 'flex-1 rounded-md px-3 py-1.5 text-sm font-medium transition-colors'
const SEG_ADVANCE = 'bg-white text-amber-600 shadow-sm dark:bg-dark-700 dark:text-amber-400'
const SEG_REPAYMENT = 'bg-white text-emerald-600 shadow-sm dark:bg-dark-700 dark:text-emerald-400'
const SEG_IDLE = 'text-gray-500 hover:text-gray-700 dark:text-dark-400 dark:hover:text-dark-200'

function entryTypeClass(opt: LedgerEntryType): string {
  if (form.entry_type !== opt) return `${SEG_BASE} ${SEG_IDLE}`
  return `${SEG_BASE} ${opt === 'advance' ? SEG_ADVANCE : SEG_REPAYMENT}`
}

function resetForm() {
  form.entry_type = 'advance'
  form.amount = null
  form.occurredLocal = ''
  form.note = ''
  form.external_ref = ''
  formError.value = ''
}

/** 金额带方向号：垫付为正（欠得更多），回款为负 */
function signedAmount(e: CreditLedgerEntry): string {
  const sign = e.entry_type === 'advance' ? '+' : '−'
  return `${sign}${displayMoney(e.amount)}`
}

function signClass(e: CreditLedgerEntry): string {
  // 垫付是运营方掏钱（风险敞口上升）标暖色，回款是钱回来了标绿
  return e.entry_type === 'advance'
    ? 'text-amber-600 dark:text-amber-400'
    : 'text-emerald-600 dark:text-emerald-400'
}

async function loadLedger() {
  if (!props.customer) return
  loading.value = true
  try {
    const res = await creditApi.listLedger(props.customer.id, page.value, PAGE_SIZE)
    entries.value = res.items || []
    pages.value = res.pages
    total.value = res.total
  } catch (e) {
    app.showError(errorMessage(e))
    entries.value = []
  } finally {
    loading.value = false
  }
}

function goPage(p: number) {
  page.value = p
  loadLedger()
}

/**
 * datetime-local 的值是无时区的本地时间，直接传给后端会被当 UTC 解析。
 * 用 Date 转成带偏移的 ISO 串，让后端 time.Parse(RFC3339) 拿到正确时刻。
 */
function toRFC3339(local: string): string {
  if (!local) return ''
  const d = new Date(local)
  if (Number.isNaN(d.getTime())) return ''
  return d.toISOString()
}

async function submitEntry() {
  if (!props.customer) return
  formError.value = ''
  const amount = form.amount ?? 0
  if (amount <= 0) {
    formError.value = t('credit.amountPositive')
    return
  }
  saving.value = true
  try {
    const updated = await creditApi.appendEntry(props.customer.id, {
      entry_type: form.entry_type,
      amount,
      occurred_at: toRFC3339(form.occurredLocal),
      note: form.note,
      external_ref: form.external_ref
    })
    app.showSuccess(t('credit.entrySaved'))
    emit('updated', updated)
    resetForm()
    // 新分录在第一页，回到首页才看得到
    page.value = 1
    await loadLedger()
  } catch (e) {
    formError.value = errorMessage(e)
  } finally {
    saving.value = false
  }
}

function askReverse(e: CreditLedgerEntry) {
  pendingReverse.value = e
}

async function doReverse() {
  const target = pendingReverse.value
  pendingReverse.value = null
  if (!target) return
  reversingId.value = target.id
  try {
    const updated = await creditApi.reverseEntry(target.id)
    app.showSuccess(t('credit.reverseOk'))
    emit('updated', updated)
    page.value = 1
    await loadLedger()
  } catch (e) {
    app.showError(errorMessage(e))
  } finally {
    reversingId.value = null
  }
}

// 换客户或重新打开都要从第一页重新拉，否则会看到上一个客户的残留分录
watch(
  () => [props.show, props.customer?.id],
  ([show]) => {
    if (!show) return
    page.value = 1
    showForm.value = false
    resetForm()
    entries.value = []
    loadLedger()
  }
)
</script>
