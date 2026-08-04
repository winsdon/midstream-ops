<template>
  <span class="inline-flex items-center gap-2">
    <span class="h-2 w-2 shrink-0 rounded-full" :class="DOT[grade]" :title="tip" />
    <slot />
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { RowGrade } from '@/utils/stabilityModel'

/**
 * 行级综合评级色点，贴在账号名左侧。
 *
 * 不做成独立列：评级是首字延迟 / 成功率 / 健康状态的派生量，与它们并排会让人
 * 反复对照「为什么这三个绿评级却黄」。挂在账号名前则零额外列宽，
 * 扫一竖列即知哪几行该看。
 */
const props = defineProps<{ grade: RowGrade }>()

const { t } = useI18n()

// 必须写完整字面量供 Tailwind 扫描
const DOT: Record<RowGrade, string> = {
  good: 'bg-emerald-500',
  warn: 'bg-amber-500',
  bad: 'bg-red-500',
  unknown: 'bg-gray-300 dark:bg-dark-600'
}

const tip = computed(() => t('stability.gradeTip.' + props.grade))
</script>
