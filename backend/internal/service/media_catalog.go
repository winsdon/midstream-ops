package service

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"sub2api-account-monitor/internal/repository"
)

// 生成能力。sub2api 没有任何 model 能力分类字段（既无 modality 也无 model_type），
// 只能按模型名判定。
const (
	MediaCapImage = "image"
	MediaCapVideo = "video"
)

// 任务类型（与 repository.MediaKind* 对应，此处重复定义是为了让 service 层
// 的参数校验不依赖 repository 包的常量——两层的枚举语义相同但职责不同）。
const (
	MediaKindText2Image  = "t2i"
	MediaKindImage2Image = "i2i"
	MediaKindText2Video  = "t2v"
	MediaKindImage2Video = "i2v"
)

// tick 是上游费用单位：cost_in_usd_ticks，1 tick = 1e-10 USD。
// 交叉验证过两个数据点：图片 200000000 ticks = $0.02；
// 720p×8s 视频 5600000000 ticks = $0.56 = 8 × $0.07。
const ticksPerUSD = 10_000_000_000

// usdToTicks 把美元价格换算成 ticks。
//
// 用 +0.5 四舍五入而非直接截断：float64 表示 0.07 时实际是 0.06999...，
// 直接 int64() 会得到 699999999 而不是 700000000，展示成 $0.0699。
func usdToTicks(usd float64) int64 {
	if usd <= 0 {
		return 0
	}
	return int64(usd*float64(ticksPerUSD) + 0.5)
}

// MediaSizeMode 该模型用哪一套尺寸参数。
//
// 【这不是「支不支持尺寸」的布尔，而是两条互斥的上游路径】早先的实现把它建模成
// SupportsSize bool，据此对 Grok 模型禁用一切尺寸控制，结论是「Grok 恒出 1024×1024」。
// 那个结论是错的：sub2api 网关的 sanitizeGrokMediaForwardBody 会在转发前主动
// 删掉 size 字段，所以传 size 当然没用；Grok 认的是 xAI 原生的 aspect_ratio +
// resolution，网关对这两个字段原样透传。
//
// 两条路径的参数**绝不能同时出现在一次请求里**：body 里只要带 size，
// 网关就走删除分支，行为不可预期。
type MediaSizeMode string

const (
	// SizeModeAspectRatio Grok 图片：aspect_ratio（14 档）+ resolution（1k/2k）。
	SizeModeAspectRatio MediaSizeMode = "aspect_ratio"
	// SizeModePixelSize OpenAI 格式图片：size 为真实 "宽x高"。
	SizeModePixelSize MediaSizeMode = "size"
)

// grokAspectRatios xAI 支持的宽高比档位。
//
// "auto" 让模型自行按提示词推断，是上游的合法取值，不是本站杜撰的占位符。
var grokAspectRatios = []string{
	"1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3",
	"2:1", "1:2", "19.5:9", "9:19.5", "20:9", "9:20", "auto",
}

// grokImageResolutions Grok 图片的分辨率档位（同时决定计费档）。
var grokImageResolutions = []string{"1k", "2k"}

// videoResolutions 视频分辨率档位。
//
// 【1080p 为何在列】早先版本只放 480p/720p，依据是「1080p 上游明确返回 400」——
// 那是对 grok-imagine-video 的观察。sub2api 的计费表为 grok-imagine-video-1.5
// 保留了独立的 1080p 单价（$0.25/s），说明该组合是上游认可的。若某个模型确实
// 不支持，上游返回的是**参数错误 400、不扣费**，如实透出即可，代价远小于
// 把一个真能用的档位永久藏起来。
var videoResolutions = []string{"480p", "720p", "1080p"}

// 图片计费档位。与 sub2api 的 ImageBillingSize* 同名同义。
const (
	ImageTier1K = "1K"
	ImageTier2K = "2K"
	ImageTier4K = "4K"
)

