<template>
  <div class="space-y-4">
    <!-- 主体类型：整张表的可见性开关，故置顶且用 segmented 而非下拉 -->
    <div>
      <label class="input-label">{{ t('credit.kyc.subjectType') }}</label>
      <div role="radiogroup" class="flex rounded-lg bg-gray-100 p-0.5 dark:bg-dark-800">
        <button
          v-for="opt in SUBJECT_TYPES"
          :key="opt"
          type="button"
          role="radio"
          :aria-checked="modelValue.subject_type === opt"
          :disabled="readonly"
          :class="subjectTypeClass(opt)"
          @click="setSubjectType(opt)"
        >
          {{ t(`credit.kyc.subject.${opt}`) }}
        </button>
      </div>
      <p class="mt-1 text-xs text-gray-400">{{ t('credit.kyc.subjectTypeHint') }}</p>
    </div>

    <div v-for="section in sections" :key="section.titleKey" class="space-y-2">
      <h4 class="border-b border-gray-200 pb-1 text-sm font-semibold text-gray-700 dark:border-dark-700 dark:text-dark-200">
        {{ t(section.titleKey) }}
      </h4>
      <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <div v-for="field in section.fields" :key="field.key" :class="field.input === 'textarea' ? 'sm:col-span-2' : ''">
          <label class="input-label" :for="`kyc-${field.key}`">
            {{ t(field.labelKey) }}
            <span v-if="field.required" class="text-red-500">*</span>
          </label>
          <textarea
            v-if="field.input === 'textarea'"
            :id="`kyc-${field.key}`"
            :value="modelValue[field.key]"
            rows="2"
            class="input"
            :readonly="readonly"
            @change="setField(field.key, $event)"
          />
          <input
            v-else
            :id="`kyc-${field.key}`"
            :value="modelValue[field.key]"
            :type="field.input === 'date' ? 'date' : 'text'"
            class="input"
            :readonly="readonly"
            @change="setField(field.key, $event)"
          />
        </div>
      </div>
    </div>

    <!-- PII 提示：录入者需要知道这些内容会加密落库，且系统不会外传 -->
    <p class="rounded-lg bg-blue-50 px-3 py-2 text-xs text-blue-700 dark:bg-blue-900/20 dark:text-blue-400">
      ⓘ {{ t('credit.kyc.piiHint') }}
    </p>
  </div>
</template>

<script setup lang="ts">
/**
 * KYC 表单主体。管理端 KycDialog 与客户自助嵌入页共用同一份模板 ——
 * 字段清单来自 utils/creditModel 的元数据，两端不会漂移。
 *
 * 本组件只负责「填」，不负责「存」：保存、送审、审核都由父组件处理，
 * 因为管理端与嵌入页调的是不同的接口（管理端带 customerId，嵌入端取自会话）。
 */
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { visibleSections } from '@/utils/creditModel'
import type { KycFieldKey } from '@/utils/creditModel'
import type { KycFormData, KycSubjectType } from '@/types/credit'

const props = defineProps<{
  modelValue: KycFormData
  /** 只读模式：已通过审核或无编辑权限时展示用 */
  readonly?: boolean
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: KycFormData): void
}>()

const { t } = useI18n()

const SUBJECT_TYPES: KycSubjectType[] = ['individual', 'company']

const sections = computed(() => visibleSections(props.modelValue.subject_type))

/**
 * 逐字段不可变更新：产生新对象后 emit，不原地改 props.modelValue。
 *
 * 用 @change 而非 @input：输入过程中每敲一个字都重建整个表单对象会白丢焦点，
 * 且这里同时做了 trim —— 在 input 阶段 trim 会让用户没法打空格。
 */
function setField(key: KycFieldKey, ev: Event) {
  const el = ev.target as HTMLInputElement | HTMLTextAreaElement
  emit('update:modelValue', { ...props.modelValue, [key]: el.value.trim() })
}

/**
 * 切换主体类型时**不清空**另一侧字段：选错了改回来数据还在。
 * 后端只校验当前类型的必填项，残留的另一侧字段无害。
 */
function setSubjectType(opt: KycSubjectType) {
  if (props.readonly || props.modelValue.subject_type === opt) return
  emit('update:modelValue', { ...props.modelValue, subject_type: opt })
}

// 必须写完整字面量：Tailwind 扫描源码文本提取类名，拼接出来的类名会被漏掉
const SEG_BASE = 'flex-1 rounded-md px-3 py-1.5 text-sm font-medium transition-colors disabled:cursor-not-allowed'
const SEG_ACTIVE = 'bg-white text-primary-600 shadow-sm dark:bg-dark-700 dark:text-primary-400'
const SEG_IDLE = 'text-gray-500 hover:text-gray-700 dark:text-dark-400 dark:hover:text-dark-200'

function subjectTypeClass(opt: KycSubjectType): string {
  return `${SEG_BASE} ${props.modelValue.subject_type === opt ? SEG_ACTIVE : SEG_IDLE}`
}
</script>
