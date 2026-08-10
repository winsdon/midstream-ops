<template>
  <main class="min-h-dvh w-full bg-gray-50 dark:bg-dark-950">
    <!-- 参数缺失 / 会话失败 -->
    <div v-if="fatalError" class="mx-auto max-w-lg px-4 pt-16">
      <EmptyState icon="exclamationTriangle" :title="t(fatalError)" :description="t('plaza.errors.openFromMenu')" />
    </div>

    <div v-else-if="loading" class="pt-24">
      <LoadingState :label="t('common.loading')" size="lg" />
    </div>

    <!-- 一把可用 key 都没有：多半是分组没开生成能力 -->
    <div v-else-if="!keys.length" class="mx-auto max-w-lg px-4 pt-16">
      <EmptyState icon="key" :title="t('media.errors.noKeys')" :description="t('media.errors.noKeysHint')" />
    </div>

    <div v-else class="mx-auto max-w-6xl px-3 py-4 sm:px-5 sm:py-6 lg:px-8">
      <!-- 页头 -->
      <header class="mb-5 flex items-start gap-3 sm:mb-6">
        <div
          class="flex h-11 w-11 shrink-0 items-center justify-center rounded-2xl bg-primary-50 text-primary-600 shadow-sm ring-1 ring-primary-100 dark:bg-primary-900/30 dark:text-primary-400 dark:ring-primary-800/50"
        >
          <Icon name="sparkles" size="md" />
        </div>
        <div class="min-w-0 pt-0.5">
          <h1 class="text-xl font-semibold tracking-tight text-gray-900 dark:text-white">
            {{ t('media.title') }}
          </h1>
          <p class="mt-0.5 text-sm leading-relaxed text-gray-500 dark:text-dark-400">
            {{ t('media.intro') }}
          </p>
        </div>
      </header>

      <div class="grid gap-5 lg:grid-cols-12 lg:items-start lg:gap-6">
        <!-- 左：生成表单（桌面 sticky） -->
        <section class="lg:col-span-5 lg:sticky lg:top-4 lg:self-start">
          <div class="card overflow-hidden">
            <!-- 模式切换：分段控件 -->
            <div class="border-b border-gray-100 bg-gray-50/80 px-3 py-3 dark:border-dark-700 dark:bg-dark-900/40 sm:px-4">
              <div class="tabs w-full">
                <button
                  v-for="k in availableKinds"
                  :key="k"
                  type="button"
                  class="tab flex-1 px-2 py-1.5 text-xs sm:text-sm"
                  :class="form.kind === k ? 'tab-active' : ''"
                  @click="switchKind(k)"
                >
                  {{ t(`media.kind.${k}`) }}
                </button>
              </div>
            </div>

            <div class="space-y-5 p-4 sm:p-5">
              <!-- Key + 模型 -->
              <div class="space-y-3.5">
                <div>
                  <label class="input-label">{{ t('media.form.key') }}</label>
                  <select v-model.number="form.keyId" class="input" @change="onKeyChange">
                    <option v-for="k in keys" :key="k.id" :value="k.id">
                      {{ k.name }} · {{ k.masked_key }} · {{ k.group_name }}
                    </option>
                  </select>
                </div>

                <div>
                  <label class="input-label">{{ t('media.form.model') }}</label>
                  <select v-model="form.model" class="input font-mono text-[13px]">
                    <option v-for="m in currentModels" :key="m.name" :value="m.name">{{ m.name }}</option>
                  </select>
                  <p v-if="!currentModels.length" class="input-hint text-amber-600 dark:text-amber-400">
                    {{ t('media.errors.noModelForKind') }}
                  </p>
                </div>
              </div>

              <!-- 提示词 -->
              <div>
                <div class="mb-1.5 flex items-center justify-between">
                  <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
                    {{ t('media.form.prompt') }}
                  </label>
                  <span
                    class="font-mono text-[11px] tabular-nums"
                    :class="
                      form.prompt.length > PROMPT_MAX_LEN * 0.9
                        ? 'text-amber-600 dark:text-amber-400'
                        : 'text-gray-400 dark:text-dark-500'
                    "
                  >
                    {{ form.prompt.length }} / {{ PROMPT_MAX_LEN }}
                  </span>
                </div>
                <textarea
                  v-model="form.prompt"
                  class="input min-h-[7.5rem] resize-y leading-relaxed"
                  :maxlength="PROMPT_MAX_LEN"
                  :placeholder="t('media.form.promptPlaceholder')"
                />
              </div>

              <!-- 参考图：图生图上传 -->
              <div v-if="needsUpload(form.kind)">
                <label class="input-label">{{ t('media.form.refImage') }}</label>
                <label
                  class="group flex cursor-pointer flex-col items-center justify-center gap-2 rounded-xl border-2 border-dashed border-gray-200 bg-gray-50/60 px-4 py-6 transition-colors hover:border-primary-300 hover:bg-primary-50/40 dark:border-dark-600 dark:bg-dark-900/30 dark:hover:border-primary-700 dark:hover:bg-primary-900/10"
                  :class="files.length ? 'border-primary-300 bg-primary-50/30 dark:border-primary-700' : ''"
                >
                  <div
                    class="flex h-10 w-10 items-center justify-center rounded-full bg-white text-gray-400 shadow-sm ring-1 ring-gray-100 transition-colors group-hover:text-primary-500 dark:bg-dark-800 dark:ring-dark-700 dark:group-hover:text-primary-400"
                  >
                    <Icon name="upload" size="sm" />
                  </div>
                  <div class="text-center">
                    <p class="text-sm font-medium text-gray-700 dark:text-dark-200">
                      {{ files.length ? t('media.form.refImageSelected', { n: files.length }) : t('media.form.refImagePick') }}
                    </p>
                    <p class="mt-0.5 text-xs text-gray-400 dark:text-dark-500">{{ t('media.form.refImageHint') }}</p>
                  </div>
                  <input type="file" accept="image/*" class="sr-only" multiple @change="onFileChange" />
                </label>
                <ul v-if="files.length" class="mt-2 space-y-1">
                  <li
                    v-for="(f, i) in files"
                    :key="`${f.name}-${i}`"
                    class="flex items-center gap-2 rounded-lg bg-gray-50 px-2.5 py-1.5 text-xs text-gray-600 dark:bg-dark-900/50 dark:text-dark-300"
                  >
                    <Icon name="document" size="xs" class="shrink-0 text-gray-400" />
                    <span class="min-w-0 flex-1 truncate">{{ f.name }}</span>
                    <span class="shrink-0 tabular-nums text-gray-400">{{ formatFileSize(f.size) }}</span>
                  </li>
                </ul>
              </div>

              <!-- 参考图：图生视频用公网 URL -->
              <div v-if="needsImageURL(form.kind)">
                <label class="input-label">{{ t('media.form.refImageURL') }}</label>
                <div class="relative">
                  <Icon
                    name="link"
                    size="sm"
                    class="pointer-events-none absolute left-3.5 top-1/2 -translate-y-1/2 text-gray-400"
                  />
                  <input
                    v-model="form.imageURL"
                    class="input pl-10 font-mono text-[13px]"
                    placeholder="https://..."
                  />
                </div>
                <p class="input-hint">{{ t('media.form.refImageURLHint') }}</p>
              </div>

              <!-- 图片参数 -->
              <template v-if="!isVideoKind(form.kind)">
                <div
                  class="grid gap-3"
                  :class="selectedModel?.supports_size ? 'grid-cols-2' : 'grid-cols-1'"
                >
                  <div>
                    <label class="input-label">{{ t('media.form.count') }}</label>
                    <div class="flex items-center gap-1.5">
                      <button
                        type="button"
                        class="btn btn-secondary btn-icon btn-sm shrink-0"
                        :disabled="form.n <= 1"
                        @click="form.n = Math.max(1, form.n - 1)"
                      >
                        <span class="text-base leading-none">−</span>
                      </button>
                      <input
                        v-model.number="form.n"
                        type="number"
                        min="1"
                        :max="IMAGE_MAX_N"
                        class="input text-center tabular-nums"
                      />
                      <button
                        type="button"
                        class="btn btn-secondary btn-icon btn-sm shrink-0"
                        :disabled="form.n >= IMAGE_MAX_N"
                        @click="form.n = Math.min(IMAGE_MAX_N, form.n + 1)"
                      >
                        <span class="text-base leading-none">+</span>
                      </button>
                    </div>
                  </div>
                  <div v-if="selectedModel?.supports_size">
                    <label class="input-label">{{ t('media.form.quality') }}</label>
                    <div class="tabs">
                      <button
                        v-for="q in qualityOptions"
                        :key="q"
                        type="button"
                        class="tab flex-1 px-2 py-1.5 text-xs capitalize"
                        :class="form.quality === q ? 'tab-active' : ''"
                        @click="form.quality = q"
                      >
                        {{ q }}
                      </button>
                    </div>
                  </div>
                </div>

                <div v-if="selectedModel?.supports_size">
                  <label class="input-label">
                    {{ t('media.form.size') }}
                    <span
                      v-if="sizeTier"
                      class="badge badge-primary ml-1.5 align-middle"
                    >{{ sizeTier }}</span>
                  </label>
                  <select v-model="form.size" class="input">
                    <option value="">{{ t('media.form.sizeDefault') }}</option>
                    <option v-for="p in IMAGE_SIZE_PRESETS" :key="p.value" :value="p.value">
                      {{ p.label }} · {{ p.value }}
                    </option>
                  </select>
                  <p class="input-hint">{{ t('media.form.sizeTierHint') }}</p>
                </div>
                <p
                  v-else-if="selectedModel"
                  class="rounded-lg bg-gray-50 px-3 py-2 text-xs leading-relaxed text-gray-500 dark:bg-dark-900/50 dark:text-dark-400"
                >
                  {{ t('media.form.fixedSizeHint') }}
                </p>
              </template>

              <!-- 视频参数 -->
              <template v-else>
                <div class="grid grid-cols-2 gap-3">
                  <div>
                    <label class="input-label">{{ t('media.form.resolution') }}</label>
                    <div class="tabs">
                      <button
                        v-for="r in VIDEO_RESOLUTIONS"
                        :key="r"
                        type="button"
                        class="tab flex-1 px-2 py-1.5 text-xs"
                        :class="form.resolution === r ? 'tab-active' : ''"
                        @click="form.resolution = r"
                      >
                        {{ r }}
                      </button>
                    </div>
                  </div>
                  <div>
                    <label class="input-label">{{ t('media.form.duration') }}</label>
                    <input
                      v-model.number="form.duration"
                      type="number"
                      :min="VIDEO_MIN_DURATION"
                      :max="VIDEO_MAX_DURATION"
                      class="input tabular-nums"
                    />
                  </div>
                </div>
              </template>

              <!-- 费用预估：视频提交即扣费不退款，必须在按钮之前显示 -->
              <div
                class="rounded-xl border px-3.5 py-3"
                :class="
                  isVideoKind(form.kind)
                    ? 'border-amber-200/80 bg-amber-50/80 dark:border-amber-800/40 dark:bg-amber-950/30'
                    : 'border-gray-100 bg-gray-50 dark:border-dark-700 dark:bg-dark-900/40'
                "
              >
                <div class="flex items-center justify-between gap-3">
                  <span
                    class="text-sm"
                    :class="
                      isVideoKind(form.kind)
                        ? 'text-amber-800 dark:text-amber-200'
                        : 'text-gray-500 dark:text-dark-400'
                    "
                  >
                    {{ t('media.form.estCost') }}
                  </span>
                  <span
                    class="font-mono text-base font-semibold tabular-nums tracking-tight"
                    :class="
                      isVideoKind(form.kind)
                        ? 'text-amber-900 dark:text-amber-100'
                        : 'text-gray-900 dark:text-white'
                    "
                  >
                    {{ estimatedTicks > 0 ? `$${ticksToUSD(estimatedTicks)}` : t('media.form.estUnknown') }}
                  </span>
                </div>
                <p
                  v-if="isVideoKind(form.kind)"
                  class="mt-1.5 text-xs leading-relaxed text-amber-700 dark:text-amber-300/90"
                >
                  {{ t('media.form.videoBillingWarning') }}
                </p>
              </div>

              <p
                v-if="formError"
                class="rounded-xl border border-red-100 bg-red-50 px-3.5 py-2.5 text-sm text-red-600 dark:border-red-900/40 dark:bg-red-900/20 dark:text-red-400"
              >
                {{ formError }}
              </p>

              <button
                type="button"
                class="btn btn-primary w-full gap-2 py-3 text-[15px]"
                :disabled="busy || !currentModels.length"
                @click="onSubmit"
              >
                <Icon v-if="!busy" name="sparkles" size="sm" />
                <span v-else class="spinner border-white/40 border-t-white" />
                {{ busy ? t('common.loading') : t('media.form.submit') }}
              </button>
            </div>
          </div>
        </section>

        <!-- 右：任务列表 -->
        <section class="min-w-0 space-y-3 lg:col-span-7">
          <div class="flex items-center justify-between gap-2 px-0.5">
            <h2 class="text-sm font-semibold text-gray-800 dark:text-dark-100">
              {{ t('media.tasks.title') }}
            </h2>
            <span
              v-if="tasks.length"
              class="badge badge-gray tabular-nums"
            >{{ tasks.length }}</span>
          </div>

          <!-- 空状态：紧凑卡片，不抢主表单视觉 -->
          <div
            v-if="!tasks.length"
            class="card flex flex-col items-center justify-center px-6 py-14 text-center"
          >
            <div
              class="mb-4 flex h-14 w-14 items-center justify-center rounded-2xl bg-primary-50 text-primary-400 dark:bg-primary-900/25 dark:text-primary-500"
            >
              <Icon name="sparkles" size="lg" />
            </div>
            <p class="text-sm font-medium text-gray-700 dark:text-dark-200">{{ t('media.tasks.empty') }}</p>
            <p class="mt-1 max-w-xs text-xs leading-relaxed text-gray-400 dark:text-dark-500">
              {{ t('media.tasks.emptyHint') }}
            </p>
          </div>

          <article
            v-for="task in tasks"
            :key="task.id"
            class="card overflow-hidden transition-shadow hover:shadow-card-hover"
          >
            <!-- 元信息条 -->
            <div class="flex flex-wrap items-center gap-2 border-b border-gray-50 px-3.5 py-2.5 dark:border-dark-700/60 sm:px-4">
              <span class="badge" :class="mediaStatusClass(task.status)">
                <span
                  v-if="task.status === 'pending'"
                  class="h-1.5 w-1.5 animate-pulse rounded-full bg-current"
                />
                {{ t(`media.status.${task.status}`) }}
              </span>
              <span class="badge badge-gray">{{ t(`media.kind.${task.kind}`) }}</span>
              <span
                class="min-w-0 truncate font-mono text-[11px] text-gray-400 dark:text-dark-500"
                :title="task.model"
              >
                {{ task.model }}
              </span>
              <span class="ml-auto shrink-0 font-mono text-xs font-medium tabular-nums text-gray-600 dark:text-dark-300">
                ${{ task.cost_usd !== '0.0000' ? task.cost_usd : task.est_cost_usd }}
              </span>
            </div>

            <div class="space-y-3 p-3.5 sm:p-4">
              <p class="line-clamp-2 text-sm leading-relaxed text-gray-700 dark:text-dark-200">
                {{ task.prompt }}
              </p>

              <!-- 进行中：进度条 -->
              <div v-if="task.status === 'pending'" class="space-y-1.5">
                <div class="progress h-1.5">
                  <div class="progress-bar" :style="{ width: `${Math.max(task.progress, 4)}%` }" />
                </div>
                <p class="text-right font-mono text-[11px] tabular-nums text-gray-400 dark:text-dark-500">
                  {{ task.progress }}%
                </p>
              </div>

              <!-- 失败：视频审核拒绝时钱已扣，必须说清楚 -->
              <div
                v-if="task.status === 'failed'"
                class="flex items-start gap-2 rounded-lg bg-red-50 px-3 py-2 dark:bg-red-900/20"
              >
                <Icon name="exclamationCircle" size="sm" class="mt-0.5 shrink-0 text-red-500" />
                <p class="text-xs leading-relaxed text-red-600 dark:text-red-400">
                  {{ task.error_message }}
                </p>
              </div>

              <!-- 成功：产物预览 -->
              <div
                v-if="task.has_content && contentURLs[task.id]"
                class="overflow-hidden rounded-xl bg-gray-900 ring-1 ring-black/5 dark:ring-white/5"
              >
                <video :src="contentURLs[task.id]" controls class="aspect-video w-full" />
              </div>
              <div
                v-else-if="inlineImages[task.id]?.length"
                class="grid gap-2"
                :class="inlineImages[task.id].length > 1 ? 'grid-cols-2' : 'grid-cols-1'"
              >
                <div
                  v-for="(src, i) in inlineImages[task.id]"
                  :key="i"
                  class="overflow-hidden rounded-xl bg-gray-100 ring-1 ring-black/5 dark:bg-dark-900 dark:ring-white/5"
                >
                  <img
                    :src="src"
                    class="aspect-square w-full object-cover"
                    :alt="task.prompt"
                    loading="lazy"
                  />
                </div>
              </div>
              <!-- 图片不落库：刷新页面后历史图片不可见 -->
              <p
                v-else-if="task.status === 'succeeded' && !isVideoKind(task.kind)"
                class="rounded-lg bg-gray-50 px-3 py-2 text-xs leading-relaxed text-gray-400 dark:bg-dark-900/50 dark:text-dark-500"
              >
                {{ t('media.tasks.imageNotRetained') }}
              </p>
              <!-- 视频产物取不到：上游有保留期，过期后 request_id 会 404。
                   显式说明而不是留一个加载不出来的空白播放器。 -->
              <p
                v-else-if="task.status === 'succeeded' && isVideoKind(task.kind) && contentFailed.has(task.id)"
                class="rounded-lg bg-gray-50 px-3 py-2 text-xs leading-relaxed text-gray-400 dark:bg-dark-900/50 dark:text-dark-500"
              >
                {{ t('media.tasks.videoExpired') }}
              </p>
            </div>
          </article>
        </section>
      </div>
    </div>

    <!-- 视频提交确认：不退款，必须二次确认 -->
    <ConfirmDialog
      :show="confirmOpen"
      :title="t('media.confirm.title')"
      :message="t('media.confirm.message', { cost: ticksToUSD(estimatedTicks) })"
      :confirm-text="t('media.confirm.ok')"
      danger
      @confirm="onConfirmSubmit"
      @cancel="confirmOpen = false"
    />

    <Toast />
  </main>