// mediaModelSpec 一个可用生成模型的规格。
//
// 【为什么用显式 allowlist 而不是前缀通配】/v1/models 会返回 grok-imagine-edit
// 这类「看起来能用」但实际 404 的模型。前缀匹配（"grok-imagine-" → 图片）会把它
// 放进下拉框，用户选中后只能收到报错。宁可漏掉将来的新模型（手工登记即可），
// 也不能让用户对着一个必然失败的选项反复试。这是刻意的「不 DRY」。
type mediaModelSpec struct {
	Capability string
	// SizeMode 仅图片模型有意义。
	SizeMode MediaSizeMode
	// ImagePriceUSD 各计费档的标准单价（USD/张）。nil 表示本站无权威价，
	// 只能依赖分组配置（gpt-image-* 属于此类，其价格由 LiteLLM 表决定，
	// 那份表只存在于 sub2api 进程内，本站的只读 PG 里查不到）。
	ImagePriceUSD map[string]float64
	// VideoPriceUSD 各分辨率的标准单价（USD/秒）。
	VideoPriceUSD map[string]float64
	// DowngradesTo 上游会把本模型静默替换成的模型名（空表示不降级）。
	//
	// 见 sub2api NormalizeGrokMediaModelForEndpoint：grok-imagine-video-1.5
	// 在**无参考图**时被换成 grok-imagine-video，并按后者计费。降级发生在
	// 网关内部，响应里没有任何提示——费用预估必须主动按降级后的模型算，
	// 否则页面报价会高于实扣。
	DowngradesTo string
	// DowngradeKinds 触发降级的任务类型。空表示 DowngradesTo 对所有类型生效。
	DowngradeKinds []string
}

// mediaModels 是全部受支持的生成模型。
//
// 图片 / 视频价格与 sub2api backend/internal/service/billing_service.go 的
// defaultGrokImagine* 常量逐项对齐——那是分组未配置自定义单价时的兜底价。
// 任何一处对不上，页面报价就会与账单对不上。
var mediaModels = map[string]mediaModelSpec{
	// Grok 图片。三档价按 sub2api getGrokImagineImageTierPrice：4K 与 2K 同价。
	"grok-imagine-image": {
		Capability: MediaCapImage, SizeMode: SizeModeAspectRatio,
		ImagePriceUSD: map[string]float64{ImageTier1K: 0.02, ImageTier2K: 0.02, ImageTier4K: 0.02},
	},
	"grok-imagine-image-quality": {
		Capability: MediaCapImage, SizeMode: SizeModeAspectRatio,
		ImagePriceUSD: map[string]float64{ImageTier1K: 0.05, ImageTier2K: 0.07, ImageTier4K: 0.07},
	},
	// grok-imagine 走图片端点时被 sub2api 映射成 grok-imagine-image-quality
	// 并按后者计费。早先它被当作陷阱模型排除，依据是「视频接口 404」——
	// 那个结论只对视频端点成立，图片端点是通的。
	"grok-imagine": {
		Capability: MediaCapImage, SizeMode: SizeModeAspectRatio,
		ImagePriceUSD: map[string]float64{ImageTier1K: 0.05, ImageTier2K: 0.07, ImageTier4K: 0.07},
		DowngradesTo:  "grok-imagine-image-quality",
	},
	// grok-imagine-edit 刻意不登记：图片编辑端点实测 404。
	//
	// Grok 视频。
	"grok-imagine-video": {
		Capability:    MediaCapVideo,
		VideoPriceUSD: map[string]float64{"480p": 0.05, "720p": 0.07, "1080p": 0.07},
	},
	"grok-imagine-video-1.5": {
		Capability:    MediaCapVideo,
		VideoPriceUSD: map[string]float64{"480p": 0.08, "720p": 0.14, "1080p": 0.25},
		// 只在文生视频时降级：有参考图时 1.5 真正生效并按 1.5 的价计费。
		DowngradesTo:   "grok-imagine-video",
		DowngradeKinds: []string{MediaKindText2Video},
	},
	// OpenAI 格式图片：size 为真实 WxH，按最长边分档计费。
	// 标准价由 sub2api 内嵌的 LiteLLM 表决定，本站查不到，故 ImagePriceUSD 为 nil：
	// 只有分组配了 image_price_* 时才能给出预估。
	"gpt-image-1":   {Capability: MediaCapImage, SizeMode: SizeModePixelSize},
	"gpt-image-1.5": {Capability: MediaCapImage, SizeMode: SizeModePixelSize},
	"gpt-image-2":   {Capability: MediaCapImage, SizeMode: SizeModePixelSize},
	// Gemini 图片模型走 /v1beta 原生协议，与 /v1/images/generations 不兼容，
	// 故不纳入——列在这里只会让用户选中后收到 404。
}

