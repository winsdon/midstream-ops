import { ref, type Ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { errorMessage } from '@/api/client'
import { useAppStore } from '@/stores/app'
import type { PaginatedData } from '@/types'

/**
 * 分页历史的加载状态。模型真伪与智商测试的历史各持一份，翻页逻辑共用。
 */
export function usePagedHistory<T>(
  fetchPage: (params: { page: number; page_size: number }) => Promise<PaginatedData<T>>,
  pageSize = 10
) {
  const { t } = useI18n()
  const app = useAppStore()

  const items = ref([]) as Ref<T[]>
  const loading = ref(false)
  const page = ref(1)
  const pages = ref(1)
  const total = ref(0)

  async function load(p = 1): Promise<void> {
    loading.value = true
    try {
      const res = await fetchPage({ page: p, page_size: pageSize })
      items.value = res.items || []
      page.value = res.page
      pages.value = res.pages
      total.value = res.total
    } catch (e) {
      app.showError(t('detect.loadFailed') + '：' + errorMessage(e))
    } finally {
      loading.value = false
    }
  }

  return { items, loading, page, pages, total, load }
}
