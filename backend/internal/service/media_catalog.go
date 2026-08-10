package service

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
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

// mediaModelSpec 一个可用生成模型的规格。
//
// 【为什么用显式 allowlist 而不是前缀通配】/v1/models 会返回三个「看起来能用」
// 但实际有害的 grok-imagine-* 模型：
//   - grok-imagine       视频接口 404
//   - grok-imagine-edit  图片编辑接口 404
//   - grok-imagine-video-1.5  请求成功但被静默降级为 grok-imagine-video，
//     并按后者计费——用户以为用了 1.5，实际没有，且钱照扣
//
// 前缀匹配（"grok-imagine-" → 图片）会把这三个全放进下拉框。宁可漏掉将来的
// 新模型（手工登记即可），也不能让用户白花钱。这是刻意的「不 DRY」。
type mediaModelSpec struct {
	Capability string
	// PricePerImageTicks 单张图片价格（仅 image 能力）。0 表示按分组定价，未知。
	PricePerImageTicks int64
	// SupportsSize 该模型是否真的接受 size 参数。
	//
	// Grok 图片模型会「接受」size 字段但静默忽略，输出恒为 1024×1024，
	// 计费也严格按张数——前端必须据此禁用尺寸选择器并说明，
	// 否则用户会以为选了 4K 却拿到 1K 图。
	SupportsSize bool
}

// mediaModels 是全部受支持的生成模型。
//
// 图片价格来自 kaola-doc/docs/guide/grok-media.md 的计费表（平台标准价，
// 实扣还要乘分组倍率）。OpenAI 格式图片模型按分辨率档计费、价格由分组配置
// 决定，本站查不到权威值，故留 0 并在前端标注「以账单为准」。
var mediaModels = map[string]mediaModelSpec{
	// Grok 图片：固定 1024×1024，size 参数被静默忽略
	"grok-imagine-image":         {Capability: MediaCapImage, PricePerImageTicks: 200_000_000, SupportsSize: false},
	"grok-imagine-image-quality": {Capability: MediaCapImage, PricePerImageTicks: 700_000_000, SupportsSize: false},
	// Grok 视频：唯一可用的视频模型
	"grok-imagine-video": {Capability: MediaCapVideo},
	// OpenAI 格式图片：size 为真实 WxH，按最长边分档计费
	"gpt-image-1":   {Capability: MediaCapImage, SupportsSize: true},
	"gpt-image-1.5": {Capability: MediaCapImage, SupportsSize: true},
	"gpt-image-2":   {Capability: MediaCapImage, SupportsSize: true},
	// Gemini 图片模型走 /v1beta 原生协议，与 /v1/images/generations 不兼容，
	// 故一期不纳入——列在这里只会让用户选中后收到 404。
}

// 视频单价（USD/秒 → ticks/秒）。仅 480p 与 720p 可用；
// 1080p 上游明确返回 400，其余取值返回 422。
var videoPricePerSecondTicks = map[string]int64{
	"480p": 500_000_000, // $0.05/s
	"720p": 700_000_000, // $0.07/s
}

// MediaModelOption 提供给前端的一个模型选项。
type MediaModelOption struct {
	Name         string `json:"name"`
	Capability   string `json:"capability"`
	SupportsSize bool   `json:"supports_size"`
	// UnitPriceTicks 图片为每张价格，视频为每秒价格。0 表示未知（按分组定价）。
	UnitPriceTicks int64 `json:"unit_price_ticks"`
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
func ClassifyModels(platform string, allowImage bool, models []string) []MediaModelOption {
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

		opt := MediaModelOption{
			Name:           name,
			Capability:     spec.Capability,
			SupportsSize:   spec.SupportsSize,
			UnitPriceTicks: spec.PricePerImageTicks,
		}
		if spec.Capability == MediaCapVideo {
			// 视频单价取 480p 作为展示基准，实际按所选分辨率计算
			opt.UnitPriceTicks = videoPricePerSecondTicks["480p"]
		}
		out = append(out, opt)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// MediaCapabilityOf 返回模型的能力，未登记的模型返回空串。
func MediaCapabilityOf(model string) string {
	return mediaModels[model].Capability
}

// MediaGenerateParams 一次生成请求的参数（已归一化）。
type MediaGenerateParams struct {
	Kind       string
	Model      string
	Prompt     string
	N          int    // 图片张数
	Size       string // 真实 WxH，仅 SupportsSize 的模型有效
	Quality    string // low | medium | high
	Resolution string // 480p | 720p
	Duration   int    // 1..15 秒
	ImageURL   string // 图生视频的参考图，须公网可达
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
		if p.Size != "" {
			if !spec.SupportsSize {
				return fmt.Errorf("模型 %s 固定输出 1024×1024，不接受尺寸参数", p.Model)
			}
			if _, _, err := parseImageSize(p.Size); err != nil {
				return err
			}
		}
	case MediaKindText2Video, MediaKindImage2Video:
		if spec.Capability != MediaCapVideo {
			return fmt.Errorf("模型 %s 不支持视频生成", p.Model)
		}
		if _, ok := videoPricePerSecondTicks[p.Resolution]; !ok {
			return fmt.Errorf("分辨率仅支持 480p 与 720p")
		}
		if p.Duration < mediaMinDuration || p.Duration > mediaMaxDuration {
			return fmt.Errorf("时长须在 %d-%d 秒之间", mediaMinDuration, mediaMaxDuration)
		}
		if p.Kind == MediaKindImage2Video && !isPublicHTTPURL(p.ImageURL) {
			return fmt.Errorf("参考图须为公网可访问的 http(s) 地址")
		}
	default:
		return fmt.Errorf("未知的任务类型 %s", p.Kind)
	}
	return nil
}

// EstimateCostTicks 提交前的费用预估。
//
// 这是给用户看的「大约会扣多少」，实扣以上游响应的 cost_in_usd_ticks 为准
// （还要乘分组倍率，本站拿不到权威倍率）。返回 0 表示无法预估。
//
// 之所以必须有这个函数：视频提交即扣费且不退款，720p×15s 是 $1.05，
// 用户有权在按下按钮之前知道这个数。
func EstimateCostTicks(p MediaGenerateParams) int64 {
	spec, ok := mediaModels[p.Model]
	if !ok {
		return 0
	}
	switch spec.Capability {
	case MediaCapImage:
		if spec.PricePerImageTicks == 0 {
			return 0 // OpenAI 格式按分组定价，本站无权威价
		}
		n := p.N
		if n < 1 {
			n = 1
		}
		return spec.PricePerImageTicks * int64(n)
	case MediaCapVideo:
		perSec, ok := videoPricePerSecondTicks[p.Resolution]
		if !ok {
			return 0
		}
		d := p.Duration
		if d < 1 {
			d = 8 // 上游默认时长
		}
		return perSec * int64(d)
	}
	return 0
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
		return "1K", nil
	case longest <= 2048:
		return "2K", nil
	default:
		return "4K", nil
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

// isPublicHTTPURL 判断参考图地址是否为可接受的公网 http(s) 地址。
//
// 只做形态校验，不做 DNS 解析或内网段判断——这个 URL 是交给上游去取的，
// 本站不会主动请求它，因此不构成本站的 SSRF 面。
func isPublicHTTPURL(raw string) bool {
	u := strings.TrimSpace(strings.ToLower(raw))
	return strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")
}
