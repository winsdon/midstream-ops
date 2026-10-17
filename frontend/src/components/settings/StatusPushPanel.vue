<template>
  <div class="space-y-5">
    <p class="text-sm text-gray-500 dark:text-dark-400">{{ t('settings.statusPush.desc') }}</p>

    <div v-if="loading" class="flex h-40 items-center justify-center"><LoadingState /></div>

    <template v-else>
      <!-- Bot Token 不在本 tab 配置，未配置时推送必然全失败，提前说清楚；
           info 为空说明 GET 失败（已弹错误提示），不要在这里误报"未配置" -->
      <p
        v-if="info && !info.telegram_bot_token_set"
        class="flex items-start gap-1.5 rounded-lg bg-amber-50 px-3 py-2 text-xs text-amber-700 dark:bg-amber-900/20 dark:text-amber-300"
      >
        <Icon name="infoCircle" size="sm" class="mt-px flex-shrink-0" />
        <span>{{ t('settings.statusPush.noBotToken') }}</span>
      </p>

      <!-- 定时推送总开关与基础参数 -->
      <div class="card p-5">
        <div class="flex items-center justify-between gap-4">
          <div class="flex items-start gap-3">
            <div class="mt-0.5 flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-xl bg-primary-50 text-primary-600 dark:bg-primary-900/30 dark:text-primary-300">
              <Icon name="chart" size="md" />
            </div>
            <div>
              <p class="font-medium text-gray-900 dark:text-white">{{ t('settings.statusPush.enableTitle') }}</p>
              <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">{{ t('settings.statusPush.enableDesc') }}</p>
            </div>
          </div>
          <ToggleSwitch v-model="settings.enabled" />
        </div>

        <div v-if="settings.enabled" class="mt-4 grid gap-4 border-t border-gray-100 pt-4 dark:border-dark-800 sm:grid-cols-3">
          <div>
            <label class="input-label">{{ t('settings.statusPush.interval') }}</label>
            <Select :model-value="settings.interval_minutes" :options="intervalOptions" @update:model-value="onIntervalChange" />
          </div>
          <div>
            <label class="input-label">{{ t('settings.statusPush.window') }}</label>
            <Select :model-value="settings.window_minutes" :options="windowOptions" @update:model-value="onWindowChange" />
          </div>
          <div>
            <label class="input-label">{{ t('settings.statusPush.titleLabel') }}</label>
            <input v-model="settings.title" class="input" :placeholder="t('settings.statusPush.titlePlaceholder')" />
          </div>
        </div>
      </div>

      <!-- 推送目标 -->
      <div v-if="settings.enabled" class="card p-5">
        <label class="input-label">{{ t('settings.statusPush.chatIds') }}</label>
        <textarea
          v-model="chatIdsText"
          rows="3"
          class="input resize-y font-mono !text-xs"
          :placeholder="t('settings.statusPush.chatIdsPlaceholder')"
        ></textarea>
        <p class="input-hint">{{ t('settings.statusPush.chatIdsHint') }}</p>
        <p v-if="chatIds.length" class="mt-1 text-xs text-gray-500 dark:text-dark-400">
          {{ t('settings.statusPush.chatCount', { n: chatIds.length }) }}
        </p>
        <p v-else class="mt-1 text-xs text-amber-600 dark:text-amber-400">
          {{ t('settings.statusPush.chatIdsEmpty') }}
        </p>
      </div>

      <!-- 分组排除 -->
      <div v-if="settings.enabled" class="card p-5">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div>
            <p class="font-medium text-gray-900 dark:text-white">{{ t('settings.statusPush.excludeTitle') }}</p>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">{{ t('settings.statusPush.excludeDesc') }}</p>
          </div>
          <div class="flex items-center gap-2">
            <span class="text-xs tabular-nums text-gray-500 dark:text-dark-400">
              {{ t('settings.statusPush.excludedCount', { n: settings.excluded_group_ids.length, total: groups.length }) }}
            </span>
            <button type="button" class="btn btn-secondary text-xs" :disabled="!groups.length" @click="selectAllGroups">
              {{ t('settings.statusPush.selectAll') }}
            </button>
            <button type="button" class="btn btn-secondary text-xs" :disabled="!groups.length" @click="clearGroupSelection">
              {{ t('settings.statusPush.selectNone') }}
            </button>
          </div>
        </div>

        <p v-if="!groups.length" class="mt-3 border-t border-gray-100 pt-3 text-sm italic text-gray-400 dark:border-dark-800">
          {{ t('settings.statusPush.noGroups') }}
        </p>
        <div v-else class="mt-3 max-h-72 space-y-0.5 overflow-y-auto border-t border-gray-100 pt-3 dark:border-dark-800">
          <!-- 顺序沿用后端下发的 sort_order，勾选只改 excluded_group_ids -->
          <label
            v-for="g in groups" :key="g.id"
            class="flex cursor-pointer items-center gap-2 rounded-lg px-2 py-1.5 text-sm hover:bg-gray-50 dark:hover:bg-dark-800"
          >
            <input type="checkbox" class="checkbox" :checked="isExcluded(g.id)" @change="toggleGroup(g.id)" />
            <span class="min-w-0 flex-1 truncate text-gray-700 dark:text-gray-300">{{ g.name }}</span>
            <span class="flex-shrink-0 text-xs tabular-nums text-gray-400 dark:text-dark-500">×{{ g.rate_multiplier }}</span>
          </label>
        </div>
      </div>

      <!-- 状态图预览 -->
      <div class="card p-5">
        <div class="flex flex-wrap items-start justify-between gap-3">
          <div>
            <p class="font-medium text-gray-900 dark:text-white">{{ t('settings.statusPush.previewTitle') }}</p>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">{{ t('settings.statusPush.previewDesc') }}</p>
          </div>
          <button type="button" class="btn btn-secondary text-xs" :disabled="previewing" @click="loadPreview">
            {{ previewing ? t('common.loading') : t('settings.statusPush.preview') }}
          </button>
        </div>

        <p v-if="previewError" class="mt-3 whitespace-pre-wrap break-all rounded-lg bg-red-50 px-3 py-2 text-xs text-red-600 dark:bg-red-900/20 dark:text-red-300">
          {{ previewError }}
        </p>

        <div v-if="previewUrl" class="mt-3 border-t border-gray-100 pt-3 dark:border-dark-800">
          <button type="button" class="mx-auto block cursor-zoom-in" @click="lightboxOpen = true">
            <img
              :src="previewUrl"
              :alt="t('settings.statusPush.previewAlt')"
              class="max-h-96 rounded-lg border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800"
            />
          </button>
          <p class="mt-2 text-center text-xs text-gray-400 dark:text-dark-500">{{ t('settings.statusPush.previewClickHint') }}</p>
        </div>

        <ImageLightbox
          :images="lightboxImages"
          :caption="t('settings.statusPush.previewCaption')"
          @close="lightboxOpen = false"
        />
      </div>

      <!-- 立即推送测试 -->
      <div class="card p-5">
        <div class="flex flex-wrap items-start justify-between gap-3">
          <div>
            <p class="font-medium text-gray-900 dark:text-white">{{ t('settings.statusPush.testTitle') }}</p>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">{{ t('settings.statusPush.testDesc') }}</p>
          </div>
          <button type="button" class="btn btn-primary text-xs" :disabled="testSending" @click="testPush">
            {{ testSending ? t('common.loading') : t('settings.statusPush.testButton') }}
          </button>
        </div>

        <div v-if="testResults !== null" class="mt-3 space-y-1.5 border-t border-gray-100 pt-3 dark:border-dark-800">
          <p v-if="!testResults.length" class="text-sm italic text-gray-400">{{ t('settings.statusPush.testNoTargets') }}</p>
          <div
            v-for="(r, i) in testResults" :key="`${i}:${r.chat_id}`"
            class="flex items-start gap-2 rounded-lg px-2.5 py-2 text-xs"
            :class="r.ok ? 'bg-emerald-50 dark:bg-emerald-900/20' : 'bg-red-50 dark:bg-red-900/20'"
          >
            <Icon
              :name="r.ok ? 'checkCircle' : 'xCircle'" size="sm" class="mt-px flex-shrink-0"
              :class="r.ok ? 'text-emerald-600 dark:text-emerald-400' : 'text-red-600 dark:text-red-400'"
            />
            <div class="min-w-0 flex-1">
              <div class="flex flex-wrap items-center gap-2">
                <span class="font-mono text-gray-700 dark:text-gray-300">{{ r.chat_id }}</span>
                <span :class="r.ok ? 'text-emerald-700 dark:text-emerald-300' : 'text-red-700 dark:text-red-300'">
                  {{ r.ok ? t('settings.statusPush.testOk') : t('settings.statusPush.testFailed') }}
                </span>
              </div>
              <!-- Telegram 的报错常常很长（含 description/参数），原样换行展示，不要截断 -->
              <p v-if="r.error" class="mt-1 whitespace-pre-wrap break-all font-mono text-red-600 dark:text-red-300">{{ r.error }}</p>
            </div>
          </div>
        </div>
      </div>

      <!-- 最近一次推送 -->
      <div class="card p-5">
        <p class="font-medium text-gray-900 dark:text-white">{{ t('settings.statusPush.lastPushTitle') }}</p>
        <p v-if="!lastPush" class="mt-1 text-sm italic text-gray-400">{{ t('settings.statusPush.lastPushNever') }}</p>
        <div v-else class="mt-2 space-y-1.5 border-t border-gray-100 pt-3 text-xs dark:border-dark-800">
          <div class="flex flex-wrap items-center gap-x-4 gap-y-1 text-gray-500 dark:text-dark-400">
            <span>{{ t('settings.statusPush.lastPushAt', { time: fmtDateTime(lastPush.at) }) }}</span>
            <span :class="lastPush.ok ? 'text-emerald-600 dark:text-emerald-400' : 'text-red-600 dark:text-red-400'">
              {{ t('settings.statusPush.lastPushSummary', { sent: lastPush.sent, failed: lastPush.failed }) }}
            </span>
          </div>
          <p v-if="lastPush.error" class="whitespace-pre-wrap break-all rounded-lg bg-red-50 px-2.5 py-2 font-mono text-red-600 dark:bg-red-900/20 dark:text-red-300">
            {{ t('settings.statusPush.lastPushError') }}: {{ lastPush.error }}
          </p>
          <template v-if="lastPush.results?.length">
            <p class="pt-1 text-gray-500 dark:text-dark-400">{{ t('settings.statusPush.lastPushResults') }}</p>
            <div
              v-for="(r, i) in lastPush.results" :key="`last:${i}:${r.chat_id}`"
              class="flex items-start gap-2"
            >
              <Icon
                :name="r.ok ? 'checkCircle' : 'xCircle'" size="sm" class="mt-px flex-shrink-0"
                :class="r.ok ? 'text-emerald-600 dark:text-emerald-400' : 'text-red-600 dark:text-red-400'"
              />
              <div class="min-w-0 flex-1">
                <span class="font-mono text-gray-600 dark:text-gray-300">{{ r.chat_id }}</span>
                <span v-if="r.error" class="ml-2 whitespace-pre-wrap break-all font-mono text-red-600 dark:text-red-300">{{ r.error }}</span>
              </div>
            </div>
          </template>
        </div>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