</template>

<script setup lang="ts">
/**
 * 生图 / 生视频嵌入页：用户在 sub2api 站内以 iframe 打开，用自己的 key 生成图片与视频。
 *
 * 【身份不在本页】用户身份全程由后端从会话上下文取。URL 上的 user_id 只在换会话时
 * 供后端与 token claims 比对。
 *
 * 【本页看不到明文 key】后端只下发掩码，真实 key 在后端 ↔ 网关那一段使用。
 *
 * 【钱的纪律】视频任务提交成功即扣费，即便上游内容审核拒绝也不退还。因此：
 * 提交前必须展示预估费用、视频必须二次确认、每次提交都带幂等键防重复下单。
 */
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { createSession, fetchKeys, fetchTasks, generate, edit, fetchContent } from '@/api/embedMedia'
import type { MediaKey, MediaTask, MediaTaskKind } from '@/api/embedMedia'
import { applyTheme, queryString, resolveLocale, stripTokenFromUrl } from '@/utils/embedQuery'
import {
  emptyMediaForm,
  estimateTicks,
  imageSizeTier,
  isVideoKind,
  mediaStatusClass,
  modelsForKind,
  needsImageURL,
  needsUpload,
  newClientRequestID,
  ticksToUSD,
  validateMediaForm,
  IMAGE_MAX_N,
  IMAGE_SIZE_PRESETS,
  PROMPT_MAX_LEN,
  VIDEO_MAX_DURATION,
  VIDEO_MIN_DURATION,
  VIDEO_RESOLUTIONS
} from '@/utils/mediaModel'
import Icon from '@/components/icons/Icon.vue'
import Toast from '@/components/common/Toast.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import LoadingState from '@/components/common/LoadingState.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import { useAppStore } from '@/stores/app'

