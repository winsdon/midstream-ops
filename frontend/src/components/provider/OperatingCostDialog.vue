<template>
  <BaseDialog
    :show="show"
    :title="provider ? `${provider.name} · ${t('opcost.title')}` : t('opcost.title')"
    width="extra-wide"
    @close="emit('close')"
  >
    <div class="space-y-4">
      <!-- 区间与合计 -->
      <div class="flex flex-wrap items-center gap-2">
        <input v-model="startDate" type="date" class="input !w-auto !py-1.5 text-sm" @change="load" />
        <span class="text-gray-400">~</span>
        <input v-model="endDate" type="date" class="input !w-auto !py-1.5 text-sm" @change="load" />
        <div class="ml-auto text-right">
          <p class="text-xs text-gray-500">{{ t('opcost.periodTotal') }}</p>
          <p class="text-lg font-bold text-gray-900 dark:text-white">{{ displayMoney(total) }}</p>
        </div>
      </div>

      <!-- 口径说明：解释为什么这笔钱要手工记，以及它如何进入利润 -->
      <p class="rounded-lg bg-blue-50 px-3 py-2 text-xs text-blue-700 dark:bg-blue-900/20 dark:text-blue-400">
        ⓘ {{ t('opcost.hint') }}
      </p>

      <!-- 按类别小计：回答「这段时间买号花了多少」 -->
      <div v-if="categorySums.length > 1" class="flex flex-wrap gap-x-4 gap-y-1 rounded-lg bg-gray-50 px-3 py-2 text-xs dark:bg-dark-800/50">
        <span v-for="s in categorySums" :key="s.category" class="text-gray-600 dark:text-dark-300">
          {{ t(categoryLabelKey(s.category)) }}
          <span class="font-semibold text-gray-900 dark:text-white">{{ displayMoney(s.amount) }}</span>
        </span>
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
            {{ t('opcost.addEntry') }}
          </span>
          <Icon :name="showForm ? 'chevronUp' : 'chevronDown'" size="sm" class="text-gray-400" />
        </button>

        <form v-if="showForm" class="space-y-3 border-t border-gray-200 p-3 dark:border-dark-700" @submit.prevent="submit">
          <div class="grid grid-cols-1 gap-3 sm:grid-cols-3">
            <div>
              <label class="input-label">{{ t('opcost.category') }}</label>
              <Select v-model="form.category" :options="categoryOptions" :searchable="false" />
            </div>
            <div>
              <label class="input-label">{{ t('opcost.amount') }}</label>
              <input v-model.number="form.amount" type="number" step="0.01" min="0.01" class="input" required />
            </div>
            <div>
              <label class="input-label">{{ t('opcost.occurredOn') }}</label>
              <input v-model="form.occurred_on" type="date" class="input" />
              <p class="mt-1 text-xs text-gray-400">{{ t('opcost.occurredOnHint') }}</p>
            </div>
          </div>
          <div>
            <label class="input-label">{{ t('opcost.note') }}</label>
            <input v-model.trim="form.note" class="input" :placeholder="t('opcost.notePlaceholder')" />
          </div>
          <p v-if="formError" class="rounded-lg bg-red-50 px-3 py-2 text-sm text-red-600 dark:bg-red-900/30 dark:text-red-400">
            {{ formError }}
          </p>
          <div class="flex justify-end">
            <button type="submit" class="btn btn-primary text-sm" :disabled="saving">
              {{ saving ? t('common.loading') : t('opcost.submitEntry') }}
            </button>
          </div>
        </form>
      </div>

      <!-- 明细 -->
      <div class="table-wrapper">
        <table class="table">
          <thead>
            <tr>
              <th>{{ t('opcost.occurredOn') }}</th>
              <th>{{ t('opcost.category') }}</th>
              <th class="text-right">{{ t('opcost.amount') }}</th>
              <th>{{ t('opcost.note') }}</th>
              <th>{{ t('opcost.operator') }}</th>
              <th>{{ t('common.actions') }}</th>
            </tr>
          </thead>
          <tbody>
            <TableState :loading="loading" :empty="!items.length" :colspan="6" icon="dollar" />
            <tr v-for="it in items" :key="it.id">
              <td class="text-xs">{{ it.occurred_on }}</td>
              <td>
                <Badge :variant="categoryVariant(it.category)">{{ t(categoryLabelKey(it.category)) }}</Badge>
              </td>
              <td class="text-right font-semibold text-gray-900 dark:text-white">{{ displayMoney(it.amount) }}</td>
              <td class="max-w-[220px] truncate text-sm" :title="it.note">{{ it.note || '-' }}</td>
              <td class="text-xs text-gray-500">{{ it.operator || '-' }}</td>
              <td>
                <button
                  class="flex items-center gap-1 rounded-lg px-2 py-1 text-xs text-gray-500 transition-colors hover:bg-red-50 hover:text-red-600 disabled:cursor-not-allowed disabled:opacity-50 dark:hover:bg-red-900/20 dark:hover:text-red-400"
                  :disabled="deletingId === it.id"
                  @click="pendingDelete = it"
                >
                  <Icon name="trash" size="xs" />
                  {{ t('common.delete') }}
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <ConfirmDialog
      :show="!!pendingDelete"
      :title="t('opcost.deleteTitle')"
      :message="t('opcost.deleteConfirm', { amount: fmtMoney(pendingDelete?.amount) })"
      danger
      @confirm="doDelete"
      @cancel="pendingDelete = null"
    />
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, reactive, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { operatingCostApi } from '@/api'
import { errorMessage } from '@/api/client'
import { fmtMoney, todayStr } from '@/utils/format'
import { usePrivacyMoney } from '@/composables/usePrivacyMoney'
import { useAppStore } from '@/stores/app'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import TableState from '@/components/common/TableState.vue'
import Badge from '@/components/common/Badge.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import {
  OPERATING_COST_CATEGORIES,
  categoryLabelKey,
  sumByCategory,
  totalAmount
} from '@/utils/operatingCostModel'
import type { OperatingCost, OperatingCostCategory, Provider } from '@/types'

