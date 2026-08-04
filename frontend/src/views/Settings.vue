<template>
  <div class="space-y-5">
    <!-- Tab 切换 -->
    <div class="flex items-center justify-between">
      <div class="flex rounded-lg bg-gray-100 p-0.5 dark:bg-dark-800">
        <button
          v-for="tab in tabs" :key="tab.key"
          class="flex items-center gap-1.5 rounded-md px-4 py-1.5 text-sm font-medium transition-colors"
          :class="activeTab === tab.key ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-700 dark:text-primary-300' : 'text-gray-500'"
          @click="activeTab = tab.key"
        >
          <Icon :name="tab.icon" size="sm" />
          {{ t(tab.label) }}
        </button>
      </div>
      <button class="btn btn-primary text-sm" :disabled="saving" @click="save">
        {{ saving ? t('common.loading') : t('settings.save') }}
      </button>
    </div>

    <div v-if="loading" class="flex h-40 items-center justify-center"><LoadingState /></div>

    <!-- ===== 自动化与策略 ===== -->
    <template v-else-if="activeTab === 'strategy'">
      <p class="text-sm text-gray-500 dark:text-dark-400">{{ t('settings.strategyDesc') }}</p>

      <!-- 数据刷新频率 -->
      <div class="card p-5">
        <div class="flex items-center justify-between gap-4">
          <div class="flex items-start gap-3">
            <div class="mt-0.5 flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-xl bg-primary-50 text-primary-600 dark:bg-primary-900/30 dark:text-primary-300">
              <Icon name="refresh" size="md" />
            </div>
            <div>
              <p class="font-medium text-gray-900 dark:text-white">{{ t('settings.refreshTitle') }}</p>
              <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">{{ t('settings.refreshDesc') }}</p>
            </div>
          </div>
          <ToggleSwitch v-model="strategy.refresh_enabled" />
        </div>
        <div v-if="strategy.refresh_enabled" class="mt-4 flex items-center gap-3 border-t border-gray-100 pt-4 dark:border-dark-800">
          <label class="text-sm text-gray-600 dark:text-dark-300">{{ t('settings.refreshInterval') }}</label>
          <input
            v-model.number="refreshIntervalMinutes" type="number" min="1" step="1"
            class="input !w-24 !py-1.5 text-sm"
          />
          <span class="text-sm text-gray-500">{{ t('settings.minutes') }}</span>
          <span class="text-xs text-gray-400">{{ t('settings.refreshMin') }}</span>
        </div>
      </div>

      <!-- 余额预警 -->
      <div class="card p-5">
        <div class="flex items-center justify-between gap-4">
          <div class="flex items-start gap-3">
            <div class="mt-0.5 flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-xl bg-amber-50 text-amber-600 dark:bg-amber-900/30 dark:text-amber-300">
              <Icon name="exclamationTriangle" size="md" />
            </div>
            <div>
              <p class="font-medium text-gray-900 dark:text-white">{{ t('settings.balanceAlertTitle') }}</p>
              <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">{{ t('settings.balanceAlertDesc') }}</p>
              <p v-if="!strategy.refresh_enabled" class="mt-1.5 flex items-center gap-1 text-xs text-primary-600 dark:text-primary-400">
                <Icon name="infoCircle" size="sm" /> {{ t('settings.needRefreshHint') }}
              </p>
            </div>
          </div>
          <ToggleSwitch v-model="strategy.balance_alert_enabled" />
        </div>

        <div v-if="strategy.balance_alert_enabled" class="mt-4 space-y-4 border-t border-gray-100 pl-12 pt-4 dark:border-dark-800">
          <!-- 触发金额 -->
          <div>
            <label class="input-label">{{ t('settings.triggerAmount') }}</label>
            <div class="relative max-w-xs">
              <span class="absolute left-3 top-1/2 -translate-y-1/2 text-sm text-gray-400">¥</span>
              <input
                v-model.number="strategy.default_balance_threshold"
                type="number" min="0" step="0.01"
                class="input !pl-7"
              />
            </div>
            <p class="mt-1 text-xs text-gray-400">{{ t('settings.triggerAmountHint') }}</p>
          </div>

          <!-- 通知渠道多选 -->
          <div>
            <label class="input-label">
              {{ t('settings.notifyChannels') }}
              <span class="text-red-500">*</span>
            </label>
            <p v-if="!availableChannels.length" class="py-1 text-sm italic text-gray-400">
              {{ t('settings.noChannelsConfigured') }}
            </p>
            <div v-else class="flex flex-wrap gap-2">
              <button
                v-for="ch in availableChannels" :key="ch"
                type="button"
                class="inline-flex items-center gap-1.5 rounded-lg border px-3 py-1.5 text-xs font-medium transition-colors"
                :class="strategy.balance_notify_channels.includes(ch)
                  ? 'border-primary-500 bg-primary-50 text-primary-700 dark:bg-primary-900/30 dark:text-primary-300'
                  : 'border-gray-200 bg-gray-50 text-gray-500 hover:bg-gray-100 dark:border-dark-700 dark:bg-dark-800 dark:text-dark-400'"
                @click="toggleChannel('balance', ch)"
              >
                <Icon name="chat" size="sm" />
                {{ channelLabel(ch) }}
              </button>
            </div>
            <p v-if="availableChannels.length && !strategy.balance_notify_channels.length"
               class="mt-1 text-xs text-red-500">{{ t('settings.mustSelectChannel') }}</p>
          </div>

          <!-- 自定义文案 -->
          <div>
            <label class="input-label">{{ t('settings.customTemplate') }}</label>
            <div class="mb-1.5 flex flex-wrap items-center gap-1.5">
              <button
                v-for="v in balanceVars" :key="v.key"
                type="button"
                class="rounded bg-primary-50 px-1.5 py-0.5 font-mono text-xs text-primary-700 transition-colors hover:bg-primary-100 dark:bg-primary-900/30 dark:text-primary-300"
                :title="t('settings.clickToInsert')"
                @click="insertVar('balance', v.key)"
              >{{ v.key }}</button>
              <span class="text-xs text-gray-400">{{ t('settings.varsHint') }}</span>
            </div>
            <textarea
              ref="balanceTemplateRef"
              v-model="strategy.balance_template"
              rows="3"
              :placeholder="defaultBalanceTemplate"
              class="input resize-none font-mono !text-xs"
            ></textarea>
          </div>
        </div>
      </div>

      <!-- 倍率变更预警 -->
      <div class="card p-5">
        <div class="flex items-center justify-between gap-4">
          <div class="flex items-start gap-3">
            <div class="mt-0.5 flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-xl bg-violet-50 text-violet-600 dark:bg-violet-900/30 dark:text-violet-300">
              <Icon name="trendingUp" size="md" />
            </div>
            <div>
              <p class="font-medium text-gray-900 dark:text-white">{{ t('settings.rateAlertTitle') }}</p>
              <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">{{ t('settings.rateAlertDesc') }}</p>
            </div>
          </div>
          <ToggleSwitch v-model="strategy.rate_alert_enabled" />
        </div>

        <div v-if="strategy.rate_alert_enabled" class="mt-4 space-y-4 border-t border-gray-100 pl-12 pt-4 dark:border-dark-800">
          <div>
            <label class="input-label">
              {{ t('settings.notifyChannels') }}
              <span class="text-red-500">*</span>
            </label>
            <p v-if="!availableChannels.length" class="py-1 text-sm italic text-gray-400">
              {{ t('settings.noChannelsConfigured') }}
            </p>
            <div v-else class="flex flex-wrap gap-2">
              <button
                v-for="ch in availableChannels" :key="ch"
                type="button"
                class="inline-flex items-center gap-1.5 rounded-lg border px-3 py-1.5 text-xs font-medium transition-colors"
                :class="strategy.rate_notify_channels.includes(ch)
                  ? 'border-primary-500 bg-primary-50 text-primary-700 dark:bg-primary-900/30 dark:text-primary-300'
                  : 'border-gray-200 bg-gray-50 text-gray-500 hover:bg-gray-100 dark:border-dark-700 dark:bg-dark-800 dark:text-dark-400'"
                @click="toggleChannel('rate', ch)"
              >
                <Icon name="chat" size="sm" />
                {{ channelLabel(ch) }}
              </button>
            </div>
          </div>

          <div>
            <label class="input-label">{{ t('settings.customTemplate') }}</label>
            <div class="mb-1.5 flex flex-wrap items-center gap-1.5">
              <button
                v-for="v in rateVars" :key="v.key"
                type="button"
                class="rounded bg-primary-50 px-1.5 py-0.5 font-mono text-xs text-primary-700 transition-colors hover:bg-primary-100 dark:bg-primary-900/30 dark:text-primary-300"
                :title="t('settings.clickToInsert')"
                @click="insertVar('rate', v.key)"
              >{{ v.key }}</button>
            </div>
            <textarea
              ref="rateTemplateRef"
              v-model="strategy.rate_template"
              rows="2"
              :placeholder="defaultRateTemplate"
              class="input resize-none font-mono !text-xs"
            ></textarea>
          </div>
        </div>
      </div>

      <!-- 授信额度预警 -->
      <div class="card p-5">
        <div class="flex items-center justify-between gap-4">
          <div class="flex items-start gap-3">
            <div class="mt-0.5 flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-xl bg-emerald-50 text-emerald-600 dark:bg-emerald-900/30 dark:text-emerald-300">
              <Icon name="creditCard" size="md" />
            </div>
            <div>
              <p class="font-medium text-gray-900 dark:text-white">{{ t('settings.creditAlertTitle') }}</p>
              <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">{{ t('settings.creditAlertDesc') }}</p>
            </div>
          </div>
          <ToggleSwitch v-model="strategy.credit_alert_enabled" />
        </div>

        <div v-if="strategy.credit_alert_enabled" class="mt-4 space-y-4 border-t border-gray-100 pl-12 pt-4 dark:border-dark-800">
          <!-- 记账驱动而非定时轮询，与前两类告警的触发方式不同，这里明说一句 -->
          <p class="flex items-start gap-1.5 rounded-lg bg-amber-50 px-3 py-2 text-xs text-amber-700 dark:bg-amber-900/20 dark:text-amber-300">
            <Icon name="infoCircle" size="sm" class="mt-px flex-shrink-0" />
            <span>{{ t('settings.creditAlertHint') }}</span>
          </p>

          <div>
            <label class="input-label">
              {{ t('settings.notifyChannels') }}
              <span class="text-red-500">*</span>
            </label>
            <p v-if="!availableChannels.length" class="py-1 text-sm italic text-gray-400">
              {{ t('settings.noChannelsConfigured') }}
            </p>
            <div v-else class="flex flex-wrap gap-2">
              <button
                v-for="ch in availableChannels" :key="ch"
                type="button"
                class="inline-flex items-center gap-1.5 rounded-lg border px-3 py-1.5 text-xs font-medium transition-colors"
                :class="strategy.credit_notify_channels.includes(ch)
                  ? 'border-primary-500 bg-primary-50 text-primary-700 dark:bg-primary-900/30 dark:text-primary-300'
                  : 'border-gray-200 bg-gray-50 text-gray-500 hover:bg-gray-100 dark:border-dark-700 dark:bg-dark-800 dark:text-dark-400'"
                @click="toggleChannel('credit', ch)"
              >
                <Icon name="chat" size="sm" />
                {{ channelLabel(ch) }}
              </button>
            </div>
            <p v-if="availableChannels.length && !strategy.credit_notify_channels.length"
               class="mt-1 text-xs text-red-500">{{ t('settings.mustSelectChannel') }}</p>
          </div>

          <div>
            <label class="input-label">{{ t('settings.customTemplate') }}</label>
            <div class="mb-1.5 flex flex-wrap items-center gap-1.5">
              <button
                v-for="v in creditVars" :key="v.key"
                type="button"
                class="rounded bg-primary-50 px-1.5 py-0.5 font-mono text-xs text-primary-700 transition-colors hover:bg-primary-100 dark:bg-primary-900/30 dark:text-primary-300"
                :title="t('settings.clickToInsert')"
                @click="insertVar('credit', v.key)"
              >{{ v.key }}</button>
            </div>
            <textarea
              ref="creditTemplateRef"
              v-model="strategy.credit_template"
              rows="2"
              :placeholder="defaultCreditTemplate"
              class="input resize-none font-mono !text-xs"
            ></textarea>
          </div>
        </div>
      </div>
    </template>

    <!-- ===== 通知与渠道 ===== -->
    <template v-else>
      <p class="text-sm text-gray-500 dark:text-dark-400">{{ t('settings.notifyDesc') }}</p>

      <!-- 钉钉 -->
      <div class="card p-5">
        <div class="flex items-center justify-between gap-4">
          <div>
            <p class="font-medium text-gray-900 dark:text-white">{{ t('settings.dingtalk') }}</p>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">{{ t('settings.dingtalkDesc') }}</p>
          </div>
          <div class="flex items-center gap-3">
            <button class="btn btn-secondary text-xs" :disabled="testing === 'dingtalk'" @click="test('dingtalk')">
              {{ testing === 'dingtalk' ? t('common.loading') : t('settings.testSend') }}
            </button>
            <ToggleSwitch v-model="channels.dingtalk.enabled" />
          </div>
        </div>
        <div v-if="channels.dingtalk.enabled" class="mt-4 space-y-3 border-t border-gray-100 pt-4 dark:border-dark-800">
          <div>
            <label class="input-label">Webhook</label>
            <input v-model.trim="channels.dingtalk.webhook" class="input" placeholder="https://oapi.dingtalk.com/robot/send?access_token=..." />
          </div>
          <div>
            <label class="input-label">{{ t('settings.signSecret') }}</label>
            <input v-model="dingtalkSecret" type="password" class="input"
              :placeholder="notify?.dingtalk.has_secret ? t('settings.secretKeep') : t('settings.secretOptional')" autocomplete="new-password" />
          </div>
        </div>
      </div>

      <!-- 飞书 -->
      <div class="card p-5">
        <div class="flex items-center justify-between gap-4">
          <div>
            <p class="font-medium text-gray-900 dark:text-white">{{ t('settings.feishu') }}</p>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">{{ t('settings.feishuDesc') }}</p>
          </div>
          <div class="flex items-center gap-3">
            <button class="btn btn-secondary text-xs" :disabled="testing === 'feishu'" @click="test('feishu')">
              {{ testing === 'feishu' ? t('common.loading') : t('settings.testSend') }}
            </button>
            <ToggleSwitch v-model="channels.feishu.enabled" />
          </div>
        </div>
        <div v-if="channels.feishu.enabled" class="mt-4 space-y-3 border-t border-gray-100 pt-4 dark:border-dark-800">
          <div>
            <label class="input-label">Webhook</label>
            <input v-model.trim="channels.feishu.webhook" class="input" placeholder="https://open.feishu.cn/open-apis/bot/v2/hook/..." />
          </div>
          <div>
            <label class="input-label">{{ t('settings.signSecret') }}</label>
            <input v-model="feishuSecret" type="password" class="input"
              :placeholder="notify?.feishu.has_secret ? t('settings.secretKeep') : t('settings.secretOptional')" autocomplete="new-password" />
          </div>
        </div>
      </div>

      <!-- Telegram -->
      <div class="card p-5">
        <div class="flex items-center justify-between gap-4">
          <div>
            <p class="font-medium text-gray-900 dark:text-white">Telegram</p>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">{{ t('settings.telegramDesc') }}</p>
          </div>
          <div class="flex items-center gap-3">
            <button class="btn btn-secondary text-xs" :disabled="testing === 'telegram'" @click="test('telegram')">
              {{ testing === 'telegram' ? t('common.loading') : t('settings.testSend') }}
            </button>
            <ToggleSwitch v-model="channels.telegram.enabled" />
          </div>
        </div>
        <div v-if="channels.telegram.enabled" class="mt-4 grid gap-3 border-t border-gray-100 pt-4 dark:border-dark-800 sm:grid-cols-2">
          <div>
            <label class="input-label">Bot Token</label>
            <input v-model="telegramToken" type="password" class="input"
              :placeholder="notify?.telegram.has_bot_token ? t('settings.secretKeep') : '123456:ABC-DEF...'" autocomplete="new-password" />
          </div>
          <div>
            <label class="input-label">Chat ID</label>
            <input v-model.trim="channels.telegram.chat_id" class="input" placeholder="-1001234567890" />
          </div>
        </div>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, nextTick, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { settingsApi } from '@/api'
