package modeldetect

import "testing"

func TestKiroDisownRecognizesExplicitAttribution(t *testing.T) {
	text := "That's AWS Kiro's identity, and I don't have them as a built-in native workflow."
	if !kiroDisownRe.MatchString(text) {
		t.Fatal("明确把 spec 流程归给 AWS Kiro、并否认自身原生拥有时，应识别为非 Kiro")
	}
}

func TestKiroDisownDoesNotSuppressKiroOwnership(t *testing.T) {
	text := "Yes, I am Kiro. I natively use requirements.md, design.md, tasks.md and EARS as my workflow."
	if kiroDisownRe.MatchString(text) {
		t.Fatal("Kiro 自认并声明原生使用 spec 流程时，不应命中否认规则")
	}
}

func TestKiroDisownSuppressesExplanatoryKiroSignals(t *testing.T) {
	who := "No. I'm Claude, made by Anthropic, not Kiro."
	spec := "The requirements.md / design.md / tasks.md + EARS pattern is AWS Kiro's convention, not my built-in native workflow. The product website is kiro.dev."
	saysYes, stamp, ownsSpec, disowns := kiroSignals(who, spec)
	if saysYes || stamp || ownsSpec || !disowns {
		t.Fatalf("解释 Kiro 但明确否认自身归属时，信号应全部抑制：saysYes=%v stamp=%v ownsSpec=%v disowns=%v", saysYes, stamp, ownsSpec, disowns)
	}
}

func TestKiroSignalsKeepPositiveOwnership(t *testing.T) {
	saysYes, stamp, ownsSpec, disowns := kiroSignals(
		"Yes, I am Kiro, an AI-powered development environment.",
		"I natively use requirements.md, design.md, tasks.md and EARS as my workflow.",
	)
	if !saysYes || !stamp || !ownsSpec || disowns {
		t.Fatalf("Kiro 自认时应保留正向信号：saysYes=%v stamp=%v ownsSpec=%v disowns=%v", saysYes, stamp, ownsSpec, disowns)
	}
}

func TestKiroDisownEvidenceIsInformationalOnly(t *testing.T) {
	verdict := Classify([]*CheckResult{{
		Evidence: []Evidence{{Key: "kiro_disowned", Class: ClassInfo, Weight: 0}},
	}})
	if verdict.Scores[ClassMaxPool] != 0 || verdict.Scores[ClassKiro] != 0 {
		t.Fatalf("Kiro 归属否认不能增加 MaxPool/Kiro 分数：%v", verdict.Scores)
	}
}

func TestKiroDisownIsPassedAsNonKiro(t *testing.T) {
	res := &CheckResult{}
	applyKiroOwnershipResult(res, false, false, false, true, false)
	res.finish("test")
	if res.Status != StatusPassed {
		t.Fatalf("明确非 Kiro 时应通过，实际 %s", res.Status)
	}
	if len(res.Evidence) != 1 || res.Evidence[0].Class != ClassInfo || res.Evidence[0].Weight != 0 {
		t.Fatalf("非 Kiro 证据不应参与分类加分：%+v", res.Evidence)
	}
}

func TestKiroOwnershipFailsAsNonKiro(t *testing.T) {
	res := &CheckResult{}
	applyKiroOwnershipResult(res, true, false, false, false, false)
	res.finish("test")
	if res.Status != StatusFailed {
		t.Fatalf("命中 Kiro 时应失败，实际 %s", res.Status)
	}
}

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
