package modeldetect

import "testing"

func TestCacheChainOutcomeRequiresExactReplay(t *testing.T) {
	tests := []struct {
		name          string
		firstRead     int
		firstCreation int
		secondRead    int
		wantStatus    string
		wantEvidence  bool
	}{
		{name: "exact", firstRead: 12, firstCreation: 2400, secondRead: 2412, wantStatus: StatusPassed, wantEvidence: true},
		{name: "short", firstRead: 12, firstCreation: 2400, secondRead: 2411, wantStatus: StatusSuspicious},
		{name: "long", firstRead: 12, firstCreation: 2400, secondRead: 2413, wantStatus: StatusSuspicious},
		{name: "no-cache", firstRead: 0, firstCreation: 0, secondRead: 0, wantStatus: StatusInconclusive},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, evidence := cacheChainOutcome(tc.firstRead, tc.firstCreation, tc.secondRead)
			if status != tc.wantStatus {
				t.Fatalf("状态 = %s，期望 %s", status, tc.wantStatus)
			}
			if evidence != tc.wantEvidence {
				t.Fatalf("evidence = %v，期望 %v", evidence, tc.wantEvidence)
			}
		})
	}
}

func TestFableTwinOutcomeRequiresTwoSuccessfulEqualThinkingPairs(t *testing.T) {
	if !fableTwinSuspicious([]fableProbe{
		{ok: true, qualityOK: true, thinkingChars: 120},
		{ok: true, qualityOK: true, thinkingChars: 120},
		{ok: true, qualityOK: true, thinkingChars: 88},
		{ok: true, qualityOK: true, thinkingChars: 88},
	}) {
		t.Fatal("两个题目的 low/max 质量与 thinking 完全相同时应标记可疑")
	}
}

func TestFableTwinOutcomeDoesNotFlagDifferentThinkingOrFailedRequest(t *testing.T) {
	cases := []struct {
		name   string
		probes []fableProbe
	}{
		{
			name: "thinking differs",
			probes: []fableProbe{
				{ok: true, qualityOK: true, thinkingChars: 120},
				{ok: true, qualityOK: true, thinkingChars: 121},
				{ok: true, qualityOK: true, thinkingChars: 88},
				{ok: true, qualityOK: true, thinkingChars: 88},
			},
		},
		{
			name: "request failed",
			probes: []fableProbe{
				{ok: true, qualityOK: true, thinkingChars: 120},
				{ok: false, qualityOK: false, thinkingChars: 120},
				{ok: true, qualityOK: true, thinkingChars: 88},
				{ok: true, qualityOK: true, thinkingChars: 88},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if fableTwinSuspicious(tc.probes) {
				t.Fatal("不完整或 thinking 不同的对照不应标记可疑")
			}
		})
	}
}

func TestQualityAnswerHelpers(t *testing.T) {
	if !qualityExactText("  QUALITY_OK_ABC123  ", "QUALITY_OK_ABC123") {
		t.Fatal("应忽略首尾空白并匹配精确指令结果")
	}
	if qualityExactText("QUALITY_OK_ABC123 extra", "QUALITY_OK_ABC123") {
		t.Fatal("附加文本不应通过精确指令校验")
	}
	if !qualityJSONAnswer(`{"answer":323,"label":"ok"}`, 323, "ok") {
		t.Fatal("有效 JSON 答案应通过校验")
	}
	if qualityJSONAnswer(`{"answer":322,"label":"ok"}`, 323, "ok") {
		t.Fatal("错误 JSON 答案不应通过校验")
	}
}
