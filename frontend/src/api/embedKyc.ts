/**
 * KYC 自助嵌入页 API。
 *
 * 通用的会话管理与 envelope 解包在 embedClient.ts，本文件只声明本页的端点。
 *
 * 【身份不在这里】所有请求的客户身份都由后端从会话上下文取，
 * 前端既不传也无法伪造 —— 保存请求体里连 customer_id 都没有。
 */

import { createEmbedClient } from './embedClient'
import type { CustomerKycProfile, KycPayload } from '@/types/credit'

const client = createEmbedClient('/api/v1/embed/kyc')

/**
 * 用 sub2api 透传的 token 换取本站短期会话。
 * userId 仅用于后端比对，不作为身份依据。
 */
export function createSession(sub2apiToken: string, userId: string): Promise<void> {
  return client.createSession(sub2apiToken, userId)
}

/** 拉取当前客户的 KYC 档案。从未填写时后端返回一份空白档案，不报错。 */
export function fetchProfile(): Promise<CustomerKycProfile> {
  return client.request<CustomerKycProfile>('/profile')
}

/** 保存档案。submit=true 送审并做必填校验，false 存草稿。 */
export function saveProfile(payload: KycPayload): Promise<CustomerKycProfile> {
  return client.request<CustomerKycProfile>('/profile', {
    method: 'PUT',
    body: JSON.stringify(payload)
  })
}
