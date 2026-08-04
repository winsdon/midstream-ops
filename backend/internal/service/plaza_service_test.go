package service

import (
	"testing"
	"time"

	"sub2api-account-monitor/internal/repository"
)

func fptr(v float64) *float64 { return &v }

// grp 构造一个分组及其可用模型。
func grp(id int64, name, platform string, rate float64, models ...string) repository.PGGroupModels {
	return repository.PGGroupModels{
		GroupID:          id,
		GroupName:        name,
		Platform:         platform,
		RateMultiplier:   rate,
		SubscriptionType: "standard",
		Models:           models,
	}
}

// pricingRow 构造一行渠道定价（绑定到某个分组）。
func pricingRow(pricingID int64, channel string, groupID int64, input *float64, models ...string) repository.PGChannelPricingRow {
	return repository.PGChannelPricingRow{
		PricingID:   pricingID,
		ChannelID:   pricingID * 10,
		ChannelName: channel,
		ChannelDesc: channel + " desc",
		Platform:    "anthropic",
		Models:      models,
		BillingMode: BillingModeToken,
		InputPrice:  input,
		GroupID:     groupID,
		GroupName:   "g" + channel,
		GroupRate:   1,
	}
}

// emptyTable 是不含任何价格的静态表，用于隔离静态兜底的影响。
func emptyTable() *StaticPriceTable { return parseStaticPriceTable([]byte(`{}`)) }

func TestBuildPlazaModels_ModelsComeFromGroups(t *testing.T) {
	// 模型清单来自分组的可用模型，与渠道定价表无关。
	groups := []repository.PGGroupModels{
		grp(1, "claude max", "anthropic", 0.85, "claude-opus-4-8", "claude-sonnet-5"),
	}
	models := buildPlazaModels(groups, nil, nil, nil, nil, emptyTable())

	if len(models) != 2 {
		t.Fatalf("want 2 models, got %d", len(models))
	}
	if models[0].Name != "claude-opus-4-8" || models[1].Name != "claude-sonnet-5" {
		t.Errorf("模型未按名称排序: %q %q", models[0].Name, models[1].Name)
	}
	// 没有渠道定价 → 无 source，价格为空
	if len(models[0].Sources) != 0 {
		t.Errorf("无渠道定价时不应有 source，got %d", len(models[0].Sources))
	}
	if models[0].PriceSource != PriceSourceUnknown {
		t.Errorf("PriceSource = %q, want unknown", models[0].PriceSource)
	}
}

func TestBuildPlazaModels_MergesGroupsAcrossGroups(t *testing.T) {
	// 同一模型出现在多个分组 → 合并为一张卡，带全部分组。
	groups := []repository.PGGroupModels{
		grp(1, "claude max", "anthropic", 0.85, "claude-opus-4-8"),
		grp(2, "claude aws", "anthropic", 3.5, "claude-opus-4-8"),
	}
	models := buildPlazaModels(groups, nil, nil, nil, nil, emptyTable())

	if len(models) != 1 {
		t.Fatalf("want 1 model, got %d", len(models))
	}
	if len(models[0].Groups) != 2 {
		t.Errorf("want 2 groups merged, got %d", len(models[0].Groups))
	}
	if len(models[0].Platforms) != 1 || models[0].Platforms[0] != "anthropic" {
		t.Errorf("platforms = %v", models[0].Platforms)
	}
}

func TestBuildPlazaModels_EmptyMappingFallsBackToDefaults(t *testing.T) {
	// 分组账号没配 model_mapping → 回落平台默认模型表
	//（对齐 sub2api 线上 /v1/models 的兜底行为）。
	groups := []repository.PGGroupModels{
		grp(1, "k3", "anthropic", 1.7), // 无模型
	}
	models := buildPlazaModels(groups, nil, nil, nil, nil, emptyTable())

	if len(models) == 0 {
		t.Fatal("空映射分组应回落到平台默认模型表，实际为空")
	}
	want := DefaultModelsForPlatform("anthropic")
	if len(models) != len(want) {
		t.Errorf("want %d default models, got %d", len(want), len(models))
	}
}

func TestBuildPlazaModels_UnknownPlatformEmptyMappingYieldsNothing(t *testing.T) {
	groups := []repository.PGGroupModels{grp(1, "weird", "some-unknown-platform", 1)}
	if models := buildPlazaModels(groups, nil, nil, nil, nil, emptyTable()); len(models) != 0 {
		t.Errorf("未知平台且无映射应产出 0 个模型，got %d", len(models))
	}
}

