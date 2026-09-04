/** 上游 Claude 渠道检测（模型检测页）的类型定义。 */

/** 检测项分组。 */
export type DetectGroup = 'gateway' | 'protocol' | 'identity' | 'capability'

/** 检测项成本档位。 */
export type DetectCost = 'low' | 'medium' | 'high'

/** 单项结论。与后端 modeldetect 的常量一一对应。 */
export type DetectCheckStatus = 'running' | 'passed' | 'failed' | 'unsupported' | 'inconclusive' | 'suspicious'

/** 渠道类别（打分维度）。 */
export type DetectClass =
  | 'claude_max_pool'
  | 'official_api'
  | 'aws_bedrock'
  | 'kiro'
  | 'vertex'
  | 'wrapper'
  | 'info'

/** 主判定标签。比类别多了 unknown / unavailable 两种兜底。 */
export type DetectLabel = DetectClass | 'unknown' | 'unavailable'

/** 真实性等级。 */
export type AuthenticityGrade = 'genuine' | 'likely' | 'doubtful' | 'fake'

export interface DetectCheckMeta {
  id: string
  title: string
  group: DetectGroup
  default: boolean
  cost: DetectCost
  requests: number
  note?: string
  requires?: string[]
}

export interface DetectAccount {
  account_id: number
  account_name: string
  platform: string
  base_url: string
  /** 0 = 未关联供应商 */
  provider_id: number
  provider_name: string
  probe_model: string
  /** 本站分组名。空数组表示未分组。 */
  groups: string[]
}

/** 后端下发的场景预设。 */
export interface DetectPreset {
  id: string
  title: string
  checks: string[]
}

export interface DetectAssertion {
  label: string
  ok: boolean
  detail?: string
  /** 诊断项：不满足也不判失败 */
  diagnostic?: boolean
}

export interface DetectEvidence {
  key: string
  label: string
  detail?: string
  class: DetectClass
  weight: number
}

export interface DetectExchange {
  kind: string
  method: string
  url: string
  request_headers: Record<string, string>
  request_body?: unknown
  status: number
  duration_ms: number
  ttft_ms?: number
  chunk_count?: number
  headers: Record<string, string>
  /** 已脱敏并截断的响应原文 */
  raw: string
  network_error?: string
}

export interface DetectCheckResult {
  id: string
  title: string
  group: DetectGroup
  status: DetectCheckStatus
  summary: string
  duration_ms: number
  assertions?: DetectAssertion[]
  evidence?: DetectEvidence[]
  exchanges?: DetectExchange[]
  auth_score?: number
  /** 非空表示触发了真实性封顶 */
  auth_cap_reason?: string
}

export interface DetectAuthenticity {
  score: number
  grade: AuthenticityGrade
  capped: boolean
  cap_reason?: string[]
}

export interface DetectVerdict {
  label: DetectLabel
  title: string
  confidence: 'high' | 'medium' | 'low'
  scores: Record<string, number>
  authenticity: DetectAuthenticity
  reasons: DetectEvidence[]
}

export interface DetectTargetRun {
  name: string
  base_url: string
  model: string
  auth_mode: string
  account_id?: number
  provider_id?: number
  checks: DetectCheckResult[] | null
  verdict?: DetectVerdict
  started_at: string
  finished_at?: string
  error?: string
}

export interface DetectJob {
  id: string
  status: 'running' | 'completed' | 'cancelled'
  checks: string[]
  total: number
  done: number
  current: string
  concurrency: number
  targets: DetectTargetRun[]
  created_at: string
  updated_at: string
}

/** 提交检测的单个目标：选账号只传 account_id，手填目标传地址与 key。 */
export interface DetectTargetInput {
  account_id?: number
  name?: string
  base_url?: string
  api_key?: string
}

export interface DetectRunPayload {
  targets: DetectTargetInput[]
  model?: string
  auth_mode?: string
  checks?: string[]
  extra_headers?: Record<string, string>
  timeout_ms?: number
  concurrency?: number
}

export interface DetectRunResult {
  job_id: string
  checks: string[]
  requests_estimate: number
}

export interface DetectHistoryItem {
  id: number
  account_id?: number | null
  account_name: string
  provider_id?: number | null
  target_fp: string
  target_name: string
  base_url: string
  model: string
  label: DetectLabel
  confidence: 'high' | 'medium' | 'low'
  authenticity_score: number
  authenticity_grade: AuthenticityGrade
  scores: Record<string, number>
  created_at: string
}

/** 历史详情：多带一份完整报告。 */
export interface DetectHistoryDetail extends DetectHistoryItem {
  report: DetectTargetRun
}
