package service

import (
	"math"
	"testing"

	"sub2api-account-monitor/internal/repository"
)

func TestAttachKeyRevenue(t *testing.T) {
	acc := func(id int64) *int64 { return &id }
	items := []repository.KeyCostRow{
		{UpstreamKeyID: 1, AccountID: acc(10), Matched: true, ActualCost: 30},
		{UpstreamKeyID: 2, AccountID: acc(10), Matched: true, ActualCost: 10}, // 与 key 1 共享账号 10
		{UpstreamKeyID: 3, AccountID: acc(20), Matched: true, ActualCost: 0},
		{UpstreamKeyID: 4, AccountID: acc(30), Matched: true, ActualCost: 0}, // 账号 30 共享且零成本
		{UpstreamKeyID: 5, AccountID: acc(30), Matched: true, ActualCost: 0},
		{UpstreamKeyID: 6, Matched: false, ActualCost: 5},                    // 未匹配：无收益
		{UpstreamKeyID: 7, AccountID: acc(40), Matched: true, ActualCost: 2}, // 区间内无流量
	}
	revenue := map[int64]float64{10: 100, 20: 7, 30: 8, 99: 1000}

	out, total := AttachKeyRevenue(items, revenue)

	want := map[int64]*float64{1: floatPtr(75), 2: floatPtr(25), 3: floatPtr(7), 4: floatPtr(4), 5: floatPtr(4), 6: nil, 7: floatPtr(0)}
	for _, k := range out {
		w := want[k.UpstreamKeyID]
		switch {
		case w == nil && k.Revenue != nil:
			t.Errorf("key %d revenue=%v, want nil", k.UpstreamKeyID, *k.Revenue)
		case w != nil && (k.Revenue == nil || math.Abs(*k.Revenue-*w) > 1e-9):
			t.Errorf("key %d revenue=%v, want %v", k.UpstreamKeyID, k.Revenue, *w)
		}
	}
	// 合计按账号去重：只算明细里出现的账号（10/20/30/40），账号 99 不属于该供应商
	if math.Abs(total-115) > 1e-9 {
		t.Errorf("total=%v, want 115", total)
	}
	if items[0].Revenue != nil {
		t.Error("input rows must not be mutated")
	}
}
