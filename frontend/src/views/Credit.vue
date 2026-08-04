<template>
  <div class="space-y-5">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div>
        <h1 class="text-xl font-bold text-gray-900 dark:text-white">{{ t('credit.title') }}</h1>
        <p class="mt-0.5 text-sm text-gray-500">{{ t('credit.subtitle') }}</p>
      </div>
      <div class="flex items-center gap-2">
        <button class="btn btn-secondary text-sm" :disabled="recalcing" @click="onRecalcAll">
          <Icon name="calculator" size="sm" />
          {{ recalcing ? t('common.loading') : t('credit.recalcAll') }}
        </button>
        <button class="btn btn-primary text-sm" @click="openCreate">
          <Icon name="plus" size="sm" />
          {{ t('credit.newCustomer') }}
        </button>
      </div>
    </div>

    <div class="grid grid-cols-2 gap-4 lg:grid-cols-4">
      <StatCard
        :title="t('credit.statCustomers')"
        :value="summary ? `${summary.granted_count} / ${summary.customer_count}` : '-'"
        icon="users"
      />
      <StatCard :title="t('credit.statTotalLimit')" :value="displayMoney(summary?.total_limit)" icon="creditCard" />
      <StatCard
        :title="t('credit.statOutstanding')"
        :value="displayMoney(summary?.total_outstanding)"
        icon="dollar"
        icon-variant="warning"
      />
      <StatCard
        :title="t('credit.statAtRisk')"
        :value="summary ? `${summary.over_limit_count} / ${summary.warning_count}` : '-'"
        icon="exclamationTriangle"
        :icon-variant="summary && summary.over_limit_count > 0 ? 'danger' : 'success'"
      />
    </div>

    <CreditToolbar v-model:keyword="keyword" v-model:status="status" />

    <div class="card overflow-hidden">
      <div class="table-wrapper">
        <table class="table">
          <thead>
            <tr>
              <SortableTh
                sort-key="name" :active-key="sortKey" :order="sortOrder" @sort="sortBy"
                :label="t('credit.customer')"
              />
              <SortableTh
                class="text-right" align="right"
                sort-key="limit" :active-key="sortKey" :order="sortOrder" @sort="sortBy"
                :label="t('credit.limit')"
              />
              <SortableTh
                class="text-right" align="right"
                sort-key="outstanding" :active-key="sortKey" :order="sortOrder" @sort="sortBy"
                :label="t('credit.outstanding')"
              />
              <SortableTh
                class="text-right" align="right"
                sort-key="available" :active-key="sortKey" :order="sortOrder" @sort="sortBy"
                :label="t('credit.available')"
              />
              <th class="min-w-[9rem]">{{ t('credit.usage') }}</th>
              <SortableTh
                sort-key="last_entry" :active-key="sortKey" :order="sortOrder" @sort="sortBy"
                :label="t('credit.lastEntry')"
              />
              <th>{{ t('common.actions') }}</th>
            </tr>
          </thead>
          <tbody>
            <TableState
              :loading="loading"
              :empty="!customers.length"
              :colspan="7"
              icon="creditCard"
              :title="t('credit.emptyTitle')"
              :description="t('credit.emptyDesc')"
            />
            <tr v-for="c in customers" :key="c.id" :class="c.status === 'archived' ? 'opacity-60' : ''">
              <td>
                <div class="flex items-center gap-2">
                  <div class="min-w-0">
                    <p class="truncate font-medium text-gray-900 dark:text-white">
                      {{ c.display_name || c.sub2api_user_id }}
                    </p>
                    <p class="truncate font-mono text-xs text-gray-500">{{ c.sub2api_user_id }}</p>
                  </div>
                  <span v-if="c.status === 'archived'" class="badge badge-gray shrink-0">
                    {{ t('credit.statusArchived') }}
                  </span>
                </div>
              </td>
              <td class="text-right">{{ displayMoney(c.credit_limit) }}</td>
              <td class="text-right font-semibold">{{ displayMoney(c.outstanding) }}</td>
              <td class="text-right font-semibold" :class="displayMoneyClass(c.available)">
                {{ displayMoney(c.available) }}
              </td>
              <td>
                <CreditUsageBar :ratio="c.usage_ratio" :limit="c.credit_limit" />
              </td>
              <td>
                <!-- 风险 #1：台账是人工录的，久未记账多半是忘了记，而不是真没交易 -->
                <div class="flex items-center gap-1 text-xs" :class="staleClass(c)">
                  <Icon v-if="isStale(c)" name="exclamationTriangle" size="xs" />
                  <span>{{ lastEntryText(c) }}</span>
                </div>
              </td>
              <td>
                <div class="flex items-center gap-1">
                  <button
                    class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700 dark:hover:text-primary-400"
                    @click="openLedger(c)"
                  >
                    <Icon name="clipboard" size="sm" /><span class="text-xs">{{ t('credit.ledger') }}</span>
                  </button>
                  <button
                    class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700 dark:hover:text-primary-400"
                    @click="openKyc(c)"
                  >
                    <Icon name="user" size="sm" /><span class="text-xs">{{ t('credit.kyc.short') }}</span>
                  </button>
                  <button
                    class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700 dark:hover:text-primary-400"
                    @click="openEdit(c)"
                  >
                    <Icon name="edit" size="sm" /><span class="text-xs">{{ t('common.edit') }}</span>
                  </button>
                  <button
                    v-if="c.status === 'active'"
                    class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20 dark:hover:text-red-400"
                    @click="pendingArchive = c"
                  >
                    <Icon name="inbox" size="sm" /><span class="text-xs">{{ t('credit.archive') }}</span>
                  </button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <Pagination :page="page" :pages="pages" :total="total" @change="goPage" />
    </div>

    <BaseDialog
      :show="showForm"
      :title="editing ? t('credit.editCustomer') : t('credit.newCustomer')"
      @close="showForm = false"
    >
      <form id="credit-customer-form" class="space-y-4" @submit.prevent="saveCustomer">
        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <div>
            <label class="input-label">{{ t('credit.sub2apiUserId') }}</label>

            <!-- 客户唯一口径，建档后不可改：改了等于换了个人，历史台账会挂错。
                 编辑态渲染为只读文本而非 disabled 下拉：既保留「不可改」语义，
                 又不必为一个不能操作的控件去加载整份线上用户列表 -->
            <p
              v-if="editing"
              class="input flex items-center !bg-gray-100 font-mono text-sm dark:!bg-dark-900"
            >
              {{ form.sub2api_user_id }}
            </p>

            <!-- 线上库挂了就建不了档是不可接受的，故保留手填兜底 -->
            <input
              v-else-if="userOptionsFailed"
              v-model.trim="form.sub2api_user_id"
              class="input"
              required
              :placeholder="t('credit.userIdManualPlaceholder')"
            />

            <Select
              v-else
              v-model="selectedUserId"
              :options="userOptions"
              :placeholder="usersLoading ? t('common.loading') : t('credit.selectUser')"
            >
              <template #option="{ option }">
                <span class="min-w-0 flex-1">
                  <span class="block truncate text-sm">
                    <span class="font-mono text-gray-500">#{{ (option as UserOpt).userId }}</span>
                    <span class="ml-1.5">{{ (option as UserOpt).email }}</span>
                  </span>
                  <span class="mt-0.5 block truncate text-xs text-gray-400">
                    {{ displayMoney((option as UserOpt).balance) }}
                    <span v-if="(option as UserOpt).status !== 'active'" class="ml-1 text-amber-500">
                      · {{ (option as UserOpt).status }}
                    </span>
                    <span v-if="(option as UserOpt).enrolled" class="ml-1 text-gray-400">
                      · {{ t('credit.alreadyEnrolled') }}
                    </span>
                  </span>
                </span>
              </template>
            </Select>

            <p v-if="!editing && userOptionsFailed" class="mt-1 text-xs text-amber-600 dark:text-amber-400">
              {{ t('credit.userListUnavailable') }}
            </p>
            <p v-else-if="!editing" class="mt-1 text-xs text-gray-400">
              {{ t('credit.enrolledDisabledHint') }}
            </p>
          </div>
          <div>
            <label class="input-label">{{ t('credit.displayName') }}</label>
            <input v-model.trim="form.display_name" class="input" />
          </div>
        </div>
        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <div>
            <label class="input-label">{{ t('credit.email') }}</label>
            <input v-model.trim="form.email" type="email" class="input" />
          </div>
          <div>
            <label class="input-label">{{ t('credit.limit') }} (USD)</label>
            <input v-model.number="form.credit_limit" type="number" step="0.01" min="0" class="input" />
            <p class="mt-1 text-xs text-gray-400">{{ t('credit.limitHint') }}</p>
          </div>
        </div>
        <div>
          <label class="input-label">{{ t('credit.note') }}</label>
          <input v-model.trim="form.note" class="input" />
        </div>
        <div>
          <label class="input-label">{{ t('credit.adminNote') }}</label>
          <textarea v-model.trim="form.admin_note" rows="2" class="input" :placeholder="t('credit.adminNoteHint')" />
        </div>
        <label v-if="editing" class="flex items-center gap-2 text-sm text-gray-600 dark:text-dark-400">
          <input v-model="formArchived" type="checkbox" class="checkbox" />
          {{ t('credit.markArchived') }}
        </label>
        <p v-if="formError" class="rounded-lg bg-red-50 px-3 py-2 text-sm text-red-600 dark:bg-red-900/30 dark:text-red-400">
          {{ formError }}
        </p>
      </form>
      <template #footer>
        <button type="button" class="btn btn-secondary" @click="showForm = false">{{ t('common.cancel') }}</button>
        <button type="submit" form="credit-customer-form" class="btn btn-primary" :disabled="saving">
          {{ saving ? t('common.loading') : t('common.save') }}
        </button>
      </template>
    </BaseDialog>

    <LedgerDialog
      :show="showLedger"
      :customer="ledgerCustomer"
      @close="showLedger = false"
      @updated="onCustomerUpdated"
    />

    <KycDialog :show="showKyc" :customer="kycCustomer" @close="showKyc = false" />

    <ConfirmDialog
      :show="!!pendingArchive"
      :title="t('credit.archiveTitle')"
      :message="t('credit.archiveConfirm', { name: pendingArchive?.display_name || pendingArchive?.sub2api_user_id })"
      danger
      @confirm="doArchive"
      @cancel="pendingArchive = null"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, watch, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { creditApi } from '@/api/credit'
