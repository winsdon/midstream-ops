<template>
  <main class="min-h-dvh w-full bg-gray-50 px-4 py-8 dark:bg-dark-950">
    <div class="mx-auto max-w-xl space-y-4">
      <header class="space-y-1">
        <h1 class="text-lg font-semibold text-gray-800 dark:text-dark-100">嵌入页调试</h1>
        <p class="text-sm text-gray-500 dark:text-dark-400">
          本地自签 sub2api 用户 token 并打开嵌入页，无需 sub2api 站点配合。
        </p>
      </header>

      <p class="rounded-lg bg-amber-50 px-3 py-2 text-sm text-amber-700 dark:bg-amber-900/20 dark:text-amber-400">
        仅供本地开发。此页依赖 plaza.dev_mode，该开关会暴露任意用户身份的签发能力，生产环境须关闭。
      </p>

      <div class="card space-y-4 p-4 sm:p-5">
        <div class="space-y-1.5">
          <label class="input-label">页面</label>
          <div class="flex flex-wrap gap-2">
            <button
              v-for="p in PAGES"
              :key="p.path"
              class="btn btn-sm"
              :class="page === p.path ? 'btn-primary' : 'btn-secondary'"
              @click="page = p.path"
            >
              {{ t(p.labelKey) }}
            </button>
          </div>
        </div>

        <div class="space-y-1.5">
          <label class="input-label" for="dev-user-id">user_id</label>
          <input id="dev-user-id" v-model="userId" class="input" inputmode="numeric" placeholder="1" />
          <p class="input-hint">签进 token 的客户身份。KYC 页会按它读写对应客户的档案。</p>
        </div>

        <div class="space-y-1.5">
          <label class="input-label">主题</label>
          <div class="flex gap-2">
            <button
              v-for="th in EMBED_THEMES"
              :key="th"
              class="btn btn-sm"
              :class="theme === th ? 'btn-primary' : 'btn-secondary'"
              @click="theme = th"
            >
              {{ th }}
            </button>
          </div>
        </div>

        <div class="space-y-1.5">
          <label class="input-label">语言</label>
          <div class="flex gap-2">
            <button
              v-for="lg in EMBED_LANGS"
              :key="lg"
              class="btn btn-sm"
              :class="lang === lg ? 'btn-primary' : 'btn-secondary'"
              @click="lang = lg"
            >
              {{ lg }}
            </button>
          </div>
        </div>

        <p v-if="error" class="rounded-lg bg-red-50 px-3 py-2 text-sm text-red-600 dark:bg-red-900/30 dark:text-red-400">
          {{ error }}
        </p>

        <div class="flex flex-wrap justify-end gap-2">
          <button class="btn btn-secondary" :disabled="busy" @click="open(true)">新标签页打开</button>
          <button class="btn btn-primary" :disabled="busy" @click="open(false)">
            {{ busy ? '签发中…' : '打开嵌入页' }}
          </button>
        </div>
      </div>

      <!-- iframe 预览：最贴近真实嵌入形态，能暴露 CSP、尺寸、滚动等只在嵌套下出现的问题 -->
      <div v-if="frameUrl" class="card overflow-hidden">
        <div class="flex items-center justify-between gap-2 border-b border-gray-200 px-4 py-2 dark:border-dark-600">
          <span class="text-xs text-gray-500 dark:text-dark-400">iframe 预览</span>
          <button class="btn btn-ghost btn-sm" @click="frameUrl = ''">关闭</button>
        </div>
        <iframe :src="frameUrl" class="h-[600px] w-full border-0" title="嵌入页预览"></iframe>
      </div>
    </div>
  </main>
</template>

<script setup lang="ts">
/**
 * 嵌入页本地调试入口（仅开发环境构建，见 router 的 import.meta.env.DEV 守卫）。
 *
 * 存在的理由：嵌入页身份来自 sub2api iframe 透传的 token，本地没有 sub2api 站点，
 * 手工拼 URL 既繁琐又容易错。此页调后端 dev 端点拿一个自签 token，拼好参数直接打开。
 *
 * 【token 不落任何持久化存储】每次点击现签现用。嵌入页自己会在拿到 token 后
 * 立刻用 stripTokenFromUrl 从地址栏抹掉，与真实 sub2api 嵌入的行为完全一致。
 *
 * 页面 / 主题 / 语言清单与签发逻辑复用在 utils 里（embedRegistry / embedDev），
 * 与主应用内的「嵌入页面」Hub 共享同一份，避免两份清单漂移。
 */
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { EMBED_PAGES } from '@/utils/embedRegistry'
import { EMBED_THEMES, EMBED_LANGS, buildEmbedUrl, issueDevToken } from '@/utils/embedDev'

const { t } = useI18n()

const PAGES = EMBED_PAGES.map((p) => ({ path: p.path, labelKey: p.labelKey }))

const page = ref<string>(PAGES[1].path)
const userId = ref('1')
const theme = ref<string>(EMBED_THEMES[0])
const lang = ref<string>(EMBED_LANGS[0])

const busy = ref(false)
const error = ref('')
const frameUrl = ref('')

/**
 * 拼装嵌入 URL 并打开。
 * @param newTab true 走新标签页，false 走页内 iframe 预览
 */
async function open(newTab: boolean) {
  error.value = ''
  busy.value = true
  try {
    if (newTab) {
      // 新标签页：复用 Hub 的一致逻辑，一次签发一个新 token
      const url = await buildEmbedUrl({ path: page.value, userId: userId.value, theme: theme.value, lang: lang.value })
      window.open(url, '_blank', 'noopener,noreferrer')
    } else {
      // iframe 预览：URL 不进地址栏，但同样需要 token 才能加载嵌入页会话
      const token = await issueDevToken(userId.value)
      const params = new URLSearchParams({
        token,
        user_id: userId.value.trim() || '1',
        theme: theme.value,
        lang: lang.value,
        ui_mode: 'embedded'
      })
      frameUrl.value = `${page.value}?${params.toString()}`
    }
  } catch (e) {
    error.value = t(e instanceof Error && e.message ? e.message : 'embedHub.devError')
  } finally {
    busy.value = false
  }
}
</script>
