<template>
  <BaseDialog :show="show" :title="''" width="wide" @close="emit('close')">
    <div v-if="model" class="space-y-5">
      <!-- 头部：模型名 + 平台 + 计费方式 -->
      <div>
        <div class="flex items-center gap-2.5">
          <PlazaModelIcon :model="model.name" />
          <span class="break-all font-mono text-xl font-bold text-gray-900 dark:text-white">
            {{ model.name }}
          </span>
          <button
            type="button"
            class="btn btn-ghost btn-icon btn-sm"
            :title="t('plaza.card.copyName')"
            @click="emit('copy', model.name)"
          >
            <Icon name="copy" size="sm" />
          </button>
        </div>
        <div class="mt-1.5 flex items-center gap-2 text-sm">
          <span class="text-gray-500 dark:text-dark-400">{{ platformText }}</span>
          <span class="text-gray-300 dark:text-dark-600">·</span>
          <span class="text-primary-600 dark:text-primary-400">{{ billingLabel }}</span>
        </div>
      </div>

      <!-- 三指标 -->
      <div class="grid grid-cols-3 divide-x divide-gray-200 rounded-xl border border-gray-200 dark:divide-dark-700 dark:border-dark-700">
        <div class="px-4 py-3">
          <div class="flex items-center gap-1.5 text-xs text-gray-500 dark:text-dark-400">
            <Icon name="bolt" size="xs" />
            {{ t('plaza.detail.tps') }}
          </div>
          <div class="mt-1 font-mono text-lg font-semibold text-gray-900 dark:text-white">
            {{ formatThroughput(model.metric?.tokens_per_second) }}
          </div>
        </div>
        <div class="px-4 py-3">
          <div class="flex items-center gap-1.5 text-xs text-gray-500 dark:text-dark-400">
            <Icon name="clock" size="xs" />
            {{ t('plaza.detail.avgLatency') }}
          </div>
          <div class="mt-1 font-mono text-lg font-semibold text-gray-900 dark:text-white">
            {{ fmtMs(model.metric?.avg_duration_ms) }}
          </div>
        </div>
        <div class="px-4 py-3">
          <div class="flex items-center gap-1.5 text-xs text-gray-500 dark:text-dark-400">
            <Icon name="checkCircle" size="xs" />
            {{ t('plaza.detail.successRate') }}
          </div>
          <div class="mt-1 font-mono text-lg font-semibold" :class="successRateClass">
            {{ successRateText }}
          </div>
        </div>
      </div>

      <!-- 定价 -->
      <section class="rounded-xl border border-gray-200 p-4 dark:border-dark-700">
        <h4 class="mb-3 text-sm font-semibold text-gray-900 dark:text-white">
          {{ t('plaza.detail.pricing') }}
        </h4>

        <!-- 基础价格 -->
        <div class="mb-4">
          <div class="mb-2 text-xs text-gray-500 dark:text-dark-400">
            {{ t('plaza.detail.basePrice') }}
          </div>
          <template v-if="basePrice">
            <div class="grid grid-cols-1 gap-2 sm:grid-cols-2">
              <div class="rounded-lg border border-gray-200 px-3 py-2.5 dark:border-dark-700">
                <div class="text-xs text-gray-500 dark:text-dark-400">{{ t('plaza.card.input') }}</div>
                <div class="mt-1 font-mono text-lg font-semibold text-gray-900 dark:text-white">
                  {{ formatScaled(basePrice.input, 1_000_000) }}
                  <span class="ml-0.5 text-xs font-normal text-gray-400">/ 1M</span>
                </div>
              </div>
              <div class="rounded-lg border border-gray-200 px-3 py-2.5 dark:border-dark-700">
                <div class="text-xs text-gray-500 dark:text-dark-400">{{ t('plaza.card.output') }}</div>
                <div class="mt-1 font-mono text-lg font-semibold text-gray-900 dark:text-white">
                  {{ formatScaled(basePrice.output, 1_000_000) }}
                  <span class="ml-0.5 text-xs font-normal text-gray-400">/ 1M</span>
                </div>
              </div>
            </div>
            <div class="mt-2 space-y-1 rounded-lg bg-gray-50 px-3 py-2 dark:bg-dark-900/40">
              <div class="flex items-center justify-between text-xs">
                <span class="text-gray-500 dark:text-dark-400">{{ t('plaza.card.cacheRead') }}</span>
                <span class="font-mono text-gray-700 dark:text-dark-200">
                  {{ formatScaled(basePrice.cache_read, 1_000_000) }}
                  <span class="text-gray-400">/ 1M</span>
                </span>
              </div>
              <div class="flex items-center justify-between text-xs">
                <span class="text-gray-500 dark:text-dark-400">{{ t('plaza.card.cacheWrite') }}</span>
                <span class="font-mono text-gray-700 dark:text-dark-200">
                  {{ formatScaled(basePrice.cache_write, 1_000_000) }}
                  <span class="text-gray-400">/ 1M</span>
                </span>
              </div>
            </div>
          </template>
          <p v-else class="text-xs text-gray-400 dark:text-dark-500">{{ t('plaza.detail.noBasePrice') }}</p>
        </div>

        <!-- 按分组定价 -->
        <div v-if="groupRows.length">
          <div class="mb-2 text-xs text-gray-500 dark:text-dark-400">
            {{ t('plaza.detail.groupPricing') }}
          </div>
          <div class="overflow-x-auto">
            <table class="w-full text-sm">
              <thead>
                <tr class="border-b border-gray-200 text-xs text-gray-500 dark:border-dark-700 dark:text-dark-400">
                  <th class="py-2 text-left font-normal">{{ t('plaza.filters.groups') }}</th>
                  <th class="py-2 text-right font-normal">{{ t('plaza.detail.rate') }}</th>
                  <th class="py-2 text-right font-normal">{{ t('plaza.card.input') }}</th>
                  <th class="py-2 text-right font-normal">{{ t('plaza.card.output') }}</th>
                  <th class="py-2 text-right font-normal">{{ t('plaza.detail.cache') }}</th>
                  <th class="py-2 text-right font-normal">{{ t('plaza.card.cacheWrite') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr
                  v-for="row in groupRows"
                  :key="row.id"
                  class="border-b border-gray-100 last:border-b-0 dark:border-dark-700/60"
                >
                  <td class="py-2.5 text-primary-600 dark:text-primary-400">{{ row.name }}</td>
                  <td class="py-2.5 text-right font-mono text-xs text-gray-500 dark:text-dark-400">
                    {{ row.rate }}x
                  </td>
                  <td class="py-2.5 text-right font-mono text-gray-900 dark:text-white">{{ row.input }}</td>
                  <td class="py-2.5 text-right font-mono text-gray-900 dark:text-white">{{ row.output }}</td>
                  <td class="py-2.5 text-right font-mono text-gray-900 dark:text-white">{{ row.cacheRead }}</td>
                  <td class="py-2.5 text-right font-mono text-gray-900 dark:text-white">{{ row.cacheWrite }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <p class="mt-2 text-[11px] text-gray-400 dark:text-dark-500">
            {{ t('plaza.detail.priceUnitHint') }}
          </p>
        </div>
      </section>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PlazaModel, PlazaPrice } from '@/types/plaza'
import { formatScaled, formatThroughput, platformLabel } from '@/utils/plazaModel'
import { fmtMs } from '@/utils/format'
import Icon from '@/components/icons/Icon.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import PlazaModelIcon from './PlazaModelIcon.vue'

const props = defineProps<{
  show: boolean
  model: PlazaModel | null
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'copy', name: string): void
}>()

const { t } = useI18n()

const platformText = computed(() =>
  (props.model?.platforms ?? []).map(platformLabel).join(' / ') || '-'
)

const billingLabel = computed(() => {
  switch (props.model?.billing_mode) {
    case 'per_request':
      return t('plaza.card.billingPerRequest')
    case 'image':
      return t('plaza.card.billingImage')
    default:
      return t('plaza.card.billingToken')
  }
})

const successRateText = computed(() => {
  const r = props.model?.metric?.success_rate
  if (r === null || r === undefined) return '-'
  return `${r.toFixed(2)}%`
})

// 成功率偏低时着色提示（阈值与探测状态条一致）。
const successRateClass = computed(() => {
  const r = props.model?.metric?.success_rate
  if (r === null || r === undefined) return 'text-gray-900 dark:text-white'
  if (r >= 95) return 'text-gray-900 dark:text-white'
  if (r >= 80) return 'text-amber-600 dark:text-amber-400'
  return 'text-red-600 dark:text-red-400'
})

/**
 * 基础价格取官方标准价；模型不在价表里时回落到本站合成价，
 * 避免详情页整块空白。
 */
const basePrice = computed<PlazaPrice | null>(() => {
  if (!props.model) return null
  if (props.model.official_price) return props.model.official_price
  const p = props.model.price
  const hasAny = p.input !== null || p.output !== null || p.cache_read !== null || p.cache_write !== null
  return hasAny ? p : null
})

interface GroupPriceRow {
  id: number
  name: string
  rate: string
  input: string
  output: string
  cacheRead: string
  cacheWrite: string
}

/** 分组定价 = 基础价 × 该分组倍率。 */
const groupRows = computed<GroupPriceRow[]>(() => {
  const base = basePrice.value
  const groups = props.model?.groups ?? []
  if (!base || groups.length === 0) return []

  const scale = (v: number | null, rate: number): string =>
    v === null ? '-' : formatScaled(v * rate, 1_000_000)

  return groups.map((g) => ({
    id: g.id,
    name: g.name,
    // 去掉多余小数：1.00 → 1，0.050 → 0.05
    rate: String(Number(g.rate_multiplier.toFixed(4))),
    input: scale(base.input, g.rate_multiplier),
    output: scale(base.output, g.rate_multiplier),
    cacheRead: scale(base.cache_read, g.rate_multiplier),
    cacheWrite: scale(base.cache_write, g.rate_multiplier)
  }))
})
</script>
