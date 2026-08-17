package service

import (
	"testing"

	"sub2api-account-monitor/internal/repository"
)

// f 取地址辅助：MediaPricing 的价格字段是指针（nil 表示未配置，0 是合法价）。
func f(v float64) *float64 { return &v }

// grok-imagine-edit 是唯一仍被过滤的陷阱模型：图片编辑端点实测 404。
//
// grok-imagine 与 grok-imagine-video-1.5 曾一并被过滤，那是错的：
// 前者走图片端点是通的（上游映射成 image-quality），后者在图生视频时真生效。
// 它们现在带着 DowngradesTo 元信息放行，由预估逻辑按降级后的模型报价。
func TestClassifyModelsFiltersTrapModels(t *testing.T) {
	all := []string{
		"grok-imagine",               // 图片端点可用，映射成 image-quality
		"grok-imagine-edit",          // 编辑接口 404，必须过滤
		"grok-imagine-video-1.5",     // 图生视频真生效，文生视频降级
		"grok-imagine-image",         // 可用
		"grok-imagine-image-quality", // 可用
		"grok-imagine-video",         // 可用
		"grok-4.5",                   // 文本模型，不是生成模型
	}
	got := ClassifyModels("grok", true, all, nil)

	names := make(map[string]MediaModelOption, len(got))
	for _, opt := range got {
		names[opt.Name] = opt
	}
	for _, want := range []string{
		"grok-imagine", "grok-imagine-image", "grok-imagine-image-quality",
		"grok-imagine-video", "grok-imagine-video-1.5",
	} {
		if _, ok := names[want]; !ok {
			t.Fatalf("可用模型 %s 被误过滤", want)
		}
	}
	for _, bad := range []string{"grok-imagine-edit", "grok-4.5"} {
		if _, ok := names[bad]; ok {
			t.Fatalf("陷阱模型 %s 未被过滤", bad)
		}
	}
}

// 视频能力由 platform 判定，不是布尔开关：groups 表没有 allow_video_generation 列，
// sub2api 对非 grok/composite 平台的视频接口直接返回 404。
func TestClassifyModelsVideoGatedByPlatform(t *testing.T) {
	models := []string{"grok-imagine-video", "grok-imagine-image"}

	cases := []struct {
		platform  string
		wantVideo bool
	}{
		{"grok", true},
		{"composite", true},
		{"openai", false},
		{"anthropic", false},
	}
	for _, tc := range cases {
		t.Run(tc.platform, func(t *testing.T) {
			got := ClassifyModels(tc.platform, true, models, nil)
			hasVideo := false
			for _, opt := range got {
				if opt.Capability == MediaCapVideo {
					hasVideo = true
				}
			}
			if hasVideo != tc.wantVideo {
				t.Fatalf("platform=%s 视频可用性应为 %v，实得 %v", tc.platform, tc.wantVideo, hasVideo)
			}
		})
	}
}

// 图片能力由 groups.allow_image_generation 门禁，关闭时上游返回 403。
func TestClassifyModelsImageGatedByFlag(t *testing.T) {
	models := []string{"grok-imagine-image", "grok-imagine-video"}

	off := ClassifyModels("grok", false, models, nil)
	for _, opt := range off {
		if opt.Capability == MediaCapImage {
			t.Fatalf("allow_image_generation=false 时不应返回图片模型: %s", opt.Name)
		}
	}
	// 视频不受该开关影响
	if len(off) != 1 || off[0].Capability != MediaCapVideo {
		t.Fatalf("图片关闭时视频仍应可用，实得 %v", off)
	}
}

// 分组无 model_mapping 时回落平台默认模型表（与 sub2api /v1/models 兜底同口径）。
func TestClassifyModelsFallsBackToPlatformDefaults(t *testing.T) {
	got := ClassifyModels("grok", true, nil, nil)
	if len(got) == 0 {
		t.Fatal("空模型列表应回落平台默认表")
	}
	for _, opt := range got {
		if opt.Name == "grok-imagine-edit" {
			t.Fatal("回落路径未过滤 grok-imagine-edit")
		}
	}
}