const route = useRoute()
const { t, locale } = useI18n()
const app = useAppStore()

const loading = ref(true)
const busy = ref(false)
const fatalError = ref('')
const formError = ref('')
const confirmOpen = ref(false)

const keys = ref<MediaKey[]>([])
const tasks = ref<MediaTask[]>([])
const form = ref(emptyMediaForm())
const files = ref<File[]>([])

const qualityOptions = ['low', 'medium', 'high'] as const

/** 视频产物 object URL 缓存：taskId → blob URL。 */
const contentURLs = ref<Record<number, string>>({})

/**
 * 本次会话生成的图片：taskId → data URI 列表。
 *
 * 【只在内存里】图片不落库（xAI CDN 直链国内不可达，存链接等于存打不开的地址），
 * 提交响应带回字节后就只存在于这个 ref 里。刷新页面即丢失 —— 这是「只存元数据」
 * 取舍的明确代价，UI 上有对应文案说明，不留破图。
 */
const inlineImages = ref<Record<number, string[]>>({})

/** 产物取回失败过的任务 ID —— 记住以避免每轮轮询重复发必然失败的请求。 */
const contentFailed = ref<Set<number>>(new Set())

const selectedKey = computed(() => keys.value.find((k) => k.id === form.value.keyId) ?? null)
const currentModels = computed(() => modelsForKind(selectedKey.value, form.value.kind))
const selectedModel = computed(() => currentModels.value.find((m) => m.name === form.value.model) ?? null)
const estimatedTicks = computed(() => estimateTicks(form.value, selectedKey.value))
const sizeTier = computed(() => (form.value.size ? imageSizeTier(form.value.size) : ''))

