import { createRouter, createWebHistory } from 'vue-router'
import { getToken, setUnauthorizedHandler } from '@/api/client'
import { useNavigationLoadingState } from '@/composables/useNavigationLoading'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/login',
      name: 'login',
      component: () => import('@/views/Login.vue'),
      meta: { public: true }
    },
    {
      // 模型广场：由 sub2api 以 iframe 嵌入，身份来自 sub2api 透传的 token。
      // 刻意挂在 Layout 之外——嵌入页不需要（也不应有）本站侧边栏与头部。
      path: '/embed/plaza',
      name: 'embed-plaza',
      component: () => import('@/views/embed/PlazaEmbedPage.vue'),
      meta: { public: true }
    },
    {
      // KYC 自助：同上的嵌入身份体系。新增 /embed/* 页面必须同步登记到
      // 后端 middleware.embedPagePaths，否则 CSP frame-ancestors 会拒绝嵌入。
      path: '/embed/kyc',
      name: 'embed-kyc',
      component: () => import('@/views/embed/KycEmbedPage.vue'),
      meta: { public: true }
    },
    {
      path: '/',
      component: () => import('@/views/Layout.vue'),
      children: [
        {
          path: '',
          name: 'dashboard',
          component: () => import('@/views/Dashboard.vue'),
          meta: { titleKey: 'nav.dashboard', descriptionKey: 'page.dashboardDesc' }
        },
        {
          path: 'providers',
          name: 'providers',
          component: () => import('@/views/Providers.vue'),
          meta: { titleKey: 'nav.providers', descriptionKey: 'page.providersDesc' }
        },
        {
          path: 'stats',
          name: 'stats',
          component: () => import('@/views/Stats.vue'),
          meta: { titleKey: 'nav.stats', descriptionKey: 'page.statsDesc' }
        },
        {
          path: 'rates',
          name: 'rates',
          component: () => import('@/views/Rates.vue'),
          meta: { titleKey: 'nav.rates', descriptionKey: 'page.ratesDesc' }
        },
        {
          path: 'pricing',
          name: 'pricing',
          component: () => import('@/views/Pricing.vue'),
          meta: { titleKey: 'nav.pricing', descriptionKey: 'page.pricingDesc' }
        },
        {
          path: 'stability',
          name: 'stability',
          component: () => import('@/views/Stability.vue'),
          meta: { titleKey: 'nav.stability', descriptionKey: 'page.stabilityDesc' }
        },
        {
          path: 'credit',
          name: 'credit',
          component: () => import('@/views/Credit.vue'),
          meta: { titleKey: 'nav.credit', descriptionKey: 'page.creditDesc' }
        },
        {
          path: 'settings',
          name: 'settings',
          component: () => import('@/views/Settings.vue'),
          meta: { titleKey: 'nav.settings', descriptionKey: 'page.settingsDesc' }
        }
      ]
    },
    { path: '/:pathMatch(.*)*', redirect: '/' }
  ]
})

// 嵌入页调试入口：只在开发构建注册。
//
// import.meta.env.DEV 是编译期常量，生产构建时这段整体被摇树消除 —— 组件不进产物，
// 路径直接落到上面的兜底 redirect。这比运行时判断更彻底：生产环境根本不存在该页面。
if (import.meta.env.DEV) {
  router.addRoute({
    path: '/embed/_dev',
    name: 'embed-dev',
    component: () => import('@/views/embed/EmbedDevPage.vue'),
    meta: { public: true }
  })
}

const nav = useNavigationLoadingState()

router.beforeEach((to) => {
  const token = getToken()
  if (!to.meta.public && !token) {
    return { name: 'login', query: { redirect: to.fullPath } }
  }
  if (to.name === 'login' && token) {
    return { name: 'dashboard' }
  }
  // 放行后才开始计时，被守卫拦下的导航不该亮进度条
  nav.startNavigation()
  return true
})

// afterEach 覆盖成功导航，onError 覆盖异步组件加载失败，两者都要收尾避免进度条卡死
router.afterEach(() => nav.endNavigation())
router.onError(() => nav.endNavigation())

// 401 时跳回登录页
setUnauthorizedHandler(() => {
  if (router.currentRoute.value.name !== 'login') {
    router.push({ name: 'login' })
  }
})

export default router
