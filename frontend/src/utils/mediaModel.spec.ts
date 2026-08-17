import { describe, it, expect } from 'vitest'
import {
  ticksToUSD,
  imageSizeTier,
  imageSizeForAspectRatio,
  imageAspectRatioFromSize,
  billingTierOf,
  downgradeTargetOf,
  emptyMediaForm,
  resetMediaFormForKind,
  estimateTicks,
  validateMediaForm,
  modelsForKind,
  selectedModelOf,
  isVideoKind,
  needsUpload,
  needsImageURL,
  uniquePublicImageURLs,
  appendRefImages,
  splitRefImageInput,
  newClientRequestID,
  IMAGE_MAX_N,
  PROMPT_MAX_LEN
} from '@/utils/mediaModel'
import type { MediaKey, MediaModelOption } from '@/api/embedMedia'

const GROK_RATIOS = ['1:1', '16:9', '9:16', '4:3', '3:4', '3:2', '2:3', '2:1', '1:2', '19.5:9', '9:19.5', '20:9', '9:20', 'auto']

/**
 * 构造一把 Grok key。
 *
 * 单价字段模拟后端下发的**已折算价**（含分组自定义价与倍率）——
 * 前端的职责只是「单价 × 数量」，这些测试钉的就是这条乘法链不出错。
 */
function grokKey(): MediaKey {
  const image = (name: string, p1k: number, p2k: number): MediaModelOption => ({
    name,
    capability: 'image',
    size_mode: 'aspect_ratio',
    aspect_ratios: GROK_RATIOS,
    resolutions: ['1k', '2k'],
    unit_price_ticks: p1k,
    price_by_tier: { '1K': p1k, '2K': p2k, '4K': p2k }
  })
  return {
    id: 1,
    name: 'my-key',
    masked_key: 'sk-...ab12',
    group_name: 'grok-group',
    platform: 'grok',
    image_models: [
      image('grok-imagine-image', 200_000_000, 200_000_000),
      image('grok-imagine-image-quality', 500_000_000, 700_000_000)
    ],
    video_models: [
      {
        name: 'grok-imagine-video',
        capability: 'video',
        size_mode: 'aspect_ratio',
        aspect_ratios: GROK_RATIOS,
        resolutions: ['480p', '720p', '1080p'],
        unit_price_ticks: 500_000_000,
        price_by_tier: { '480p': 500_000_000, '720p': 700_000_000, '1080p': 700_000_000 }
      },
      {
        name: 'grok-imagine-video-1.5',
        capability: 'video',
        size_mode: 'aspect_ratio',
        aspect_ratios: GROK_RATIOS,
        resolutions: ['480p', '720p', '1080p'],
        unit_price_ticks: 800_000_000,
        price_by_tier: { '480p': 800_000_000, '720p': 1_400_000_000, '1080p': 2_500_000_000 },
        downgrades_to: 'grok-imagine-video',
        downgrade_kinds: ['t2v']
      }
    ],
    pricing_known: true
  }
}

/** 构造一把只有图片能力的 OpenAI key（size 模式，无权威价）。 */
function openaiKey(): MediaKey {
  return {
    id: 2,
    name: 'oai',
    masked_key: 'sk-...cd34',
    group_name: 'oai-group',
    platform: 'openai',
    image_models: [
      {
        name: 'gpt-image-2',
        capability: 'image',
        size_mode: 'size',
        unit_price_ticks: 0,
        price_by_tier: { '1K': 0, '2K': 0, '4K': 0 }
      }
    ],
    video_models: [],
    pricing_known: false
  }
}

