package repository

import (
	"math"
	"testing"
)

// ptr 便捷取地址。
func fptr(v float64) *float64 { return &v }
func iptr(v int64) *int64     { return &v }
func sptr(v string) *string   { return &v }

func TestReferenceAggregation(t *testing.T) {
	sources := []PricingSource{
		{ProviderID: 1, UpstreamGroup: "a"},
		{ProviderID: 2, UpstreamGroup: "b"},
		{ProviderID: 3, UpstreamGroup: "c"},
	}
	rates := map[string]float64{
		SourceKey(1, "a"): 1.0,
		SourceKey(2, "b"): 2.0,
		SourceKey(3, "c"): 3.0,
	}

	cases := []struct {
		name    string
		pricing LocalGroupPricing
		rates   map[string]float64
		want    float64
		wantOK  bool
	}{
		{
			name:    "primary 取指定主上游",
			pricing: LocalGroupPricing{PriceSource: PriceSourcePrimary, PrimaryProvider: iptr(2), PrimaryGroup: sptr("b"), Sources: sources},
			rates:   rates, want: 2.0, wantOK: true,
		},
		{
			name:    "primary 主上游数据缺失时失败",
			pricing: LocalGroupPricing{PriceSource: PriceSourcePrimary, PrimaryProvider: iptr(9), PrimaryGroup: sptr("x"), Sources: sources},
			rates:   rates, wantOK: false,
		},
		{
			name:    "primary 未指定主上游时失败",
			pricing: LocalGroupPricing{PriceSource: PriceSourcePrimary, Sources: sources},
			rates:   rates, wantOK: false,
		},
		{
			name:    "lowest 取最低",
			pricing: LocalGroupPricing{PriceSource: PriceSourceLowest, Sources: sources},
			rates:   rates, want: 1.0, wantOK: true,
		},
		{
			name:    "highest 取最高",
			pricing: LocalGroupPricing{PriceSource: PriceSourceHighest, Sources: sources},
			rates:   rates, want: 3.0, wantOK: true,
		},
		{
			name:    "average 取算术平均",
			pricing: LocalGroupPricing{PriceSource: PriceSourceAverage, Sources: sources},
			rates:   rates, want: 2.0, wantOK: true,
		},
		{
			name:    "聚合模式跳过缺失数据源",
			pricing: LocalGroupPricing{PriceSource: PriceSourceLowest, Sources: sources},
			rates:   map[string]float64{SourceKey(2, "b"): 2.0}, want: 2.0, wantOK: true,
		},
		{
			name:    "全部数据源缺失时失败",
			pricing: LocalGroupPricing{PriceSource: PriceSourceAverage, Sources: sources},
			rates:   map[string]float64{}, wantOK: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := c.pricing.Reference(c.rates)
			if ok != c.wantOK {
				t.Fatalf("ok 应为 %v，实际 %v", c.wantOK, ok)
			}
			if ok && math.Abs(got-c.want) > 1e-9 {
				t.Errorf("参考价应为 %v，实际 %v", c.want, got)
			}
		})
	}
}

func TestTargetCalculation(t *testing.T) {
	cases := []struct {
		name    string
		pricing LocalGroupPricing
		ref     float64
		want    float64
	}{
		{
			name:    "固定加价",
			pricing: LocalGroupPricing{MarkupMode: MarkupFixed, MarkupValue: 0.1},
			ref:     1.0, want: 1.1,
		},
		{
			name:    "百分比加价",
			pricing: LocalGroupPricing{MarkupMode: MarkupPercentage, MarkupValue: 10},
			ref:     1.0, want: 1.1,
		},
		{
			name:    "百分比加价非整数",
			pricing: LocalGroupPricing{MarkupMode: MarkupPercentage, MarkupValue: 15},
			ref:     1.08, want: 1.242,
		},
		{
			name:    "低于下限被夹紧",
			pricing: LocalGroupPricing{MarkupMode: MarkupFixed, MarkupValue: 0, MinRate: fptr(1.5)},
			ref:     1.0, want: 1.5,
		},
		{
			name:    "高于上限被夹紧",
			pricing: LocalGroupPricing{MarkupMode: MarkupPercentage, MarkupValue: 100, MaxRate: fptr(1.3)},
			ref:     1.0, want: 1.3,
		},
		{
			name:    "区间内不夹紧",
			pricing: LocalGroupPricing{MarkupMode: MarkupFixed, MarkupValue: 0.1, MinRate: fptr(1.0), MaxRate: fptr(1.3)},
			ref:     1.0, want: 1.1,
		},
		{
			name:    "结果保留 4 位小数",
			pricing: LocalGroupPricing{MarkupMode: MarkupPercentage, MarkupValue: 3.33333},
			ref:     1.0, want: 1.0333,
		},
		{
			name:    "负加价（折扣）",
			pricing: LocalGroupPricing{MarkupMode: MarkupPercentage, MarkupValue: -20},
			ref:     1.0, want: 0.8,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.pricing.Target(c.ref)
			if math.Abs(got-c.want) > 1e-9 {
				t.Errorf("目标倍率应为 %v，实际 %v", c.want, got)
			}
		})
	}
}

// TestClampOrderMinWinsOverMax min > max 时的行为：先夹 min 再夹 max，故 max 生效。
// 这是防御性文档：handler 层已校验 min <= max，此处只固化实现行为。
func TestClampOrder(t *testing.T) {
	p := LocalGroupPricing{MarkupMode: MarkupFixed, MarkupValue: 0, MinRate: fptr(2.0), MaxRate: fptr(1.0)}
	if got := p.Target(1.5); math.Abs(got-1.0) > 1e-9 {
		t.Errorf("min>max 时应以 max 收尾，实际 %v", got)
	}
}
