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
        <!-- 左：生成表单（桌面 sticky）
             Tab 钉顶、按钮钉底，只滚中间参数。dvh 而非 vh：移动端地址栏收放时 vh 不变会算错。 -->
        <section
          class="lg:col-span-5 lg:sticky lg:top-4 lg:flex lg:max-h-[calc(100dvh-2rem)] lg:min-h-0 lg:flex-col lg:self-start lg:overflow-hidden"
        >
          <div class="media-panel flex min-h-0 flex-1 flex-col overflow-hidden">
            <!-- 模式切换：分段控件（钉在顶，不参与中间滚动） -->
            <div class="media-panel-tabs shrink-0 border-b border-gray-100 bg-gray-50/80 px-3 py-3 dark:border-dark-700 dark:bg-dark-900/40 sm:px-4">
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

            <div class="media-panel-body min-h-0 flex-1 space-y-5 overflow-y-auto overscroll-contain p-4 sm:p-5">
              <p class="media-section-kicker">{{ t('media.form.parameters') }}</p>
              <!-- Key + 模型 -->
              <div class="space-y-3.5">
                <div>
                  <label class="input-label">{{ t('media.form.key') }}</label>
                  <Select
                    v-model="form.keyId"
                    :options="keyOptions"
                    :searchable="keys.length > 5"
                    @change="onKeyChange"
                  />
                </div>

                <div>
                  <label class="media-label">{{ t('media.form.model') }}</label>
                  <Select
                    v-model="form.model"
                    :options="modelOptions"
                    :searchable="currentModels.length > 5"
                    @change="syncModelDefaults"
                  />
                  <p v-if="!currentModels.length" class="input-hint text-amber-600 dark:text-amber-400">
                    {{ t('media.errors.noModelForKind') }}
                  </p>
                  <!-- 上游静默换模型：不说清楚的话，用户以为自己在用所选的那个 -->
                  <p
                    v-else-if="downgradeTarget"
                    class="mt-1.5 flex items-start gap-1.5 rounded-lg bg-amber-50 px-2.5 py-1.5 text-xs leading-relaxed text-amber-700 dark:bg-amber-950/30 dark:text-amber-300"
                  >
                    <Icon name="exclamationTriangle" size="xs" class="mt-0.5 shrink-0" />
                    <span>{{ t('media.form.downgradeHint', { model: downgradeTarget }) }}</span>
                  </p>
                </div>
              </div>


              <!-- 提示词 -->
              <div>
                <div class="mb-1.5 flex items-center justify-between">
                  <label class="media-label mb-0">
                    {{ t('media.form.prompt') }}
                  </label>
                  <span class="media-counter">
                    {{ form.prompt.length }} / {{ PROMPT_MAX_LEN }}
                  </span>
                </div>
                <textarea
                  v-model="form.prompt"
                  class="media-textarea min-h-[7.5rem] resize-y leading-relaxed"
                  :maxlength="PROMPT_MAX_LEN"
                  :placeholder="t('media.form.promptPlaceholder')"
                />
              </div>

              <!-- 参考图：图生图只走本地上传；图生视频可用按钮切换上传 / 填地址。 -->
              <div v-if="needsUpload(form.kind)">
                <div class="mb-1.5 flex items-center justify-between">
                  <label class="media-label mb-0">{{ t('media.form.refImage') }}</label>
                  <span class="media-counter">{{ refImageCount }} / {{ REF_IMAGE_MAX }}</span>
                </div>
                <div v-if="form.kind === 'i2v'" class="mb-3 tabs w-full">
                  <button
                    type="button"
                    class="tab flex-1 px-2 py-1.5 text-xs sm:text-sm"
                    :class="refSource === 'upload' ? 'tab-active' : ''"
                    @click="refSource = 'upload'"
                  >
                    {{ t('media.form.refSourceUpload') }}
                  </button>
                  <button
                    type="button"
                    class="tab flex-1 px-2 py-1.5 text-xs sm:text-sm"
                    :class="refSource === 'url' ? 'tab-active' : ''"
                    @click="refSource = 'url'"
                  >
                    {{ t('media.form.refSourceURL') }}
                  </button>
                </div>

                <template v-if="form.kind !== 'i2v' || refSource === 'upload'">
                  <label
                    class="group flex flex-col items-center justify-center gap-2 rounded-xl border-2 border-dashed px-4 py-6 transition-colors"
                    :class="refPickerClass"
                  >
                    <div
                      class="flex h-10 w-10 items-center justify-center rounded-full bg-white text-gray-400 shadow-sm ring-1 ring-gray-100 transition-colors group-hover:text-primary-500 dark:bg-dark-800 dark:ring-dark-700 dark:group-hover:text-primary-400"
                    >
                      <Icon name="upload" size="sm" />
                    </div>
                    <div class="text-center">
                      <p class="text-sm font-medium text-gray-700 dark:text-dark-200">
                        {{ refPickerTitle }}
                      </p>
                      <p class="mt-0.5 text-xs text-gray-400 dark:text-dark-500">
                        {{ isVideoKind(form.kind) ? t('media.form.refImageHintVideo') : t('media.form.refImageHint') }}
                      </p>
                    </div>
                    <input
                      type="file"
                      accept="image/jpeg,image/png,image/webp"
                      class="sr-only"
                      multiple
                      :disabled="refSlotsLeft <= 0"
                      @change="onFileChange"
                    />
                  </label>
                  <ul v-if="files.length" class="mt-2 space-y-1">
                    <li
                      v-for="(f, i) in files"
                      :key="`${f.name}-${f.size}-${f.lastModified}-${i}`"
                      class="flex items-center gap-2 rounded-lg bg-gray-50 px-2.5 py-1.5 text-xs text-gray-600 dark:bg-dark-900/50 dark:text-dark-300"
                    >
                      <img
                        v-if="filePreviews[i]"
                        :src="filePreviews[i]"
                        :alt="f.name"
                        class="h-8 w-8 shrink-0 rounded object-cover"
                      />
                      <Icon v-else name="document" size="xs" class="shrink-0 text-gray-400" />
                      <span class="min-w-0 flex-1 truncate">{{ f.name }}</span>
                      <span class="shrink-0 tabular-nums text-gray-400">{{ formatFileSize(f.size) }}</span>
                      <button
                        type="button"
                        class="icon-btn shrink-0 text-gray-400 hover:text-red-600 dark:text-dark-500 dark:hover:text-red-400"
                        :title="t('media.form.refImageRemove')"
                        @click="removeFile(i)"
                      >
                        <Icon name="trash" size="xs" />
                      </button>
                    </li>
                  </ul>
                </template>

                <template v-else>
                  <p class="input-hint mb-2">{{ t('media.form.refImageURLHint') }}</p>
                  <ul v-if="typedRefURLs.length" class="mb-2 space-y-1">
                    <li
                      v-for="(item, i) in typedRefURLs"
                      :key="`url-${item}`"
                      class="flex items-center gap-2 rounded-lg bg-gray-50 px-2.5 py-1.5 text-xs text-gray-600 dark:bg-dark-900/50 dark:text-dark-300"
                    >
                      <img :src="item" alt="" class="h-8 w-8 shrink-0 rounded object-cover" />
                      <span class="min-w-0 flex-1 truncate font-mono">{{ item }}</span>
                      <button
                        type="button"
                        class="icon-btn shrink-0 text-gray-400 hover:text-red-600 dark:text-dark-500 dark:hover:text-red-400"
                        :title="t('media.form.refImageRemove')"
                        @click="removeTypedURL(i)"
                      >
                        <Icon name="trash" size="xs" />
                      </button>
                    </li>
                  </ul>
                  <div class="flex gap-2">
                    <div class="relative min-w-0 flex-1">
                      <Icon
                        name="link"
                        size="sm"
                        class="pointer-events-none absolute left-3.5 top-1/2 -translate-y-1/2 text-gray-400"
                      />
                      <input
                        v-model="urlDraft"
                        class="media-select pl-10 font-mono text-[13px]"
                        :placeholder="t('media.form.refURLPlaceholder')"
                        :disabled="refSlotsLeft <= 0"
                        @keydown.enter.prevent="addTypedURLs"
                      />
                    </div>
                    <button
                      type="button"
                      class="btn btn-secondary shrink-0"
                      :disabled="refSlotsLeft <= 0"
                      @click="addTypedURLs"
                    >
                      {{ t('media.form.refURLAdd') }}
                    </button>
                  </div>
                </template>
              </div>

              <!-- 图片参数 -->
              <template v-if="!isVideoKind(form.kind)">
                <div class="media-design-controls">
                  <!-- aspect_ratio 模式（Grok）：宽高比 + 分辨率档。
                       传 size 对这类模型无效——sub2api 网关会在转发前删掉它。 -->
                  <template v-if="selectedModel?.size_mode === 'aspect_ratio'">
                    <div class="space-y-2">
                      <label class="media-label">{{ t('media.form.aspectRatio') }}</label>
                      <div class="media-ratio-grid">
                        <button
                          v-for="ratio in selectedModel.aspect_ratios ?? []"
                          :key="ratio"
                          type="button"
                          class="media-ratio"
                          :class="form.aspectRatio === ratio ? 'media-ratio-active' : ''"
                          @click="form.aspectRatio = ratio"
                        >
                          <span class="media-ratio-shape" :class="ratioClass(ratio)" />
                          <span>{{ ratio }}</span>
                        </button>
                      </div>
                    </div>
                    <div class="space-y-2">
                      <div class="flex items-center justify-between">
                        <label class="media-label mb-0">{{ t('media.form.imageResolution') }}</label>
                        <span class="media-value">{{ billingTier }}</span>
                      </div>
                      <Select
                        v-model="form.imageResolution"
                        :options="imageResolutionOptions"
                        :searchable="false"
                      />
                    </div>
                  </template>

                  <!-- size 模式（gpt-image-*）：真实像素尺寸，宽高比由尺寸反推 -->
                  <template v-else>
                    <div class="space-y-2">
                      <div class="flex items-center justify-between">
                        <label class="media-label mb-0">{{ t('media.form.size') }}</label>
                        <span class="media-value">{{ billingTier }}</span>
                      </div>
                      <Select
                        v-model="form.size"
                        :options="imageSizeOptions"
                        :searchable="false"
                      />
                      <p class="input-hint">{{ t('media.form.sizeTierHint') }}</p>
                    </div>
                  </template>

                  <div class="space-y-2">
                    <div class="flex items-center justify-between"><label class="media-label mb-0">{{ t('media.form.count') }}</label><span class="media-value">{{ form.n }}</span></div>
                    <input v-model.number="form.n" type="range" min="1" :max="IMAGE_MAX_N" class="media-range" />
                  </div>
                  <label class="media-check-row"><input v-model="form.stream" type="checkbox" class="media-checkbox" /><span>{{ t('media.form.streamPreview') }}</span></label>
                </div>
              </template>

              <!-- 视频参数 -->
              <template v-else>
                <div class="media-design-controls">
                  <div class="space-y-2">
                    <div class="flex items-center justify-between"><label class="media-label mb-0">{{ t('media.form.duration') }}</label><span class="media-value">{{ form.duration }}</span></div>
                    <input v-model.number="form.duration" type="range" :min="VIDEO_MIN_DURATION" :max="VIDEO_MAX_DURATION" class="media-range" />
                  </div>
                  <div class="space-y-2"><label class="media-label">{{ t('media.form.resolution') }}</label><Select v-model="form.resolution" :options="videoResolutionOptions" :searchable="false" /></div>
                  <div class="space-y-2">
                    <label class="media-label">{{ t('media.form.aspectRatio') }}</label>
                    <div class="media-ratio-grid">
                      <button
                        v-for="ratio in selectedModel?.aspect_ratios ?? []"
                        :key="ratio"
                        type="button"
                        class="media-ratio"
                        :class="form.aspectRatio === ratio ? 'media-ratio-active' : ''"
                        @click="form.aspectRatio = ratio"
                      >
                        <span class="media-ratio-shape" :class="ratioClass(ratio)" />
                        <span>{{ ratio }}</span>
                      </button>
                    </div>
                    <p v-if="needsImageURL(form.kind)" class="input-hint">{{ t('media.form.aspectFollowsRefImage') }}</p>
                  </div>
                </div>
              </template>

            </div>

            <div class="media-panel-footer shrink-0">
              <p
                v-if="formError"
                class="mb-3 rounded-xl border border-red-100 bg-red-50 px-3.5 py-2.5 text-sm text-red-600 dark:border-red-900/40 dark:bg-red-900/20 dark:text-red-400"
              >
                {{ formError }}
              </p>
              <button
                type="button"
                class="media-submit w-full gap-2 py-3 text-[15px]"
                :disabled="busy || !currentModels.length"
                @click="onSubmit"
              >
                <Icon v-if="!busy" name="sparkles" size="sm" />
                <span v-else class="spinner border-white/40 border-t-white" />
                {{ submitLabel }}
              </button>
              <p
                v-if="estimatedTicks > 0 && selectedKey && !selectedKey.pricing_known"
                class="mt-2 text-center text-xs leading-relaxed text-gray-400 dark:text-dark-500"
              >
                {{ t('media.form.estReferenceOnly') }}
              </p>
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
              <div class="flex shrink-0 items-center gap-1">
                <button
                  type="button"
                  class="icon-btn text-gray-400 hover:text-primary-600 dark:text-dark-500 dark:hover:text-primary-400"
                  :title="t('media.tasks.reuse')"
                  @click="reuseTask(task)"
                >
                  <Icon name="refresh" size="sm" />
                </button>
                <button
                  type="button"
                  class="icon-btn text-gray-400 hover:text-red-600 dark:text-dark-500 dark:hover:text-red-400"
                  :title="t('media.tasks.delete')"
                  @click="askDeleteTask(task)"
                >
                  <Icon name="trash" size="sm" />
                </button>
              </div>
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

              <!-- 成功：产物预览。
                   优先级链：R2 长期链接 → 本次会话的 data URI → 视频代理 blob。
                   第一项刷新后依然可见，后两项只在本次会话内有效。 -->
              <template v-if="videoSrc(task)">
                <div class="overflow-hidden rounded-xl bg-gray-900 ring-1 ring-black/5 dark:ring-white/5">
                  <video :src="videoSrc(task)" controls class="aspect-video w-full" />
                </div>
              </template>

              <template v-else-if="taskImages(task).length">
                <!-- 【每一张都要显示】n 最大为 4，旧实现固定两列 + object-cover，
                     非方图被裁掉边缘，用户看不到自己付费生成的完整画面。 -->
                <div class="grid gap-2" :class="taskImages(task).length > 1 ? 'grid-cols-2' : 'grid-cols-1'">
                  <button
                    v-for="(src, i) in taskImages(task)"
                    :key="i"
                    type="button"
                    class="group/img relative overflow-hidden rounded-xl bg-gray-100 ring-1 ring-black/5 transition hover:ring-primary-300 dark:bg-dark-900 dark:ring-white/5"
                    :aria-label="t('media.preview.title')"
                    @click="openPreview(task, i)"
                  >
                    <img
                      :src="src"
                      class="max-h-72 w-full object-contain"
                      :alt="task.prompt"
                      loading="lazy"
                    />
                    <span
                      class="pointer-events-none absolute inset-0 flex items-center justify-center bg-black/0 transition-colors group-hover/img:bg-black/25"
                    >
                      <Icon
                        name="eye"
                        size="lg"
                        class="text-white opacity-0 drop-shadow-lg transition-opacity group-hover/img:opacity-100"
                      />
                    </span>
                  </button>
                </div>
              </template>

              <!-- 视频产物尚未转存完成，且代理也取不到：说清楚而不是留空白播放器 -->
              <p
                v-else-if="task.status === 'succeeded' && isVideoKind(task.kind) && contentFailed.has(task.id)"
                class="rounded-lg bg-gray-50 px-3 py-2 text-xs leading-relaxed text-gray-400 dark:bg-dark-900/50 dark:text-dark-500"
              >
                {{ t('media.tasks.videoExpired') }}
              </p>
              <p
                v-else-if="task.status === 'succeeded' && !isVideoKind(task.kind)"
                class="rounded-lg bg-gray-50 px-3 py-2 text-xs leading-relaxed text-gray-400 dark:bg-dark-900/50 dark:text-dark-500"
              >
                {{ t('media.tasks.imageNotRetained') }}
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

    <ConfirmDialog
      :show="deleteConfirmOpen"
      :title="t('media.tasks.deleteTitle')"
      :message="t('media.tasks.deleteMessage')"
      :confirm-text="t('media.tasks.delete')"
      danger
      @confirm="onConfirmDelete"
      @cancel="deleteConfirmOpen = false"
    />

    <ImageLightbox
      :images="preview.images"
      :start-index="preview.index"
      :caption="preview.caption"
      @close="preview = { images: [], index: 0, caption: '' }"
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
import { createSession, fetchKeys, fetchTasks, generate, prepareUploads, fetchContent, deleteTask } from '@/api/embedMedia'
import type { MediaKey, MediaTask, MediaTaskKind } from '@/api/embedMedia'
import { applyTheme, queryString, resolveLocale, stripTokenFromUrl } from '@/utils/embedQuery'
import {
  billingTierOf,
  downgradeTargetOf,
  emptyMediaForm,
  estimateTicks,
  isVideoKind,
  mediaStatusClass,
  modelsForKind,
  needsImageURL,
  needsUpload,
  newClientRequestID,
  uniquePublicImageURLs,
  appendRefImages,
  splitRefImageInput,
  resetMediaFormForKind,
  ticksToUSD,
  validateMediaForm,
  IMAGE_MAX_N,
  IMAGE_SIZE_PRESETS,
  PROMPT_MAX_LEN,
  REF_IMAGE_MAX,
  VIDEO_MAX_DURATION,
  VIDEO_MIN_DURATION
} from '@/utils/mediaModel'
import Icon from '@/components/icons/Icon.vue'
import Toast from '@/components/common/Toast.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import LoadingState from '@/components/common/LoadingState.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import ImageLightbox from '@/components/common/ImageLightbox.vue'
import Select from '@/components/common/Select.vue'
import { useAppStore } from '@/stores/app'