/** 当前 key 支持的任务类型：没有视频模型时不显示视频 tab。 */
const availableKinds = computed<MediaTaskKind[]>(() => {
  const k = selectedKey.value
  if (!k) return []
  const out: MediaTaskKind[] = []
  if (k.image_models.length) out.push('t2i', 'i2i')
  if (k.video_models.length) out.push('t2v', 'i2v')
  return out
})

function formatFileSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

function switchKind(kind: MediaTaskKind) {
  form.value.kind = kind
  form.value.model = currentModels.value[0]?.name ?? ''
  formError.value = ''
}

function onKeyChange() {
  form.value.kind = availableKinds.value[0] ?? 't2i'
  form.value.model = currentModels.value[0]?.name ?? ''
}

function onFileChange(e: Event) {
  files.value = Array.from((e.target as HTMLInputElement).files ?? [])
}

/** 惰性加载视频产物：带会话凭据 fetch 成 blob URL。 */
async function ensureContent(task: MediaTask) {
  if (!task.has_content || contentURLs.value[task.id] || contentFailed.value.has(task.id)) return
  try {
    contentURLs.value = { ...contentURLs.value, [task.id]: await fetchContent(task.id) }
  } catch {
    // 【记住失败，不再重试】视频产物在上游有保留期，过期后 request_id 返回 404。
    // 不记的话每轮 5 秒轮询都会再发一次必然失败的请求，控制台被 404 刷屏，
    // 真正的错误反而被淹没。任务状态在本次会话内不会再变，记一次就够。
    contentFailed.value = new Set(contentFailed.value).add(task.id)
  }
}

