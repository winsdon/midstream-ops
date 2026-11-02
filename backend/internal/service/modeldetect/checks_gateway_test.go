package modeldetect

import "testing"

func TestCacheChainOutcomeClassifiesThreeRequestChain(t *testing.T) {
	tests := []struct {
		name  string
		chain cacheChain
		want  string
	}{
		{
			name:  "逐级推进",
			chain: cacheChain{write1: 2400, read2: 2400, write2: 900, read3: 3300, write3: 900},
			want:  cacheChainPassed,
		},
		{
			name:  "首轮就命中（新 nonce 不该命中）",
			chain: cacheChain{write1: 2400, read1: 4030, read2: 4030, read3: 4030},
			want:  cacheChainPreHit,
		},
		{
			name:  "3 次都无缓存用量",
			chain: cacheChain{},
			want:  cacheChainUnavailable,
		},
		{
			name:  "写完不命中",
			chain: cacheChain{write1: 4033, write2: 4033, write3: 4033},
			want:  cacheChainNeverHits,
		},
		{
			name:  "第三次没把第二次写进去的读回来",
			chain: cacheChain{write1: 2400, read2: 2400, write2: 900, read3: 2400, write3: 900},
			want:  cacheChainDrift,
		},
		{
			name:  "第二次读取量与首次写入不符",
			chain: cacheChain{write1: 4033, read2: 4024, write2: 900, read3: 4924, write3: 900},
			want:  cacheChainDrift,
		},
		{
			// 只查不变式的话这一例会「通过」：read₃ = read₂ 天然成立。
			// 但三次请求测的是同一件事，前缀根本没增长。
			name:  "后续请求零写入",
			chain: cacheChain{write1: 2400, read2: 2400, read3: 2400},
			want:  cacheChainNotExtended,
		},
		{
			name:  "第三次零写入",
			chain: cacheChain{write1: 2400, read2: 2400, write2: 900, read3: 3300},
			want:  cacheChainNotExtended,
		},
		{
			name:  "首轮未写却命中（缓存来源不是本次请求）",
			chain: cacheChain{read2: 2400, read3: 2400},
			want:  cacheChainMismatch,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := tc.chain.outcome()
			if got != tc.want {
				t.Fatalf("结局 = %s，期望 %s", got, tc.want)
			}
			if tc.want == cacheChainPassed && reason != "" {
				t.Fatalf("通过的链不该带原因，实际 %q", reason)
			}
			if tc.want != cacheChainPassed && reason == "" {
				t.Fatal("非通过结局必须给出可读原因")
			}
		})
	}
}
