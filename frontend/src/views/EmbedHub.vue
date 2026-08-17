<template>
  <div class="space-y-5">
    <!-- 本地生成能力提示：dev token 端点不可用时不展示具体链接，但要说明为什么、怎么开 -->
    <p
      v-if="!devAvailable && probed"
      class="flex items-start gap-1.5 rounded-lg bg-amber-50 px-3 py-2 text-sm text-amber-700 dark:bg-amber-900/20 dark:text-amber-300"
    >
      <Icon name="exclamationTriangle" size="sm" class="mt-px flex-shrink-0" />
      <span>{{ devHintLabel }}</span>
    </p>

    <!-- 页面清单：统一展示所有支持嵌入 sub2api 的页面 -->
    <div class="grid gap-4 md:grid-cols-3">
      <div v-for="p in EMBED_PAGES" :key="p.path" class="card flex flex-col p-5">
        <div class="flex items-center gap-3">
          <div
            class="flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-xl bg-primary-50 text-primary-600 dark:bg-primary-900/30 dark:text-primary-300"
          >
            <Icon :name="p.icon" size="md" />
          </div>
          <div class="min-w-0">
            <p class="font-medium text-gray-900 dark:text-white">{{ t(p.labelKey) }}</p>
            <p class="truncate font-mono text-xs text-gray-400" :title="p.path">{{ p.path }}</p>
          </div>
        </div>

        <p class="mt-3 flex-1 text-sm text-gray-500 dark:text-dark-400">{{ t(p.descriptionKey) }}</p>

        <div class="mt-4 flex flex-wrap items-center gap-1.5">
          <span class="rounded bg-gray-100 px-2 py-0.5 font-mono text-[11px] text-gray-500 dark:bg-dark-800 dark:text-dark-400">
            {{ p.dependsOn }}
          </span>
        </div>

        <button
          type="button"
          class="btn btn-primary mt-4 w-full text-sm"
          :disabled="!devAvailable || busyPath === p.path"
          @click="open(p)"
        >
          <Icon name="externalLink" size="sm" />
          {{ busyPath === p.path ? t('embedHub.generating') : t('embedHub.open') }}
        </button>
      </div>
    </div>

    <!-- 参数区：user_id / 主题 / 语言，与 /embed/_dev 调试页一致 -->
    <div class="card space-y-4 p-5">
      <p class="text-sm font-medium text-gray-900 dark:text-white">{{ t('embedHub.paramsTitle') }}</p>
      <div class="grid gap-4 sm:grid-cols-3">
        <div class="space-y-1.5">
          <label class="input-label" for="hub-user-id">{{ t('embedHub.userId') }}</label>
          <input id="hub-user-id" v-model="userId" class="input" inputmode="numeric" :placeholder="t('embedHub.userIdPlaceholder')" />
        </div>

        <div class="space-y-1.5">
          <label class="input-label">{{ t('embedHub.theme') }}</label>
          <div class="flex gap-2">
            <button
              v-for="th in EMBED_THEMES"
              :key="th"
              type="button"
              class="btn btn-sm"
              :class="theme === th ? 'btn-primary' : 'btn-secondary'"
              @click="theme = th"
            >
              {{ th }}
            </button>
          </div>
        </div>

        <div class="space-y-1.5">
          <label class="input-label">{{ t('embedHub.lang') }}</label>
          <div class="flex gap-2">
            <button
              v-for="l in EMBED_LANGS"
              :key="l"
              type="button"
              class="btn btn-sm"
              :class="lang === l ? 'btn-primary' : 'btn-secondary'"
              @click="lang = l"
            >
              {{ l }}
            </button>
          </div>
        </div>
      </div>
      <p class="text-xs text-gray-400">{{ t('embedHub.paramsHint') }}</p>
    </div>
  </div>
</template>

<script setup lang="ts">
/**
 * 可嵌入 sub2api 页面的统一入口（主应用内，登录后可见）。
 *
 * 存在的意义：/embed/* 各自是独立页面，散落在哪、能不能在本地打开都不直观。
 * 本页把「哪些页面支持嵌入、各自依赖什么后端开关、如何在本地签 token 打开」
 * 收敛到一处。页面清单来自 utils/embedRegistry.ts，签发 + 拼 URL 复用
 * utils/embedDev.ts，与 /embed/_dev 调试页共享同一套逻辑，避免两份漂移。
 *
 * 本地可用性靠探测后端 dev token 端点判断：端点存在（plaza.dev_mode=true）才给
 * 出生成按钮，否则说明怎么开。生产环境端点必然不存在，本页退化为「只读清单」。
 */
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { EMBED_PAGES, type EmbedPageDef } from '@/utils/embedRegistry'
import { EMBED_THEMES, EMBED_LANGS, probeDevToken, buildEmbedUrl } from '@/utils/embedDev'

const { t } = useI18n()

const userId = ref('1')
const theme = ref<string>(EMBED_THEMES[0])
const lang = ref<string>(EMBED_LANGS[0])

const probed = ref(false)
const devAvailable = ref(false)
const devHintKey = ref('')
const busyPath = ref('')

const devHintLabel = computed(() => (devHintKey.value ? t(devHintKey.value) : ''))

// 挂载时探测一次；端点 404（dev_mode 未开 / 生产）则本地生成不可用，只保留清单。
onMounted(async () => {
  const [ok, hint] = await probeDevToken()
  devAvailable.value = ok
  devHintKey.value = ok ? '' : hint
  probed.value = true
})

/** 为指定页面签发 token 并以新标签页打开（本地可访问的完整嵌入 URL）。 */
async function open(page: EmbedPageDef): Promise<void> {
  busyPath.value = page.path
  try {
    const url = await buildEmbedUrl({ path: page.path, userId: userId.value, theme: theme.value, lang: lang.value })
    window.open(url, '_blank', 'noopener,noreferrer')
  } catch (e) {
    devHintKey.value = e instanceof Error && e.message ? e.message : 'embedHub.devError'
    devAvailable.value = false
    probed.value = true
  } finally {
    busyPath.value = ''
  }
}
</script>
