<template>
  <div class="table-wrapper">
    <table class="table">
      <thead>
        <tr>
          <th class="text-left">{{ t('plaza.list.model') }}</th>
          <th class="text-left">{{ t('plaza.list.groups') }}</th>
          <th class="text-right">{{ t('plaza.card.input') }}</th>
          <th class="text-right">{{ t('plaza.card.output') }}</th>
          <th class="text-center">{{ t('plaza.list.billing') }}</th>
          <th class="text-right">{{ t('plaza.card.latency') }}</th>
          <th class="text-right">{{ t('plaza.card.throughput') }}</th>
          <th class="text-center">{{ t('plaza.card.status') }}</th>
          <th class="text-center">{{ t('common.actions') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="m in models" :key="m.name">
          <td>
            <div class="flex items-center gap-2">
              <PlazaModelIcon :model="m.name" size="sm" />
              <span class="max-w-[240px] truncate font-mono text-xs font-semibold" :title="m.name">
                {{ m.name }}
              </span>
              <button
                type="button"
                class="btn btn-ghost btn-icon btn-sm"
                :title="t('plaza.card.copyName')"
                @click="emit('copy', m.name)"
              >
                <Icon name="copy" size="xs" />
              </button>
            </div>
          </td>
          <td>
            <div class="flex flex-wrap gap-1">
              <Badge v-for="g in m.groups ?? []" :key="g.id" variant="gray">
                {{ g.name }}
              </Badge>
              <span v-if="!m.groups?.length" class="text-xs text-gray-400">-</span>
            </div>
          </td>
          <td class="text-right font-mono text-xs">
            {{ m.billing_mode === 'per_request' ? '-' : formatScaled(m.price.input, unitScale) }}
          </td>
          <td class="text-right font-mono text-xs">
            {{ m.billing_mode === 'per_request' ? '-' : formatScaled(m.price.output, unitScale) }}
          </td>
          <td class="text-center">
            <Badge variant="primary">{{ billingLabel(m) }}</Badge>
          </td>
          <td class="text-right font-mono text-xs">{{ fmtMs(m.metric?.avg_duration_ms) }}</td>
          <td class="text-right font-mono text-xs">{{ formatThroughput(m.metric?.tokens_per_second) }}</td>
          <td>
            <div class="flex justify-center"><PlazaStatusBars :model="m" /></div>
          </td>
          <td class="text-center">
            <button type="button" class="btn btn-secondary btn-sm" @click="emit('detail', m)">
              {{ t('plaza.card.details') }}
            </button>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { PlazaModel } from '@/types/plaza'
import { formatScaled, formatThroughput, type UnitScale } from '@/utils/plazaModel'
import { fmtMs } from '@/utils/format'
import Icon from '@/components/icons/Icon.vue'
import Badge from '@/components/common/Badge.vue'
import PlazaModelIcon from './PlazaModelIcon.vue'
import PlazaStatusBars from './PlazaStatusBars.vue'

defineProps<{ models: PlazaModel[]; unitScale: UnitScale }>()
const emit = defineEmits<{
  (e: 'detail', model: PlazaModel): void
  (e: 'copy', name: string): void
}>()

const { t } = useI18n()

function billingLabel(m: PlazaModel): string {
  switch (m.billing_mode) {
    case 'per_request':
      return t('plaza.card.billingPerRequest')
    case 'image':
      return t('plaza.card.billingImage')
    default:
      return t('plaza.card.billingToken')
  }
}
</script>