// Grok 图片模型走 aspect_ratio 而不是 size。
//
// 【这条曾经是反的】早先断言「Grok 不支持尺寸参数、传 size 应被拒」，结论是
// 「Grok 恒出 1024×1024」。真相是 sub2api 网关在转发前主动删掉 size，Grok 认的
// 是 aspect_ratio + resolution。拒 size 这一半是对的，但缺了「该传什么」。
func TestGrokImageModelsUseAspectRatio(t *testing.T) {
	got := ClassifyModels("grok", true, []string{"grok-imagine-image"}, nil)
	if len(got) != 1 {
		t.Fatalf("应返回 1 个模型，实得 %d", len(got))
	}
	opt := got[0]
	if opt.SizeMode != SizeModeAspectRatio {
		t.Fatalf("Grok 图片模型应为 aspect_ratio 模式，实得 %q", opt.SizeMode)
	}
	if len(opt.AspectRatios) != len(grokAspectRatios) {
		t.Fatalf("应下发 %d 档宽高比，实得 %d", len(grokAspectRatios), len(opt.AspectRatios))
	}

	base := MediaGenerateParams{
		Kind: MediaKindText2Image, Model: "grok-imagine-image", Prompt: "test", N: 1,
	}

	// 传 size 应被拒：它会被网关删掉，让用户以为生效是最坏的结果
	withSize := base
	withSize.Size = "3840x2160"
	if err := ValidateGenerateParams(withSize); err == nil {
		t.Fatal("给 Grok 图片模型传 size 应被拒绝")
	}

	// 传合法 aspect_ratio 应放行
	for _, ratio := range []string{"1:1", "16:9", "21:9", "auto"} {
		p := base
		p.AspectRatio = ratio
		err := ValidateGenerateParams(p)
		if ratio == "21:9" {
			// 21:9 不在 xAI 的 14 档里——早先前端硬编码过它
			if err == nil {
				t.Fatal("21:9 不在支持列表内，应被拒绝")
			}
			continue
		}
		if err != nil {
			t.Fatalf("合法宽高比 %s 不应被拒: %v", ratio, err)
		}
	}
}

// OpenAI 格式图片走 size，传 aspect_ratio 应被拒——端点不认这个字段。
func TestOpenAIImageModelsUsePixelSize(t *testing.T) {
	got := ClassifyModels("openai", true, []string{"gpt-image-2"}, nil)
	if len(got) != 1 || got[0].SizeMode != SizeModePixelSize {
		t.Fatalf("gpt-image-2 应为 size 模式，实得 %v", got)
	}

	base := MediaGenerateParams{
		Kind: MediaKindText2Image, Model: "gpt-image-2", Prompt: "test", N: 1,
	}
	ok := base
	ok.Size = "2048x1152"
	if err := ValidateGenerateParams(ok); err != nil {
		t.Fatalf("合法 size 不应被拒: %v", err)
	}

	bad := base
	bad.AspectRatio = "16:9"
	if err := ValidateGenerateParams(bad); err == nil {
		t.Fatal("给 OpenAI 格式模型传 aspect_ratio 应被拒绝")
	}
}

// 计费档位按最长边判定：2560x1440 是 4K 而不是 2K，
// 这是最容易让用户意外超支的一条规则。
func TestImageSizeTierUsesLongestEdge(t *testing.T) {
	cases := []struct {
		size string
		want string
	}{
		{"1024x1024", "1K"},
		{"1024x576", "1K"},
		{"576x1024", "1K"}, // 竖版同样按最长边
		{"2048x1152", "2K"},
		{"2048x2048", "2K"},
		{"2560x1440", "4K"}, // 陷阱：看起来像 2K，实际按 4K 计费
		{"3840x2160", "4K"},
		{"2160x3840", "4K"},
	}
	for _, tc := range cases {
		t.Run(tc.size, func(t *testing.T) {
			got, err := ImageSizeTier(tc.size)
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if got != tc.want {
				t.Fatalf("尺寸 %s 应为 %s 档，实得 %s", tc.size, tc.want, got)
			}
		})
	}
}

// 档位字符串（"4K"/"2K"）上游明确返回 400 Invalid image size，本地就要拦掉。
func TestImageSizeTierRejectsTierStrings(t *testing.T) {
	for _, bad := range []string{"4K", "2K", "1024", "1024*1024", "", "axb", "-1x100"} {
		if _, err := ImageSizeTier(bad); err == nil {
			t.Fatalf("非法尺寸 %q 应被拒绝", bad)
		}
	}
}

