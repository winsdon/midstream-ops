import { defineStore } from 'pinia'
import { authApi } from '@/api'
import { getToken, setToken, clearToken } from '@/api/client'

export const useAuthStore = defineStore('auth', {
  state: () => ({
    username: localStorage.getItem('monitor_username') || '',
    token: getToken()
  }),
  getters: {
    isAuthenticated: (s) => !!s.token
  },
  actions: {
    async login(username: string, password: string) {
      const res = await authApi.login(username, password)
      this.token = res.token
      this.username = res.username
      setToken(res.token)
      localStorage.setItem('monitor_username', res.username)
    },
    logout() {
      this.token = null
      this.username = ''
      clearToken()
      localStorage.removeItem('monitor_username')
    }
  }
})