/**
 * 渠道状态图定时推送（Telegram）。
 *
 * 【为什么整体自持状态】这些字段与「自动化与策略」「通知与渠道」零重叠，父级 Settings.vue
 * 只需要在保存按钮上按 activeTab 调一次 save()，用 expose 而不是把十几个字段再往上传，
 * 免得父组件变成中转站。保存成功必须用后端返回值覆盖本地值：后端会做 normalize。
 */
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { settingsApi } from '@/api'
import { errorMessage } from '@/api/client'
import { useAppStore } from '@/stores/app'
import { fmtDateTime } from '@/utils/format'
import type { StatusPushInfo, StatusPushResultItem, StatusPushSettings } from '@/types'
import Icon from '@/components/icons/Icon.vue'
import ImageLightbox from '@/components/common/ImageLightbox.vue'
import LoadingState from '@/components/common/LoadingState.vue'
import Select from '@/components/common/Select.vue'
import ToggleSwitch from '@/components/common/ToggleSwitch.vue'

const { t } = useI18n()
const app = useAppStore()

// 兜底默认值：与后端默认一致，只在 GET 未回来时短暂生效
function defaultSettings(): StatusPushSettings {
  return {
    enabled: false,
    interval_minutes: 60,
    window_minutes: 90,
    chat_ids: [],
    excluded_group_ids: [],
    title: t('settings.statusPush.defaultTitle')
  }
}

