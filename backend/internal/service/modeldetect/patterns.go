package modeldetect

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// 身份与人设识别正则。移植自 cctest-awsb / cctest-ccmax 两个参考脚本，
// 并补上国产模型与其他 IDE 逆向的品牌词。
var (
	// claudeCodeClaimRe 自称 Claude Code。号池注入 CC 人设是分类证据，不是造假证据。
	claudeCodeClaimRe = regexp.MustCompile(`(?i)(i'?m claude code|i am claude code|this is claude code|you are claude code|anthropic'?s official cli|我是\s*claude code|当前以\s*claude code|cli coding agent)`)

	// claudeCodeDenyRe 明确否认 Claude Code。
	claudeCodeDenyRe = regexp.MustCompile(`(?i)(not claude code|isn'?t claude code|i am not claude code|不是\s*claude code|no_claude_code_persona)`)

	// codeToolVocabRe Claude Code 特有的工具与文件词汇，泄露即说明系统提示来自 CC。
	codeToolVocabRe = regexp.MustCompile(`(?i)\b(todowrite|webfetch|websearch|notebookedit|claude\.md|system-reminder)\b`)

	// kiroStampRe Kiro 钢印句与其 spec 三件套。
	kiroStampRe    = regexp.MustCompile(`(?i)(ai-powered development environment|kiro\.dev)`)
	kiroSpecRe     = regexp.MustCompile(`(?i)(requirements\.md.*design\.md|design\.md.*tasks\.md|\bEARS\b)`)
	kiroDisownRe   = regexp.MustCompile(`(?i)(not mine|not something i natively|not\s+(?:an?\s+)?(?:kiro|ide)\b|not\s+(?:my|a|an)\s+(?:built[- ]in\s+)?native\s+(?:workflow|feature|process)|don'?t\s+have\s+(?:\w+\s+){0,5}(?:as\s+)?(?:a\s+)?built[- ]in\s+native\s+(?:workflow|feature|process)|不属于|belongs?\s+to\s+(?:aws\s+)?kiro|that'?s\s+(?:aws\s+)?kiro)`)
	kiroAffirmRe   = regexp.MustCompile(`(?i)^\s*(yes|是的|对)\b`)
	amazonQRe      = regexp.MustCompile(`(?i)(amazon q|aws toolkit)`)
	bedrockClaimRe = regexp.MustCompile(`(?i)(aws bedrock|amazon bedrock|\bbedrock\b)`)
	vertexClaimRe  = regexp.MustCompile(`(?i)(vertex ai|google vertex|antigravity)`)

	// foreignBrandRe 其他家的模型/IDE 品牌词。出现在系统提示或自我介绍里 = 后端不是 Claude。
	foreignBrandRe = regexp.MustCompile(`(?i)(通义|千问|qwen|deepseek|kimi|moonshot|智谱|chatglm|\bglm-|minimax|豆包|doubao|文心|ernie|讯飞|星火|spark desk|gpt-4|gpt-5|openai|gemini|llama|mistral|grok|cursor|windsurf|trae|copilot)`)

	// pathLeakRe 本机路径泄露：Claude Code 号池的模板工作区特征。
	pathLeakRe = regexp.MustCompile(`(/Users/[\w.\-]+(?:/[\w.\-]+)+|/home/[\w.\-]+(?:/[\w.\-]+)+|[A-Za-z]:\\[\w.\-\\]+|~/\.claude(?:/[\w.\-]+)+)`)

	// noWorkspaceRe 明确报告没有工作区。
	noWorkspaceRe = regexp.MustCompile(`(?i)(no_workspace|no workspace|没有工作区|无法访问文件系统)`)

	// modelIDRe / modelAliasRe 自报模型名。
	modelIDRe    = regexp.MustCompile(`(?i)\b(claude-(?:opus|sonnet|haiku|fable|mythos)-[\w.\-]+)\b`)
	modelAliasRe = regexp.MustCompile(`(?i)\b(claude[\s-](?:opus|sonnet|haiku|fable|mythos)[\s-][\d.]+)\b`)

	// cutoffRe 自报知识截止日期，覆盖中英文常见写法。
	cutoffRe = regexp.MustCompile(`(?i)(20\d{2}\s*年\s*\d{1,2}\s*月|(?:Jan(?:uary)?|Feb(?:ruary)?|Mar(?:ch)?|Apr(?:il)?|May|Jun(?:e)?|Jul(?:y)?|Aug(?:ust)?|Sep(?:tember)?|Oct(?:ober)?|Nov(?:ember)?|Dec(?:ember)?)\s+20\d{2}|20\d{2}-\d{2}(?:-\d{2})?)`)

	// signatureRejectRe 篡改签名被拒时的报错特征。
	signatureRejectRe = regexp.MustCompile(`(?i)(signature|thinking.*invalid|invalid.*thinking|签名)`)

	// minBudgetHintRe thinking 预算过小时官方报错里的 1024 提示。
	minBudgetHintRe = regexp.MustCompile(`1024`)

	// extraFieldRejectRe 未知字段被拒时的报错特征。
	extraFieldRejectRe = regexp.MustCompile(`(?i)(extra inputs are not permitted|unexpected keyword|unrecognized|additional properties|not permitted|unknown field)`)
)