import { errorMessage } from '@/api/client'
import { minutesSince } from '@/utils/format'
import { usePrivacyMoney } from '@/composables/usePrivacyMoney'
import { useAppStore } from '@/stores/app'
import type { SortOrder } from '@/utils/tableSort'
import StatCard from '@/components/common/StatCard.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import TableState from '@/components/common/TableState.vue'
import SortableTh from '@/components/common/SortableTh.vue'
import Select from '@/components/common/Select.vue'
import Pagination from '@/components/Pagination.vue'
import Icon from '@/components/icons/Icon.vue'
import CreditToolbar from '@/components/credit/CreditToolbar.vue'
import CreditUsageBar from '@/components/credit/CreditUsageBar.vue'
import LedgerDialog from '@/components/credit/LedgerDialog.vue'
import KycDialog from '@/components/credit/KycDialog.vue'
import type { CreditCustomer, CreditSummary, CustomerStatus, Sub2apiUserOption } from '@/types/credit'

const { t } = useI18n()
const app = useAppStore()
const { displayMoney, displayMoneyClass } = usePrivacyMoney()

const PAGE_SIZE = 20
/** 超过这个天数没记账就提示，人工台账最大的失效模式是「忘了记」 */
const STALE_DAYS = 30
const MINUTES_PER_DAY = 1440