/** Grok 图片表单：aspect_ratio 模式必须清空 size。 */
function grokImageForm(overrides: Partial<ReturnType<typeof emptyMediaForm>> = {}) {
  return {
    ...emptyMediaForm(),
    keyId: 1,
    model: 'grok-imagine-image',
    prompt: '小熊猫',
    n: 1,
    size: '',
    aspectRatio: '1:1',
    imageResolution: '1k',
    ...overrides
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

describe('图片尺寸与宽高比联动（仅 size 模式的模型用得上）', () => {
  it('切换比例时保留当前清晰度档位', () => {
    expect(imageSizeForAspectRatio('16:9', '1K')).toBe('1024x576')
    expect(imageSizeForAspectRatio('16:9', '2K')).toBe('2048x1152')
    expect(imageSizeForAspectRatio('16:9', '4K')).toBe('3840x2160')
    expect(imageSizeForAspectRatio('2:1', '4K')).toBe('4096x2048')
  })

  it('切换尺寸时反推出对应宽高比', () => {
    expect(imageAspectRatioFromSize('1024x1024')).toBe('1:1')
    expect(imageAspectRatioFromSize('1536x2048')).toBe('3:4')
    expect(imageAspectRatioFromSize('123x456')).toBe('')
  })

  it('21:9 已移除 —— 它不在 xAI 的合法取值里，选中只会收到上游报错', () => {
    expect(imageSizeForAspectRatio('21:9', '4K')).toBe('')
  })
})

describe('任务类型判定', () => {
  it('区分图片与视频', () => {
    expect(isVideoKind('t2v')).toBe(true)
    expect(isVideoKind('i2v')).toBe(true)
    expect(isVideoKind('t2i')).toBe(false)
    expect(isVideoKind('i2i')).toBe(false)
  })

  it('图生图和图生视频都走本地选图', () => {
    expect(needsUpload('i2i')).toBe(true)
    expect(needsUpload('i2v')).toBe(true)
    expect(needsUpload('t2i')).toBe(false)
    expect(needsUpload('t2v')).toBe(false)
  })

  it('图生视频仍可粘贴公网 URL（复用任务 / 已有地址）', () => {
    expect(needsImageURL('i2v')).toBe(true)
    expect(needsImageURL('i2i')).toBe(false)
  })
})

describe('appendRefImages', () => {
  it('在上限内追加，不覆盖已有项', () => {
    expect(appendRefImages(['a'], ['b', 'c'], 4)).toEqual({ items: ['a', 'b', 'c'], overflow: false })
  })

  it('超出上限只收下空位，并标记 overflow', () => {
    expect(appendRefImages(['a', 'b', 'c'], ['d', 'e'], 4)).toEqual({
      items: ['a', 'b', 'c', 'd'],
      overflow: true
    })
  })

  it('已满时不再追加', () => {
    expect(appendRefImages(['a', 'b', 'c', 'd'], ['e'], 4)).toEqual({
      items: ['a', 'b', 'c', 'd'],
      overflow: true
    })
  })
})

describe('uniquePublicImageURLs', () => {
  it('合并去重并丢掉非法地址', () => {
    expect(uniquePublicImageURLs('https://a.com/1.jpg', ['https://a.com/1.jpg', 'https://b.com/2.jpg', '/local.png'])).toEqual([
      'https://a.com/1.jpg',
      'https://b.com/2.jpg'
    ])
  })
})

describe('splitRefImageInput', () => {
  it('按空白和逗号拆出多个公网地址', () => {
    expect(splitRefImageInput('https://a.com/1.jpg, https://b.com/2.jpg\nhttps://c.com/3.jpg')).toEqual([
      'https://a.com/1.jpg',
      'https://b.com/2.jpg',
      'https://c.com/3.jpg'
    ])
  })

  it('非法地址丢掉', () => {
    expect(splitRefImageInput('ftp://x/a.jpg /local.png')).toEqual([])
  })
})

describe('modelsForKind', () => {
  it('按任务类型取对应能力的模型', () => {
    const key = grokKey()
    expect(modelsForKind(key, 't2i')).toHaveLength(2)
    expect(modelsForKind(key, 't2v')).toHaveLength(2)
  })

  it('无 key 时返回空数组', () => {
    expect(modelsForKind(null, 't2i')).toEqual([])
  })

  it('不支持视频的 key 视频模型为空', () => {
    expect(modelsForKind(openaiKey(), 't2v')).toEqual([])
  })
})

describe('billingTierOf', () => {
  it('aspect_ratio 模式按分辨率档判定', () => {
    const key = grokKey()
    const model = selectedModelOf(grokImageForm(), key)!
    expect(billingTierOf(grokImageForm({ imageResolution: '1k' }), model)).toBe('1K')
    expect(billingTierOf(grokImageForm({ imageResolution: '2k' }), model)).toBe('2K')
  })

  it('size 模式按最长边判定，未填时按 2K 兜底（与后端一致）', () => {
    const model = openaiKey().image_models[0]
    const form = { ...emptyMediaForm(), model: 'gpt-image-2', aspectRatio: '', imageResolution: '' }
    expect(billingTierOf({ ...form, size: '2560x1440' }, model)).toBe('4K')
    expect(billingTierOf({ ...form, size: '' }, model)).toBe('2K')
  })
})

describe('estimateTicks', () => {
  it('图片按张数线性', () => {
    const key = grokKey()
    expect(estimateTicks(grokImageForm(), key)).toBe(200_000_000)
    expect(estimateTicks(grokImageForm({ n: 4 }), key)).toBe(800_000_000)
  })

  it('高质量图按分辨率档取价 —— 1K 与 2K 不同价', () => {
    const key = grokKey()
    const form = grokImageForm({ model: 'grok-imagine-image-quality' })
    expect(estimateTicks({ ...form, imageResolution: '1k' }, key)).toBe(500_000_000)
    expect(estimateTicks({ ...form, imageResolution: '2k' }, key)).toBe(700_000_000)
  })

  it('视频按分辨率单价 × 秒数，含 1080p 档', () => {
    const key = grokKey()
    const base = { ...emptyMediaForm(), kind: 't2v' as const, model: 'grok-imagine-video' }
    expect(estimateTicks({ ...base, resolution: '480p', duration: 8 }, key)).toBe(4_000_000_000)
    expect(estimateTicks({ ...base, resolution: '720p', duration: 8 }, key)).toBe(5_600_000_000)
    expect(estimateTicks({ ...base, resolution: '1080p', duration: 8 }, key)).toBe(5_600_000_000)
    expect(estimateTicks({ ...base, resolution: '720p', duration: 15 }, key)).toBe(10_500_000_000)
  })

  it('1.5 的单价按下发的 price_by_tier 取，不与基础版混淆', () => {
    const key = grokKey()
    const base = { ...emptyMediaForm(), kind: 'i2v' as const, model: 'grok-imagine-video-1.5' }
    // 720p $0.14/s × 8s = $1.12
    expect(estimateTicks({ ...base, resolution: '720p', duration: 8 }, key)).toBe(11_200_000_000)
  })

  it('未选模型或未知模型时返回 0', () => {
    expect(estimateTicks(emptyMediaForm(), grokKey())).toBe(0)
    expect(estimateTicks({ ...emptyMediaForm(), model: 'nope' }, grokKey())).toBe(0)
  })

  it('本站查不到标准价的模型无法预估', () => {
    const form = { ...emptyMediaForm(), model: 'gpt-image-2', n: 1, aspectRatio: '', imageResolution: '' }
    expect(estimateTicks(form, openaiKey())).toBe(0)
  })
})

describe('downgradeTargetOf', () => {
  const key = grokKey()

  it('文生视频选 1.5 时提示会被替换 —— 否则用户以为自己在用 1.5', () => {
    const form = { ...emptyMediaForm(), kind: 't2v' as const, model: 'grok-imagine-video-1.5' }
    expect(downgradeTargetOf(form, key)).toBe('grok-imagine-video')
  })

  it('图生视频选 1.5 时不降级，1.5 真生效', () => {
    const form = { ...emptyMediaForm(), kind: 'i2v' as const, model: 'grok-imagine-video-1.5' }
    expect(downgradeTargetOf(form, key)).toBe('')
  })

  it('不会降级的模型返回空串', () => {
    const form = { ...emptyMediaForm(), kind: 't2v' as const, model: 'grok-imagine-video' }
    expect(downgradeTargetOf(form, key)).toBe('')
  })
})

describe('validateMediaForm', () => {
  const key = grokKey()

  it('合法图片表单通过', () => {
    expect(validateMediaForm(grokImageForm(), key)).toBe('')
  })

  it('合法视频表单通过', () => {
    const form = {
      ...emptyMediaForm(),
      kind: 't2v' as const,
      keyId: 1,
      model: 'grok-imagine-video',
      prompt: '海浪',
      aspectRatio: '16:9',
      resolution: '720p',
      duration: 8
    }
    expect(validateMediaForm(form, key)).toBe('')
  })

  it('缺 key / 模型 / 提示词分别报错', () => {
    expect(validateMediaForm(emptyMediaForm(), null)).toBe('media.errors.selectKey')
    expect(validateMediaForm(emptyMediaForm(), key)).toBe('media.errors.selectModel')
    expect(validateMediaForm(grokImageForm({ prompt: '  ' }), key)).toBe('media.errors.emptyPrompt')
  })

  it('提示词超长被拒', () => {
    expect(validateMediaForm(grokImageForm({ prompt: 'a'.repeat(PROMPT_MAX_LEN + 1) }), key)).toBe(
      'media.errors.promptTooLong'
    )
  })

  it('张数越界被拒', () => {
    expect(validateMediaForm(grokImageForm({ n: 0 }), key)).toBe('media.errors.badCount')
    expect(validateMediaForm(grokImageForm({ n: IMAGE_MAX_N + 1 }), key)).toBe('media.errors.badCount')
  })

  it('宽高比与分辨率档对着模型下发的清单校验，不用前端硬编码的清单', () => {
    // 21:9 曾在前端的硬编码清单里，但上游根本不认
    expect(validateMediaForm(grokImageForm({ aspectRatio: '21:9' }), key)).toBe(
      'media.errors.badAspectRatio'
    )
    expect(validateMediaForm(grokImageForm({ imageResolution: '4k' }), key)).toBe(
      'media.errors.badResolution'
    )
    // 下发清单里的取值全部放行，包括 auto 与小数比例
    for (const ratio of ['auto', '19.5:9', '20:9']) {
      expect(validateMediaForm(grokImageForm({ aspectRatio: ratio }), key)).toBe('')
    }
  })

  it('1080p 现在合法（上游为 1.5 保留了该档单价），越界时长仍被拒', () => {
    const base = {
      ...emptyMediaForm(),
      kind: 't2v' as const,
      model: 'grok-imagine-video',
      prompt: 'x',
      aspectRatio: '16:9',
      resolution: '720p',
      duration: 8
    }
    expect(validateMediaForm({ ...base, resolution: '1080p' }, key)).toBe('')
    expect(validateMediaForm({ ...base, resolution: '360p' }, key)).toBe('media.errors.badResolution')
    expect(validateMediaForm({ ...base, duration: 0 }, key)).toBe('media.errors.badDuration')
    expect(validateMediaForm({ ...base, duration: 16 }, key)).toBe('media.errors.badDuration')
  })

  it('size 模式的模型校验尺寸格式', () => {
    const oai = openaiKey()
    const base = {
      ...emptyMediaForm(),
      keyId: 2,
      model: 'gpt-image-2',
      prompt: 'x',
      n: 1,
      aspectRatio: '',
      imageResolution: ''
    }
    expect(validateMediaForm({ ...base, size: '2048x1152' }, oai)).toBe('')
    expect(validateMediaForm({ ...base, size: '4K' }, oai)).toBe('media.errors.badSize')
  })

  it('图生视频：空 URL 留给选图，非法 URL 拒绝，公网 URL 通过', () => {
    const base = {
      ...emptyMediaForm(),
      kind: 'i2v' as const,
      model: 'grok-imagine-video',
      prompt: 'x',
      aspectRatio: '16:9',
      resolution: '480p',
      duration: 8
    }
    expect(validateMediaForm(base, key)).toBe('')
    expect(validateMediaForm({ ...base, imageURL: '/local.jpg' }, key)).toBe('media.errors.badImageURL')
    expect(validateMediaForm({ ...base, imageURL: 'https://x.com/a.jpg' }, key)).toBe('')
  })
})

describe('resetMediaFormForKind', () => {
  it('keeps the prompt and selected key while restoring generation defaults', () => {
    const current = {
      ...emptyMediaForm(),
      kind: 'i2v' as const,
      keyId: 42,
      model: 'old-model',
      prompt: '保留这段提示词',
      n: 4,
      size: '3840x2160',
      aspectRatio: '9:16',
      imageResolution: '2k',
      quality: 'low',
      resolution: '1080p',
      duration: 15,
      imageURL: 'https://example.com/image.jpg',
      stream: true
    }

    expect(resetMediaFormForKind(current, 't2v')).toEqual({
      ...emptyMediaForm(),
      keyId: 42,
      kind: 't2v',
      prompt: '保留这段提示词'
    })
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
