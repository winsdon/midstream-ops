/**
 * 表头点击排序的状态与两态翻转。
 *
 * 两态而非三态：点同列 asc⇄desc，点新列从 asc 起。三态（含「取消排序」）
 * 要求用户点三次才回到原状，而「原状」本身在界面上不可见，收益不抵认知成本。
 *
 * 未选中任何列时 sorted 原样返回入参 —— 后端的业务默认序（如收益统计的
 * 「未归属」桶恒垫底）由此完整保留，只有用户主动点表头才切换到本地排序。
 */
import { ref, computed, type Ref, type ComputedRef } from 'vue'
import { sortRows, type SortOrder } from '@/utils/tableSort'

/** 列 key → 取值函数。key 与 SortableTh 的 sort-key 一一对应。 */
export type SortAccessors<T> = Record<string, (row: T) => unknown>

export interface UseTableSort<T> {
  sortKey: Ref<string>
  sortOrder: Ref<SortOrder>
  /** 排序后的行；未选中列时原样返回入参 */
  sorted: ComputedRef<T[]>
  toggle: (key: string) => void
}

export function useTableSort<T>(
  rows: Ref<T[]> | ComputedRef<T[]>,
  accessors: SortAccessors<T>,
  initial?: { key: string; order?: SortOrder }
): UseTableSort<T> {
  const sortKey = ref(initial?.key ?? '')
  const sortOrder = ref<SortOrder>(initial?.order ?? 'asc')

  const sorted = computed(() => {
    const pick = accessors[sortKey.value]
    // 未选列或 key 没有对应取值函数：原样返回，让后端默认序生效
    if (!pick) return rows.value
    return sortRows(rows.value, pick, sortOrder.value)
  })

  function toggle(key: string) {
    if (sortKey.value === key) {
      sortOrder.value = sortOrder.value === 'asc' ? 'desc' : 'asc'
      return
    }
    sortKey.value = key
    sortOrder.value = 'asc'
  }

  return { sortKey, sortOrder, sorted, toggle }
}