const customers = ref<CreditCustomer[]>([])
const summary = ref<CreditSummary | null>(null)
const loading = ref(false)
const recalcing = ref(false)
const saving = ref(false)
const page = ref(1)
const pages = ref(1)
const total = ref(0)
const keyword = ref('')
const status = ref<CustomerStatus | null>(null)

/* ---------- 排序（后端） ----------
 *
 * 这张表有分页，故排序必须由后端做：本地排序只能排当前页 20 行，
 * 用户点「敞口降序」看到的会是「这 20 人里敞口最大的」，而真正欠最多的
 * 可能在第 3 页 —— 授信决策不能建立在这种误读上。
 *
 * 复用 SortableTh 的表头交互，只是把 useTableSort 换成一对状态 + 请求触发。
 */
const sortKey = ref('')
const sortOrder = ref<SortOrder>('asc')

function sortBy(key: string) {
  if (sortKey.value === key) {
    sortOrder.value = sortOrder.value === 'asc' ? 'desc' : 'asc'
  } else {
    sortKey.value = key
    sortOrder.value = 'asc'
  }
  // 换排序后停在第 3 页看到的是新排序的第 3 页，语义混乱，回首页
  page.value = 1
  loadCustomers()
}

const showForm = ref(false)
const formError = ref('')
const editing = ref<CreditCustomer | null>(null)
const showLedger = ref(false)
const ledgerCustomer = ref<CreditCustomer | null>(null)
const showKyc = ref(false)
const kycCustomer = ref<CreditCustomer | null>(null)
const pendingArchive = ref<CreditCustomer | null>(null)

