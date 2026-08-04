<template>
  <div class="flex items-end gap-0.5 h-4" :title="tooltip">
    <span
      v-for="(bar, idx) in bars"
      :key="idx"
      class="w-1 rounded-sm transition-colors"
      :class="bar.color"
      :style="{ height: bar.height + '%' }"
    ></span>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { probeLevel, type ProbeLevel } from '@/utils/plazaModel'
import type { PlazaModel } from '@/types/plaza'

const props = defineProps<{ model: PlazaModel }>()
const { t } = useI18n()

// 高度 + 颜色双重编码：高且绿=正常，中且黄=降级，短且红=失败，很短灰=无数据。
// 必须写完整字面量，Tailwind 扫描源码文本提取类名。
const LEVEL_STYLE: Record<ProbeLevel, { color: string; height: number }> = {
  operational: { color: 'bg-emerald-500', height: 100 },
  degraded: { color: 'bg-amber-500', height: 65 },
  failed: { color: 'bg-red-500', height: 40 },
  unknown: { color: 'bg-gray-300 dark:bg-dark-600', height: 25 }
}

const level = computed(() => probeLevel(props.model))

const bars = computed(() => {
  const style = LEVEL_STYLE[level.value]
  return [style, style, style]
})

const tooltip = computed(() => {
  const p = props.model.probe
  if (!p || p.total === 0) return t('plaza.card.noProbe')
  const rate = ((p.success_count / p.total) * 100).toFixed(0)
  return t('plaza.card.probeTooltip', { rate, total: p.total })
})
</script>
