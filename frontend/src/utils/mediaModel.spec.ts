import { describe, it, expect } from 'vitest'
import {
  ticksToUSD,
  imageSizeTier,
  emptyMediaForm,
  estimateTicks,
  validateMediaForm,
  modelsForKind,
  isVideoKind,
  needsUpload,
  needsImageURL,
  newClientRequestID,
  IMAGE_MAX_N,
  PROMPT_MAX_LEN
} from '@/utils/mediaModel'
import type { MediaKey } from '@/api/embedMedia'

/** 构造一把 Grok key：图片两档 + 视频。 */
function grokKey(): MediaKey {
  return {
    id: 1,
    name: 'my-key',
    masked_key: 'sk-...ab12',
    group_name: 'grok-group',
    platform: 'grok',
    image_models: [
      { name: 'grok-imagine-image', capability: 'image', supports_size: false, unit_price_ticks: 200_000_000 },
      { name: 'grok-imagine-image-quality', capability: 'image', supports_size: false, unit_price_ticks: 700_000_000 }
    ],
    video_models: [
      { name: 'grok-imagine-video', capability: 'video', supports_size: false, unit_price_ticks: 500_000_000 }
    ],
    video_pricing: { '480p': 500_000_000, '720p': 700_000_000 }
  }
}

/** 构造一把只有图片能力的 OpenAI key。 */
function openaiKey(): MediaKey {
  return {
    id: 2,
    name: 'oai',
    masked_key: 'sk-...cd34',
    group_name: 'oai-group',
    platform: 'openai',
    image_models: [
      { name: 'gpt-image-2', capability: 'image', supports_size: true, unit_price_ticks: 0 }
    ],
    video_models: [],
    video_pricing: {}
  }
}

describe('ticksToUSD', () => {
  it('按 1e-10 USD 换算', () => {
    expect(ticksToUSD(200_000_000)).toBe('0.0200')
    expect(ticksToUSD(5_600_000_000)).toBe('0.5600')
    expect(ticksToUSD(10_500_000_000)).toBe('1.0500')
    expect(ticksToUSD(0)).toBe('0.0000')
  })
})

describe('imageSizeTier', () => {
  it('按最长边判定档位', () => {
    expect(imageSizeTier('1024x1024')).toBe('1K')
    expect(imageSizeTier('576x1024')).toBe('1K')
    expect(imageSizeTier('2048x1152')).toBe('2K')
    expect(imageSizeTier('3840x2160')).toBe('4K')
  })

  it('2560x1440 是 4K 而不是 2K —— 最容易让用户超支的陷阱', () => {
    expect(imageSizeTier('2560x1440')).toBe('4K')
  })

  it('非法尺寸返回空串', () => {
    expect(imageSizeTier('4K')).toBe('')
    expect(imageSizeTier('1024')).toBe('')
    expect(imageSizeTier('')).toBe('')
  })
})

describe('任务类型判定', () => {
  it('区分图片与视频', () => {
    expect(isVideoKind('t2v')).toBe(true)
    expect(isVideoKind('i2v')).toBe(true)
    expect(isVideoKind('t2i')).toBe(false)
    expect(isVideoKind('i2i')).toBe(false)
  })

  it('只有图生图需要上传文件', () => {
    expect(needsUpload('i2i')).toBe(true)
    expect(needsUpload('i2v')).toBe(false)
  })

  it('只有图生视频需要公网 URL —— multipart 会被上游 415', () => {
    expect(needsImageURL('i2v')).toBe(true)
    expect(needsImageURL('i2i')).toBe(false)
  })
})

describe('modelsForKind', () => {
  it('按任务类型取对应能力的模型', () => {
    const key = grokKey()
    expect(modelsForKind(key, 't2i')).toHaveLength(2)
    expect(modelsForKind(key, 't2v')).toHaveLength(1)
  })

  it('无 key 时返回空数组', () => {
    expect(modelsForKind(null, 't2i')).toEqual([])
  })

  it('不支持视频的 key 视频模型为空', () => {
    expect(modelsForKind(openaiKey(), 't2v')).toEqual([])
  })
})