const loading = ref(true)
const info = ref<StatusPushInfo | null>(null)
const settings = ref<StatusPushSettings>(defaultSettings())
/** textarea 的原始文本；提交时才解析，避免用户敲一半就被 trim 掉 */
const chatIdsText = ref('')

const groups = computed(() => info.value?.available_groups ?? [])
const lastPush = computed(() => info.value?.last_push ?? null)

// 后端下发档位；缺失时回落到契约里的固定档位，避免下拉是空的
const intervalOptions = computed<{ value: number; label: string }[]>(() =>
  (info.value?.interval_options?.length ? info.value.interval_options : [30, 60, 120, 180, 240, 360, 720]).map((n) => ({
    value: n,
    label: t('settings.statusPush.intervalOption', { n })
  }))
)
const windowOptions = computed<{ value: number; label: string }[]>(() =>
  (info.value?.window_options?.length ? info.value.window_options : [60, 90, 180, 360]).map((n) => ({
    value: n,
    label: t('settings.statusPush.windowOption', { n })
  }))
)

/** 每行一个 chat id：trim、丢空行、去重（保序），后端不再关心格式问题 */
function parseChatIds(text: string): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const line of text.split('\n')) {
    const id = line.trim()
    if (!id || seen.has(id)) continue
    seen.add(id)
    out.push(id)
  }
  return out
}

