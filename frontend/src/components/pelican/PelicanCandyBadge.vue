<template>
  <Badge v-if="view" :variant="pelicanCandyVariant(view)" :title="title">{{ label }}</Badge>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Badge from '@/components/common/Badge.vue'
import type { PelicanCandy } from '@/types/pelican'
import { pelicanCandyVariant, pelicanCandyView } from '@/utils/pelican'

/** 糖果题结果徽章：卡片与详情共用，管理端和客户页一致；没测糖果题时不渲染。 */
const props = defineProps<{ candy?: PelicanCandy | null; status?: string }>()
const { t } = useI18n()
const view = computed(() => pelicanCandyView(props.candy, props.status))
const label = computed(() => {
  const candy = props.candy
  if (!view.value || !candy) return ''
  return t(`pelican.candyStates.${view.value}`, { answer: candy.answer ?? '', expected: candy.expected ?? '' })
})
const title = computed(() => (props.candy?.error ? `${t('pelican.candyTitle')}: ${props.candy.error}` : t('pelican.candyTitle')))
</script>
