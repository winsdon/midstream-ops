import { describe, it, expect } from 'vitest'
import {
  countStatuses,
  estimateRequests,
  findCheck,
  groupChecks,
  gradeVariant,
  highCostSelection,
  labelVariant,
  matchingPresetId,
  progressPercent,
  sameCheckSet,
  splitReasons,
  statusIcon,
  targetKey,
  withDependencies
} from '@/utils/detectModel'
import type { DetectCheckMeta, DetectCheckResult, DetectTargetRun } from '@/types/detect'

const CHECKS: DetectCheckMeta[] = [
  { id: 'ping', title: '基础请求', group: 'gateway', default: true, cost: 'low', requests: 1 },
  { id: 'ping-again', title: '官方缓存链路', group: 'gateway', default: true, cost: 'low', requests: 1, requires: ['ping'] },
  { id: 'thinking-sig', title: 'thinking 签名', group: 'protocol', default: true, cost: 'medium', requests: 1 },
  {
    id: 'sig-tamper',
    title: '签名完整性',
    group: 'protocol',
    default: true,
    cost: 'medium',
    requests: 2,
    requires: ['thinking-sig']
  },
  { id: 'hello-entropy', title: '采样熵', group: 'capability', default: false, cost: 'high', requests: 10 },
  { id: 'quality-baseline', title: '回复质量 / 速度 / thinking', group: 'capability', default: true, cost: 'high', requests: 7 }
]

function check(id: string, status: DetectCheckResult['status']): DetectCheckResult {
  return { id, title: id, group: 'gateway', status, summary: '', duration_ms: 1 }
}

describe('groupChecks', () => {
  it('按固定顺序归拢分组，且不含空分组', () => {
    const groups = groupChecks(CHECKS)
    expect(groups.map((g) => g.group)).toEqual(['gateway', 'protocol', 'capability'])
    expect(groups[0].items.map((c) => c.id)).toEqual(['ping', 'ping-again'])
  })
})

describe('withDependencies', () => {
  it('补齐前置项', () => {
    const got = withDependencies(CHECKS, new Set(['sig-tamper']))
    expect([...got].sort()).toEqual(['sig-tamper', 'thinking-sig'])
  })

  it('忽略不存在的检测项', () => {
    expect([...withDependencies(CHECKS, new Set(['nope']))]).toEqual([])
  })
})

describe('estimateRequests', () => {
  it('按目标数量放大请求估算', () => {
    const selected = new Set(['ping', 'sig-tamper'])
    expect(estimateRequests(CHECKS, selected, 1)).toBe(3)
    expect(estimateRequests(CHECKS, selected, 3)).toBe(9)
  })

  it('目标为 0 时按 1 个算，避免显示 0 次请求', () => {
    expect(estimateRequests(CHECKS, new Set(['ping']), 0)).toBe(1)
  })

  it('质量基准按 Fable 双档的最多 7 次请求估算', () => {
    expect(estimateRequests(CHECKS, new Set(['quality-baseline']), 1)).toBe(7)
    expect(estimateRequests(CHECKS, new Set(['quality-baseline']), 2)).toBe(14)
  })
})

describe('highCostSelection', () => {
  it('只挑出勾中的高成本项', () => {
    expect(highCostSelection(CHECKS, new Set(['ping', 'hello-entropy'])).map((c) => c.id)).toEqual([
      'hello-entropy'
    ])
    expect(highCostSelection(CHECKS, new Set(['ping']))).toEqual([])
  })
})

describe('countStatuses / findCheck', () => {
  const run: DetectTargetRun = {
    name: 't',
    base_url: 'https://x',
    model: 'claude-opus-5',
    auth_mode: 'both',
    started_at: '',
    checks: [check('ping', 'passed'), check('stream', 'failed'), check('pdf', 'unsupported')]
  }

  it('统计各结论数量', () => {
    expect(countStatuses(run)).toEqual({
      passed: 1,
      failed: 1,
      suspicious: 0,
      unsupported: 1,
      inconclusive: 0
    })
  })

  it('目标尚未开始时返回全零而不是崩溃', () => {
    expect(countStatuses(undefined).passed).toBe(0)
  })

  it('按 id 找到对应项', () => {
    expect(findCheck(run, 'stream')?.status).toBe('failed')
    expect(findCheck(run, 'not-run')).toBeUndefined()
  })
})

describe('splitReasons', () => {
  it('把支持主判定的证据单独分出来，零权重归入其他线索', () => {
    const reasons = [
      { class: 'aws_bedrock' as const, weight: 5, key: 'a' },
      { class: 'wrapper' as const, weight: 2, key: 'b' },
      { class: 'info' as const, weight: 0, key: 'c' }
    ]
    const { primary, others } = splitReasons(reasons, 'aws_bedrock')
    expect(primary.map((r) => r.key)).toEqual(['a'])
    expect(others.map((r) => r.key)).toEqual(['b', 'c'])
  })
})

describe('配色映射', () => {
  it('包装伪装用危险色，官方直连用成功色', () => {
    expect(labelVariant('wrapper')).toBe('danger')
    expect(labelVariant('official_api')).toBe('success')
    expect(labelVariant('unknown')).toBe('gray')
  })

  it('伪装等级用危险色', () => {
    expect(gradeVariant('fake')).toBe('danger')
    expect(gradeVariant('genuine')).toBe('success')
  })

  it('未运行的格子给中性点号', () => {
    expect(statusIcon(undefined)).toBe('·')
    expect(statusIcon('passed')).toBe('✓')
  })

  it('执行中不计入口径统计', () => {
    const run: DetectTargetRun = {
      name: 't',
      base_url: 'https://x',
      model: 'm',
      auth_mode: 'both',
      started_at: '',
      checks: [check('ping', 'running'), check('stream', 'passed')]
    }
    expect(countStatuses(run)).toEqual({
      passed: 1,
      failed: 0,
      suspicious: 0,
      unsupported: 0,
      inconclusive: 0
    })
    expect(statusIcon('running')).toBe('…')
  })
})

describe('targetKey', () => {
  const base: DetectTargetRun = {
    name: 'A',
    base_url: 'https://x',
    model: 'm',
    auth_mode: 'both',
    started_at: '',
    checks: null
  }

  it('账号目标按 id 串联', () => {
    expect(targetKey({ ...base, account_id: 9 })).toBe('account:9')
  })

  it('手填目标按地址与名称串联', () => {
    expect(targetKey(base)).toBe('manual:https://x:A')
  })
})

describe('progressPercent', () => {
  it('总数为 0 时返回 0 而不是 NaN', () => {
    expect(progressPercent(0, 0)).toBe(0)
  })

  it('超出总数时封顶 100', () => {
    expect(progressPercent(12, 10)).toBe(100)
    expect(progressPercent(3, 12)).toBe(25)
  })
})

describe('matchingPresetId', () => {
  const presets = [
    { id: 'cc_max', title: 'CC Max', checks: ['ping', 'persona-cc'] },
    { id: 'aws_bedrock', title: 'AWS Bedrock', checks: ['ping', 'tool-use'] }
  ]

  it('顺序不同仍算命中预设', () => {
    expect(sameCheckSet(['tool-use', 'ping'], ['ping', 'tool-use'])).toBe(true)
    expect(matchingPresetId(['tool-use', 'ping'], presets)).toBe('aws_bedrock')
  })

  it('加减过检测项就不再算预设', () => {
    expect(matchingPresetId(['ping', 'persona-cc', 'pdf'], presets)).toBeNull()
    expect(matchingPresetId(['ping'], presets)).toBeNull()
  })
})
