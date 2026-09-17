package modeldetect

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestWithinRatioUsesAbsoluteFloor(t *testing.T) {
	if !withinRatio(105, 100, 0.35, 32) {
		t.Fatal("small token drift should pass")
	}
	if withinRatio(50, 100, 0.35, 32) {
		t.Fatal("large token deficit should fail")
	}
	if !withinRatio(70, 100, 0.45, 64) {
		t.Fatal("thinking drift below absolute floor should pass")
	}
	if withinRatio(10, 100, 0.45, 64) {
		t.Fatal("large thinking deficit should fail")
	}
}

func TestCompareBaselineFallsBackToThinkingChars(t *testing.T) {
	base := &BaselineStats{QualityOK: true, OutputTokens: 100, ThinkingChars: 200}
	actual := &BaselineStats{QualityOK: true, OutputTokens: 100, ThinkingChars: 190}
	quality, tokens, thinking, _ := compareBaseline(actual, base)
	if !quality || !tokens || !thinking {
		t.Fatalf("expected baseline comparison to pass: %v %v %v", quality, tokens, thinking)
	}
}

func TestBaselineRequestMatchesCCMaxTemplate(t *testing.T) {
	body := BaselineRequest("claude-fable-5")
	if body["max_tokens"] != 32000 {
		t.Fatalf("max_tokens=%v", body["max_tokens"])
	}
	if body["stream"] != false {
		t.Fatal("baseline must be non-streaming")
	}
	thinking := mapOf(body["thinking"])
	if str(thinking["type"]) != "adaptive" || str(thinking["display"]) != "summarized" {
		t.Fatalf("thinking=%v", thinking)
	}
	if str(mapOf(body["output_config"])["effort"]) != "high" {
		t.Fatal("effort must be high")
	}
}

func TestSameBaselineModel(t *testing.T) {
	if !SameBaselineModel("claude-opus-5", " claude-opus-5 ") {
		t.Fatal("trim + case should match")
	}
	if !SameBaselineModel("claude-opus-5", "Claude-Opus-5") {
		t.Fatal("case-insensitive match")
	}
	if SameBaselineModel("claude-opus-5", "claude-sonnet-4-6") {
		t.Fatal("different models must not match")
	}
}

func TestCheckBaselineQualitySkipsOnModelMismatch(t *testing.T) {
	st := &runState{baseline: &BaselineStats{Model: "claude-opus-5", QualityOK: true, OutputTokens: 100, ThinkingTokens: 80}}
	c := NewClient(Target{BaseURL: "https://example.invalid", APIKey: "sk-test-key-value", Model: "claude-sonnet-4-6", Timeout: time.Second})
	r := checkBaselineQuality(context.Background(), c, st)
	if r.Status != StatusInconclusive {
		t.Fatalf("status=%s", r.Status)
	}
	if !strings.Contains(r.Summary, "不一致") {
		t.Fatalf("summary=%s", r.Summary)
	}
}

func TestRunWithBaselineWiresStats(t *testing.T) {
	up := &fakeUpstream{behaviour: "official"}
	srv := newFakeServer(t, up)
	target, err := Target{
		Name: "t", BaseURL: srv.URL, APIKey: "sk-test-key-1234567890", Model: "claude-opus-5",
	}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	base := &BaselineStats{Model: "claude-opus-5", QualityOK: true, OutputTokens: 5, ThinkingTokens: 10, ThinkingChars: 10}
	run := RunWithBaseline(context.Background(), target, []string{"baseline-quality"}, NewGate(1), base, nil)
	var found *CheckResult
	for _, c := range run.Checks {
		if c.ID == "baseline-quality" {
			found = c
		}
	}
	if found == nil {
		t.Fatal("missing baseline-quality")
	}
	if strings.Contains(found.Summary, "未生成") {
		t.Fatalf("baseline was not wired: %s", found.Summary)
	}
}
