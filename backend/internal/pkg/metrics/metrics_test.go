package metrics

import (
	"math"
	"testing"
)

func TestSLAPercent(t *testing.T) {
	cases := []struct {
		name    string
		success int64
		errs    int64
		want    *float64
	}{
		{name: "全成功", success: 10, errs: 0, want: f(100)},
		{name: "全失败", success: 0, errs: 5, want: f(0)},
		{name: "对半", success: 1, errs: 1, want: f(50)},
		{name: "无样本返回 nil 而不是 0", success: 0, errs: 0, want: nil},
		{name: "1/3 成功", success: 1, errs: 2, want: f(100.0 / 3)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertFloatPtr(t, SLAPercent(c.success, c.errs), c.want)
		})
	}
}

func TestCacheHitRate(t *testing.T) {
	cases := []struct {
		name      string
		cacheRead int64
		input     int64
		want      *float64
	}{
		{name: "命中一半", cacheRead: 50, input: 50, want: f(50)},
		{name: "全命中", cacheRead: 100, input: 0, want: f(100)},
		// 0% 与 nil 是两件事：真的读了 100 输入且没命中，就是 0%
		{name: "完全没命中", cacheRead: 0, input: 100, want: f(0)},
		// 两边都 0 = 没走 token 的请求，不参与比例
		{name: "无样本返回 nil 而不是 0", cacheRead: 0, input: 0, want: nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertFloatPtr(t, CacheHitRate(c.cacheRead, c.input), c.want)
		})
	}
}

func TestTokensPerSecond(t *testing.T) {
	cases := []struct {
		name          string
		outputTokens  int64
		durationMsSum float64
		want          *float64
	}{
		{name: "100 token / 1s", outputTokens: 100, durationMsSum: 1000, want: f(100)},
		{name: "50 token / 2s", outputTokens: 50, durationMsSum: 2000, want: f(25)},
		{name: "零耗时返回 nil", outputTokens: 100, durationMsSum: 0, want: nil},
		{name: "负耗时返回 nil", outputTokens: 100, durationMsSum: -1, want: nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertFloatPtr(t, TokensPerSecond(c.outputTokens, c.durationMsSum), c.want)
		})
	}
}

func f(v float64) *float64 { return &v }

// assertFloatPtr 比较可空浮点：nil 与 nil 相等，其余按 1e-9 容差。
func assertFloatPtr(t *testing.T, got, want *float64) {
	t.Helper()
	if want == nil {
		if got != nil {
			t.Fatalf("want nil, got %v", *got)
		}
		return
	}
	if got == nil {
		t.Fatalf("want %v, got nil", *want)
	}
	if math.Abs(*got-*want) > 1e-9 {
		t.Fatalf("want %v, got %v", *want, *got)
	}
}
