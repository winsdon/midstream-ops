import axios, { AxiosError, AxiosInstance, InternalAxiosRequestConfig } from 'axios'
import type { ApiResponse } from '@/types'
import { clearAuthTokens, getRefreshToken, getToken } from './authTokens'
import { refreshAuthTokens } from './tokenRefresh'

export {
  getToken,
  setToken,
  getRefreshToken,
  setRefreshToken,
  persistTokenPair,
  getTokenExpiresAt,
  setTokenExpiresAt,
  getStoredUsername,
  setStoredUsername
} from './authTokens'

export function clearToken() {
  clearAuthTokens()
}

// 401 且续期失败时由 router 跳登录。
let onUnauthorized: (() => void) | null = null
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn
}

export const http: AxiosInstance = axios.create({
  baseURL: '/api/v1',
  timeout: 120000 // 探测类接口可能耗时较长
})

http.interceptors.request.use((config) => {
  const token = getToken()
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

function isAuthEndpoint(url?: string): boolean {
  if (!url) return false
  return url.includes('/auth/login') || url.includes('/auth/refresh') || url.includes('/auth/logout')
}

type RetryConfig = InternalAxiosRequestConfig & { _retry?: boolean }

http.interceptors.response.use(
  (resp) => resp,
  async (error: AxiosError<ApiResponse>) => {
    const original = error.config as RetryConfig | undefined
    if (error.response?.status !== 401 || !original || isAuthEndpoint(original.url)) {
      return Promise.reject(error)
    }
    const snapshotRefresh = getRefreshToken()
    if (!original._retry && snapshotRefresh) {
      original._retry = true
      try {
        const tokens = await refreshAuthTokens()
        original.headers = original.headers ?? {}
        original.headers.Authorization = `Bearer ${tokens.token}`
        return http(original)
      } catch {
        const peerToken = getToken()
        const peerRefresh = getRefreshToken()
        if (peerRefresh && peerRefresh !== snapshotRefresh && peerToken) {
          original.headers = original.headers ?? {}
          original.headers.Authorization = `Bearer ${peerToken}`
          return http(original)
        }
      }
    }
    clearToken()
    onUnauthorized?.()
    return Promise.reject(error)
  }
)

// 解包后端 envelope {code,message,data}；code!==0 视为业务错误。
export async function unwrap<T>(promise: Promise<{ data: ApiResponse<T> }>): Promise<T> {
  const resp = await promise
  const body = resp.data
  if (body.code !== 0) {
    throw new Error(body.message || '请求失败')
  }
  return body.data as T
}

export function errorMessage(e: unknown): string {
  if (axios.isAxiosError(e)) {
    const data = e.response?.data as ApiResponse | undefined
    if (data?.message) return data.message
    if (e.message) return e.message
  }
  if (e instanceof Error) return e.message
  return '请求失败'
}