describe('estimateTicks', () => {
  it('图片按张数线性', () => {
    const key = grokKey()
    const form = { ...emptyMediaForm(), model: 'grok-imagine-image', n: 1 }
    expect(estimateTicks(form, key)).toBe(200_000_000)
    expect(estimateTicks({ ...form, n: 4 }, key)).toBe(800_000_000)
  })

  it('高质量图单价更高', () => {
    const form = { ...emptyMediaForm(), model: 'grok-imagine-image-quality', n: 1 }
    expect(estimateTicks(form, grokKey())).toBe(700_000_000)
  })

  it('视频按分辨率单价 × 秒数', () => {
    const key = grokKey()
    const base = { ...emptyMediaForm(), kind: 't2v' as const, model: 'grok-imagine-video' }
    expect(estimateTicks({ ...base, resolution: '480p', duration: 8 }, key)).toBe(4_000_000_000)
    expect(estimateTicks({ ...base, resolution: '720p', duration: 8 }, key)).toBe(5_600_000_000)
    // 15 秒 720p 是最贵的组合：$1.05
    expect(estimateTicks({ ...base, resolution: '720p', duration: 15 }, key)).toBe(10_500_000_000)
  })

  it('未选模型或未知模型时返回 0', () => {
    expect(estimateTicks(emptyMediaForm(), grokKey())).toBe(0)
    expect(estimateTicks({ ...emptyMediaForm(), model: 'nope' }, grokKey())).toBe(0)
  })

  it('按分组定价的模型无法预估', () => {
    const form = { ...emptyMediaForm(), model: 'gpt-image-2', n: 1 }
    expect(estimateTicks(form, openaiKey())).toBe(0)
  })
})

describe('validateMediaForm', () => {
  const key = grokKey()

  it('合法图片表单通过', () => {
    const form = { ...emptyMediaForm(), keyId: 1, model: 'grok-imagine-image', prompt: '小熊猫', n: 1 }
    expect(validateMediaForm(form, key)).toBe('')
  })

  it('合法视频表单通过', () => {
    const form = {
      ...emptyMediaForm(),
      kind: 't2v' as const,
      keyId: 1,
      model: 'grok-imagine-video',
      prompt: '海浪',
      resolution: '720p',
      duration: 8
    }
    expect(validateMediaForm(form, key)).toBe('')
  })

  it('缺 key / 模型 / 提示词分别报错', () => {
    expect(validateMediaForm(emptyMediaForm(), null)).toBe('media.errors.selectKey')
    expect(validateMediaForm(emptyMediaForm(), key)).toBe('media.errors.selectModel')
    expect(
      validateMediaForm({ ...emptyMediaForm(), model: 'grok-imagine-image', prompt: '  ' }, key)
    ).toBe('media.errors.emptyPrompt')
  })

  it('提示词超长被拒', () => {
    const form = {
      ...emptyMediaForm(),
      model: 'grok-imagine-image',
      prompt: 'a'.repeat(PROMPT_MAX_LEN + 1)
    }
    expect(validateMediaForm(form, key)).toBe('media.errors.promptTooLong')
  })

  it('张数越界被拒', () => {
    const base = { ...emptyMediaForm(), model: 'grok-imagine-image', prompt: 'x' }
    expect(validateMediaForm({ ...base, n: 0 }, key)).toBe('media.errors.badCount')
    expect(validateMediaForm({ ...base, n: IMAGE_MAX_N + 1 }, key)).toBe('media.errors.badCount')
  })

  it('1080p 与越界时长被拒', () => {
    const base = {
      ...emptyMediaForm(),
      kind: 't2v' as const,
      model: 'grok-imagine-video',
      prompt: 'x',
      resolution: '720p',
      duration: 8
    }
    expect(validateMediaForm({ ...base, resolution: '1080p' }, key)).toBe('media.errors.badResolution')
    expect(validateMediaForm({ ...base, duration: 0 }, key)).toBe('media.errors.badDuration')
    expect(validateMediaForm({ ...base, duration: 16 }, key)).toBe('media.errors.badDuration')
  })

  it('图生视频必须给公网 http(s) 参考图', () => {
    const base = {
      ...emptyMediaForm(),
      kind: 'i2v' as const,
      model: 'grok-imagine-video',
      prompt: 'x',
      resolution: '480p',
      duration: 8
    }
    expect(validateMediaForm(base, key)).toBe('media.errors.badImageURL')
    expect(validateMediaForm({ ...base, imageURL: '/local.jpg' }, key)).toBe('media.errors.badImageURL')
    expect(validateMediaForm({ ...base, imageURL: 'https://x.com/a.jpg' }, key)).toBe('')
  })
})

describe('newClientRequestID', () => {
  it('生成非空且互不相同的幂等键', () => {
    const a = newClientRequestID()
    const b = newClientRequestID()
    expect(a).toBeTruthy()
    expect(a).not.toBe(b)
  })
})