function onSubmit() {
  formError.value = ''
  const invalid = validateMediaForm(form.value, selectedKey.value)
  if (invalid) {
    formError.value = t(invalid)
    return
  }
  if (needsUpload(form.value.kind) && !files.value.length) {
    formError.value = t('media.errors.missingImage')
    return
  }
  // 视频提交即扣费且不退款，必须二次确认
  if (isVideoKind(form.value.kind)) {
    confirmOpen.value = true
    return
  }
  void doSubmit()
}

/** 确认后关闭弹窗再提交 —— 弹窗自身不管状态，由本页控制。 */
function onConfirmSubmit() {
  confirmOpen.value = false
  void doSubmit()
}

async function doSubmit() {
  busy.value = true
  formError.value = ''
  const clientRequestID = newClientRequestID()
  try {
    let result
    if (needsUpload(form.value.kind)) {
      const fd = new FormData()
      fd.append('key_id', String(form.value.keyId))
      fd.append('model', form.value.model)
      fd.append('prompt', form.value.prompt)
      fd.append('n', String(form.value.n))
      if (form.value.size) fd.append('size', form.value.size)
      if (form.value.quality) fd.append('quality', form.value.quality)
      fd.append('client_request_id', clientRequestID)
      files.value.forEach((f) => fd.append('image', f))
      result = await edit(fd)
    } else {
      result = await generate({
        key_id: form.value.keyId,
        kind: form.value.kind as Exclude<MediaTaskKind, 'i2i'>,
        model: form.value.model,
        prompt: form.value.prompt,
        n: form.value.n,
        size: form.value.size || undefined,
        quality: selectedModel.value?.supports_size ? form.value.quality : undefined,
        resolution: form.value.resolution,
        duration: form.value.duration,
        image_url: form.value.imageURL || undefined,
        client_request_id: clientRequestID
      })
    }
    // 图片字节只随这一次响应返回，错过就没有了 —— 立刻挂到任务 ID 上
    if (result.images?.length) {
      inlineImages.value = { ...inlineImages.value, [result.task.id]: result.images }
    }
    app.showSuccess(t('media.submitted'))
    await refreshTasks()
  } catch (e) {
    // 后端返回的 message 可能是 i18n key，也可能是已脱敏的上游可读错误。
    // t() 对未登记的 key 会原样返回，两种情况都能正确展示。
    formError.value = t(e instanceof Error ? e.message : 'plaza.errors.loadFailed')
  } finally {
    busy.value = false
  }
}