// 费用预估的标准价锚点（无分组自定义价、倍率 1）。
//
// 数值逐条对齐 sub2api billing_service.go 的 defaultGrokImagine* 常量——
// 那是分组没配自定义价时上游实际使用的价。改动这些数字等于改动给用户的报价，
// 且会让页面预估与账单对不上。
func TestEstimateCostTicksStandardPrices(t *testing.T) {
	cases := []struct {
		name string
		p    MediaGenerateParams
		want int64
	}{
		{
			"标准图 1K 1 张 = $0.02",
			MediaGenerateParams{Kind: MediaKindText2Image, Model: "grok-imagine-image", N: 1, ImageResolution: "1k"},
			200_000_000,
		},
		{
			"标准图 4 张线性计费 = $0.08",
			MediaGenerateParams{Kind: MediaKindText2Image, Model: "grok-imagine-image", N: 4, ImageResolution: "1k"},
			800_000_000,
		},
		{
			// 这一条曾经是错的：旧实现写死 $0.07/张，与 sub2api 的 1K 档 $0.05 差 40%
			"高质量图 1K 1 张 = $0.05（非 $0.07）",
			MediaGenerateParams{Kind: MediaKindText2Image, Model: "grok-imagine-image-quality", N: 1, ImageResolution: "1k"},
			500_000_000,
		},
		{
			"高质量图 2K 1 张 = $0.07",
			MediaGenerateParams{Kind: MediaKindText2Image, Model: "grok-imagine-image-quality", N: 1, ImageResolution: "2k"},
			700_000_000,
		},
		{
			"grok-imagine 按映射后的 image-quality 计价",
			MediaGenerateParams{Kind: MediaKindText2Image, Model: "grok-imagine", N: 1, ImageResolution: "2k"},
			700_000_000,
		},
		{
			"480p 8 秒 = $0.40",
			MediaGenerateParams{Kind: MediaKindText2Video, Model: "grok-imagine-video", Resolution: "480p", Duration: 8},
			4_000_000_000,
		},
		{
			"720p 8 秒 = $0.56（与文档 usage 样例一致）",
			MediaGenerateParams{Kind: MediaKindText2Video, Model: "grok-imagine-video", Resolution: "720p", Duration: 8},
			5_600_000_000,
		},
		{
			"1080p 按 720p 同价（sub2api getDefaultGrokImagineVideoPrice 的口径）",
			MediaGenerateParams{Kind: MediaKindText2Video, Model: "grok-imagine-video", Resolution: "1080p", Duration: 8},
			5_600_000_000,
		},
		{
			"720p 15 秒上限 = $1.05",
			MediaGenerateParams{Kind: MediaKindText2Video, Model: "grok-imagine-video", Resolution: "720p", Duration: 15},
			10_500_000_000,
		},
		{
			"未登记模型无法预估",
			MediaGenerateParams{Kind: MediaKindText2Image, Model: "unknown-model", N: 1},
			0,
		},
		{
			"OpenAI 格式图片标准价本站查不到，返回 0",
			MediaGenerateParams{Kind: MediaKindText2Image, Model: "gpt-image-2", N: 1, Size: "1024x1024"},
			0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EstimateCostTicks(tc.p, nil); got != tc.want {
				t.Fatalf("预估应为 %d ticks，实得 %d", tc.want, got)
			}
		})
	}
}

// grok-imagine-video-1.5 的静默降级：文生视频时上游换成 grok-imagine-video
// 并按后者计费，图生视频时 1.5 真生效。
//
// 【报价必须跟着降】否则用户看到 $0.64 的预估、实扣 $0.40，页面在骗人。
func TestEstimateCostTicksVideo15Downgrade(t *testing.T) {
	cases := []struct {
		name string
		kind string
		want int64
	}{
		// 文生视频降级为 grok-imagine-video：480p $0.05/s × 8s
		{"文生视频降级按 $0.05/s 计", MediaKindText2Video, 4_000_000_000},
		// 图生视频 1.5 生效：480p $0.08/s × 8s
		{"图生视频按 1.5 的 $0.08/s 计", MediaKindImage2Video, 6_400_000_000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := EstimateCostTicks(MediaGenerateParams{
				Kind: tc.kind, Model: "grok-imagine-video-1.5",
				Resolution: "480p", Duration: 8,
			}, nil)
			if got != tc.want {
				t.Fatalf("预估应为 %d ticks，实得 %d", tc.want, got)
			}
		})
	}

	// 图生视频 720p × 8s = $0.14 × 8 = $1.12
	got := EstimateCostTicks(MediaGenerateParams{
		Kind: MediaKindImage2Video, Model: "grok-imagine-video-1.5",
		Resolution: "720p", Duration: 8,
	}, nil)
	if got != 11_200_000_000 {
		t.Fatalf("1.5 图生视频 720p×8s 应为 $1.12，实得 %s", FormatTicksUSD(got))
	}
}