import { errorMessage } from '@/api/client'
import { useAppStore } from '@/stores/app'
import type { NotifyChannels, StrategySettings } from '@/types'
import Icon from '@/components/icons/Icon.vue'
import LoadingState from '@/components/common/LoadingState.vue'
import ToggleSwitch from '@/components/common/ToggleSwitch.vue'

const { t } = useI18n()
const app = useAppStore()

const tabs = [
  { key: 'strategy', label: 'settings.tabStrategy', icon: 'clock' },
  { key: 'notify', label: 'settings.tabNotify', icon: 'chat' }
] as const

const activeTab = ref<'strategy' | 'notify'>('strategy')
const loading = ref(true)
const saving = ref(false)
const testing = ref('')

// 策略
const strategy = ref<StrategySettings>({
  refresh_enabled: false,
  refresh_interval_seconds: 600,
  balance_alert_enabled: false,
  default_balance_threshold: 10,
  balance_notify_channels: [],
  balance_template: '',
  rate_alert_enabled: false,
  rate_notify_channels: [],
  rate_template: '',
  credit_alert_enabled: false,
  credit_notify_channels: [],
  credit_template: ''
})

// 后端下发的默认模板与可用渠道（默认模板只在后端定义一份，避免前后端漂移）
const defaultBalanceTemplate = ref('')
const defaultRateTemplate = ref('')
const defaultCreditTemplate = ref('')
const availableChannels = ref<string[]>([])