const route = useRoute()
const { t, locale } = useI18n()
const app = useAppStore()

const loading = ref(true)
const busy = ref(false)
const fatalError = ref('')
const formError = ref('')
const confirmOpen = ref(false)
const deleteConfirmOpen = ref(false)
const deleteTarget = ref<MediaTask | null>(null)
const deleting = ref(false)

const keys = ref<MediaKey[]>([])
const tasks = ref<MediaTask[]>([])
const form = ref(emptyMediaForm())
const files = ref<File[]>([])
const filePreviews = ref<string[]>([])
/** 图生视频参考图来源。图生图固定走 upload。 */
const refSource = ref<'upload' | 'url'>('upload')
const urlDraft = ref('')

/** 大图预览状态。images 非空即打开。 */
const preview = ref<{ images: string[]; index: number; caption: string }>({
  images: [],
  index: 0,
  caption: ''
})

/**
 * 宽高比预览方块的形状类名。
 *
 * 支持 "19.5:9" 这类小数比例——parseFloat 而非 Number 转整数。
 * "auto" 没有确定比例，用虚线框表示。
 */
function ratioClass(ratio: string): string {
  if (ratio === 'auto') return 'ratio-auto'
  const [w, h] = ratio.split(':').map(parseFloat)
  if (!w || !h) return 'ratio-landscape'
  if (w < h) return 'ratio-portrait'
  return w / h > 1.5 ? 'ratio-wide' : 'ratio-landscape'
}

