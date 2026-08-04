package service

import (
	"math"
	"testing"

	"sub2api-account-monitor/internal/repository"
)

// row 构造一行「分组 × 账号」用量，只关心分摊用得到的两个字段。
func row(groupID int64, requests int64, costWeight float64) repository.GroupAccountUsageRow {
	return repository.GroupAccountUsageRow{GroupID: groupID, Requests: requests, CostWeight: costWeight}
}

func TestApportionShares(t *testing.T) {
	cases := []struct {
		name string
		rows []repository.GroupAccountUsageRow
		want []float64
	}{
		{
			name: "按裸用量占比分摊",
			rows: []repository.GroupAccountUsageRow{
				row(1, 10, 30),
				row(2, 90, 10),
			},
			// 权重取 CostWeight 而非请求数：3:1 而不是 1:9
			want: []float64{0.75, 0.25},
		},
		{
			name: "用量全为 0 时退化为按请求数分摊",
			rows: []repository.GroupAccountUsageRow{
				row(1, 30, 0),
				row(2, 10, 0),
			},
			want: []float64{0.75, 0.25},
		},
		{
			name: "用量与请求数全为 0 时均分",
			rows: []repository.GroupAccountUsageRow{
				row(1, 0, 0),
				row(2, 0, 0),
				row(3, 0, 0),
			},
			want: []float64{1.0 / 3, 1.0 / 3, 1.0 / 3},
		},
		{
			name: "单分组独占整笔成本",
			rows: []repository.GroupAccountUsageRow{row(1, 5, 0)},
			want: []float64{1},
		},
		{
			name: "部分分组用量为 0 时不参与分摊",
			rows: []repository.GroupAccountUsageRow{
				row(1, 10, 40),
				row(2, 10, 0),
				row(3, 10, 60),
			},
			want: []float64{0.4, 0, 0.6},
		},
		{
			name: "无行时返回空",
			rows: nil,
			want: []float64{},
		},
	}

	const eps = 1e-9
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := apportionShares(tc.rows)
			if len(got) != len(tc.want) {
				t.Fatalf("份额个数 = %d, 期望 %d", len(got), len(tc.want))
			}
			var sum float64
			for i := range got {
				if math.Abs(got[i]-tc.want[i]) > eps {
					t.Errorf("shares[%d] = %v, 期望 %v", i, got[i], tc.want[i])
				}
				sum += got[i]
			}
			// 分摊既不能凭空造出成本也不能吞掉成本：非空输入的份额必须恰好和为 1
			if len(tc.rows) > 0 && math.Abs(sum-1) > eps {
				t.Errorf("份额之和 = %v, 期望 1", sum)
			}
		})
	}
}

// TestApportionSharesPreservesTotalCost 分摊后各分组成本相加须等于账号实扣，
// 这是「分组合计 ≡ 供应商合计」这一口径承诺的核心。
func TestApportionSharesPreservesTotalCost(t *testing.T) {
	rows := []repository.GroupAccountUsageRow{
		row(1, 100, 7.3),
		row(2, 3, 0.11),
		row(3, 55, 2.9),
	}
	const actualCost = 123.456

	var total float64
	for _, s := range apportionShares(rows) {
		total += actualCost * s
	}
	if math.Abs(total-actualCost) > 1e-9 {
		t.Errorf("分摊后成本合计 = %v, 期望 %v", total, actualCost)
	}
}
