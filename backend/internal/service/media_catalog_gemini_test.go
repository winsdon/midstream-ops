package service

import (
	"testing"

	"sub2api-account-monitor/internal/repository"
)

// geminiImageModels 用户指定登记的全部 Gemini（Nano Banana）生图模型。
var geminiImageModels = []string{
	"gemini-nano-banana-2.1",
	"gemini-3.1-flash-image",
	"gemini-3.1-flash-image-preview",
	"gemini-3-pro-image",
	"gemini-3-pro-image-preview",
}

// Gemini 生图只能走 /v1beta 原生协议，sub2api 只对 Gemini 系分组放行该入口。
// 其他平台的分组列出它们，用户选中后只会收到报错。
func TestClassifyModelsGeminiImageGatedByPlatform(t *testing.T) {
	cases := []struct {
		platform string
		want     bool
	}{
		{"gemini", true},
		{"antigravity", true},
		{"composite", true},
		{"openai", false},
		{"grok", false},
		{"anthropic", false},
	}
	for _, tc := range cases {
		t.Run(tc.platform, func(t *testing.T) {
			got := ClassifyModels(tc.platform, true, geminiImageModels, nil)
			if tc.want && len(got) != len(geminiImageModels) {
				t.Fatalf("platform=%s 应返回 %d 个 Gemini 生图模型，实得 %d", tc.platform, len(geminiImageModels), len(got))
			}
			if !tc.want && len(got) != 0 {
				t.Fatalf("platform=%s 不应返回 Gemini 生图模型，实得 %v", tc.platform, got)
			}
		})
	}
}

// 生图开关同样约束 Gemini 模型（关闭时上游返回 400）。
func TestClassifyModelsGeminiImageGatedByFlag(t *testing.T) {
	if got := ClassifyModels("gemini", false, geminiImageModels, nil); len(got) != 0 {
		t.Fatalf("allow_image_generation=false 时不应返回 Gemini 生图模型，实得 %v", got)
	}
}

// Gemini 默认模型表里的 gemini-3.1-flash-image 在空映射分组也要出现。
func TestClassifyModelsGeminiDefaults(t *testing.T) {
	got := ClassifyModels("gemini", true, nil, nil)
	if len(got) != 1 || got[0].Name != "gemini-3.1-flash-image" {
		t.Fatalf("gemini 空映射分组应只列出 gemini-3.1-flash-image，实得 %v", got)
	}
}

// 下发的选项：image_size 模式，宽高比与 1K/2K/4K 档位。
func TestGeminiImageModelsUseImageSize(t *testing.T) {
	got := ClassifyModels("gemini", true, geminiImageModels, nil)
	for _, opt := range got {
		if opt.SizeMode != SizeModeImageSize {
			t.Fatalf("%s 应为 image_size 模式，实得 %q", opt.Name, opt.SizeMode)
		}
		if opt.Capability != MediaCapImage {
			t.Fatalf("%s 应为图片能力", opt.Name)
		}
		if len(opt.AspectRatios) != len(geminiAspectRatios) {
			t.Fatalf("%s 应下发 %d 档宽高比，实得 %v", opt.Name, len(geminiAspectRatios), opt.AspectRatios)
		}
		if len(opt.Resolutions) != 3 || opt.Resolutions[0] != "1K" || opt.Resolutions[2] != "4K" {
			t.Fatalf("%s 档位应为 1K/2K/4K（默认 1K 在首位），实得 %v", opt.Name, opt.Resolutions)
		}
	}
	if got[0].Name != "gemini-nano-banana-2.1" {
		t.Fatalf("最新的 Nano Banana 2.1 应排在最前，实得 %s", got[0].Name)
	}
}

func TestValidateGeminiImageParams(t *testing.T) {
	base := MediaGenerateParams{
		Kind: MediaKindText2Image, Model: "gemini-3.1-flash-image", Prompt: "蓝色机器人", N: 2,
		AspectRatio: "16:9", ImageResolution: "2K",
	}
	if err := ValidateGenerateParams(base); err != nil {
		t.Fatalf("合法参数不应被拒: %v", err)
	}
	for _, size := range []string{"1K", "2K", "4K"} {
		p := base
		p.ImageResolution = size
		if err := ValidateGenerateParams(p); err != nil {
			t.Fatalf("档位 %s 不应被拒: %v", size, err)
		}
	}

	bad := map[string]func(*MediaGenerateParams){
		// 上游要求 K 大写，小写会被当成非法值
		"小写档位":  func(p *MediaGenerateParams) { p.ImageResolution = "1k" },
		"未知档位":  func(p *MediaGenerateParams) { p.ImageResolution = "8K" },
		"像素尺寸":  func(p *MediaGenerateParams) { p.Size = "1024x1024" },
		"非法宽高比": func(p *MediaGenerateParams) { p.AspectRatio = "19.5:9" },
		"张数越界":  func(p *MediaGenerateParams) { p.N = 5 },
	}
	for name, mutate := range bad {
		t.Run(name, func(t *testing.T) {
			p := base
			mutate(&p)
			if err := ValidateGenerateParams(p); err == nil {
				t.Fatalf("%s 应被拒绝", name)
			}
		})
	}

	i2i := base
	i2i.Kind = MediaKindImage2Image
	i2i.ImageURLs = []string{"https://refs.example.com/a.png", "https://refs.example.com/b.png"}
	if err := ValidateGenerateParams(i2i); err != nil {
		t.Fatalf("多图参考图生图不应被拒: %v", err)
	}
}

// 报价按 imageSize 档位 × 张数；不填档位按 2K（与上游缺省计费一致）。
// 无分组自定义价时与 gpt-image 一样无法预估（默认价在 sub2api 进程内）。
func TestEstimateCostTicksGeminiImage(t *testing.T) {
	pricing := &repository.MediaPricing{
		ImagePrice1K:        f(0.10),
		ImagePrice2K:        f(0.15),
		ImagePrice4K:        f(0.20),
		GroupRateMultiplier: 1,
	}
	cases := []struct {
		size string
		n    int
		want int64
	}{
		{"1K", 1, 1_000_000_000},
		{"2K", 2, 3_000_000_000},
		{"4K", 3, 6_000_000_000},
		{"", 1, 1_500_000_000},
	}
	for _, tc := range cases {
		p := MediaGenerateParams{Kind: MediaKindText2Image, Model: "gemini-3-pro-image", N: tc.n, ImageResolution: tc.size}
		if got := EstimateCostTicks(p, pricing); got != tc.want {
			t.Fatalf("档位 %q × %d 应为 %s，实得 %s", tc.size, tc.n, FormatTicksUSD(tc.want), FormatTicksUSD(got))
		}
	}

	if got := EstimateCostTicks(MediaGenerateParams{Kind: MediaKindText2Image, Model: "gemini-3-pro-image", N: 1, ImageResolution: "1K"}, nil); got != 0 {
		t.Fatalf("无分组价时应返回 0（以账单为准），实得 %s", FormatTicksUSD(got))
	}
}
