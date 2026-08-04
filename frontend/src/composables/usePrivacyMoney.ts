/**
 * 隐私模式下的金额展示组合式函数
 * 收口「打码金额」与「打码后不着色」两条规则，避免各页面各写一份而漂移
 */
import { readonly } from 'vue'
import { storeToRefs } from 'pinia'
import { fmtMoney, fmtPct, moneyClass } from '@/utils/format'
import { useAppStore } from '@/stores/app'

/** 隐私模式下替代金额的占位符 */
const MASK = '****'

/**
 * 金额的隐私模式包装
 *
 * privacyMode 只读导出：修改必须走 store 的 setPrivacyMode，
 * 直接改 ref 会绕过 localStorage 持久化，刷新后偏好丢失
 */
export function usePrivacyMoney() {
  const { privacyMode } = storeToRefs(useAppStore())

  /** 金额文本，隐私模式下打码 */
  const displayMoney = (value?: number | null): string => (privacyMode.value ? MASK : fmtMoney(value))

  /**
   * 金额正负着色 class，隐私模式下返回空串
   * 打了码却仍标红标绿等于泄露盈亏方向，着色必须与打码同进同退
   */
  const displayMoneyClass = (value?: number | null): string => (privacyMode.value ? '' : moneyClass(value))

  /**
   * 利润率文本，隐私模式下打码
   *
   * 利润率比金额本身泄露得更多 —— 它直接就是盈亏方向。故与金额同等对待，
   * 且任何由它派生的视觉量（进度条长度等）也必须一并隐藏：条长是模拟量泄露。
   *
   * 刻意不叫 displayPercent：通用名会诱使有人拿它去打码稳定性页的成功率，
   * 而成功率不是财务数据，必须始终可见。
   */
  const displayMargin = (pct?: number | null): string => (privacyMode.value ? MASK : fmtPct(pct))

  return {
    privacyMode: readonly(privacyMode),
    displayMoney,
    displayMoneyClass,
    displayMargin
  }
}