// 分组自定义单价优先于标准价——这是 sub2api CalculateImageCost 的第一优先级，
// 漏读这几列会让配了自定义价的分组全部报错价。
func TestEstimateCostTicksHonorsGroupPrices(t *testing.T) {
	pricing := &repository.MediaPricing{
		ImagePrice1K:        f(0.10),
		VideoPrice720P:      f(0.20),
		GroupRateMultiplier: 1,
	}

	img := EstimateCostTicks(MediaGenerateParams{
		Kind: MediaKindText2Image, Model: "grok-imagine-image", N: 2, ImageResolution: "1k",
	}, pricing)
	if img != 2_000_000_000 { // $0.10 × 2
		t.Fatalf("应取分组自定义单价 $0.10×2=$0.20，实得 %s", FormatTicksUSD(img))
	}

	vid := EstimateCostTicks(MediaGenerateParams{
		Kind: MediaKindText2Video, Model: "grok-imagine-video", Resolution: "720p", Duration: 5,
	}, pricing)
	if vid != 10_000_000_000 { // $0.20 × 5
		t.Fatalf("应取分组自定义单价 $0.20×5=$1.00，实得 %s", FormatTicksUSD(vid))
	}

	// 未配置的档位仍回落标准价：2K 没配，走 image-quality 的 $0.07
	fallback := EstimateCostTicks(MediaGenerateParams{
		Kind: MediaKindText2Image, Model: "grok-imagine-image-quality", N: 1, ImageResolution: "2k",
	}, pricing)
	if fallback != 700_000_000 {
		t.Fatalf("未配置档位应回落标准价 $0.07，实得 %s", FormatTicksUSD(fallback))
	}
}

// 倍率必须参与计算。旧实现完全没读倍率，配了 1.5 倍的分组会被少报三分之一。
func TestEstimateCostTicksAppliesMultipliers(t *testing.T) {
	cases := []struct {
		name    string
		pricing *repository.MediaPricing
		kind    string
		model   string
		want    int64
	}{
		{
			"分组倍率 1.5 作用于图片",
			&repository.MediaPricing{GroupRateMultiplier: 1.5},
			MediaKindText2Image, "grok-imagine-image", 300_000_000, // $0.02 × 1.5
		},
		{
			"图片独立倍率覆盖分组倍率",
			&repository.MediaPricing{
				GroupRateMultiplier:  1.5,
				ImageRateIndependent: true, ImageRateMultiplier: 2,
			},
			MediaKindText2Image, "grok-imagine-image", 400_000_000, // $0.02 × 2
		},
		{
			"图片独立倍率不影响视频（视频仍用分组倍率）",
			&repository.MediaPricing{
				GroupRateMultiplier:  2,
				ImageRateIndependent: true, ImageRateMultiplier: 10,
			},
			MediaKindText2Video, "grok-imagine-video", 8_000_000_000, // $0.05×8s×2
		},
		{
			"视频独立倍率覆盖分组倍率",
			&repository.MediaPricing{
				GroupRateMultiplier:  1,
				VideoRateIndependent: true, VideoRateMultiplier: 3,
			},
			MediaKindText2Video, "grok-imagine-video", 12_000_000_000, // $0.05×8s×3
		},
		{
			"负倍率收敛到 0（上游的口径是免费而非报错）",
			&repository.MediaPricing{
				GroupRateMultiplier:  1,
				ImageRateIndependent: true, ImageRateMultiplier: -1,
			},
			MediaKindText2Image, "grok-imagine-image", 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := EstimateCostTicks(MediaGenerateParams{
				Kind: tc.kind, Model: tc.model,
				N: 1, ImageResolution: "1k",
				Resolution: "480p", Duration: 8,
			}, tc.pricing)
			if got != tc.want {
				t.Fatalf("预估应为 %d ticks（%s），实得 %d（%s）",
					tc.want, FormatTicksUSD(tc.want), got, FormatTicksUSD(got))
			}
		})
	}
}