/** 视频产物 object URL 缓存：taskId → blob URL。 */
const contentURLs = ref<Record<number, string>>({})

/**
 * 本次会话生成的图片：taskId → data URI 列表。
 *
 * 仅在 R2 转存未启用或失败时才有值 —— 正常情况下产物落 R2，任务记录里带
 * 长期可用的 URL，刷新页面照样能看。这个 ref 是兜底路径。
 */
const inlineImages = ref<Record<number, string[]>>({})

/** 产物取回失败过的任务 ID —— 记住以避免每轮轮询重复发必然失败的请求。 */
const contentFailed = ref<Set<number>>(new Set())

const selectedKey = computed(() => keys.value.find((k) => k.id === form.value.keyId) ?? null)
const currentModels = computed(() => modelsForKind(selectedKey.value, form.value.kind))
const selectedModel = computed(() => currentModels.value.find((m) => m.name === form.value.model) ?? null)
const typedRefURLs = computed(() => uniquePublicImageURLs(form.value.imageURL, form.value.imageURLs))
const usingURLRefs = computed(() => form.value.kind === 'i2v' && refSource.value === 'url')
const refImageCount = computed(() =>
  usingURLRefs.value ? typedRefURLs.value.length : files.value.length
)
const refSlotsLeft = computed(() => Math.max(0, REF_IMAGE_MAX - refImageCount.value))
const refPickerTitle = computed(() => {
  if (refSlotsLeft.value <= 0) return t('media.form.refImageFull')
  if (refImageCount.value) return t('media.form.refImageAdd', { n: refSlotsLeft.value })
  return t('media.form.refImagePick')
})
const refPickerClass = computed(() => {
  if (refSlotsLeft.value <= 0) {
    return 'cursor-not-allowed border-gray-200 bg-gray-50/40 opacity-70 dark:border-dark-600 dark:bg-dark-900/20'
  }
  if (refImageCount.value) {
    return 'cursor-pointer border-primary-300 bg-primary-50/30 hover:border-primary-300 hover:bg-primary-50/40 dark:border-primary-700 dark:hover:border-primary-700 dark:hover:bg-primary-900/10'
  }
  return 'cursor-pointer border-gray-200 bg-gray-50/60 hover:border-primary-300 hover:bg-primary-50/40 dark:border-dark-600 dark:bg-dark-900/30 dark:hover:border-primary-700 dark:hover:bg-primary-900/10'
})
const estimatedTicks = computed(() => estimateTicks(form.value, selectedKey.value))
const submitLabel = computed(() => {
  if (busy.value) return t('common.loading')
  if (estimatedTicks.value > 0) {
    return t('media.form.submitWithCost', { cost: ticksToUSD(estimatedTicks.value) })
  }
  return t('media.form.submit')
})
const downgradeTarget = computed(() => downgradeTargetOf(form.value, selectedKey.value))
const billingTier = computed(() =>
  selectedModel.value && !isVideoKind(form.value.kind)
    ? billingTierOf(form.value, selectedModel.value)
    : ''
)
const keyOptions = computed(() => keys.value.map((key) => ({
  value: key.id,
  label: `${key.name} · ${key.platform === 'openai' ? 'chatgpt' : key.platform}`
})))
const modelOptions = computed(() => currentModels.value.map((model) => ({ value: model.name, label: model.name })))
const imageSizeOptions = computed(() =>
  IMAGE_SIZE_PRESETS.map((preset) => ({
    value: preset.value,
    label: `${preset.value.replace('x', ' × ')} · ${preset.ratio} · ${preset.tier}`
  }))
)
/** 可选档位随模型下发，不在前端硬编码——后端的登记表是唯一来源。 */
const imageResolutionOptions = computed(() =>
  (selectedModel.value?.resolutions ?? []).map((r) => ({ value: r, label: r.toUpperCase() }))
)
const videoResolutionOptions = computed(() =>
  (selectedModel.value?.resolutions ?? []).map((r) => ({ value: r, label: r }))
)