const balanceVars = [
  { key: '{siteName}' },
  { key: '{balance}' },
  { key: '{threshold}' }
]
const rateVars = [
  { key: '{entityName}' },
  { key: '{oldRate}' },
  { key: '{newRate}' },
  { key: '{direction}' }
]
const creditVars = [
  { key: '{customerName}' },
  { key: '{band}' },
  { key: '{outstanding}' },
  { key: '{limit}' },
  { key: '{available}' }
]

const balanceTemplateRef = ref<HTMLTextAreaElement | null>(null)
const rateTemplateRef = ref<HTMLTextAreaElement | null>(null)
const creditTemplateRef = ref<HTMLTextAreaElement | null>(null)

/**
 * 三类告警的字段名与 textarea ref 集中在一张表里。
 * 早先是 kind === 'balance' ? ... : ... 的二元三目，加第三种告警要改两处分支；
 * 查表后再加第四种只需在这里补一行。
 */
type AlertKind = 'balance' | 'rate' | 'credit'
const ALERT_FIELDS: Record<
  AlertKind,
  { channels: 'balance_notify_channels' | 'rate_notify_channels' | 'credit_notify_channels'
    template: 'balance_template' | 'rate_template' | 'credit_template'
    ref: typeof balanceTemplateRef }
> = {
  balance: { channels: 'balance_notify_channels', template: 'balance_template', ref: balanceTemplateRef },
  rate: { channels: 'rate_notify_channels', template: 'rate_template', ref: rateTemplateRef },
  credit: { channels: 'credit_notify_channels', template: 'credit_template', ref: creditTemplateRef }
}

