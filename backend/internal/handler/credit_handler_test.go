package handler

import (
	"testing"

	"sub2api-account-monitor/internal/repository"
)

func TestToCustomerDTOEffectiveLowBalanceThreshold(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		override float64
		global   float64
		want     float64
	}{
		{name: "客户覆盖优先", override: 50, global: 10, want: 50},
		{name: "覆盖为 0 跟随全局", override: 0, global: 10, want: 10},
		{name: "覆盖与全局都为 0", override: 0, global: 0, want: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := toCustomerDTO(&repository.Customer{
				LowBalanceThreshold: tc.override,
			}, tc.global)
			if got.EffectiveLowBalanceThreshold != tc.want {
				t.Fatalf("effective_low_balance_threshold = %v, want %v",
					got.EffectiveLowBalanceThreshold, tc.want)
			}
			if got.LowBalanceThreshold != tc.override {
				t.Fatalf("low_balance_threshold 必须保留客户覆盖原值，得到 %v", got.LowBalanceThreshold)
			}
		})
	}
}
