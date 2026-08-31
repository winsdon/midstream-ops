import axios from 'axios'
import type { ApiResponse, LoginResult } from '@/types'
import { getRefreshToken, getToken, persistTokenPair } from './authTokens'

const REFRESH_URL = '/api/v1/auth/refresh'
const TOKEN_REFRESH_TIMEOUT_MS = 30_000
const PEER_REFRESH_WAIT_MS = 1_000
const PEER_REFRESH_POLL_MS = 25

export interface RefreshTokenResponse {
  token: string
  refresh_token: string
  expires_in: number
}

let inFlightRefresh: Promise<RefreshTokenResponse> | null = null

function readPeerResult(snapshotRefresh: string): RefreshTokenResponse | null {
  const token = getToken()
  const refresh = getRefreshToken()
  if (!token || !refresh || refresh === snapshotRefresh) {
    return null
  }
  return {
    token,
    refresh_token: refresh,
    expires_in: 1
  }
}

async function waitForPeer(snapshotRefresh: string, deadline: number): Promise<RefreshTokenResponse | null> {
  while (Date.now() < deadline) {
    const peer = readPeerResult(snapshotRefresh)
    if (peer) return peer
    await new Promise((resolve) => setTimeout(resolve, PEER_REFRESH_POLL_MS))
  }
  return readPeerResult(snapshotRefresh)
}

async function requestTokenPair(snapshotRefresh: string): Promise<RefreshTokenResponse> {
  try {
    const response = await axios.post<ApiResponse<LoginResult>>(
      REFRESH_URL,
      { refresh_token: snapshotRefresh },
      { headers: { 'Content-Type': 'application/json' }, timeout: TOKEN_REFRESH_TIMEOUT_MS }
    )
    const body = response.data
    if (body.code !== 0 || !body.data?.token || !body.data.refresh_token) {
      throw new Error(body.message || 'Token refresh failed')
    }
    if (getRefreshToken() !== snapshotRefresh) {
      const peer = readPeerResult(snapshotRefresh)
      if (peer) return peer
      throw new Error('Session changed during token refresh')
    }
    const pair: RefreshTokenResponse = {
      token: body.data.token,
      refresh_token: body.data.refresh_token,
      expires_in: body.data.expires_in
    }
    persistTokenPair(pair)
    return pair
  } catch (error) {
    const peer = await waitForPeer(snapshotRefresh, Date.now() + PEER_REFRESH_WAIT_MS)
    if (peer) return peer
    throw error
  }
}

async function runRefresh(): Promise<RefreshTokenResponse> {
  const snapshotRefresh = getRefreshToken()
  if (!snapshotRefresh) {
    throw new Error('No refresh token available')
  }
  const peer = readPeerResult(snapshotRefresh)
  if (peer) return peer
  return requestTokenPair(snapshotRefresh)
}

/** 同一文档内并发续期合并为一次请求；失败时短暂等待采用其它 tab 已写入的新 token。 */
export function refreshAuthTokens(): Promise<RefreshTokenResponse> {
  if (inFlightRefresh) return inFlightRefresh
  const pending = runRefresh()
  inFlightRefresh = pending
  const clear = () => {
    if (inFlightRefresh === pending) inFlightRefresh = null
  }
  void pending.then(clear, clear)
  return pending
}
