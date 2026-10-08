export type PelicanProtocol = 'messages' | 'responses' | 'chat'
export interface PelicanConfig { gateway_url: string; user_id: string }
export interface PelicanGroup {
  id: number; name: string; platform: string; models: string[]
  keys: { id: number; name: string; masked: string }[]
  reason?: string
}
export interface PelicanTarget { group_id: number; key_id: number; model: string; protocol: PelicanProtocol; retry_of?: number }
export interface PelicanRequest {
  request_id: string; prompt: string; targets: PelicanTarget[]
  concurrency: number; max_tokens: number; timeout_seconds: number
}
export interface PelicanResult {
  id: number; group_id: number; group_name: string; model: string
  tested_at: string; published_at: string | null; duration_ms: number
  prompt: string; protocol: PelicanProtocol; max_tokens: number; has_document: boolean
  batch_id?: number; key_id?: number; retry_of?: number; timeout_seconds?: number
  started_at?: string; finished_at?: string; status?: string; error?: string
  output?: { document?: string; kind?: 'html' | 'svg'; text?: string; usage?: Record<string, unknown>; request?: Record<string, unknown> }
}
export interface PelicanBatch { id: number; request_id: string; created_at: string }
export interface PelicanPage { items: PelicanResult[]; total: number; pages: number; page: number }
export interface PelicanFacet { group_id: number; group_name: string; model: string }
