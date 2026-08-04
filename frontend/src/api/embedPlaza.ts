/**
 * 模型广场嵌入页 API。
 *
 * 通用的会话管理与 envelope 解包在 embedClient.ts，本文件只声明本页的端点。
 */

import { createEmbedClient } from './embedClient'
import type { PlazaData } from '@/types/plaza'

const client = createEmbedClient('/api/v1/embed/plaza')

/**
 * 用 sub2api 透传的 token 换取本站短期会话。
 * userId 仅用于后端比对，不作为身份依据。
 */
export function createSession(sub2apiToken: string, userId: string): Promise<void> {
  return client.createSession(sub2apiToken, userId)
}

/** 拉取模型广场数据（需已换取会话）。 */
export function fetchPlaza(): Promise<PlazaData> {
  return client.request<PlazaData>('/models')
}
