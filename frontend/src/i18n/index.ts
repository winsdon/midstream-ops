import { createI18n } from 'vue-i18n'
import zhCN from './zh-CN'
import enUS from './en-US'

/** 可选语言列表，供 LocaleSwitcher 渲染 */
export const availableLocales = [
  { code: 'zh-CN', name: '简体中文', flag: '🇨🇳' },
  { code: 'en-US', name: 'English', flag: '🇺🇸' }
] as const

const i18n = createI18n({
  legacy: false,
  locale: localStorage.getItem('monitor_locale') || 'zh-CN',
  fallbackLocale: 'zh-CN',
  messages: {
    'zh-CN': zhCN,
    'en-US': enUS
  }
})

export function setLocale(locale: string) {
  i18n.global.locale.value = locale as 'zh-CN' | 'en-US'
  localStorage.setItem('monitor_locale', locale)
}

export default i18n
