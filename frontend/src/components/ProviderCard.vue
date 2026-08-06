<template>
  <div
    class="card relative flex flex-col p-5"
    :class="lowBalance ? 'border-red-300 bg-red-50/40 dark:border-red-800 dark:bg-red-900/10' : ''"
  >
    <!-- 头部：头像 + 名称 + 平台 + 状态 -->
    <div class="flex items-start justify-between gap-3">
      <div class="flex min-w-0 items-center gap-3">
        <div
          class="flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-xl border text-lg font-bold"
          :class="logoClass"
        >{{ initial }}</div>
        <div class="min-w-0">
          <a
            v-if="provider.base_url"
            :href="provider.base_url" target="_blank" rel="noopener noreferrer"
            class="block truncate font-semibold text-gray-900 transition-colors hover:text-primary-600 dark:text-white dark:hover:text-primary-400"
            :title="provider.name"
          >{{ provider.name }}</a>
          <span v-else class="block truncate font-semibold text-gray-900 dark:text-white">{{ provider.name }}</span>
          <span class="mt-0.5 inline-block rounded-md bg-primary-50 px-2 py-0.5 text-[10px] font-bold uppercase tracking-wider text-primary-700 dark:bg-primary-900/30 dark:text-primary-300">
            {{ provider.platform || 'sub2api' }}
          </span>
        </div>
      </div>
      <div class="flex flex-wrap items-center justify-end gap-1.5">
        <Badge v-if="provider.self_operated" variant="success">
          {{ t('provider.selfOperated') }}
        </Badge>
        <Badge v-if="lowBalance" variant="danger">
          {{ t('provider.lowBalanceAlert') }}
        </Badge>
        <Badge :variant="statusVariant">{{ statusLabel }}</Badge>
      </div>
    </div>

    <!-- 三指标格：主显 CNY、副显 USD；充值倍率无效时降级为 USD 主显 -->
    <div class="mt-4 grid grid-cols-3 gap-3">
      <MetricCell :label="t('provider.balance')" :usd="provider.last_balance" :rate="rechargeRate" tone="primary" />
      <MetricCell :label="t('provider.todayCost')" :usd="provider.today_cost" :rate="rechargeRate" tone="warning" />
      <MetricCell :label="t('provider.totalCost')" :usd="provider.total_cost" :rate="rechargeRate" tone="muted" />
    </div>

    <!-- 错误提示 -->
    <div
      v-if="provider.last_balance_error"
      class="mt-3 flex items-start gap-2 rounded-xl border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-700 dark:border-amber-900/40 dark:bg-amber-900/20 dark:text-amber-300"
    >
      <Icon name="exclamationTriangle" size="sm" class="mt-0.5 flex-shrink-0" />
      <span class="line-clamp-2">{{ provider.last_balance_error }}</span>
    </div>
    <div
      v-else-if="provider.login_cooldown_until"
      class="mt-3 rounded-xl border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-700 dark:border-amber-900/40 dark:bg-amber-900/20 dark:text-amber-300"
    >
      {{ t('provider.loginCooldown', { time: provider.login_cooldown_until }) }}
    </div>

    <!-- 查看可用分组 -->
    <button class="btn btn-secondary mt-4 w-full text-sm" @click="$emit('groups', provider)">
      {{ t('provider.viewGroups') }}
    </button>

    <!-- 底部：更新时间 + 操作 -->
    <div class="mt-4 flex items-end justify-between border-t border-gray-100 pt-3 dark:border-dark-800">
      <div>
        <p class="text-[10px] text-gray-400">{{ t('provider.updatedAt') }}</p>
        <p class="text-xs text-gray-500 dark:text-dark-400">
          {{ provider.last_balance_at || t('provider.neverSynced') }}
        </p>
      </div>
      <div class="flex items-center gap-1">
        <button
          v-if="provider.balance_type === 'sub2api'"
          class="icon-btn" :title="t('provider.refresh')" :disabled="refreshing"
          @click="$emit('refresh', provider)"
        >
          <Icon name="refresh" size="sm" :class="refreshing ? 'animate-spin' : ''" />
        </button>
        <!-- 运营成本仅自营站可录：非自营站成本已由上游实扣完整表达 -->
        <button
          v-if="provider.self_operated"
          class="icon-btn" :title="t('opcost.title')"
          @click="$emit('opcost', provider)"
        >
          <Icon name="creditCard" size="sm" />
        </button>
        <button class="icon-btn" :title="t('provider.siteSettings')" @click="$emit('settings', provider)">
          <Icon name="cog" size="sm" />
        </button>
        <button class="icon-btn" :title="t('provider.linkAccounts')" @click="$emit('link', provider)">
          <Icon name="link" size="sm" />
        </button>
        <button class="icon-btn" :title="t('common.edit')" @click="$emit('edit', provider)">
          <Icon name="edit" size="sm" />
        </button>
        <button class="icon-btn icon-btn-danger" :title="t('common.delete')" @click="$emit('delete', provider)">
          <Icon name="trash" size="sm" />
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Provider } from '@/types'
import Icon from '@/components/icons/Icon.vue'
import Badge from '@/components/common/Badge.vue'
import MetricCell from '@/components/MetricCell.vue'
import { isLowBalance, providerStatus, type ProviderStatus } from '@/utils/providerModel'