// ClassifyModels 下发的单价必须已含倍率与分组自定义价——前端只做乘法，
// 单价错了整页报价全错。
func TestClassifyModelsDownstreamPrices(t *testing.T) {
	pricing := &repository.MediaPricing{
		ImagePrice1K:        f(0.10),
		GroupRateMultiplier: 2,
	}
	got := ClassifyModels("grok", true, []string{"grok-imagine-image", "grok-imagine-video"}, pricing)

	for _, opt := range got {
		switch opt.Name {
		case "grok-imagine-image":
			if opt.PriceByTier["1K"] != 2_000_000_000 { // $0.10 × 2
				t.Fatalf("图片 1K 单价应为 $0.20，实得 %s", FormatTicksUSD(opt.PriceByTier["1K"]))
			}
			if opt.PriceByTier["2K"] != 400_000_000 { // 标准价 $0.02 × 2
				t.Fatalf("图片 2K 单价应为 $0.04，实得 %s", FormatTicksUSD(opt.PriceByTier["2K"]))
			}
		case "grok-imagine-video":
			if opt.PriceByTier["720p"] != 1_400_000_000 { // $0.07 × 2
				t.Fatalf("视频 720p 单价应为 $0.14/s，实得 %s", FormatTicksUSD(opt.PriceByTier["720p"]))
			}
			if len(opt.Resolutions) != 3 {
				t.Fatalf("视频应下发 3 档分辨率（含 1080p），实得 %v", opt.Resolutions)
			}
		}
	}
}

// ticks 与美元的换算锚点。搞错量级就会给用户报错价格。
func TestFormatTicksUSD(t *testing.T) {
	cases := []struct {
		ticks int64
		want  string
	}{
		{200_000_000, "0.0200"},
		{500_000_000, "0.0500"},
		{5_600_000_000, "0.5600"},
		{10_500_000_000, "1.0500"},
		{0, "0.0000"},
	}
	for _, tc := range cases {
		if got := FormatTicksUSD(tc.ticks); got != tc.want {
			t.Fatalf("%d ticks 应格式化为 %s，实得 %s", tc.ticks, tc.want, got)
		}
	}
}

// 浮点换算不能截断：0.07 在 float64 里是 0.06999...，直接 int64() 会得到
// 699999999，展示成 $0.0699。
func TestUSDToTicksRounds(t *testing.T) {
	cases := []struct {
		usd  float64
		want int64
	}{
		{0.02, 200_000_000},
		{0.05, 500_000_000},
		{0.07, 700_000_000},
		{0.14, 1_400_000_000},
		{0.25, 2_500_000_000},
		{0, 0},
		{-1, 0},
	}
	for _, tc := range cases {
		if got := usdToTicks(tc.usd); got != tc.want {
			t.Fatalf("$%v 应为 %d ticks，实得 %d", tc.usd, tc.want, got)
		}
	}
}

// 视频参数边界：分辨率认 480p/720p/1080p，时长 1-15 秒。
//
// 【1080p 从被拒改为放行】旧实现拒它，依据是对 grok-imagine-video 的观察；
// 但 sub2api 为 1.5 保留了独立的 1080p 单价，说明该组合上游认可。若某模型确实
// 不支持，上游返回参数错误 400 且不扣费，代价远小于永久藏起一个能用的档位。
func TestValidateVideoParams(t *testing.T) {
	base := MediaGenerateParams{
		Kind: MediaKindText2Video, Model: "grok-imagine-video",
		Prompt: "海浪拍打礁石", Resolution: "720p", Duration: 8,
	}
	if err := ValidateGenerateParams(base); err != nil {
		t.Fatalf("合法参数不应被拒: %v", err)
	}

	for _, res := range videoResolutions {
		p := base
		p.Resolution = res
		if err := ValidateGenerateParams(p); err != nil {
			t.Fatalf("分辨率 %s 不应被拒: %v", res, err)
		}
	}

	bad := []struct {
		name  string
		patch func(*MediaGenerateParams)
	}{
		{"360p 上游返回 422", func(p *MediaGenerateParams) { p.Resolution = "360p" }},
		{"空分辨率", func(p *MediaGenerateParams) { p.Resolution = "" }},
		{"时长 0 秒", func(p *MediaGenerateParams) { p.Duration = 0 }},
		{"时长 16 秒超上限", func(p *MediaGenerateParams) { p.Duration = 16 }},
		{"空提示词", func(p *MediaGenerateParams) { p.Prompt = "   " }},
		{"图片模型不能生成视频", func(p *MediaGenerateParams) { p.Model = "grok-imagine-image" }},
		{"非法宽高比", func(p *MediaGenerateParams) { p.AspectRatio = "21:9" }},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			p := base
			tc.patch(&p)
			if err := ValidateGenerateParams(p); err == nil {
				t.Fatalf("非法参数应被拒绝")
			}
		})
	}
}