const CHANNEL_LABELS: Record<string, string> = {
  dingtalk: '钉钉',
  feishu: '飞书',
  telegram: 'Telegram'
}
function channelLabel(ch: string): string {
  return CHANNEL_LABELS[ch] || ch
}

function toggleChannel(kind: AlertKind, ch: string): void {
  const key = ALERT_FIELDS[kind].channels
  const list = strategy.value[key]
  const idx = list.indexOf(ch)
  // 不可变更新：直接 splice 会绕过部分响应式追踪场景
  strategy.value = {
    ...strategy.value,
    [key]: idx >= 0 ? list.filter((c) => c !== ch) : [...list, ch]
  }
}

// 在光标位置插入变量；无 ref 时退化为追加
function insertVar(kind: AlertKind, v: string): void {
  const { template: key, ref: elRef } = ALERT_FIELDS[kind]
  const el = elRef.value
  const cur = strategy.value[key] || ''
  if (!el) {
    strategy.value = { ...strategy.value, [key]: cur + v }
    return
  }
  const s = el.selectionStart ?? cur.length
  const e = el.selectionEnd ?? s
  strategy.value = { ...strategy.value, [key]: cur.slice(0, s) + v + cur.slice(e) }
  nextTick(() => {
    el.focus()
    el.setSelectionRange(s + v.length, s + v.length)
  })
}

