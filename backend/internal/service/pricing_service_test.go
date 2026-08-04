package service

import "testing"

// TestThresholdExceeded 跟随阈值语义（极易理解反，故用例写明预期）：
// 上游变化幅度 ≤ 阈值 → 自动跟随（不超限）；超过阈值 → 不动，留给人工。
func TestThresholdExceeded(t *testing.T) {
	cases := []struct {
		name      string
		oldRef    float64
		newRef    float64
		threshold float64
		want      bool // true = 超限，不自动跟随
	}{
		{name: "变化 8% 未超 10% 阈值 → 跟随", oldRef: 1.0, newRef: 1.08, threshold: 10, want: false},
		{name: "变化恰好等于阈值 → 跟随（等于不算超）", oldRef: 1.0, newRef: 1.10, threshold: 10, want: false},
		{name: "变化 20% 超过 10% 阈值 → 不跟随", oldRef: 1.0, newRef: 1.20, threshold: 10, want: true},
		{name: "下跌 8% 未超阈值 → 跟随", oldRef: 1.0, newRef: 0.92, threshold: 10, want: false},
		{name: "下跌 30% 超阈值 → 不跟随", oldRef: 1.0, newRef: 0.70, threshold: 10, want: true},
		{name: "阈值 0 时任何变化都超限", oldRef: 1.0, newRef: 1.001, threshold: 0, want: true},
		{name: "阈值 0 且无变化 → 不超限", oldRef: 1.0, newRef: 1.0, threshold: 0, want: false},
		{name: "旧值为 0 时不判超限（避免除零）", oldRef: 0, newRef: 1.0, threshold: 10, want: false},
		{name: "浮点边界：10% 的浮点误差不应误判", oldRef: 0.3, newRef: 0.33, threshold: 10, want: false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := thresholdExceeded(c.oldRef, c.newRef, c.threshold); got != c.want {
				t.Errorf("thresholdExceeded(%v, %v, %v) = %v，期望 %v",
					c.oldRef, c.newRef, c.threshold, got, c.want)
			}
		})
	}
}
