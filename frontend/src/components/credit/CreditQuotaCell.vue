<template>
  <div class="min-w-[12rem] space-y-1.5">
    <dl class="space-y-0.5">
      <div class="flex items-baseline justify-between gap-3">
        <dt class="shrink-0 text-xs text-gray-400">{{ t('credit.limit') }}</dt>
        <dd class="tabular-nums text-xs font-medium text-gray-900 dark:text-white">
          {{ displayMoney(customer.credit_limit) }}
        </dd>
      </div>
      <div class="flex items-baseline justify-between gap-3">
        <dt class="shrink-0 text-xs text-gray-400">{{ t('credit.outstanding') }}</dt>
        <dd class="tabular-nums text-xs font-semibold text-gray-900 dark:text-white">
          {{ displayMoney(customer.outstanding) }}
        </dd>
      </div>
      <div class="flex items-baseline justify-between gap-3">
        <dt class="shrink-0 text-xs text-gray-400">{{ t('credit.available') }}</dt>
        <dd class="tabular-nums text-xs font-semibold" :class="displayMoneyClass(customer.available)">
          {{ displayMoney(customer.available) }}
        </dd>
      </div>
    </dl>
    <CreditUsageBar :ratio="customer.usage_ratio" :limit="customer.credit_limit" />
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { usePrivacyMoney } from '@/composables/usePrivacyMoney'
import CreditUsageBar from './CreditUsageBar.vue'
import type { CreditCustomer } from '@/types/credit'

defineProps<{ customer: CreditCustomer }>()

const { t } = useI18n()
const { displayMoney, displayMoneyClass } = usePrivacyMoney()
</script>
