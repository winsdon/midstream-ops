<template>
  <article class="card card-hover flex flex-col gap-3 p-4">
    <!-- 头部：图标 + 模型名 + 操作 -->
    <div class="flex items-start gap-2.5">
      <PlazaModelIcon :model="model.name" />
      <div class="min-w-0 flex-1">
        <h3 class="truncate font-mono text-sm font-bold text-gray-900 dark:text-white" :title="model.name">
          {{ model.name }}
        </h3>
        <p
          v-if="model.description"
          class="mt-0.5 truncate text-xs text-gray-500 dark:text-dark-400"
          :title="model.description"
        >
          {{ model.description }}
        </p>
        <p v-else class="mt-0.5 text-xs text-gray-400 dark:text-dark-500">
          {{ t('plaza.card.noDescription') }}
        </p>
      </div>
      <div class="flex shrink-0 items-center gap-1">
        <button type="button" class="btn btn-secondary btn-sm" @click="emit('detail', model)">
          {{ t('plaza.card.details') }}
        </button>
        <button
          type="button"
          class="btn btn-ghost btn-icon btn-sm"
          :title="t('plaza.card.copyName')"
          @click="copyName"
        >
          <Icon name="copy" size="sm" />
        </button>
      </div>
    </div>

    <!-- 价格 -->
    <div class="font-mono text-xs">
      <template v-if="model.billing_mode === 'per_request'">
        <span class="text-gray-500 dark:text-dark-400">{{ t('plaza.card.perRequest') }}</span>
        <span class="ml-1.5 font-semibold text-gray-900 dark:text-white">
          <span v-if="model.multi_price" class="mr-0.5 font-sans text-[10px] font-normal text-gray-400">
            {{ t('plaza.card.from') }}
          </span>
          {{ formatPerRequest(model.price.per_request) }}
        </span>
      </template>
      <div v-else class="flex flex-wrap gap-x-3 gap-y-1">
        <span>
          <span class="text-gray-500 dark:text-dark-400">{{ t('plaza.card.input') }}</span>
          <span class="ml-1 font-semibold text-gray-900 dark:text-white">
            <span v-if="model.multi_price" class="mr-0.5 font-sans text-[10px] font-normal text-gray-400">
              {{ t('plaza.card.from') }}
            </span>
            {{ formatScaled(model.price.input, unitScale) }}
          </span>
        </span>
        <span>
          <span class="text-gray-500 dark:text-dark-400">{{ t('plaza.card.output') }}</span>
          <span class="ml-1 font-semibold text-gray-900 dark:text-white">
            {{ formatScaled(model.price.output, unitScale) }}
          </span>
        </span>
        <span>
          <span class="text-gray-500 dark:text-dark-400">{{ t('plaza.card.cacheRead') }}</span>
          <span class="ml-1 font-semibold text-gray-900 dark:text-white">
            {{ formatScaled(model.price.cache_read, unitScale) }}
          </span>
        </span>
      </div>
    </div>

    <!-- 分组徽章 -->
    <div v-if="model.groups?.length" class="flex flex-wrap gap-1">
      <Badge v-for="g in model.groups" :key="g.id" variant="gray">
        <span class="truncate">{{ g.name }}</span>
        <span class="ml-1 font-mono opacity-70">x{{ formatRate(g.rate_multiplier) }}</span>
      </Badge>
    </div>

    <!-- 底部：计费方式 + 指标 -->
    <div class="mt-auto flex items-end justify-between border-t border-gray-100 pt-3 dark:border-dark-700">
      <div class="flex items-center gap-1.5">
        <Badge variant="primary">{{ billingLabel }}</Badge>
        <Badge v-if="model.price.has_intervals" variant="purple">{{ t('plaza.card.tiered') }}</Badge>
        <Badge
          v-if="model.price_source === 'official'"
          variant="gray"
          :title="t('plaza.card.priceOfficialHint')"
        >
          {{ t('plaza.card.priceOfficial') }}
        </Badge>
      </div>
      <div class="flex items-center gap-4 text-right">
        <div>
          <div class="text-[10px] uppercase tracking-wide text-gray-400 dark:text-dark-500">
            {{ t('plaza.card.latency') }}
          </div>
          <div class="font-mono text-xs font-semibold text-gray-700 dark:text-dark-200">
            {{ fmtMs(model.metric?.avg_duration_ms) }}
          </div>
        </div>
        <div>
          <div class="text-[10px] uppercase tracking-wide text-gray-400 dark:text-dark-500">
            {{ t('plaza.card.throughput') }}
          </div>
          <div class="font-mono text-xs font-semibold text-gray-700 dark:text-dark-200">
            {{ formatThroughput(model.metric?.tokens_per_second) }}
          </div>
        </div>
        <div>
          <div class="mb-0.5 text-[10px] uppercase tracking-wide text-gray-400 dark:text-dark-500">
            {{ t('plaza.card.status') }}
          </div>
          <PlazaStatusBars :model="model" />
        </div>
      </div>
    </div>
  </article>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PlazaModel } from '@/types/plaza'
import { formatPerRequest, formatScaled, formatThroughput, type UnitScale } from '@/utils/plazaModel'
import { fmtMs } from '@/utils/format'
import Icon from '@/components/icons/Icon.vue'
import Badge from '@/components/common/Badge.vue'
import PlazaModelIcon from './PlazaModelIcon.vue'
import PlazaStatusBars from './PlazaStatusBars.vue'

const props = defineProps<{ model: PlazaModel; unitScale: UnitScale }>()
const emit = defineEmits<{
  (e: 'detail', model: PlazaModel): void
  (e: 'copy', name: string): void
}>()

const { t } = useI18n()

const billingLabel = computed(() => {
  switch (props.model.billing_mode) {
    case 'per_request':
      return t('plaza.card.billingPerRequest')
    case 'image':
      return t('plaza.card.billingImage')
    default:
      return t('plaza.card.billingToken')
  }
})

// 倍率去掉多余小数（1.00 → 1，0.050 → 0.05）
function formatRate(v: number): string {
  return String(Number(v.toFixed(4)))
}

function copyName() {
  emit('copy', props.model.name)
}
</script>