async function refreshTasks() {
  try {
    tasks.value = await fetchTasks()
    // 已完成的产物惰性拉取，未加载过的才发请求
    await Promise.all(tasks.value.filter((task) => task.has_content).map(ensureContent))
  } catch {
    // 轮询失败不打断页面，等下一轮
  }
}

/**
 * 轮询间隔。文档建议 5 秒 —— 视频任务的状态刷新由后端在列表查询时顺带完成，
 * 前端只需按这个节奏拉列表。
 */
const POLL_INTERVAL = 5000
let timer: ReturnType<typeof setInterval> | null = null

onMounted(async () => {
  // 主题与语言必须先于任何网络请求应用，保证错误态也是正确外观。
  applyTheme(queryString(route.query.theme))
  locale.value = resolveLocale(queryString(route.query.lang))

  const token = queryString(route.query.token)
  const userId = queryString(route.query.user_id)
  // 拿到 token 后立刻从地址栏抹掉：请求失败或用户分享/收藏地址都会泄露明文 token。
  if (token) stripTokenFromUrl()

  if (!token) {
    fatalError.value = 'plaza.errors.missingParams'
    loading.value = false
    return
  }

  try {
    await createSession(token, userId)
    keys.value = await fetchKeys()
    if (keys.value.length) {
      form.value.keyId = keys.value[0].id
      onKeyChange()
    }
    await refreshTasks()
    // 只在有进行中任务时才轮询，避免空转
    timer = setInterval(() => {
      if (tasks.value.some((task) => task.status === 'pending')) void refreshTasks()
    }, POLL_INTERVAL)
  } catch (e) {
    fatalError.value = e instanceof Error ? e.message : 'plaza.errors.loadFailed'
  } finally {
    loading.value = false
  }
})

onUnmounted(() => {
  if (timer) clearInterval(timer)
  // blob URL 必须显式释放，否则一直占内存
  Object.values(contentURLs.value).forEach((url) => URL.revokeObjectURL(url))
})
</script>
