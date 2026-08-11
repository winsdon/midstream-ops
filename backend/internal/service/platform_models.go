package service

// 平台默认模型表。
//
// 当一个分组下所有账号都没配 credentials->'model_mapping' 时，sub2api 的
// GET /v1/models 会回落到 Go 硬编码的默认模型列表（gateway_handler.go 的兜底分支）。
// 这些常量只存在于 sub2api 的源码里，Postgres 中查不到，因此本站内嵌一份同源副本，
// 否则这类分组在广场上会显示为「零个模型」，与用户实际能调用的模型不符。
//
// 数据来源：sub2api origin/main 的
//
//	internal/pkg/claude/constants.go   DefaultModels
//	internal/pkg/openai/constants.go   DefaultModels
//	internal/pkg/geminicli/models.go   DefaultModels
//	internal/pkg/xai/models.go         DefaultModelIDs()
//
// sub2api 升级新增模型后此表会滞后；表现为个别新模型在这类空映射分组里缺失，
// 属可接受的降级（配了 model_mapping 的分组不受影响）。
var platformDefaultModels = map[string][]string{
	"anthropic": {
		"claude-fable-5",
		"claude-opus-4-5-20251101",
		"claude-opus-4-6",
		"claude-opus-4-7",
		"claude-opus-4-8",
		"claude-opus-5",
		"claude-sonnet-5",
		"claude-sonnet-4-6",
		"claude-sonnet-4-5-20250929",
		"claude-haiku-4-5-20251001",
	},
	"openai": {
		"gpt-5.6-sol",
		"gpt-5.6",
		"gpt-5.6-terra",
		"gpt-5.6-luna",
		"gpt-5.5",
		"gpt-5.4",
		"gpt-5.4-mini",
		"gpt-5.3-codex-spark",
		"codex-auto-review",
		"gpt-5.2",
		"gpt-image-1",
		"gpt-image-1.5",
		"gpt-image-2",
	},
	"gemini": {
		"gemini-2.0-flash",
		"gemini-2.5-flash",
		"gemini-2.5-flash-image",
		"gemini-2.5-pro",
		"gemini-3.5-flash",
		"gemini-3-flash-preview",
		"gemini-3-pro-preview",
		"gemini-3.1-pro-preview",
		"gemini-3.1-flash-image",
	},
	"grok": {
		"grok-4.5",
		"grok-4.3",
		"grok-build-0.1",
		"grok-composer-2.5-fast",
		"grok-4.20-0309-reasoning",
		"grok-4.20-0309-non-reasoning",
		"grok-4.20-multi-agent-0309",
		"grok-imagine",
		"grok-imagine-image",
		"grok-imagine-image-quality",
		"grok-imagine-edit",
		"grok-imagine-video",
		"grok-imagine-video-1.5",
	},
}

// DefaultModelsForPlatform 返回指定平台的默认模型列表（副本，调用方可安全修改）。
// 未知平台返回 nil。
func DefaultModelsForPlatform(platform string) []string {
	models, ok := platformDefaultModels[platform]
	if !ok {
		return nil
	}
	out := make([]string, len(models))
	copy(out, models)
	return out
}
