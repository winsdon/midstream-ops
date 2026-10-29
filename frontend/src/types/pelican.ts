export type PelicanProtocol = 'messages' | 'responses' | 'chat'
/** 目标凭据来源：本站已建档上游账号 / 手填网关地址 + Key。 */
export type PelicanSource = 'account' | 'manual'
export interface PelicanGroup {
  id: number; name: string; platform: string; models: string[]
}
/** 账号当前状态，口径同 sub2api 后台账号列表。 */
export type PelicanAccountState = 'active' | 'inactive' | 'error' | 'rate_limited' | 'overloaded' | 'temp_unschedulable' | 'unschedulable'
export interface PelicanAccount {
  id: number; name: string; platform: string; base_url: string
  /** 账号归属的分组 id，用于按归属分组过滤账号选择项。 */
  group_ids: number[]
  state?: PelicanAccountState
  /** 限流中 / 过载 / 临时不可调度的恢复时间。 */
  state_until?: string
  error_message?: string
}
/** 糖果题判定结果（客户接口只下发 status / answer / expected）。 */
export interface PelicanCandy {
  status: 'queued' | 'correct' | 'wrong' | 'no_answer' | 'failed'
  answer?: string; expected?: string; duration_ms?: number; error?: string
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
  /** 每个目标同时答糖果题；不传表示不测。 */
  candy?: boolean
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
  /** 糖果题结果；为空表示这次没测糖果题。 */
  candy?: PelicanCandy
  output?: { document?: string; kind?: 'html' | 'svg'; text?: string; usage?: Record<string, unknown>; request?: Record<string, unknown>; candy_text?: string }
}
export interface PelicanBatch { id: number; request_id: string; created_at: string }
export interface PelicanPage { items: PelicanResult[]; total: number; pages: number; page: number }
export interface PelicanFacet { group_id: number; group_name: string; model: string }
