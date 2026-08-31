const TOKEN_KEY = 'monitor_token'
const REFRESH_TOKEN_KEY = 'monitor_refresh_token'
const EXPIRES_AT_KEY = 'monitor_token_expires_at'
const USERNAME_KEY = 'monitor_username'

export function getToken(): string | null {
  return localStorage.getItem(TOKEN_KEY)
}

export function setToken(token: string) {
  localStorage.setItem(TOKEN_KEY, token)
}

export function getRefreshToken(): string | null {
  return localStorage.getItem(REFRESH_TOKEN_KEY)
}

export function setRefreshToken(token: string) {
  localStorage.setItem(REFRESH_TOKEN_KEY, token)
}

export function getTokenExpiresAt(): number | null {
  const raw = localStorage.getItem(EXPIRES_AT_KEY)
  if (!raw) return null
  const n = Number(raw)
  return Number.isFinite(n) ? n : null
}

export function setTokenExpiresAt(expiresInSeconds: number) {
  localStorage.setItem(EXPIRES_AT_KEY, String(Date.now() + expiresInSeconds * 1000))
}

export function persistTokenPair(pair: { token: string; refresh_token: string; expires_in: number }) {
  setToken(pair.token)
  setTokenExpiresAt(pair.expires_in)
  // refresh 最后写，作为其它 tab 的提交标记
  setRefreshToken(pair.refresh_token)
}

export function clearAuthTokens() {
  localStorage.removeItem(TOKEN_KEY)
  localStorage.removeItem(REFRESH_TOKEN_KEY)
  localStorage.removeItem(EXPIRES_AT_KEY)
  localStorage.removeItem(USERNAME_KEY)
}

export function getStoredUsername(): string {
  return localStorage.getItem(USERNAME_KEY) || ''
}

export function setStoredUsername(username: string) {
  localStorage.setItem(USERNAME_KEY, username)
}
