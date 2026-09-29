/** 上游 Claude 渠道检测（模型检测页）的类型定义。 */

/** 测试套件：模型真伪（渠道指纹 + 真实性评分）或智商测试（降智对照）。 */
export type DetectSuite = 'authenticity' | 'iq'

/** 智商题的判分方式：visual 交出作品人工评判，answer 自动核对答案。 */
export type DetectGrading = 'visual' | 'answer'

/** 检测项分组。 */
export type DetectGroup = 'gateway' | 'protocol' | 'identity' | 'capability' | 'iq'

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
  /** 缺省即模型真伪；只有智商题显式标 iq */
  suite?: DetectSuite
  /** 只做检测，不参与分类与真实性评分（如「是否 0 注入」） */
  informational?: boolean
  /** 智商题的判分方式 */
  grading?: DetectGrading
  /** 智商题原文 */
  prompt?: string
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

/** 智商题的完整作答（exchange 原文会截断，作品与回复全文单独保存）。 */
export interface DetectCheckOutput {
  /** 可见回复全文（不含 thinking） */
  text?: string
  /** 从回复中取出的完整作品，只能在沙箱 iframe 里渲染 */
  document?: string
  document_kind?: 'svg' | 'html'
  /** 解析出的最终答案与标准答案（自动判分的题目才有） */
  answer?: string
  expected?: string
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
  /** 只做检测，不参与分类与真实性评分 */
  informational?: boolean
  /** 智商题本轮实际发送的提示词（自定义鹈鹕题时存在） */
  prompt?: string
  output?: DetectCheckOutput
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
  suite?: DetectSuite
  checks: DetectCheckResult[] | null
  /** 智商测试不出判定，恒为空 */
  verdict?: DetectVerdict
  started_at: string
  finished_at?: string
  error?: string
}

export interface DetectJob {
  id: string
  suite?: DetectSuite
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


export interface DetectBaseline {
  id: number
  account_id?: number | null
  target_fp: string
  target_name: string
  base_url: string
  model: string
  template_version: string
  status: string
  quality_ok: boolean
  input_tokens: number
  output_tokens: number
  thinking_tokens: number
  thinking_chars: number
  ttft_ms: number
  duration_ms: number
  response_summary?: string
  error?: string
  created_at: string
  exchange?: DetectExchange | null
}

export interface DetectBaselinePayload {
  account_id: number
  model?: string
  auth_mode?: string
  extra_headers?: Record<string, string>
  timeout_ms?: number
}

export interface DetectRunPayload {
  targets: DetectTargetInput[]
  /** 缺省即模型真伪 */
  suite?: DetectSuite
  baseline_id?: number
  model?: string
  auth_mode?: string
  checks?: string[]
  /** 智商测试鹈鹕题的自定义提示词；为空时使用内置原题 */
  pelican_prompt?: string
  extra_headers?: Record<string, string>
  timeout_ms?: number
  concurrency?: number
}

export interface DetectRetryPayload {
  target_index?: number
  check_id?: string
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

/** 智商测试历史里每道题的一行结论。 */
export interface DetectIQResult {
  id: string
  title: string
  status: DetectCheckStatus
  summary: string
}

export interface DetectIQHistoryItem {
  id: number
  account_id?: number | null
  account_name: string
  provider_id?: number | null
  target_fp: string
  target_name: string
  base_url: string
  model: string
  passed: number
  total: number
  results: DetectIQResult[]
  created_at: string
}

/** 智商测试历史详情：带完整报告（含作品与回复）。 */
export interface DetectIQHistoryDetail extends DetectIQHistoryItem {
  report: DetectTargetRun
}