// UI 用分钟展示，落库为秒
const refreshIntervalMinutes = computed({
  get: () => Math.max(1, Math.round(strategy.value.refresh_interval_seconds / 60)),
  set: (v: number) => {
    strategy.value = { ...strategy.value, refresh_interval_seconds: Math.max(60, Math.round(v) * 60) }
  }
})

// 通知渠道（表单态；secret 单独存，空串 = 未改动不回传）
const notify = ref<NotifyChannels | null>(null)
const channels = ref({
  dingtalk: { enabled: false, webhook: '' },
  feishu: { enabled: false, webhook: '' },
  telegram: { enabled: false, chat_id: '' }
})
const dingtalkSecret = ref('')
const feishuSecret = ref('')
const telegramToken = ref('')

async function load(): Promise<void> {
  loading.value = true
  try {
    const [st, nf] = await Promise.all([settingsApi.getStrategy(), settingsApi.getNotify()])
    strategy.value = {
      ...st.strategy,
      balance_notify_channels: st.strategy.balance_notify_channels || [],
      rate_notify_channels: st.strategy.rate_notify_channels || [],
      credit_notify_channels: st.strategy.credit_notify_channels || []
    }
    defaultBalanceTemplate.value = st.default_balance_template
    defaultRateTemplate.value = st.default_rate_template
    defaultCreditTemplate.value = st.default_credit_template
    availableChannels.value = st.available_channels || []
    notify.value = nf
    channels.value = {
      dingtalk: { enabled: nf.dingtalk.enabled, webhook: nf.dingtalk.webhook },
      feishu: { enabled: nf.feishu.enabled, webhook: nf.feishu.webhook },
      telegram: { enabled: nf.telegram.enabled, chat_id: nf.telegram.chat_id }
    }
  } catch (e) {
    app.showError(errorMessage(e))
  } finally {
    loading.value = false
  }
}