/** 当前 key 支持的任务类型：没有视频模型时不显示视频 tab。 */
const availableKinds = computed<MediaTaskKind[]>(() => {
  const k = selectedKey.value
  if (!k) return []
  const out: MediaTaskKind[] = []
  if (k.image_models.length) out.push('t2i', 'i2i')
  if (k.video_models.length) out.push('t2v', 'i2v')
  return out
})

/**
 * 一个任务的可展示图片列表。
 *
 * 优先取落库的产物 URL（R2，刷新后依然可用），退而取本次会话的 data URI。
 * 两者都没有时返回空数组，由模板落到「无法预览」提示分支。
 */
function taskImages(task: MediaTask): string[] {
  if (isVideoKind(task.kind)) return []
  const stored = task.artifacts?.map((a) => a.url).filter(Boolean) ?? []
  if (stored.length) return stored
  return inlineImages.value[task.id] ?? []
}

/**
 * 一个视频任务的播放地址。
 *
 * R2 URL 优先：它不需要认证头也不会过期。转存未完成时回落到后端代理的
 * blob URL —— 代理端点带 key 取流，是上游保留期内的唯一取法。
 */
function videoSrc(task: MediaTask): string {
  if (!isVideoKind(task.kind)) return ''
  const stored = task.artifacts?.[0]?.url
  if (stored) return stored
  return contentURLs.value[task.id] ?? ''
}

