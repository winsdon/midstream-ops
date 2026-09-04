package repository

import "testing"

func TestMediaPricingVideoModelPrice(t *testing.T) {
	pricing := &MediaPricing{
		VideoModelPrices: map[string]map[string]float64{
			"grok-imagine-video-1.5": {"720p": 0.28, "1080p": 0.50},
		},
	}

	if got := pricing.VideoModelPrice("grok-imagine-video-1.5", "1080p"); got == nil || *got != 0.50 {
		t.Fatalf("应读取模型级 1080p 价格 $0.50，实得 %v", got)
	}
	if got := pricing.VideoModelPrice("xai/grok-video-1.5-preview", "1080p"); got == nil || *got != 0.50 {
		t.Fatalf("应把 1.5 别名归一到模型价格族，实得 %v", got)
	}
	if got := pricing.VideoModelPrice("grok-imagine-video-1.5", "480p"); got == nil || *got != 0.50 {
		t.Fatalf("缺少档位时应按上游顺序回退到已有价格，实得 %v", got)
	}
	if got := pricing.VideoModelPrice("grok-imagine-video", "1080p"); got != nil {
		t.Fatalf("未配置的模型不应返回模型级价格，实得 %v", *got)
	}
}