func TestBuildPlazaModels_ChannelPricingOverlay(t *testing.T) {
	// 渠道定价按「分组 × 模型」覆盖到对应卡片上。
	groups := []repository.PGGroupModels{
		grp(1, "claude max", "anthropic", 0.85, "claude-opus-4-8", "claude-sonnet-5"),
	}
	pricing := []repository.PGChannelPricingRow{
		pricingRow(1, "Claude", 1, fptr(3e-6), "claude-opus-4-8"),
	}
	models := buildPlazaModels(groups, pricing, nil, nil, nil, emptyTable())

	byName := map[string]PlazaModel{}
	for _, m := range models {
		byName[m.Name] = m
	}
	opus := byName["claude-opus-4-8"]
	if opus.Price.Input == nil || *opus.Price.Input != 3e-6 {
		t.Errorf("opus input price = %v, want 3e-6", opus.Price.Input)
	}
	if opus.PriceSource != PriceSourceChannel {
		t.Errorf("PriceSource = %q, want channel", opus.PriceSource)
	}
	if len(opus.Sources) != 1 {
		t.Errorf("want 1 source, got %d", len(opus.Sources))
	}
	// 未被定价覆盖的模型仍在清单里，只是没价
	if sonnet := byName["claude-sonnet-5"]; sonnet.Price.Input != nil {
		t.Errorf("未覆盖的模型不应有价格: %v", sonnet.Price.Input)
	}
}

func TestBuildPlazaModels_ChannelPricingIsCaseInsensitive(t *testing.T) {
	groups := []repository.PGGroupModels{grp(1, "g", "anthropic", 1, "Claude-Opus-4-8")}
	pricing := []repository.PGChannelPricingRow{
		pricingRow(1, "ch", 1, fptr(2e-6), "claude-opus-4-8"),
	}
	models := buildPlazaModels(groups, pricing, nil, nil, nil, emptyTable())
	if models[0].Price.Input == nil {
		t.Error("模型名大小写不同也应匹配到渠道定价")
	}
}

func TestBuildPlazaModels_SkipsWildcardPricingEntries(t *testing.T) {
	groups := []repository.PGGroupModels{grp(1, "g", "anthropic", 1, "claude-opus-4-8")}
	pricing := []repository.PGChannelPricingRow{
		pricingRow(1, "ch", 1, fptr(2e-6), "claude-*"),
	}
	models := buildPlazaModels(groups, pricing, nil, nil, nil, emptyTable())
	if models[0].Price.Input != nil {
		t.Error("通配符定价条目不应被当作精确匹配")
	}
}

func TestBuildPlazaModels_StaticPriceFallback(t *testing.T) {
	table := parseStaticPriceTable([]byte(`{
		"claude-opus-4-8": {"input_cost_per_token": 5e-6, "output_cost_per_token": 25e-6}
	}`))
	groups := []repository.PGGroupModels{grp(1, "g", "anthropic", 1, "claude-opus-4-8")}

	t.Run("无渠道定价时全用静态价", func(t *testing.T) {
		models := buildPlazaModels(groups, nil, nil, nil, nil, table)
		if models[0].Price.Input == nil || *models[0].Price.Input != 5e-6 {
			t.Errorf("input = %v, want 5e-6", models[0].Price.Input)
		}
		if models[0].PriceSource != PriceSourceOfficial {
			t.Errorf("PriceSource = %q, want official", models[0].PriceSource)
		}
	})

	t.Run("渠道定价优先，仅补齐缺失字段", func(t *testing.T) {
		// 渠道只配了 input，output 应回落静态表
		pricing := []repository.PGChannelPricingRow{
			pricingRow(1, "ch", 1, fptr(3e-6), "claude-opus-4-8"),
		}
		models := buildPlazaModels(groups, pricing, nil, nil, nil, table)
		if models[0].Price.Input == nil || *models[0].Price.Input != 3e-6 {
			t.Errorf("input 应用渠道价 3e-6, got %v", models[0].Price.Input)
		}
		if models[0].Price.Output == nil || *models[0].Price.Output != 25e-6 {
			t.Errorf("output 应回落静态价 25e-6, got %v", models[0].Price.Output)
		}
		if models[0].PriceSource != PriceSourceMixed {
			t.Errorf("PriceSource = %q, want mixed", models[0].PriceSource)
		}
	})
}