// MediaModelOption 提供给前端的一个模型选项。
//
// 单价字段下发的是**已折算的最终价**（含分组自定义单价与倍率），前端只做
// 「单价 × 数量」的乘法。定价的全部复杂度收敛在后端，前端不需要知道倍率的存在。
type MediaModelOption struct {
	Name       string `json:"name"`
	Capability string `json:"capability"`
	// SizeMode 决定前端渲染宽高比按钮还是尺寸下拉。
	SizeMode MediaSizeMode `json:"size_mode"`
	// AspectRatios / Resolutions 仅 aspect_ratio 模式有值。
	AspectRatios []string `json:"aspect_ratios,omitempty"`
	Resolutions  []string `json:"resolutions,omitempty"`
	// UnitPriceTicks 图片为每张价格（按 PriceTierKey 对应的档），视频为每秒价格。
	// 0 表示无法预估。
	UnitPriceTicks int64 `json:"unit_price_ticks"`
	// PriceByTier 图片各档单价 / 视频各分辨率单价，供前端在用户切换档位时
	// 即时更新报价而无需再请求一次。
	PriceByTier map[string]int64 `json:"price_by_tier,omitempty"`
	// DowngradesTo 上游会静默替换成的模型（空表示不降级）。
	// 前端据此提示用户「你选的不是你得到的」。
	DowngradesTo string `json:"downgrades_to,omitempty"`
	// DowngradeKinds 触发降级的任务类型；空表示全部类型。
	DowngradeKinds []string `json:"downgrade_kinds,omitempty"`
}

