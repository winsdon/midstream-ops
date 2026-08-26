<template>
  <div
    class="min-w-[12rem] space-y-1.5"
    :title="scanTitle"
  >
    <dl class="space-y-0.5">
      <div class="flex items-baseline justify-between gap-3">
        <dt class="shrink-0 text-xs text-gray-400">{{ t('credit.userBalance') }}</dt>
        <dd
          class="tabular-nums text-xs font-semibold"
          :class="balanceClass"
        >
          {{ displayMoney(customer.user_balance) }}
        </dd>
      </div>
      <div class="flex items-baseline justify-between gap-3">
        <dt class="shrink-0 text-xs text-gray-400">{{ t('credit.alertThreshold') }}</dt>
        <dd class="tabular-nums text-xs font-medium text-gray-900 dark:text-white">
          {{ thresholdText }}
        </dd>
      </div>
    </dl>
    <div class="min-w-[7rem]">
      <div class="flex items-center justify-between gap-2 text-xs">
        <span :class="privacyMode ? 'text-gray-400' : textClass">{{ ratioLabel }}</span>
      </div>
      <div class="mt-1 h-1.5 w-full overflow-hidden rounded-full bg-gray-200 dark:bg-dark-700">
        <div
          class="h-full rounded-full transition-all duration-300"
          :class="barClass"
          :style="{ width: barWidth }"
        ></div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { fmtDateTime, fmtPct } from '@/utils/format'
import { usePrivacyMoney } from '@/composables/usePrivacyMoney'
import {
  balanceAlertBarClass,
  balanceAlertBarWidth,
  balanceAlertRatio,
  balanceAlertTextClass,
  balanceAlertTone
} from '@/utils/creditMeter'
import type { CreditCustomer } from '@/types/credit'

const props = defineProps<{ customer: CreditCustomer }>()

const { t } = useI18n()
const { displayMoney, privacyMode } = usePrivacyMoney()

const ratio = computed(() =>
  balanceAlertRatio(props.customer.user_balance, props.customer.effective_low_balance_threshold)
)

const tone = computed(() => balanceAlertTone(ratio.value))

const barClass = computed(() => {
  if (privacyMode.value) return 'bg-gray-300 dark:bg-dark-600'
  return balanceAlertBarClass(tone.value)
})

const textClass = computed(() => balanceAlertTextClass(tone.value))

const barWidth = computed(() => {
  if (privacyMode.value) return '0%'
  return `${balanceAlertBarWidth(ratio.value)}%`
})

const ratioLabel = computed(() => {
  if (privacyMode.value) return '****'
  if (ratio.value === null) return '-'
  return fmtPct(ratio.value * 100)
})

const thresholdText = computed(() => {
  const th = props.customer.effective_low_balance_threshold
  if (!(th > 0)) return '-'
  return displayMoney(th)
})

const scanTitle = computed(() =>
  props.customer.user_balance_at
    ? t('credit.userBalanceAt', { time: fmtDateTime(props.customer.user_balance_at) })
    : ''
)

// 跌破标红与条长一样是模拟量泄露，隐私模式下只打码不着色
const balanceClass = computed(() => {
  if (privacyMode.value || !props.customer.below_balance_threshold) {
    return 'text-gray-900 dark:text-white'
  }
  return 'text-red-600 dark:text-red-400'
})
</script>