// 图生图同样必须先有公网 URL：本地文件在 Submit 里上传对象存储后再校验。
func TestValidateImage2ImageRequiresPublicURL(t *testing.T) {
	base := MediaGenerateParams{
		Kind: MediaKindImage2Image, Model: "grok-imagine-image",
		Prompt: "改成赛博朋克", N: 1,
	}
	if err := ValidateGenerateParams(base); err == nil {
		t.Fatal("缺少参考图 URL 应被拒绝")
	}

	base.ImageURL = "https://example.com/a.jpg"
	if err := ValidateGenerateParams(base); err != nil {
		t.Fatalf("合法参考图 URL 不应被拒: %v", err)
	}

	for _, bad := range []string{"ftp://x/a.jpg", "/local/a.jpg", "data:image/png;base64,xxx"} {
		p := base
		p.ImageURL = bad
		if err := ValidateGenerateParams(p); err == nil {
			t.Fatalf("非 http(s) 参考图 %q 应被拒绝", bad)
		}
	}
}

// 图生视频的参考图必须是公网可达的 http(s) 地址：
// 上游要自己去取这张图，multipart 上传会直接 415。
func TestValidateImage2VideoRequiresPublicURL(t *testing.T) {
	base := MediaGenerateParams{
		Kind: MediaKindImage2Video, Model: "grok-imagine-video",
		Prompt: "苹果旋转", Resolution: "480p", Duration: 8,
	}

	if err := ValidateGenerateParams(base); err == nil {
		t.Fatal("缺少参考图 URL 应被拒绝")
	}

	base.ImageURL = "https://example.com/a.jpg"
	if err := ValidateGenerateParams(base); err != nil {
		t.Fatalf("合法参考图 URL 不应被拒: %v", err)
	}

	for _, bad := range []string{"ftp://x/a.jpg", "/local/a.jpg", "data:image/png;base64,xxx"} {
		p := base
		p.ImageURL = bad
		if err := ValidateGenerateParams(p); err == nil {
			t.Fatalf("非 http(s) 参考图 %q 应被拒绝", bad)
		}
	}
}

func TestValidateImage2VideoAllowsUpToFourRefs(t *testing.T) {
	base := MediaGenerateParams{
		Kind: MediaKindImage2Video, Model: "grok-imagine-video",
		Prompt: "四张参考图", Resolution: "480p", Duration: 8,
		ImageURLs: []string{
			"https://example.com/1.jpg",
			"https://example.com/2.jpg",
			"https://example.com/3.jpg",
			"https://example.com/4.jpg",
		},
	}
	base.ImageURL = base.ImageURLs[0]
	if err := ValidateGenerateParams(base); err != nil {
		t.Fatalf("4 张参考图应放行: %v", err)
	}
	base.ImageURLs = append(base.ImageURLs, "https://example.com/5.jpg")
	if err := ValidateGenerateParams(base); err == nil {
		t.Fatal("超过 4 张参考图应被拒绝")
	}
}

// 提示词长度上限。
func TestValidateRejectsOverlongPrompt(t *testing.T) {
	long := make([]byte, mediaMaxPromptLen+1)
	for i := range long {
		long[i] = 'a'
	}
	err := ValidateGenerateParams(MediaGenerateParams{
		Kind: MediaKindText2Image, Model: "grok-imagine-image",
		Prompt: string(long), N: 1,
	})
	if err == nil {
		t.Fatal("超长提示词应被拒绝")
	}
}

// 图片张数边界。
func TestValidateImageCount(t *testing.T) {
	base := MediaGenerateParams{
		Kind: MediaKindText2Image, Model: "grok-imagine-image", Prompt: "test",
	}
	for _, n := range []int{0, -1, mediaMaxImageN + 1} {
		p := base
		p.N = n
		if err := ValidateGenerateParams(p); err == nil {
			t.Fatalf("张数 %d 应被拒绝", n)
		}
	}
	p := base
	p.N = 1
	if err := ValidateGenerateParams(p); err != nil {
		t.Fatalf("张数 1 不应被拒: %v", err)
	}
}