const { t } = useI18n()

const props = defineProps<{
  provider: Provider
  /** 列表下标，用于头像配色轮转 */
  index: number
  refreshing?: boolean
  /** 系统设置中的全局 CNY 阈值，站点未覆盖时使用。 */
  defaultBalanceThreshold?: number
}>()

defineEmits<{
  refresh: [p: Provider]
  settings: [p: Provider]
  link: [p: Provider]
  edit: [p: Provider]
  delete: [p: Provider]
  groups: [p: Provider]
  /** 打开运营成本弹窗（仅自营站会触发） */
  opcost: [p: Provider]
}>()

// 必须写完整字面量：Tailwind 扫描源码文本提取类名，拼接的类名会被漏掉
const LOGO_CLASSES = [
  'border-primary-200 bg-primary-50 text-primary-700 dark:border-primary-900/40 dark:bg-primary-900/30 dark:text-primary-300',
  'border-violet-200 bg-violet-50 text-violet-700 dark:border-violet-900/40 dark:bg-violet-900/30 dark:text-violet-300',
  'border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-900/40 dark:bg-amber-900/30 dark:text-amber-300',
  'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900/40 dark:bg-emerald-900/30 dark:text-emerald-300'
]

const initial = computed(() => props.provider.name.trim().charAt(0).toUpperCase() || 'U')
const logoClass = computed(() => LOGO_CLASSES[props.index % LOGO_CLASSES.length])
const rechargeRate = computed(() => props.provider.recharge_rate || 1)
const lowBalance = computed(() => isLowBalance(props.provider, props.defaultBalanceThreshold))

// 状态推导：登录冷却/连续失败 → 异常；从未采集 → 未同步；否则已连接
// 规则本体在 providerModel.providerStatus，这里只做 status → 样式/文案的映射，
// 避免与列表页的状态筛选各写一套而漂移。
const STATUS_VARIANTS: Record<ProviderStatus, 'gray' | 'danger' | 'success' | 'warning'> = {
  connected: 'success',
  error: 'danger',
  // 待配置凭据是「还没干完的活」而非故障，用 warning 与真实异常区分
  credentialsPending: 'warning',
  pending: 'gray',
  unmonitored: 'gray'
}

const STATUS_LABELS: Record<ProviderStatus, string> = {
  connected: 'provider.statusConnected',
  error: 'provider.statusError',
  credentialsPending: 'provider.statusCredentialsPending',
  pending: 'provider.statusPending',
  unmonitored: 'provider.notMonitored'
}

const status = computed(() => providerStatus(props.provider))
const statusVariant = computed(() => STATUS_VARIANTS[status.value])
const statusLabel = computed(() => t(STATUS_LABELS[status.value]))
</script>

<style scoped>
.icon-btn {
  @apply flex h-8 w-8 items-center justify-center rounded-lg border border-gray-200 text-gray-500 transition-colors;
  @apply hover:border-primary-400 hover:bg-primary-50 hover:text-primary-600;
  @apply dark:border-dark-700 dark:hover:border-primary-500 dark:hover:bg-primary-900/20 dark:hover:text-primary-400;
  @apply disabled:cursor-not-allowed disabled:opacity-50;
}
.icon-btn-danger {
  @apply hover:border-red-400 hover:bg-red-50 hover:text-red-600;
  @apply dark:hover:border-red-500 dark:hover:bg-red-900/20 dark:hover:text-red-400;
}
</style>