const form = reactive({
  sub2api_user_id: '',
  display_name: '',
  email: '',
  note: '',
  admin_note: '',
  credit_limit: 0,
  status: 'active' as CustomerStatus
})

// 归档在表单里是个复选框，比让用户从下拉里选 archived 更直白
const formArchived = computed({
  get: () => form.status === 'archived',
  set: (v: boolean) => {
    form.status = v ? 'archived' : 'active'
  }
})

/* ---------- 建档下拉：从 sub2api 选用户 ---------- */

/**
 * 下拉选项。除 Select 约定的 value/label/disabled 外，附带原始字段供 #option 插槽渲染。
 * 索引签名是 Select 的 options prop 要求的（它接受任意形状的对象数组）。
 */
interface UserOpt {
  value: string
  label: string
  disabled: boolean
  userId: string
  email: string
  balance: number
  status: string
  enrolled: boolean
  [key: string]: unknown
}

const userList = ref<Sub2apiUserOption[]>([])
const usersLoading = ref(false)
/** 线上用户列表不可用：表单降级为手填，否则 PG 挂了就建不了档 */
const userOptionsFailed = ref(false)

/**
 * label 必须同时含 user_id 与 email：Select 的搜索只匹配 label 不匹配 value，
 * #option 插槽只改渲染不改搜索，光靠插槽是搜不到的。
 */
const userOptions = computed<UserOpt[]>(() =>
  userList.value.map((u) => ({
    value: u.id,
    label: `#${u.id} ${u.email}`,
    disabled: u.enrolled,
    userId: u.id,
    email: u.email,
    balance: u.balance,
    status: u.status,
    enrolled: u.enrolled
  }))
)

/**
 * Select 与 form.sub2api_user_id 的桥。
 *
 * 泛型放宽到 Select emit 的联合类型再在 setter 里收敛为 string：
 * 写死 <string> 会与 'update:modelValue' 的签名不兼容。
 */
const selectedUserId = computed<string | number | boolean | null>({
  get: () => form.sub2api_user_id,
  set: (v) => {
    form.sub2api_user_id = v == null ? '' : String(v)
    // 顺带回填联系方式，但不覆盖用户已经填过的内容
    const hit = userList.value.find((u) => u.id === form.sub2api_user_id)
    if (!hit) return
    if (!form.email) form.email = hit.email
    if (!form.display_name) form.display_name = hit.email
  }
})

async function loadUsers() {
  if (userList.value.length) return // 弹窗会反复开，列表没变就不重复请求
  usersLoading.value = true
  userOptionsFailed.value = false
  try {
    const res = await creditApi.sub2apiUsers()
    userList.value = res.items || []
    // 空列表按失败处理：给个能填的输入框，比给个选不了的空下拉有用
    if (!userList.value.length) userOptionsFailed.value = true
  } catch {
    userOptionsFailed.value = true
  } finally {
    usersLoading.value = false
  }
}

/** 距上次记账的天数；从未记过账返回 null */
function daysSinceEntry(c: CreditCustomer): number | null {
  const mins = minutesSince(c.last_entry_at)
  return mins === null ? null : Math.floor(mins / MINUTES_PER_DAY)
}

function isStale(c: CreditCustomer): boolean {
  // 未授信的客户不催记账，本来就没有敞口要跟
  if (c.status !== 'active' || c.credit_limit <= 0) return false
  const days = daysSinceEntry(c)
  return days === null || days >= STALE_DAYS
}