const props = defineProps<{
  show: boolean
  provider: Provider | null
}>()

const emit = defineEmits<{
  (e: 'close'): void
  /** 金额变化后通知父组件刷新统计口径（成本卡片会变） */
  (e: 'changed'): void
}>()

const { t } = useI18n()
const app = useAppStore()
const { displayMoney } = usePrivacyMoney()

const items = ref<OperatingCost[]>([])
const loading = ref(false)
const saving = ref(false)
const showForm = ref(false)
const formError = ref('')
const deletingId = ref<number | null>(null)
const pendingDelete = ref<OperatingCost | null>(null)

// 区间默认本月至今，与后端 resolveDates 的缺省一致
const startDate = ref(monthStart())
const endDate = ref(todayStr())

function monthStart(): string {
  return todayStr().slice(0, 8) + '01'
}

const form = reactive<{ category: OperatingCostCategory; amount: number | null; occurred_on: string; note: string }>({
  category: 'account',
  amount: null,
  occurred_on: todayStr(),
  note: ''
})

const categoryOptions = computed(() =>
  OPERATING_COST_CATEGORIES.map((value) => ({ value, label: t(categoryLabelKey(value)) }))
)

// 合计从明细算出而非用接口返回值：删除后无需重新请求即可就地更新
const total = computed(() => totalAmount(items.value))
const categorySums = computed(() => sumByCategory(items.value))

const CATEGORY_VARIANT: Record<OperatingCostCategory, 'primary' | 'purple' | 'warning' | 'gray'> = {
  account: 'primary',
  subscription: 'purple',
  server: 'warning',
  other: 'gray'
}

function categoryVariant(category: string) {
  return CATEGORY_VARIANT[category as OperatingCostCategory] ?? 'gray'
}

function resetForm() {
  form.category = 'account'
  form.amount = null
  form.occurred_on = todayStr()
  form.note = ''
  formError.value = ''
}

async function load() {
  const p = props.provider
  if (!p) return
  loading.value = true
  try {
    const res = await operatingCostApi.list(p.id, startDate.value, endDate.value)
    items.value = res.items || []
  } catch (e) {
    app.showError(errorMessage(e))
  } finally {
    loading.value = false
  }
}

async function submit() {
  const p = props.provider
  if (!p || !form.amount) return
  saving.value = true
  formError.value = ''
  try {
    await operatingCostApi.create(p.id, {
      category: form.category,
      amount: form.amount,
      occurred_on: form.occurred_on,
      note: form.note
    })
    resetForm()
    showForm.value = false
    await load()
    emit('changed')
  } catch (e) {
    formError.value = errorMessage(e)
  } finally {
    saving.value = false
  }
}

async function doDelete() {
  const target = pendingDelete.value
  pendingDelete.value = null
  if (!target) return
  deletingId.value = target.id
  try {
    await operatingCostApi.remove(target.id)
    app.showSuccess(t('common.success'))
    await load()
    emit('changed')
  } catch (e) {
    app.showError(errorMessage(e))
  } finally {
    deletingId.value = null
  }
}

// 换站点或重新打开都要重新拉，否则会看到上一个站点的残留明细
watch(
  () => [props.show, props.provider?.id],
  ([show]) => {
    if (!show) return
    showForm.value = false
    resetForm()
    items.value = []
    load()
  }
)
</script>
