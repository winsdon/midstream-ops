import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import axios from 'axios'
import { persistTokenPair, setRefreshToken, setToken } from './authTokens'

function installLocalStorage() {
  const store = new Map<string, string>()
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: {
      getItem: (k: string) => store.get(k) ?? null,
      setItem: (k: string, v: string) => {
        store.set(k, String(v))
      },
      removeItem: (k: string) => {
        store.delete(k)
      },
      clear: () => store.clear()
    }
  })
}

vi.mock('axios', () => ({
  default: {
    post: vi.fn()
  }
}))

const mockedPost = vi.mocked(axios.post)

function seedSession(refresh = 'old-refresh') {
  setToken('old-access')
  setRefreshToken(refresh)
}

function refreshedResponse() {
  return {
    data: {
      code: 0,
      message: 'ok',
      data: {
        token: 'new-access',
        refresh_token: 'new-refresh',
        expires_in: 3600,
        expires_at: new Date(Date.now() + 3600_000).toISOString(),
        username: 'admin'
      }
    }
  }
}

describe('refreshAuthTokens', () => {
  beforeEach(() => {
    installLocalStorage()
    mockedPost.mockReset()
    vi.resetModules()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('同一文档内并发调用只打一次 /auth/refresh', async () => {
    seedSession()
    let resolveRequest!: (value: ReturnType<typeof refreshedResponse>) => void
    mockedPost.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveRequest = resolve
        })
    )
    const { refreshAuthTokens } = await import('./tokenRefresh')

    const first = refreshAuthTokens()
    const second = refreshAuthTokens()
    expect(mockedPost).toHaveBeenCalledTimes(1)
    resolveRequest(refreshedResponse())

    await expect(first).resolves.toMatchObject({ token: 'new-access' })
    await expect(second).resolves.toMatchObject({ refresh_token: 'new-refresh' })
    expect(localStorage.getItem('monitor_refresh_token')).toBe('new-refresh')
  })

  it('续期失败时采用其它 tab 已写入的新 token', async () => {
    seedSession('old-refresh')
    mockedPost.mockRejectedValueOnce({ response: { status: 401 } })
    const { refreshAuthTokens } = await import('./tokenRefresh')

    const pending = refreshAuthTokens()
    persistTokenPair({ token: 'peer-access', refresh_token: 'peer-refresh', expires_in: 3600 })

    await expect(pending).resolves.toMatchObject({
      token: 'peer-access',
      refresh_token: 'peer-refresh'
    })
  })

  it('无 refresh_token 时直接失败', async () => {
    const { refreshAuthTokens } = await import('./tokenRefresh')
    await expect(refreshAuthTokens()).rejects.toThrow('No refresh token available')
    expect(mockedPost).not.toHaveBeenCalled()
  })
})
