export type PelicanProtocol = 'messages' | 'responses' | 'chat'
/** 目标凭据来源：本站已建档上游账号 / 手填网关地址 + Key。 */
export type PelicanSource = 'account' | 'manual'
export interface PelicanGroup {
  id: number; name: string; platform: string; models: string[]
}
export interface PelicanAccount {
  id: number; name: string; platform: string; base_url: string
  /** 账号归属的分组 id，用于按归属分组过滤账号选择项。 */
  group_ids: number[]
}
export interface PelicanTarget {
  group_id: number; source: PelicanSource
  account_id?: number; base_url?: string; api_key?: string
  model: string; protocol: PelicanProtocol; retry_of?: number
}
export interface PelicanRequest {
  request_id: string; prompt: string; targets: PelicanTarget[]
  concurrency: number; max_tokens: number; timeout_seconds: number
  /** 思考强度，只对 Messages 协议生效；不传表示上游默认。 */
  effort?: string
}
export interface PelicanResult {
  id: number; group_id: number; group_name: string; model: string
  tested_at: string; published_at: string | null; duration_ms: number
  prompt: string; protocol: PelicanProtocol; max_tokens: number; has_document: boolean
  batch_id?: number; retry_of?: number; timeout_seconds?: number; effort?: string
  /** account / manual；旧的分组 Key 记录为空且不可重新测试 */
  source?: string; account_id?: number; base_url?: string
  /** 发起测试时的账号名称快照（仅管理端接口返回，客户接口不下发） */
  account_name?: string
  started_at?: string; finished_at?: string; status?: string; error?: string
  output?: { document?: string; kind?: 'html' | 'svg'; text?: string; usage?: Record<string, unknown>; request?: Record<string, unknown> }
}
export interface PelicanBatch { id: number; request_id: string; created_at: string }
export interface PelicanPage { items: PelicanResult[]; total: number; pages: number; page: number }
export interface PelicanFacet { group_id: number; group_name: string; model: string }
