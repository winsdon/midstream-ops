<template>
  <main class="min-h-dvh w-full bg-gray-50 px-3 py-4 dark:bg-dark-950 sm:px-5 sm:py-6 lg:px-8">
    <!-- 参数缺失 / 会话失败 -->
    <div v-if="fatalError" class="mx-auto max-w-lg pt-16">
      <EmptyState icon="exclamationTriangle" :title="t(fatalError)" :description="t('plaza.errors.openFromMenu')" />
    </div>

    <!-- 首屏加载 -->
    <div v-else-if="loading && !data" class="pt-24">
      <LoadingState :label="t('common.loading')" size="lg" />
    </div>

    <template v-else>
      <!-- 搜索框 -->
      <div class="mb-5 w-full">
        <div class="relative">
          <Icon
            name="search"
            size="md"
            class="pointer-events-none absolute left-3.5 top-1/2 -translate-y-1/2 text-gray-400"
          />
          <input
            v-model="searchInput"
            type="text"
            class="input h-11 pl-11 text-sm"
            :placeholder="t('plaza.searchPlaceholder')"
          />
        </div>
      </div>

      <div class="flex flex-col gap-5 lg:flex-row">
        <!-- 左侧筛选 -->
        <aside class="w-full shrink-0 lg:w-56">
          <div class="card p-4">
            <PlazaFilterSidebar
              :groups="groupOpts"
              :platforms="platformOpts"
              :group-id="groupId"
              :platform="platform"
              :total="searched.length"
              @update:group-id="groupId = $event"
              @update:platform="platform = $event"
            />
          </div>
        </aside>

        <!-- 右侧主区 -->
        <div class="min-w-0 flex-1 space-y-4">
          <PlazaToolbar
            :count="visible.length"
            :unit-scale="unitScale"
            :sort-key="sortKey"
            :view-mode="viewMode"
            @update:unit-scale="unitScale = $event"
            @update:sort-key="sortKey = $event"
            @update:view-mode="viewMode = $event"
          />

          <EmptyState
            v-if="visible.length === 0 && allModels.length === 0"
            icon="cube"
            :title="t('plaza.empty.title')"
            :description="t('plaza.empty.description')"
          />
          <EmptyState
            v-else-if="visible.length === 0"
            icon="search"
            :title="t('plaza.empty.filtered')"
            :description="t('plaza.empty.filteredDesc')"
          />

          <div
            v-else-if="viewMode === 'grid'"
            class="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3"
          >
            <PlazaCard
              v-for="m in visible"
              :key="m.name"
              :model="m"
              :unit-scale="unitScale"
              @detail="openDetail"
              @copy="copyName"
            />
          </div>

          <PlazaListTable
            v-else
            :models="visible"
            :unit-scale="unitScale"
            @detail="openDetail"
            @copy="copyName"
          />
        </div>
      </div>
    </template>

    <PlazaDetailDialog
      :show="detailVisible"
      :model="detailModel"
      @close="detailVisible = false"
      @copy="copyName"
    />
    <Toast />
  </main>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { createSession, fetchPlaza } from '@/api/embedPlaza'
import type { PlazaData, PlazaModel } from '@/types/plaza'
import { applyTheme, queryString, resolveLocale, stripTokenFromUrl } from '@/utils/embedQuery'
import {
  filterModels,
  groupOptions,
  platformOptions,
  searchModels,
  sortModels,
  type SortKey,
  type UnitScale,
  type ViewMode
} from '@/utils/plazaModel'
import Icon from '@/components/icons/Icon.vue'
import Toast from '@/components/common/Toast.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import LoadingState from '@/components/common/LoadingState.vue'
import PlazaFilterSidebar from '@/components/plaza/PlazaFilterSidebar.vue'
import PlazaToolbar from '@/components/plaza/PlazaToolbar.vue'
import PlazaCard from '@/components/plaza/PlazaCard.vue'
import PlazaListTable from '@/components/plaza/PlazaListTable.vue'
import PlazaDetailDialog from '@/components/plaza/PlazaDetailDialog.vue'

const route = useRoute()
const { t, locale } = useI18n()
const app = useAppStore()

const loading = ref(true)
const fatalError = ref('')
const data = ref<PlazaData | null>(null)

// 搜索框输入与实际生效的查询分离，中间加防抖（项目无 @vueuse，手写）。
const searchInput = ref('')
const searchQuery = ref('')
let searchTimer: ReturnType<typeof setTimeout> | undefined
watch(searchInput, (v) => {
  clearTimeout(searchTimer)
  searchTimer = setTimeout(() => {
    searchQuery.value = v
  }, 300)
})

const groupId = ref<number | null>(null)
const platform = ref<string | null>(null)
const unitScale = ref<UnitScale>(1_000_000)
const sortKey = ref<SortKey>('name')
const viewMode = ref<ViewMode>('grid')

const detailVisible = ref(false)
const detailModel = ref<PlazaModel | null>(null)

const allModels = computed(() => data.value?.models ?? [])
const searched = computed(() => searchModels(allModels.value, searchQuery.value))
// 侧栏计数基于搜索结果，随搜索联动。
const groupOpts = computed(() => groupOptions(searched.value))
const platformOpts = computed(() => platformOptions(searched.value))
const visible = computed(() =>
  sortModels(filterModels(searched.value, groupId.value, platform.value), sortKey.value)
)

function openDetail(model: PlazaModel) {
  detailModel.value = model
  detailVisible.value = true
}

async function copyName(name: string) {
  try {
    await navigator.clipboard.writeText(name)
    app.showSuccess(t('plaza.card.copied'))
  } catch {
    app.showError(t('plaza.errors.copyFailed'))
  }
}

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
    data.value = await fetchPlaza()
  } catch (e) {
    // 后端返回的 message 即 i18n key。
    fatalError.value = e instanceof Error ? e.message : 'plaza.errors.loadFailed'
  } finally {
    loading.value = false
  }
})
</script>
