import axios, { AxiosError, AxiosInstance } from 'axios'
import type { ApiResponse } from '@/types'

const TOKEN_KEY = 'monitor_token'

export function getToken(): string | null {
  return localStorage.getItem(TOKEN_KEY)
}
export function setToken(token: string) {
  localStorage.setItem(TOKEN_KEY, token)
}
export function clearToken() {
  localStorage.removeItem(TOKEN_KEY)
}

// 401 时由 router guard 处理跳转，这里仅清 token 并抛出。
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

http.interceptors.response.use(
  (resp) => resp,
  (error: AxiosError<ApiResponse>) => {
    if (error.response?.status === 401) {
      clearToken()
      onUnauthorized?.()
    }
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