const chatIds = computed(() => parseChatIds(chatIdsText.value))

function onIntervalChange(v: string | number | boolean | null): void {
  const n = Number(v)
  if (!Number.isFinite(n) || n <= 0) return
  settings.value = { ...settings.value, interval_minutes: Math.round(n) }
}

function onWindowChange(v: string | number | boolean | null): void {
  const n = Number(v)
  if (!Number.isFinite(n) || n <= 0) return
  settings.value = { ...settings.value, window_minutes: Math.round(n) }
}

function isExcluded(id: number): boolean {
  return settings.value.excluded_group_ids.includes(id)
}

/** 勾选结果按 available_groups 顺序重排，落库的数组顺序与页面展示一致，便于比对 */
function sortByGroupOrder(ids: number[]): number[] {
  const order = new Map<number, number>(groups.value.map((g, i): [number, number] => [g.id, i]))
  // 已不在可选列表里的 id（分组被删/隐藏）排到末尾，靠 sort 的稳定性保持原有相对顺序
  return [...ids].sort(
    (a, b) => (order.get(a) ?? Number.MAX_SAFE_INTEGER) - (order.get(b) ?? Number.MAX_SAFE_INTEGER)
  )
}

function toggleGroup(id: number): void {
  const cur = settings.value.excluded_group_ids
  const next = cur.includes(id) ? cur.filter((x) => x !== id) : [...cur, id]
  settings.value = { ...settings.value, excluded_group_ids: sortByGroupOrder(next) }
}

function selectAllGroups(): void {
  settings.value = { ...settings.value, excluded_group_ids: groups.value.map((g) => g.id) }
}

function clearGroupSelection(): void {
  settings.value = { ...settings.value, excluded_group_ids: [] }
}

/** 后端 normalize 后的值一律覆盖本地（含 chat_ids 的排序/去重） */
function applySettings(s: StatusPushSettings): void {
  settings.value = { ...s, chat_ids: s.chat_ids || [], excluded_group_ids: s.excluded_group_ids || [] }
  chatIdsText.value = (s.chat_ids || []).join('\n')
}

async function load(silent = false): Promise<void> {
  // 保存后的重新加载走 silent：数据已经就位，没必要让整页闪一次 loading
  if (!silent) loading.value = true
  try {
    const data = await settingsApi.getStatusPush()
    info.value = data
    applySettings(data.settings)
  } catch (e) {
    app.showError(errorMessage(e))
  } finally {
    if (!silent) loading.value = false
  }
}

function buildPayload(): StatusPushSettings {
  return {
    enabled: settings.value.enabled,
    interval_minutes: settings.value.interval_minutes,
    window_minutes: settings.value.window_minutes,
    chat_ids: parseChatIds(chatIdsText.value),
    excluded_group_ids: sortByGroupOrder(settings.value.excluded_group_ids),
    title: settings.value.title.trim()
  }
}