function openPreview(task: MediaTask, index: number) {
  preview.value = { images: taskImages(task), index, caption: task.prompt }
}

function formatFileSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

function switchKind(kind: MediaTaskKind) {
  form.value = resetMediaFormForKind(form.value, kind)
  form.value.model = currentModels.value[0]?.name ?? ''
  syncModelDefaults()
  files.value = []
  revokeFilePreviews()
  refSource.value = 'upload'
  urlDraft.value = ''
  formError.value = ''
}

function onKeyChange() {
  form.value.kind = availableKinds.value[0] ?? 't2i'
  form.value.model = currentModels.value[0]?.name ?? ''
  syncModelDefaults()
}

/**
 * 把表单里与尺寸相关的字段收敛到当前模型的合法取值。
 *
 * 【为什么必须做这件事】两套尺寸参数互斥且各模型的可选清单不同。从 Grok 切到
 * gpt-image 时若不清掉 aspectRatio，请求里就会同时带上两套参数——后端会直接
 * 拒绝，而用户完全不知道自己"填"了什么。
 */
function syncModelDefaults() {
  const model = selectedModel.value
  if (!model) return

  const ratios = model.aspect_ratios ?? []
  const resolutions = model.resolutions ?? []

  if (isVideoKind(form.value.kind)) {
    if (!ratios.includes(form.value.aspectRatio)) {
      form.value.aspectRatio = ratios.includes('16:9') ? '16:9' : (ratios[0] ?? '')
    }
    if (!resolutions.includes(form.value.resolution)) {
      form.value.resolution = resolutions[0] ?? '480p'
    }
    return
  }

  if (model.size_mode === 'aspect_ratio') {
    form.value.size = '' // 传了也会被网关删掉，留着只会触发后端的互斥校验
    if (!ratios.includes(form.value.aspectRatio)) {
      form.value.aspectRatio = ratios.includes('1:1') ? '1:1' : (ratios[0] ?? '')
    }
    if (!resolutions.includes(form.value.imageResolution)) {
      form.value.imageResolution = resolutions[0] ?? '1k'
    }
    return
  }

  // size 模式：清掉 aspect_ratio 与档位，端点不认这两个字段
  form.value.aspectRatio = ''
  form.value.imageResolution = ''
  if (!form.value.size) form.value.size = '1024x1024'
}