// ClassifyModels 把分组可用模型列表筛成生图 / 生视频选项。
//
// platform 决定视频能力：sub2api 的视频接口只在 grok / composite 平台放行，
// 其余平台直接返回 404（见 sub2api routes/gateway.go 的 videoGenerationHandler）。
// 注意这里没有 allow_video_generation 那样的布尔开关——groups 表根本没有这一列。
//
// allowImage 对应 groups.allow_image_generation：图片能力是显式布尔门禁，
// 关闭时上游返回 403 permission_error。
//
// models 为空表示该分组下账号都没配 model_mapping，回落平台默认模型表
// （与 sub2api /v1/models 的兜底分支同口径）。
//
// pricing 为该分组的计费参数，nil 时退化到标准价（不含倍率）。
func ClassifyModels(platform string, allowImage bool, models []string,
	pricing *repository.MediaPricing) []MediaModelOption {
	if len(models) == 0 {
		models = DefaultModelsForPlatform(platform)
	}
	videoAllowed := platform == "grok" || platform == "composite"

	out := make([]MediaModelOption, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, name := range models {
		spec, ok := mediaModels[name]
		if !ok {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		if spec.Capability == MediaCapImage && !allowImage {
			continue
		}
		if spec.Capability == MediaCapVideo && !videoAllowed {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, buildModelOption(name, spec, pricing))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// buildModelOption 组装单个模型选项，含折算后的各档单价。
func buildModelOption(name string, spec mediaModelSpec, pricing *repository.MediaPricing) MediaModelOption {
	opt := MediaModelOption{
		Name:           name,
		Capability:     spec.Capability,
		SizeMode:       spec.SizeMode,
		DowngradesTo:   spec.DowngradesTo,
		DowngradeKinds: spec.DowngradeKinds,
	}
	opt.PriceByTier = make(map[string]int64)

	switch spec.Capability {
	case MediaCapImage:
		if spec.SizeMode == SizeModeAspectRatio {
			opt.AspectRatios = append([]string(nil), grokAspectRatios...)
			opt.Resolutions = append([]string(nil), grokImageResolutions...)
		}
		for _, tier := range []string{ImageTier1K, ImageTier2K, ImageTier4K} {
			opt.PriceByTier[tier] = imageUnitPriceTicks(name, tier, pricing)
		}
		// 展示基准取 1K：aspect_ratio 模式默认就是 1k 档，
		// size 模式的默认预设 1024x576 也落在 1K。
		opt.UnitPriceTicks = opt.PriceByTier[ImageTier1K]
	case MediaCapVideo:
		opt.Resolutions = append([]string(nil), videoResolutions...)
		for _, res := range videoResolutions {
			opt.PriceByTier[res] = videoUnitPriceTicks(name, res, pricing)
		}
		opt.UnitPriceTicks = opt.PriceByTier["480p"]
	}
	if len(opt.PriceByTier) == 0 {
		opt.PriceByTier = nil
	}
	return opt
}

// imageUnitPriceTicks 计算一张图的最终单价（ticks）。
//
// 口径与 sub2api 的 CalculateImageCost 严格对齐：
//
//	单价 = 分组自定义档位价（若非 NULL）: 模型标准档位价
//	最终 = 单价 × 图片倍率
//
// 任一环节缺失（既无分组价又无标准价）返回 0，前端展示「以账单为准」。
func imageUnitPriceTicks(model, tier string, pricing *repository.MediaPricing) int64 {
	usd, ok := 0.0, false
	if pricing != nil {
		if p := pricing.ImagePrice(tier); p != nil {
			usd, ok = *p, true
		}
	}
	if !ok {
		if spec, exists := mediaModels[model]; exists && spec.ImagePriceUSD != nil {
			usd, ok = spec.ImagePriceUSD[tier], true
		}
	}
	if !ok {
		return 0
	}
	return usdToTicks(usd * mediaMultiplier(pricing, MediaCapImage))
}

// videoUnitPriceTicks 计算一秒视频的最终单价（ticks），口径同上。
func videoUnitPriceTicks(model, resolution string, pricing *repository.MediaPricing) int64 {
	usd, ok := 0.0, false
	if pricing != nil {
		if p := pricing.VideoPrice(resolution); p != nil {
			usd, ok = *p, true
		}
	}
	if !ok {
		if spec, exists := mediaModels[model]; exists && spec.VideoPriceUSD != nil {
			if v, has := spec.VideoPriceUSD[resolution]; has {
				usd, ok = v, true
			}
		}
	}
	if !ok {
		return 0
	}
	return usdToTicks(usd * mediaMultiplier(pricing, MediaCapVideo))
}

// mediaMultiplier 取图片 / 视频计费倍率。
//
// 【不叠高峰倍率】sub2api 的 computePeakAwareMultipliers 只对 token 计费乘
// PeakMultiplierAt，图片走 resolveImageRateMultiplier 独立分支、不叠峰。
// 这里跟着不叠——多乘一次会让报价凭空变高。
func mediaMultiplier(pricing *repository.MediaPricing, capability string) float64 {
	if pricing == nil {
		return 1
	}
	if capability == MediaCapVideo {
		return pricing.EffectiveVideoMultiplier()
	}
	return pricing.EffectiveImageMultiplier()
}

// MediaCapabilityOf 返回模型的能力，未登记的模型返回空串。
func MediaCapabilityOf(model string) string {
	return mediaModels[model].Capability
}

// MediaSizeModeOf 返回模型的尺寸参数模式，未登记的模型返回空串。
func MediaSizeModeOf(model string) MediaSizeMode {
	return mediaModels[model].SizeMode
}

// effectiveModel 返回上游实际会使用的模型名（考虑静默降级）。
//
// 这是费用预估的关键：用户选了 grok-imagine-video-1.5 做文生视频，
// 上游用的是 grok-imagine-video 并按其价计费。报价必须用后者。
func effectiveModel(model, kind string) string {
	spec, ok := mediaModels[model]
	if !ok || spec.DowngradesTo == "" {
		return model
	}
	if len(spec.DowngradeKinds) == 0 {
		return spec.DowngradesTo
	}
	for _, k := range spec.DowngradeKinds {
		if k == kind {
			return spec.DowngradesTo
		}
	}
	return model
}

// MediaGenerateParams 一次生成请求的参数（已归一化）。
type MediaGenerateParams struct {
	Kind   string
	Model  string
	Prompt string
	N      int // 图片张数
	// Size 真实 "宽x高"，仅 SizeModePixelSize 的模型有效。
	Size string
	// AspectRatio 宽高比，仅 SizeModeAspectRatio 的模型有效（图片）；
	// 视频模型对所有实现都接受该字段。
	AspectRatio string
	// ImageResolution Grok 图片的分辨率档（1k / 2k），仅 SizeModeAspectRatio 有效。
	//
	// 与视频的 Resolution 分成两个字段而不是复用一个：两者取值域完全不同
	// （1k/2k vs 480p/720p/1080p），复用会让「视频分辨率填了 1k」这类错误
	// 直到打上游才暴露。
	ImageResolution string
	Quality         string // low | medium | high，仅 OpenAI 格式
	Resolution      string // 480p | 720p | 1080p，仅视频
	Duration        int    // 1..15 秒
	ImageURL        string // 图生视频的参考图，须公网可达
	Stream          bool   // 是否请求流式预览（由上游支持时透传）
}

// 参数边界。均来自上游的硬性约束，越界上游会 400/422。
const (
	mediaMaxPromptLen = 2000
	mediaMinDuration  = 1
	mediaMaxDuration  = 15
	mediaMaxImageN    = 4
)

// ValidateGenerateParams 在打上游之前做本地校验。
//
// 【为什么本地先校验】上游的参数校验 400 不扣费，本可以放任它去拦。但视频任务
// 一旦提交成功就扣费且不退款，把能在本地拦下的错误提前拦掉，能避免用户因一个
// 拼错的 resolution 而反复试错。同时本地校验能给出中文可读原因，上游只给英文串。
func ValidateGenerateParams(p MediaGenerateParams) error {
	spec, ok := mediaModels[p.Model]
	if !ok {
		return fmt.Errorf("模型 %s 不在支持列表内", p.Model)
	}
	prompt := strings.TrimSpace(p.Prompt)
	if prompt == "" {
		return fmt.Errorf("提示词不能为空")
	}
	if len(prompt) > mediaMaxPromptLen {
		return fmt.Errorf("提示词超过 %d 字符", mediaMaxPromptLen)
	}

	switch p.Kind {
	case MediaKindText2Image, MediaKindImage2Image:
		if spec.Capability != MediaCapImage {
			return fmt.Errorf("模型 %s 不支持图片生成", p.Model)
		}
		if p.N < 1 || p.N > mediaMaxImageN {
			return fmt.Errorf("生成张数须在 1-%d 之间", mediaMaxImageN)
		}
		return validateImageSizeParams(spec, p)
	case MediaKindText2Video, MediaKindImage2Video:
		if spec.Capability != MediaCapVideo {
			return fmt.Errorf("模型 %s 不支持视频生成", p.Model)
		}
		if !containsString(videoResolutions, p.Resolution) {
			return fmt.Errorf("分辨率仅支持 %s", strings.Join(videoResolutions, " / "))
		}
		if p.Duration < mediaMinDuration || p.Duration > mediaMaxDuration {
			return fmt.Errorf("时长须在 %d-%d 秒之间", mediaMinDuration, mediaMaxDuration)
		}
		if p.AspectRatio != "" && !containsString(grokAspectRatios, p.AspectRatio) {
			return fmt.Errorf("宽高比 %s 不在支持列表内", p.AspectRatio)
		}
		if p.Kind == MediaKindImage2Video && !isPublicHTTPURL(p.ImageURL) {
			return fmt.Errorf("参考图须为公网可访问的 http(s) 地址")
		}
		return nil
	default:
		return fmt.Errorf("未知的任务类型 %s", p.Kind)
	}
}

// validateImageSizeParams 按尺寸模式校验，两套参数互斥。
//
// 【互斥必须在本地拦】body 里同时带 size 与 aspect_ratio 时，sub2api 网关会走
// 「删除 size」分支，实际生效的是哪一个取决于上游实现细节。与其让用户对着一个
// 行为不确定的组合调参，不如在这里直接拒绝并说清该用哪个。
func validateImageSizeParams(spec mediaModelSpec, p MediaGenerateParams) error {
	switch spec.SizeMode {
	case SizeModeAspectRatio:
		if p.Size != "" {
			return fmt.Errorf("模型 %s 用宽高比控制画面比例，不接受像素尺寸参数", p.Model)
		}
		if p.AspectRatio != "" && !containsString(grokAspectRatios, p.AspectRatio) {
			return fmt.Errorf("宽高比 %s 不在支持列表内", p.AspectRatio)
		}
		if p.ImageResolution != "" && !containsString(grokImageResolutions, p.ImageResolution) {
			return fmt.Errorf("分辨率仅支持 %s", strings.Join(grokImageResolutions, " / "))
		}
	case SizeModePixelSize:
		if p.AspectRatio != "" {
			return fmt.Errorf("模型 %s 用像素尺寸控制画面比例，不接受宽高比参数", p.Model)
		}
		if p.ImageResolution != "" {
			return fmt.Errorf("模型 %s 不接受分辨率档位参数", p.Model)
		}
		if p.Size != "" {
			if _, _, err := parseImageSize(p.Size); err != nil {
				return err
			}
		}
	}
	return nil
}

// EstimateCostTicks 提交前的费用预估。
//
// 这是给用户看的「大约会扣多少」，口径与 sub2api 的 CalculateImageCost /
// CalculateVideoCost 对齐：单价（分组自定义优先）× 数量 × 倍率。返回 0 表示
// 无法预估（本站既无标准价、分组也没配）。
//
// 之所以必须有这个函数：视频提交即扣费且不退款，720p×15s 是 $1.05，
// 用户有权在按下按钮之前知道这个数。
func EstimateCostTicks(p MediaGenerateParams, pricing *repository.MediaPricing) int64 {
	spec, ok := mediaModels[p.Model]
	if !ok {
		return 0
	}
	// 按上游实际使用的模型算价：1.5 在文生视频时被降级，报价要跟着降。
	billed := effectiveModel(p.Model, p.Kind)

	switch spec.Capability {
	case MediaCapImage:
		n := p.N
		if n < 1 {
			n = 1
		}
		return imageUnitPriceTicks(billed, billingTierOf(spec, p), pricing) * int64(n)
	case MediaCapVideo:
		d := p.Duration
		if d < 1 {
			d = 8 // 上游默认时长
		}
		res := p.Resolution
		if res == "" {
			res = "480p"
		}
		return videoUnitPriceTicks(billed, res, pricing) * int64(d)
	}
	return 0
}

// billingTierOf 推断本次请求会落在哪个图片计费档。
//
// 【aspect_ratio 模式下的口径】sub2api 优先按响应里各图的实际输出尺寸定档
// （ResolveImageBillingSize 的 output 分支），我们请求 1k 就得到 ~1024 长边、
// 请求 2k 得到 ~2048 长边，故直接用请求的档位即可对齐。
//
// 需要留意的边界：若上游响应未携带每图 size，sub2api 会回落到「输入 size」，
// 而 Grok 请求里根本没有 size，最终落到默认 2K。这种情况下 1k 档的实扣会比
// 本预估略高（image-quality 差 $0.02/张）。列表里展示的 cost_usd 取的是上游
// 实扣，二者不一致时以实扣为准。
func billingTierOf(spec mediaModelSpec, p MediaGenerateParams) string {
	if spec.SizeMode == SizeModeAspectRatio {
		switch strings.ToLower(strings.TrimSpace(p.ImageResolution)) {
		case "2k":
			return ImageTier2K
		default:
			return ImageTier1K
		}
	}
	if p.Size == "" {
		return ImageTier2K // 与 sub2api NormalizeImageBillingTierOrDefault 的兜底一致
	}
	tier, err := ImageSizeTier(p.Size)
	if err != nil {
		return ImageTier2K
	}
	return tier
}

// FormatTicksUSD 把 ticks 格式化成美元字符串（4 位小数，够表达 $0.0001 级差异）。
func FormatTicksUSD(ticks int64) string {
	return strconv.FormatFloat(float64(ticks)/float64(ticksPerUSD), 'f', 4, 64)
}

// ImageSizeTier 返回图片尺寸的计费档位（1K / 2K / 4K）。
//
// 【按最长边判定，不是按面积也不是按较短边】上游的口径是：最长边 ≤1024 算 1K，
// ≤2048 算 2K，>2048 算 4K。这意味着 2560×1440 会按 4K 计费而不是 2K——
// 这是最容易让用户意外超支的一条规则，前端必须实时显示档位。
func ImageSizeTier(size string) (string, error) {
	w, h, err := parseImageSize(size)
	if err != nil {
		return "", err
	}
	longest := w
	if h > longest {
		longest = h
	}
	switch {
	case longest <= 1024:
		return ImageTier1K, nil
	case longest <= 2048:
		return ImageTier2K, nil
	default:
		return ImageTier4K, nil
	}
}

// parseImageSize 解析 "3840x2160" 形式的尺寸。
//
// 刻意不接受 "4K" / "2K" 这类档位字符串：上游对它们返回 400 Invalid image size。
// 在本地就拒绝，能给出比上游更明确的原因。
func parseImageSize(size string) (int, int, error) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(size)), "x")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("尺寸格式须为「宽x高」，如 1024x1024")
	}
	w, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || w <= 0 {
		return 0, 0, fmt.Errorf("尺寸格式须为「宽x高」，如 1024x1024")
	}
	h, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || h <= 0 {
		return 0, 0, fmt.Errorf("尺寸格式须为「宽x高」，如 1024x1024")
	}
	return w, h, nil
}

// containsString 判断字符串是否在候选列表内。
func containsString(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

// isPublicHTTPURL 判断参考图地址是否为可接受的公网 http(s) 地址。
//
// 只做形态校验，不做 DNS 解析或内网段判断——这个 URL 是交给上游去取的，
// 本站不会主动请求它，因此不构成本站的 SSRF 面。
func isPublicHTTPURL(raw string) bool {
	u := strings.TrimSpace(strings.ToLower(raw))
	return strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")
}
