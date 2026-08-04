<template>
  <BaseDialog
    :show="show"
    :title="(provider?.name || '') + ' · ' + t('provider.linkAccounts')"
    width="wide"
    @close="emit('close')"
  >
    <div class="space-y-3">
      <p class="text-xs text-gray-400">{{ t('provider.linkHint') }}</p>

      <div class="relative">
        <Icon name="search" size="sm" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
        <!-- 纯本地过滤，几十到几百条逐字符重算一个 computed 可忽略，不必防抖 -->
        <input
          v-model.trim="query"
          class="input !w-full !py-2 !pl-9 text-sm"
          :placeholder="t('provider.linkSearchPlaceholder')"
        />
      </div>

      <LoadingState v-if="loading" />
      <!-- 两层空态语义不同：搜不到该清搜索词，一个账号都没有该查线上库连接 -->
      <EmptyState v-else-if="!groups.length" icon="search" :title="t('provider.linkNoAccounts')" />
      <EmptyState
        v-else-if="!visibleGroups.length"
        icon="search"
        :title="t('provider.linkNoMatch')"
        :description="t('provider.linkNoMatchHint')"
      />
      <div v-else class="max-h-[26rem] space-y-3 overflow-y-auto">
        <div
          v-for="g in visibleGroups" :key="g.base_url"
          class="rounded-lg border border-gray-200 p-2 dark:border-dark-700"
        >
          <p class="truncate px-1 font-mono text-xs text-gray-500">
            {{ g.base_url || t('provider.noBaseUrl') }}
          </p>

          <!-- 计数用可见数量而非 account_count：搜索后「全选本组」只作用于看得见的账号 -->
          <label class="mt-1 flex cursor-pointer items-center gap-2 px-1 text-xs text-gray-500">
            <input
              type="checkbox" class="checkbox"
              :checked="isGroupAllSelected(g, selected)"
              @change="selected = toggleGroup(g, selected)"
            />
            {{ t('provider.linkSelectAllGroup', { n: g.accounts.length }) }}
          </label>

          <div class="mt-1 space-y-1">
            <label
              v-for="a in g.accounts" :key="a.id"
              class="flex cursor-pointer items-center justify-between gap-2 rounded px-2 py-1 text-sm hover:bg-gray-50 dark:hover:bg-dark-800"
            >
              <span class="flex min-w-0 items-center gap-2">
                <input type="checkbox" :value="a.id" v-model="selected" class="checkbox" />
                <span class="truncate">{{ a.name }}</span>
              </span>
              <span class="flex shrink-0 items-center gap-1.5 text-xs text-gray-400">
                {{ a.platform }}
                <!-- 勾选已归属别的站的账号 = 把它抢过来，必须让人看见 -->
                <span
                  v-if="a.linked_to && a.linked_to !== provider?.name"
                  class="badge badge-warning !text-[10px]"
                >
                  {{ t('provider.linkedTo', { name: a.linked_to }) }}
                </span>
              </span>
            </label>
          </div>
        </div>
      </div>
    </div>

    <template #footer>
      <span class="mr-auto text-xs" :class="hiddenCount > 0 ? 'text-amber-600 dark:text-amber-400' : 'text-gray-400'">
        {{ countText }}
      </span>
      <button type="button" class="btn btn-secondary" @click="emit('close')">{{ t('common.cancel') }}</button>
      <button type="button" class="btn btn-primary" :disabled="saving" @click="save">
        {{ saving ? t('common.loading') : t('common.save') }}
      </button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { providerApi } from '@/api'
import { errorMessage } from '@/api/client'
import { useAppStore } from '@/stores/app'
import {
  searchLinkGroups,
  visibleSelectedCount,
  isGroupAllSelected,
  toggleGroup
} from '@/utils/linkModel'
import BaseDialog from '@/components/common/BaseDialog.vue'
import LoadingState from '@/components/common/LoadingState.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Icon from '@/components/icons/Icon.vue'
import type { Provider, URLGroupItem } from '@/types'

/**
 * 关联账号弹窗：全量替换该站的关联集合。
 *
 * 从 Providers.vue 抽出 —— 那个文件已 1500+ 行，而这个弹窗有自己的
 * 取数、搜索、勾选三套状态，够独立成一个组件。
 */
const props = defineProps<{ show: boolean; provider: Provider | null }>()

const emit = defineEmits<{
  (e: 'close'): void
  /** 保存成功；父组件据此刷新列表的 account_count */
  (e: 'saved'): void
}>()

const { t } = useI18n()
const app = useAppStore()

const groups = ref<URLGroupItem[]>([])
const loading = ref(false)
const saving = ref(false)
const query = ref('')

/**
 * 勾选态独立于渲染列表：搜索过滤只动 visibleGroups，不碰这里，
 * 因此被隐藏的勾选项天然不会丢。
 */
const selected = ref<number[]>([])

const visibleGroups = computed(() => searchLinkGroups(groups.value, query.value))

const visibleCount = computed(() => visibleSelectedCount(visibleGroups.value, selected.value))
const hiddenCount = computed(() => selected.value.length - visibleCount.value)

/**
 * 保存是全量替换，被搜索隐藏的勾选项会照样提交 —— 这是对的，
 * 危险的是用户心智：搜到 2 个勾选很容易以为保存后就只剩这 2 个。
 * 有隐藏项时必须把三个数字都摆出来。
 */
const countText = computed(() =>
  hiddenCount.value > 0
    ? t('provider.linkSelectedHidden', {
        total: selected.value.length,
        visible: visibleCount.value,
        hidden: hiddenCount.value
      })
    : t('provider.linkSelectedCount', { n: selected.value.length })
)

watch(
  () => props.show,
  (open) => {
    if (open) void loadLinks()
  }
)

/** 所有账号按站点地址分组展示，已关联到本站的默认勾上 */
async function loadLinks() {
  const p = props.provider
  if (!p) return
  loading.value = true
  groups.value = []
  selected.value = []
  query.value = ''
  try {
    const [urlGroups, links] = await Promise.all([providerApi.scanUrls(), providerApi.links(p.id)])
    groups.value = urlGroups.items || []
    selected.value = (links.items || []).map((l) => l.account_id)
  } catch (e) {
    app.showError(errorMessage(e))
  } finally {
    loading.value = false
  }
}

async function save() {
  const p = props.provider
  if (!p) return
  saving.value = true
  try {
    const res = await providerApi.saveLinks(p.id, selected.value)
    app.showSuccess(t('provider.linkedResult', { name: p.name, n: res.linked }))
    emit('saved')
    emit('close')
  } catch (e) {
    app.showError(errorMessage(e))
  } finally {
    saving.value = false
  }
}
</script>
