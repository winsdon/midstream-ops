package service

import "testing"

// 三个陷阱模型必须被过滤掉：它们都出现在 /v1/models 里，但
// grok-imagine 与 grok-imagine-edit 会 404，grok-imagine-video-1.5 会被
// 静默降级为 grok-imagine-video 却照原价计费。放进下拉框就是让用户白花钱。
func TestClassifyModelsFiltersTrapModels(t *testing.T) {
	all := []string{
		"grok-imagine",               // 视频接口 404
		"grok-imagine-edit",          // 编辑接口 404
		"grok-imagine-video-1.5",     // 静默降级但照原价计费
		"grok-imagine-image",         // 可用
		"grok-imagine-image-quality", // 可用
		"grok-imagine-video",         // 可用
		"grok-4.5",                   // 文本模型，不是生成模型
	}
	got := ClassifyModels("grok", true, all)

	if len(got) != 3 {
		t.Fatalf("应只剩 3 个可用模型，实得 %d: %v", len(got), got)
	}
	for _, opt := range got {
		switch opt.Name {
		case "grok-imagine", "grok-imagine-edit", "grok-imagine-video-1.5", "grok-4.5":
			t.Fatalf("陷阱模型 %s 未被过滤", opt.Name)
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
			got := ClassifyModels(tc.platform, true, models)
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

	off := ClassifyModels("grok", false, models)
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
	got := ClassifyModels("grok", true, nil)
	if len(got) == 0 {
		t.Fatal("空模型列表应回落平台默认表")
	}
	// 默认表里同样含三个陷阱模型，回落后也必须被过滤
	for _, opt := range got {
		switch opt.Name {
		case "grok-imagine", "grok-imagine-edit", "grok-imagine-video-1.5":
			t.Fatalf("回落路径未过滤陷阱模型 %s", opt.Name)
		}
	}
}

// Grok 图片模型固定 1024×1024，size 被上游静默忽略——
// 必须让前端知道不能给尺寸选择器，否则用户以为选了 4K 却拿到 1K 图。
func TestGrokImageModelsRejectSize(t *testing.T) {
	got := ClassifyModels("grok", true, []string{"grok-imagine-image"})
	if len(got) != 1 || got[0].SupportsSize {
		t.Fatalf("Grok 图片模型不应声明支持尺寸参数: %v", got)
	}

	err := ValidateGenerateParams(MediaGenerateParams{
		Kind: MediaKindText2Image, Model: "grok-imagine-image",
		Prompt: "test", N: 1, Size: "3840x2160",
	})
	if err == nil {
		t.Fatal("给 Grok 图片模型传 size 应被拒绝")
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

// 费用预估：图片按张数线性，视频按分辨率单价 × 秒数。
// 数值锚点来自文档计费表，改动这些数字等于改动给用户的报价。
func TestEstimateCostTicks(t *testing.T) {
	cases := []struct {
		name string
		p    MediaGenerateParams
		want int64
	}{
		{
			"标准图 1 张 = $0.02",
			MediaGenerateParams{Model: "grok-imagine-image", N: 1},
			200_000_000,
		},
		{
			"标准图 4 张线性计费 = $0.08",
			MediaGenerateParams{Model: "grok-imagine-image", N: 4},
			800_000_000,
		},
		{
			"高质量图 1 张 = $0.07",
			MediaGenerateParams{Model: "grok-imagine-image-quality", N: 1},
			700_000_000,
		},
		{
			"480p 8 秒 = $0.40",
			MediaGenerateParams{Model: "grok-imagine-video", Resolution: "480p", Duration: 8},
			4_000_000_000,
		},
		{
			"720p 8 秒 = $0.56（与文档 usage 样例一致）",
			MediaGenerateParams{Model: "grok-imagine-video", Resolution: "720p", Duration: 8},
			5_600_000_000,
		},
		{
			"720p 15 秒上限 = $1.05",
			MediaGenerateParams{Model: "grok-imagine-video", Resolution: "720p", Duration: 15},
			10_500_000_000,
		},
		{
			"未登记模型无法预估",
			MediaGenerateParams{Model: "unknown-model", N: 1},
			0,
		},
		{
			"OpenAI 格式图片按分组定价，本站无权威价",
			MediaGenerateParams{Model: "gpt-image-2", N: 1},
			0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EstimateCostTicks(tc.p); got != tc.want {
				t.Fatalf("预估应为 %d ticks，实得 %d", tc.want, got)
			}
		})
	}
}

// ticks 与美元的换算锚点。搞错量级就会给用户报错价格。
func TestFormatTicksUSD(t *testing.T) {
	cases := []struct {
		ticks int64
		want  string
	}{
		{200_000_000, "0.0200"},
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

// 视频参数边界：分辨率只认 480p/720p，时长 1-15 秒。
func TestValidateVideoParams(t *testing.T) {
	base := MediaGenerateParams{
		Kind: MediaKindText2Video, Model: "grok-imagine-video",
		Prompt: "海浪拍打礁石", Resolution: "720p", Duration: 8,
	}
	if err := ValidateGenerateParams(base); err != nil {
		t.Fatalf("合法参数不应被拒: %v", err)
	}

	bad := []struct {
		name  string
		patch func(*MediaGenerateParams)
	}{
		{"1080p 上游返回 400", func(p *MediaGenerateParams) { p.Resolution = "1080p" }},
		{"360p 上游返回 422", func(p *MediaGenerateParams) { p.Resolution = "360p" }},
		{"时长 0 秒", func(p *MediaGenerateParams) { p.Duration = 0 }},
		{"时长 16 秒超上限", func(p *MediaGenerateParams) { p.Duration = 16 }},
		{"空提示词", func(p *MediaGenerateParams) { p.Prompt = "   " }},
		{"图片模型不能生成视频", func(p *MediaGenerateParams) { p.Model = "grok-imagine-image" }},
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
