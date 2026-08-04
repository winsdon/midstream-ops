<template>
  <div class="text-right">
    <span :class="toneClass" :title="bandTip">{{ fmtMs(ms) }}</span>
    <!-- P95 收成小字副值：原本 P50/P95 各占一列，被动表五个等权重数字列
         扫一眼得不到结论。合并后主值负责分档着色，长尾仍可查但不抢视线。 -->
    <div v-if="showP95" class="text-xs text-gray-400 dark:text-dark-500">
      {{ t('stability.p95Prefix') }} {{ fmtMs(p95) }}
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { fmtMs } from '@/utils/format'
import { latencyBand, latencyBandThresholds, type LatencyBand, type LatencyKind } from '@/utils/latencyBand'

const props = withDefaults(
  defineProps<{
    /** 主值（P50 或均值） */
    ms?: number | null
    /** 长尾副值；不传则不渲染第二行 */
    p95?: number | null
    kind?: LatencyKind
    /** 主指标列加粗以确立视觉主次 */
    primary?: boolean
  }>(),
  { kind: 'ttft', primary: false }
)

const { t } = useI18n()

/**
 * 必须写完整字面量：Tailwind 扫描源码文本提取类名，拼接出来的会被漏掉。
 *
 * ok 档刻意用中性灰而非绿 —— 四档里两档给绿色会让绿色泛滥，
 * 只有真快（首字 <1.5s）才值得一个正向信号。
 */
const TONE: Record<LatencyBand, string> = {
  fast: 'text-emerald-600 dark:text-emerald-400',
  ok: 'text-gray-700 dark:text-dark-300',
  slow: 'text-amber-600 dark:text-amber-400',
  bad: 'text-red-600 dark:text-red-400',
  unknown: 'text-gray-400 dark:text-dark-500'
}

const band = computed(() => latencyBand(props.ms, props.kind))

const toneClass = computed(() =>
  props.primary ? `font-semibold ${TONE[band.value]}` : TONE[band.value]
)

/**
 * 悬停说明当前列的四档阈值 —— 颜色依据不写在界面上就只能靠猜，
 * 尤其在阈值调整之后（旧档偏严，多数行常亮告警色）。
 */
const bandTip = computed(() => {
  const [good, warn, bad] = latencyBandThresholds(props.kind)
  return t('stability.bandTip', {
    good: fmtMs(good),
    warn: fmtMs(warn),
    bad: fmtMs(bad)
  })
})

// 显式判 undefined 而非真值：p95 为 null（有该列但无样本）时仍要渲染 '-'，
// 否则「这个账号没有长尾数据」会与「这一列不存在」混为一谈。
const showP95 = computed(() => props.p95 !== undefined)
</script>