var monthMap = map[string]string{
	"jan": "01", "feb": "02", "mar": "03", "apr": "04", "may": "05", "jun": "06",
	"jul": "07", "aug": "08", "sep": "09", "oct": "10", "nov": "11", "dec": "12",
}

var (
	isoCutoffRe    = regexp.MustCompile(`(20\d{2})-(\d{2})`)
	zhCutoffRe     = regexp.MustCompile(`(20\d{2})\s*年\s*(\d{1,2})\s*月`)
	enCutoffRe     = regexp.MustCompile(`(?i)([A-Za-z]{3})[a-z]*\.?\s+(20\d{2})`)
	whitespaceOnly = regexp.MustCompile(`\s+`)
)

// normalizeCutoff 把各种写法的截止日期归一到 YYYY-MM。无法识别返回空串。
func normalizeCutoff(raw string) string {
	s := strings.TrimSpace(raw)
	if m := isoCutoffRe.FindStringSubmatch(s); m != nil {
		return m[1] + "-" + m[2]
	}
	if m := zhCutoffRe.FindStringSubmatch(s); m != nil {
		month, _ := strconv.Atoi(m[2])
		return fmt.Sprintf("%s-%02d", m[1], month)
	}
	if m := enCutoffRe.FindStringSubmatch(s); m != nil {
		if mm, ok := monthMap[strings.ToLower(m[1])]; ok {
			return m[2] + "-" + mm
		}
	}
	return ""
}

// compactModelName 去掉空格、连字符、点，便于把 "Claude Opus 5" 与 "claude-opus-5" 视为同一个。
func compactModelName(s string) string {
	r := strings.NewReplacer(" ", "", "-", "", "_", "", ".", "")
	return r.Replace(strings.ToLower(strings.TrimSpace(s)))
}

// modelMatchesRequested 判断自报模型名是否与请求模型一致。
func modelMatchesRequested(claimed, requested string) bool {
	want := map[string]bool{compactModelName(requested): true}
	if c, ok := LookupCutoff(requested); ok {
		want[compactModelName(c.Name)] = true
	}
	// 带日期后缀的 id 与不带后缀的应视作同一型号
	if i := strings.LastIndex(strings.ToLower(requested), "-20"); i > 0 {
		want[compactModelName(requested[:i])] = true
	}
	got := compactModelName(claimed)
	for w := range want {
		if w == "" {
			continue
		}
		if got == w || strings.HasPrefix(got, w) || strings.HasPrefix(w, got) {
			return true
		}
	}
	return false
}

// squash 把多余空白压成单个空格，便于把长回复塞进摘要。
func squash(s string) string { return strings.TrimSpace(whitespaceOnly.ReplaceAllString(s, " ")) }

// clip 截断展示文本。
func clip(s string, n int) string {
	s = squash(s)
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}
