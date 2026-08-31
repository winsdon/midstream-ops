import { defineStore } from 'pinia'
import { authApi } from '@/api'
import {
  clearToken,
  getRefreshToken,
  getStoredUsername,
  getToken,
  persistTokenPair,
  setStoredUsername
} from '@/api/client'
import { refreshAuthTokens } from '@/api/tokenRefresh'

const TOKEN_REFRESH_BUFFER_MS = 120_000
const MAX_TIMEOUT_MS = 2_147_483_647

let tokenRefreshTimeoutId: ReturnType<typeof setTimeout> | null = null

export const useAuthStore = defineStore('auth', {
  state: () => ({
    username: getStoredUsername(),
    token: getToken() as string | null
  }),
  getters: {
    isAuthenticated: (s) => !!s.token
  },
  actions: {
    async initSession() {
      if (!getRefreshToken()) return
      const expiresAt = Number(localStorage.getItem('monitor_token_expires_at'))
      const stale = !Number.isFinite(expiresAt) || expiresAt - Date.now() <= TOKEN_REFRESH_BUFFER_MS
      if (stale) {
        try {
          const pair = await refreshAuthTokens()
          this.token = pair.token
        } catch {
          this.forceLogout()
          return
        }
      }
      this.scheduleTokenRefresh()
    },
    async login(username: string, password: string) {
      const res = await authApi.login(username, password)
      this.applyTokenPair(res.token, res.refresh_token, res.expires_in, res.username)
    },
    applyTokenPair(token: string, refreshToken: string, expiresIn: number, username?: string) {
      this.token = token
      persistTokenPair({ token, refresh_token: refreshToken, expires_in: expiresIn })
      if (username) {
        this.username = username
        setStoredUsername(username)
      }
      this.scheduleTokenRefresh()
    },
    scheduleTokenRefresh() {
      this.stopTokenRefresh()
      const expiresAt = Number(localStorage.getItem('monitor_token_expires_at'))
      if (!getRefreshToken() || !Number.isFinite(expiresAt)) return
      const delay = Math.max(0, Math.min(expiresAt - Date.now() - TOKEN_REFRESH_BUFFER_MS, MAX_TIMEOUT_MS))
      tokenRefreshTimeoutId = setTimeout(() => {
        void this.performTokenRefresh()
      }, delay)
    },
    async performTokenRefresh() {
      if (!getRefreshToken()) return
      try {
        const pair = await refreshAuthTokens()
        this.token = pair.token
        this.scheduleTokenRefresh()
      } catch {
        // 401 拦截器会处理真正的会话失效
      }
    },
    stopTokenRefresh() {
      if (tokenRefreshTimeoutId) {
        clearTimeout(tokenRefreshTimeoutId)
        tokenRefreshTimeoutId = null
      }
    },
    forceLogout() {
      this.stopTokenRefresh()
      this.token = null
      this.username = ''
      clearToken()
    },
    async logout() {
      const refresh = getRefreshToken()
      if (refresh) {
        try {
          await authApi.logout(refresh)
        } catch {
          // 本地仍须清掉，服务端撤销失败不挡登出
        }
      }
      this.forceLogout()
    }
  }
})