function lastEntryText(c: CreditCustomer): string {
  const days = daysSinceEntry(c)
  if (days === null) return t('credit.neverEntered')
  if (days === 0) return t('credit.today')
  return t('credit.daysAgo', { n: days })
}

function staleClass(c: CreditCustomer): string {
  return isStale(c) ? 'text-amber-600 dark:text-amber-400' : 'text-gray-500'
}

async function loadSummary() {
  try {
    summary.value = await creditApi.summary()
  } catch (e) {
    app.showError(errorMessage(e))
  }
}

async function loadCustomers() {
  loading.value = true
  try {
    const res = await creditApi.listCustomers({
      keyword: keyword.value || undefined,
      status: status.value ?? undefined,
      sort: sortKey.value || undefined,
      order: sortKey.value ? sortOrder.value : undefined,
      page: page.value,
      page_size: PAGE_SIZE
    })
    customers.value = res.items || []
    pages.value = res.pages
    total.value = res.total
  } catch (e) {
    app.showError(errorMessage(e))
    customers.value = []
  } finally {
    loading.value = false
  }
}

function goPage(p: number) {
  page.value = p
  loadCustomers()
}

// 改筛选条件后停在第 3 页会看到空表，必须回首页
watch([keyword, status], () => {
  page.value = 1
  loadCustomers()
})

function openCreate() {
  editing.value = null
  formError.value = ''
  Object.assign(form, {
    sub2api_user_id: '',
    display_name: '',
    email: '',
    note: '',
    admin_note: '',
    credit_limit: 0,
    status: 'active' as CustomerStatus
  })
  showForm.value = true
  loadUsers()
}

function openEdit(c: CreditCustomer) {
  editing.value = c
  formError.value = ''
  Object.assign(form, {
    sub2api_user_id: c.sub2api_user_id,
    display_name: c.display_name,
    email: c.email,
    note: c.note,
    admin_note: c.admin_note,
    credit_limit: c.credit_limit,
    status: c.status
  })
  showForm.value = true
}

function openLedger(c: CreditCustomer) {
  ledgerCustomer.value = c
  showLedger.value = true
}

function openKyc(c: CreditCustomer) {
  kycCustomer.value = c
  showKyc.value = true
}

async function saveCustomer() {
  formError.value = ''
  if (!form.sub2api_user_id) {
    formError.value = t('credit.userIdRequired')
    return
  }
  saving.value = true
  // 先存下来：下面 showForm 关闭后 editing 仍是进入时的值，但依赖隐式时序太脆
  const isCreate = !editing.value
  try {
    const payload = { ...form }
    if (editing.value) {
      await creditApi.updateCustomer(editing.value.id, payload)
    } else {
      await creditApi.createCustomer(payload)
    }
    app.showSuccess(t('credit.customerSaved'))
    showForm.value = false
    // 刚建的这个用户现在已建档，清缓存让下次开弹窗重新拉 enrolled 标记
    if (isCreate) userList.value = []
    // 改额度会变分母，敞口占比与告警档位都可能变，总览必须一起刷
    await Promise.all([loadCustomers(), loadSummary()])
  } catch (e) {
    formError.value = errorMessage(e)
  } finally {
    saving.value = false
  }
}

async function doArchive() {
  const target = pendingArchive.value
  pendingArchive.value = null
  if (!target) return
  try {
    await creditApi.archiveCustomer(target.id)
    app.showSuccess(t('credit.archived'))
    await Promise.all([loadCustomers(), loadSummary()])
  } catch (e) {
    app.showError(errorMessage(e))
  }
}

async function onRecalcAll() {
  recalcing.value = true
  try {
    const res = await creditApi.recalcAll()
    app.showSuccess(t('credit.recalcDone', { n: res.recalculated }))
    await Promise.all([loadCustomers(), loadSummary()])
  } catch (e) {
    app.showError(errorMessage(e))
  } finally {
    recalcing.value = false
  }
}

/** 台账弹窗记完账回传最新客户：就地换行，省一次整表请求 */
function onCustomerUpdated(updated: CreditCustomer) {
  customers.value = customers.value.map((c) => (c.id === updated.id ? updated : c))
  if (ledgerCustomer.value?.id === updated.id) ledgerCustomer.value = updated
  loadSummary()
}

onMounted(() => {
  loadCustomers()
  loadSummary()
})
</script>