func TestBuildPlazaModels_JoinsMetricsAndProbes(t *testing.T) {
	groups := []repository.PGGroupModels{grp(1, "g", "anthropic", 1, "m1", "m2")}
	metrics := []repository.PGModelMetric{
		{Model: "m1", RequestCount: 12, AvgDurationMs: 3400, TokensPerSecond: 82},
	}
	probes := []*repository.ModelProbeRow{{Model: "m1", Total: 5, SuccessCnt: 4}}

	models := buildPlazaModels(groups, nil, nil, metrics, probes, emptyTable())
	byName := map[string]PlazaModel{}
	for _, m := range models {
		byName[m.Name] = m
	}
	if byName["m1"].Metric == nil || byName["m1"].Metric.TokensPerSecond != 82 {
		t.Errorf("m1 metric 未 join: %+v", byName["m1"].Metric)
	}
	if byName["m1"].Probe == nil || byName["m1"].Probe.SuccessCnt != 4 {
		t.Errorf("m1 probe 未 join: %+v", byName["m1"].Probe)
	}
	if byName["m2"].Metric != nil || byName["m2"].Probe != nil {
		t.Error("m2 无指标数据，应保持 nil")
	}
}

func TestBuildPlazaModels_AttachesIntervals(t *testing.T) {
	groups := []repository.PGGroupModels{grp(1, "g", "anthropic", 1, "m1")}
	pricing := []repository.PGChannelPricingRow{pricingRow(7, "ch", 1, fptr(1e-6), "m1")}
	intervals := []repository.PGPricingInterval{
		{PricingID: 7, MinTokens: 0, MaxTokens: nil, InputPrice: fptr(1e-6)},
	}
	models := buildPlazaModels(groups, pricing, intervals, nil, nil, emptyTable())

	if !models[0].Price.HasIntervals {
		t.Error("存在阶梯定价时 HasIntervals 应为 true")
	}
	if len(models[0].Sources[0].Intervals) != 1 {
		t.Errorf("want 1 interval on source, got %d", len(models[0].Sources[0].Intervals))
	}
}

func TestBuildPlazaModels_CompositePlatformExcludedFromPlatforms(t *testing.T) {
	groups := []repository.PGGroupModels{grp(1, "mix", "composite", 1, "m1")}
	models := buildPlazaModels(groups, nil, nil, nil, nil, emptyTable())
	if len(models[0].Platforms) != 0 {
		t.Errorf("composite 不应作为供应商平台展示: %v", models[0].Platforms)
	}
}

func TestBuildPlazaModels_ExcludesModelsWithNoVisibleGroup(t *testing.T) {
	// 专属分组在 SQL 层已被排除，因此不会出现在入参里。
	// 这里验证：只要没有分组承载，模型就不会进入广场（而不是变成孤立卡片）。
	models := buildPlazaModels(nil, nil, nil, nil, nil, emptyTable())
	if len(models) != 0 {
		t.Errorf("无任何分组时应产出 0 个模型，got %d", len(models))
	}
}

func TestPickCheapestSource(t *testing.T) {
	tokenSrc := func(name string, input *float64) PlazaSource {
		return PlazaSource{ChannelName: name, BillingMode: BillingModeToken, Price: PlazaPrice{Input: input}}
	}

	t.Run("nil price treated as most expensive", func(t *testing.T) {
		got := pickCheapestSource([]PlazaSource{tokenSrc("a", nil), tokenSrc("b", fptr(2e-6))})
		if got.ChannelName != "b" {
			t.Errorf("got %q, want b", got.ChannelName)
		}
	})

	t.Run("all nil keeps first", func(t *testing.T) {
		got := pickCheapestSource([]PlazaSource{tokenSrc("a", nil), tokenSrc("b", nil)})
		if got.ChannelName != "a" {
			t.Errorf("got %q, want a", got.ChannelName)
		}
	})

	t.Run("per_request compares per-request price", func(t *testing.T) {
		cheap := PlazaSource{ChannelName: "cheap", BillingMode: BillingModePerRequest, Price: PlazaPrice{PerRequest: fptr(0.01)}}
		costly := PlazaSource{ChannelName: "costly", BillingMode: BillingModePerRequest, Price: PlazaPrice{PerRequest: fptr(0.05)}}
		if got := pickCheapestSource([]PlazaSource{costly, cheap}); got.ChannelName != "cheap" {
			t.Errorf("got %q, want cheap", got.ChannelName)
		}
	})

	t.Run("falls back to output when input missing", func(t *testing.T) {
		src := PlazaSource{BillingMode: BillingModeToken, Price: PlazaPrice{Output: fptr(5e-6)}}
		p := effectivePrice(src)
		if p == nil || *p != 5e-6 {
			t.Errorf("effectivePrice = %v, want 5e-6", p)
		}
	})

	t.Run("empty returns zero value with token mode", func(t *testing.T) {
		if got := pickCheapestSource(nil); got.BillingMode != BillingModeToken {
			t.Errorf("billing mode = %q, want token", got.BillingMode)
		}
	})
}

