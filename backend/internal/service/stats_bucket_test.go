package service

import "testing"

// 成本完整性判定：只有「非豁免 + 未匹配 + 有流量」三者同时成立才算不完整。
//
// 自营站账号必须豁免：其上游实扣被有意计 0，且站点可能根本没接上游采集
// （balance_type=none），账号永远匹配不到成本行。不豁免会让 ⚠ 变成永久噪音，
// 用户每天看到与自己无关的告警，真有问题时反而忽略。
func TestStatBucketCostCompleteness(t *testing.T) {
	tests := []struct {
		name        string
		matched     bool
		requests    int64
		exempt      bool
		wantMissing int
		wantOK      bool
	}{
		{
			name:    "已匹配有流量 → 完整",
			matched: true, requests: 100, exempt: false,
			wantMissing: 0, wantOK: true,
		},
		{
			name:    "未匹配有流量 → 不完整",
			matched: false, requests: 100, exempt: false,
			wantMissing: 1, wantOK: false,
		},
		{
			// 零流量账号没有成本很正常，不该报警
			name:    "未匹配零流量 → 完整",
			matched: false, requests: 0, exempt: false,
			wantMissing: 0, wantOK: true,
		},
		{
			// 本次修复的核心场景
			name:    "自营站未匹配有流量 → 完整（豁免）",
			matched: false, requests: 100, exempt: true,
			wantMissing: 0, wantOK: true,
		},
		{
			name:    "自营站已匹配有流量 → 完整",
			matched: true, requests: 100, exempt: true,
			wantMissing: 0, wantOK: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &statBucket{CostComplete: true}
			b.add(AccountStat{
				AccountID:   1,
				Requests:    tt.requests,
				Revenue:     50,
				CostMatched: tt.matched,
			}, tt.exempt)

			if b.AccountsMissing != tt.wantMissing {
				t.Errorf("AccountsMissing = %d, want %d", b.AccountsMissing, tt.wantMissing)
			}
			if b.CostComplete != tt.wantOK {
				t.Errorf("CostComplete = %v, want %v", b.CostComplete, tt.wantOK)
			}
		})
	}
}

// 一个桶里混有正常账号与自营账号时，只有正常账号的缺失才计数。
func TestStatBucketMixedAccounts(t *testing.T) {
	b := &statBucket{CostComplete: true}
	b.add(AccountStat{AccountID: 1, Requests: 100, Revenue: 10, CostMatched: true}, false)
	b.add(AccountStat{AccountID: 2, Requests: 200, Revenue: 20, CostMatched: false}, true)  // 自营，豁免
	b.add(AccountStat{AccountID: 3, Requests: 300, Revenue: 30, CostMatched: false}, false) // 真缺失

	if b.AccountsMissing != 1 {
		t.Errorf("AccountsMissing = %d, want 1（只算 #3）", b.AccountsMissing)
	}
	if b.CostComplete {
		t.Error("CostComplete 应为 false（#3 真缺失）")
	}
	// 豁免不影响金额累加
	if b.Revenue != 60 {
		t.Errorf("Revenue = %v, want 60", b.Revenue)
	}
	if b.Requests != 600 {
		t.Errorf("Requests = %v, want 600", b.Requests)
	}
}

// 运营成本参与桶级利润，但不摊到子账号明细。
func TestStatBucketFinalizeDeductsOperatingCost(t *testing.T) {
	b := &statBucket{CostComplete: true}
	b.add(AccountStat{AccountID: 1, Requests: 10, Revenue: 100, Cost: 30, CostMatched: true}, false)
	b.OperatingCost = 25
	b.finalize()

	if b.Profit != 45 { // 100 − 30 − 25
		t.Errorf("Profit = %v, want 45（收益 100 − 实扣 30 − 运营 25）", b.Profit)
	}
	// 子账号利润不含运营成本，故其和大于桶级利润，差额即运营成本
	if b.Accounts[0].Profit != 0 {
		t.Logf("子账号 Profit = %v（由调用方填充，此处未设置）", b.Accounts[0].Profit)
	}
}
