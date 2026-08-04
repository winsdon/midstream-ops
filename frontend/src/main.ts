import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import router from './router'
import i18n from './i18n'
import './style.css'

function initThemeClass() {
  const saved = localStorage.getItem('monitor_theme')
  const dark =
    saved === 'dark' || (!saved && window.matchMedia('(prefers-color-scheme: dark)').matches)
  document.documentElement.classList.toggle('dark', dark)
}

async function bootstrap() {
  initThemeClass()
  const app = createApp(App)
  app.use(createPinia())
  app.use(router)
  app.use(i18n)
  await router.isReady()
  app.mount('#app')
}

bootstrap()