func TestMergeWithStaticPrice(t *testing.T) {
	t.Run("nil static keeps base", func(t *testing.T) {
		base := PlazaPrice{Input: fptr(1e-6)}
		got, used := mergeWithStaticPrice(base, nil)
		if used {
			t.Error("static 为 nil 时不应标记已使用")
		}
		if got.Input == nil || *got.Input != 1e-6 {
			t.Errorf("input = %v", got.Input)
		}
	})

	t.Run("does not override existing fields", func(t *testing.T) {
		base := PlazaPrice{Input: fptr(1e-6)}
		static := &StaticModelPrice{Input: fptr(9e-6), Output: fptr(2e-6)}
		got, used := mergeWithStaticPrice(base, static)
		if *got.Input != 1e-6 {
			t.Errorf("已有字段不应被覆盖: %v", *got.Input)
		}
		if got.Output == nil || *got.Output != 2e-6 {
			t.Errorf("缺失字段应被补齐: %v", got.Output)
		}
		if !used {
			t.Error("补齐了字段应标记已使用")
		}
	})
}

func TestNormalizeBillingMode(t *testing.T) {
	cases := map[string]string{
		"":            BillingModeToken,
		"weird":       BillingModeToken,
		"token":       BillingModeToken,
		"per_request": BillingModePerRequest,
		"image":       BillingModeImage,
	}
	for in, want := range cases {
		if got := normalizeBillingMode(in); got != want {
			t.Errorf("normalizeBillingMode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDefaultModelsForPlatform(t *testing.T) {
	if got := DefaultModelsForPlatform("anthropic"); len(got) == 0 {
		t.Error("anthropic 应有默认模型表")
	}
	if got := DefaultModelsForPlatform("nonexistent"); got != nil {
		t.Errorf("未知平台应返回 nil, got %v", got)
	}
	// 返回副本，调用方修改不影响原表
	a := DefaultModelsForPlatform("anthropic")
	a[0] = "mutated"
	if b := DefaultModelsForPlatform("anthropic"); b[0] == "mutated" {
		t.Error("应返回副本而非原切片")
	}
}

func TestEmbedSessionStore(t *testing.T) {
	store := NewEmbedSessionStore(time.Hour)
	defer store.Close()

	token, expiresIn, err := store.Create("42", "u@example.com")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if token == "" || expiresIn != 3600 {
		t.Fatalf("token=%q expiresIn=%d", token, expiresIn)
	}

	sess, ok := store.Get(token)
	if !ok || sess.UserID != "42" {
		t.Errorf("Get returned ok=%v sess=%+v", ok, sess)
	}
	if _, ok := store.Get("nope"); ok {
		t.Error("unknown token should not resolve")
	}
	if _, ok := store.Get(""); ok {
		t.Error("empty token should not resolve")
	}
}

func TestEmbedSessionStore_Expiry(t *testing.T) {
	store := NewEmbedSessionStore(time.Hour)
	defer store.Close()

	token, _, err := store.Create("1", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	store.mu.Lock()
	sess := store.sessions[token]
	sess.ExpiresAt = time.Now().Add(-time.Minute)
	store.sessions[token] = sess
	store.mu.Unlock()

	if _, ok := store.Get(token); ok {
		t.Error("expired session should not resolve")
	}
	store.mu.RLock()
	_, still := store.sessions[token]
	store.mu.RUnlock()
	if still {
		t.Error("expired session should be evicted on read")
	}
}