function revokeFilePreviews() {
  filePreviews.value.forEach((url) => URL.revokeObjectURL(url))
  filePreviews.value = []
}

function onFileChange(e: Event) {
  const input = e.target as HTMLInputElement
  const picked = Array.from(input.files ?? [])
  input.value = ''
  if (!picked.length) return

  const taken = files.value.length
  const next = appendRefImages(files.value, picked, REF_IMAGE_MAX)
  const added = next.items.slice(files.value.length)
  files.value = next.items
  filePreviews.value = [
    ...filePreviews.value,
    ...added.map((f) => URL.createObjectURL(f))
  ]
  if (next.overflow) {
    formError.value = t('media.errors.tooManyImages')
  } else if (taken === 0) {
    formError.value = ''
  }
}

function removeFile(index: number) {
  const preview = filePreviews.value[index]
  if (preview) URL.revokeObjectURL(preview)
  files.value = files.value.filter((_, i) => i !== index)
  filePreviews.value = filePreviews.value.filter((_, i) => i !== index)
  formError.value = ''
}

function setTypedURLs(next: string[]) {
  form.value = { ...form.value, imageURL: next[0] ?? '', imageURLs: next }
}

function addTypedURLs() {
  const incoming = splitRefImageInput(urlDraft.value)
  if (!incoming.length) {
    formError.value = t('media.errors.badImageURL')
    return
  }
  const next = appendRefImages(typedRefURLs.value, incoming, REF_IMAGE_MAX)
  setTypedURLs(next.items)
  urlDraft.value = ''
  formError.value = next.overflow ? t('media.errors.tooManyImages') : ''
}