/** 保存失败向上抛：父级统一弹错误提示 */
async function save(): Promise<void> {
  // 【未成功加载过就不许保存】load 失败（例如线上库不可用）时 settings 仍是
  // defaultSettings()，此时 PUT 会把后端已保存的配置整体覆盖成默认值，
  // 顺带把 enabled 置 false 关掉定时推送 —— 而父组件照样会弹「配置已保存」。
  // 抛出去让父组件的 catch 提示，绝不能把一次网络抖动变成静默的数据丢失。
  if (!info.value) {
    throw new Error(t('settings.statusPush.notLoaded'))
  }
  const saved = await settingsApi.saveStatusPush(buildPayload())
  applySettings(saved)
  // 再拉一次以刷新 last_push / available_groups 等派生信息；load 内部已自己吞错误
  await load(true)
}

// ---- 预览图 ----
const previewUrl = ref('')
const previewing = ref(false)
const previewError = ref('')
const lightboxOpen = ref(false)

// 用 computed 而不是模板里现搓数组：内联数组每次渲染都是新引用，
// ImageLightbox 内部 watch(props.images) 会被无谓触发
const lightboxImages = computed(() => (lightboxOpen.value && previewUrl.value ? [previewUrl.value] : []))

/** responseType=blob 时错误响应体也是 Blob，errorMessage 读不到 envelope 里的 message，这里补一刀 */
async function previewErrorText(e: unknown): Promise<string> {
  const resp = (e as { response?: { data?: unknown } } | null)?.response
  const data = resp?.data
  if (data instanceof Blob) {
    try {
      const body = JSON.parse(await data.text()) as { message?: string }
      if (body.message) return body.message
    } catch {
      // 不是 JSON（例如网关返回 HTML），落到通用文案
    }
  }
  return errorMessage(e)
}

async function loadPreview(): Promise<void> {
  previewing.value = true
  previewError.value = ''
  try {
    // 传表单当前值（可能还没保存）：预览必须与眼前的下拉/勾选一致
    const blob = await settingsApi.statusPushPreview({
      windowMinutes: settings.value.window_minutes,
      excludedGroupIds: settings.value.excluded_group_ids
    })
    // 先释放上一张：反复点预览会不断产生 blob URL，不撤销就泄漏到页面关闭
    revokePreview()
    previewUrl.value = URL.createObjectURL(blob)
  } catch (e) {
    // 失败时连同旧图一起清掉，否则错误提示旁边还挂着一张过期预览，分不清是哪一次的结果
    revokePreview()
    previewError.value = await previewErrorText(e)
  } finally {
    previewing.value = false
  }
}

function revokePreview(): void {
  if (!previewUrl.value) return
  URL.revokeObjectURL(previewUrl.value)
  previewUrl.value = ''
  lightboxOpen.value = false
}

onUnmounted(revokePreview)

// ---- 立即推送测试 ----
const testSending = ref(false)
const testResults = ref<StatusPushResultItem[] | null>(null)

async function testPush(): Promise<void> {
  testSending.value = true
  try {
    const r = await settingsApi.testStatusPush()
    const results = r.results || []
    testResults.value = results
    const failed = results.filter((x) => !x.ok).length
    if (!results.length) {
      app.showError(t('settings.statusPush.testNoTargets'))
    } else if (failed === 0) {
      app.showSuccess(t('settings.statusPush.testAllOk', { n: results.length }))
    } else {
      app.showError(t('settings.statusPush.testSomeFailed', { ok: results.length - failed, failed }))
    }
  } catch (e) {
    app.showError(errorMessage(e))
  } finally {
    testSending.value = false
  }
}

defineExpose({ save, reload: load })

// 【必须自己拉数据】父级用 v-if 只在「渠道推送」tab 下渲染本组件（切走即销毁），
// 所以挂载即等于「用户打开了这个 tab」。父级刻意不代为调用 reload：它只负责按
// activeTab 分发保存，把加载时机留在数据自持的组件里，两边不会各拉一次。
// 不写这一句，面板会永久停在「加载中…」——loading 初值是 true，而没有任何人翻它。
onMounted(() => {
  void load()
})
</script>
