package service

import "testing"

func TestStaticPriceTable_LoadsEmbedded(t *testing.T) {
	table := LoadStaticPriceTable()
	if table.Size() < 100 {
		t.Fatalf("内嵌价表只有 %d 条，疑似未正确加载", table.Size())
	}
}

func TestStaticPriceTable_Lookup(t *testing.T) {
	table := LoadStaticPriceTable()

	cases := []struct {
		name  string
		model string
		want  bool // 是否应命中
	}{
		{"精确名", "claude-opus-4-8", true},
		{"大小写不敏感", "CLAUDE-OPUS-4-8", true},
		{"首尾空白", "  claude-opus-4-8  ", true},
		{"thinking 变体回落基础模型", "claude-opus-4-8-thinking", true},
		{"sonnet thinking 变体", "claude-sonnet-5-thinking", true},
		{"带日期版本号", "claude-haiku-4-5-20251001", true},
		{"gemini", "gemini-2.5-pro", true},
		{"空字符串", "", false},
		{"完全不存在", "totally-made-up-model-xyz", false},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := table.Lookup(tt.model)
			if tt.want && got == nil {
				t.Errorf("Lookup(%q) = nil，期望命中", tt.model)
			}
			if !tt.want && got != nil {
				t.Errorf("Lookup(%q) 意外命中: %+v", tt.model, got)
			}
		})
	}
}

func TestStaticPriceTable_ThinkingMatchesBase(t *testing.T) {
	// thinking 变体应与基础模型同价（sub2api 计费口径一致）。
	table := LoadStaticPriceTable()
	base := table.Lookup("claude-opus-4-8")
	thinking := table.Lookup("claude-opus-4-8-thinking")
	if base == nil || thinking == nil {
		t.Skip("价表中无该模型，跳过")
	}
	if base.Input == nil || thinking.Input == nil || *base.Input != *thinking.Input {
		t.Errorf("thinking 变体价格应与基础模型一致: base=%v thinking=%v", base.Input, thinking.Input)
	}
}

func TestBaseModelName(t *testing.T) {
	cases := map[string]string{
		"claude-opus-4-5-20251101": "claude-opus-4-5",
		"claude-haiku-4-5-20251001": "claude-haiku-4-5",
		"claude-opus-4-8":          "claude-opus-4-8", // 无版本号后缀，原样返回
		"gemini-2.5-pro":           "gemini-2.5-pro",
	}
	for in, want := range cases {
		if got := baseModelName(in); got != want {
			t.Errorf("baseModelName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseStaticPriceTable_Malformed(t *testing.T) {
	// 损坏的 JSON 应退化为空表而非 panic。
	tbl := parseStaticPriceTable([]byte("{ not json"))
	if tbl == nil {
		t.Fatal("解析失败时应返回空表而非 nil")
	}
	if tbl.Size() != 0 {
		t.Errorf("损坏输入应得到空表，实际 %d 条", tbl.Size())
	}
	if tbl.Lookup("anything") != nil {
		t.Error("空表查询应返回 nil")
	}
}

func TestParseStaticPriceTable_SkipsPricelessEntries(t *testing.T) {
	// 条目存在但所有价格字段为空 → 视为未配置，不入表
	//（对齐 sub2api channel_available.go 的 pricingNeedsFallback 判定）。
	raw := []byte(`{
		"has-price":  {"input_cost_per_token": 0.000003},
		"no-price":   {"max_tokens": 8192},
		"null-price": {"input_cost_per_token": null}
	}`)
	tbl := parseStaticPriceTable(raw)
	if tbl.Lookup("has-price") == nil {
		t.Error("有价条目应入表")
	}
	if tbl.Lookup("no-price") != nil {
		t.Error("无价格字段的条目不应入表")
	}
	if tbl.Lookup("null-price") != nil {
		t.Error("价格字段全为 null 的条目不应入表")
	}
}
