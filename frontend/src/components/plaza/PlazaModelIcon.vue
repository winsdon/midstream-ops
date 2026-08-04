<template>
  <span
    class="flex shrink-0 items-center justify-center rounded-lg font-bold text-white"
    :class="[sizeClass, brand.color]"
    :title="brand.label"
  >
    {{ brand.initial }}
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(defineProps<{ model: string; size?: 'sm' | 'md' }>(), {
  size: 'md'
})

// 按模型名前缀匹配厂商，取品牌主色。命中顺序即优先级，必须写完整 Tailwind 类字面量。
const BRANDS: Array<{ match: RegExp; label: string; initial: string; color: string }> = [
  { match: /^claude|^anthropic/i, label: 'Anthropic', initial: 'C', color: 'bg-orange-500' },
  { match: /^gpt|^o[1-9]|^chatgpt|^text-|^davinci/i, label: 'OpenAI', initial: 'O', color: 'bg-emerald-600' },
  { match: /^gemini|^gemma|^palm/i, label: 'Google', initial: 'G', color: 'bg-blue-500' },
  { match: /^grok/i, label: 'xAI', initial: 'X', color: 'bg-gray-900 dark:bg-gray-700' },
  { match: /^deepseek/i, label: 'DeepSeek', initial: 'D', color: 'bg-indigo-600' },
  { match: /^qwen|^tongyi/i, label: 'Qwen', initial: 'Q', color: 'bg-purple-600' },
  { match: /^glm|^chatglm|^zhipu/i, label: 'Zhipu', initial: 'Z', color: 'bg-sky-600' },
  { match: /^moonshot|^kimi/i, label: 'Moonshot', initial: 'K', color: 'bg-fuchsia-600' },
  { match: /^doubao/i, label: 'Doubao', initial: 'D', color: 'bg-cyan-600' },
  { match: /^llama|^meta/i, label: 'Meta', initial: 'L', color: 'bg-blue-700' },
  { match: /^mistral|^mixtral/i, label: 'Mistral', initial: 'M', color: 'bg-amber-600' },
  { match: /^minimax|^abab/i, label: 'MiniMax', initial: 'M', color: 'bg-rose-600' }
]

const FALLBACK = { label: 'Model', color: 'bg-gray-400 dark:bg-dark-600' }

const brand = computed(() => {
  const name = props.model.trim()
  for (const b of BRANDS) {
    if (b.match.test(name)) return b
  }
  return {
    ...FALLBACK,
    initial: (name.charAt(0) || '?').toUpperCase()
  }
})

const sizeClass = computed(() =>
  props.size === 'sm' ? 'h-5 w-5 text-[10px]' : 'h-7 w-7 text-xs'
)
</script>