function removeTypedURL(index: number) {
  setTypedURLs(typedRefURLs.value.filter((_, i) => i !== index))
  formError.value = ''
}

function guessImageType(file: File): string {
  if (file.type) return file.type
  const name = file.name.toLowerCase()
  if (name.endsWith('.png')) return 'image/png'
  if (name.endsWith('.webp')) return 'image/webp'
  return 'image/jpeg'
}

/** 浏览器直传对象存储，返回公开 URL。凭据不下发，只拿短时预签名。 */
async function uploadRefsDirect(picked: File[]): Promise<string[]> {
  const slots = await prepareUploads(
    picked.map((file) => ({
      filename: file.name,
      content_type: guessImageType(file),
      size: file.size
    }))
  )
  await Promise.all(
    slots.map(async (slot, i) => {
      const resp = await fetch(slot.upload_url, {
        method: 'PUT',
        headers: slot.headers,
        body: picked[i]
      })
      if (!resp.ok) {
        throw new Error('media.errors.uploadFailed')
      }
    })
  )
  return slots.map((slot) => slot.public_url)
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

function reuseTask(task: MediaTask) {
  try {
    const raw = JSON.parse(task.params) as Record<string, unknown>
    const get = <T>(name: string, fallback: T): T => (raw[name] as T | undefined) ?? (raw[name[0].toUpperCase() + name.slice(1)] as T | undefined) ?? fallback
    const keyId = keys.value.some((key) => key.id === task.key_id) ? task.key_id : form.value.keyId
    form.value = {
      ...emptyMediaForm(),
      keyId,
      kind: get('kind', task.kind) as MediaTaskKind,
      model: get('model', task.model),
      prompt: get('prompt', task.prompt),
      n: get('n', 1),
      size: get('size', ''),
      aspectRatio: get('aspectRatio', get('aspect_ratio', '')),
      imageResolution: get('imageResolution', get('image_resolution', '')),
      quality: get('quality', 'high'),
      resolution: get('resolution', '480p'),
      duration: get('duration', 8),
      imageURL: get('imageURL', ''),
      imageURLs: Array.isArray(raw.imageURLs)
        ? (raw.imageURLs as string[])
        : Array.isArray(raw.ImageURLs)
          ? (raw.ImageURLs as string[])
          : [],
      stream: get('stream', false)
    }
    // 复用的可能是旧格式参数（那时 Grok 还在传 size），收敛到当前模型的合法取值
    syncModelDefaults()
    files.value = []
    revokeFilePreviews()
    urlDraft.value = ''
    refSource.value =
      form.value.kind === 'i2v' && uniquePublicImageURLs(form.value.imageURL, form.value.imageURLs).length
        ? 'url'
        : 'upload'
    formError.value = ''
    window.scrollTo({ top: 0, behavior: 'smooth' })
  } catch {
    formError.value = t('media.errors.invalidParams')
  }
}

function askDeleteTask(task: MediaTask) {
  deleteTarget.value = task
  deleteConfirmOpen.value = true
}

async function onConfirmDelete() {
  const task = deleteTarget.value
  if (!task || deleting.value) return
  deleting.value = true
  try {
    await deleteTask(task.id)
    tasks.value = tasks.value.filter((item) => item.id !== task.id)
    deleteConfirmOpen.value = false
  } catch (e) {
    formError.value = t(e instanceof Error ? e.message : 'media.errors.deleteTaskFailed')
  } finally {
    deleting.value = false
    deleteTarget.value = null
  }
}

function onSubmit() {
  formError.value = ''
  const invalid = validateMediaForm(form.value, selectedKey.value)
  if (invalid) {
    formError.value = t(invalid)
    return
  }
  if (needsUpload(form.value.kind)) {
    const hasFiles = files.value.length > 0
    const hasURLs = typedRefURLs.value.length > 0
    const missing =
      form.value.kind === 'i2v'
        ? usingURLRefs.value ? !hasURLs : !hasFiles
        : !hasFiles && !hasURLs
    if (missing) {
      formError.value = t('media.errors.missingImage')
      return
    }
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
  // 两套尺寸参数互斥：aspect_ratio 模式的模型传 size 会被网关删掉、
  // size 模式的模型不认 aspect_ratio。只按模式发一套。
  const byRatio = selectedModel.value?.size_mode === 'aspect_ratio'
  try {
    const imageURLs =
      usingURLRefs.value
        ? typedRefURLs.value
        : files.value.length
          ? await uploadRefsDirect(files.value)
          : typedRefURLs.value
    const video = isVideoKind(form.value.kind)
    const result = await generate({
      key_id: form.value.keyId,
      kind: form.value.kind,
      model: form.value.model,
      prompt: form.value.prompt,
      n: form.value.n,
      size: !video && !byRatio ? form.value.size || undefined : undefined,
      quality: !video && !byRatio ? form.value.quality : undefined,
      aspect_ratio: video || byRatio ? form.value.aspectRatio || undefined : undefined,
      image_resolution: !video && byRatio ? form.value.imageResolution || undefined : undefined,
      resolution: form.value.resolution,
      duration: form.value.duration,
      image_url: imageURLs[0],
      image_urls: imageURLs.length ? imageURLs : undefined,
      stream: form.value.stream,
      client_request_id: clientRequestID
    })
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
  revokeFilePreviews()
})
</script>
