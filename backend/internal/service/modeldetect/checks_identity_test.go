package modeldetect

import "testing"

func TestClaudeCodeSignalsDoNotIncreaseMaxPoolScore(t *testing.T) {
	verdict := Classify([]*CheckResult{{
		Evidence: []Evidence{
			{Key: "cc_persona", Class: ClassInfo, Weight: 0},
			{Key: "cc_prompt_leak", Class: ClassInfo, Weight: 0},
		},
	}})
	if verdict.Scores[ClassMaxPool] != 0 {
		t.Fatalf("Claude Code 证据不应增加 MaxPool 分数：%v", verdict.Scores)
	}
}