async function save(): Promise<void> {
  saving.value = true
  try {
    await settingsApi.saveStrategy(strategy.value)
    await settingsApi.saveNotify({
      dingtalk: {
        ...channels.value.dingtalk,
        secret: dingtalkSecret.value ? dingtalkSecret.value : null
      },
      feishu: {
        ...channels.value.feishu,
        secret: feishuSecret.value ? feishuSecret.value : null
      },
      telegram: {
        enabled: channels.value.telegram.enabled,
        chat_id: channels.value.telegram.chat_id,
        bot_token: telegramToken.value ? telegramToken.value : null
      }
    })
    app.showSuccess(t('settings.saved'))
    dingtalkSecret.value = ''
    feishuSecret.value = ''
    telegramToken.value = ''
    await load()
  } catch (e) {
    app.showError(errorMessage(e))
  } finally {
    saving.value = false
  }
}

// 测试发送用「已保存」的配置：有未保存改动时先提示保存
async function test(channel: string): Promise<void> {
  testing.value = channel
  try {
    const r = await settingsApi.testNotify(channel)
    if (r.ok) {
      app.showSuccess(t('settings.testOk'))
    } else {
      app.showError(t('settings.testFailed') + ': ' + (r.error || ''))
    }
  } catch (e) {
    app.showError(errorMessage(e))
  } finally {
    testing.value = ''
  }
}

onMounted(load)
</script>
