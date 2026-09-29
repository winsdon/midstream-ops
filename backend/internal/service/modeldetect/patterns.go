package modeldetect

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
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
	kiroStampRe  = regexp.MustCompile(`(?i)(ai-powered development environment|kiro\.dev)`)
	kiroSpecRe   = regexp.MustCompile(`(?i)(requirements\.md.*design\.md|design\.md.*tasks\.md|\bEARS\b)`)
	kiroDisownRe = regexp.MustCompile(`(?i)(not mine|not something i natively|not\s+(?:an?\s+)?(?:kiro|ide)\b|not\s+(?:my|a|an)\s+(?:built[- ]in\s+)?native\s+(?:workflow|feature|process)|don'?t\s+have\s+(?:\w+\s+){0,5}(?:as\s+)?(?:a\s+)?built[- ]in\s+native\s+(?:workflow|feature|process)|不属于|belongs?\s+to\s+(?:aws\s+)?kiro|that'?s\s+(?:aws\s+)?kiro)`)
	// affirmLeadRe 回答以肯定开头（配合 answerLead 使用）。中文不用 \b：「是的，」里「的」与「，」
	// 之间没有 ASCII 词边界；也不能只认一个「对」字，否则「对于……」会被当成肯定。
	affirmLeadRe = regexp.MustCompile(`(?i)^(` + affirmLead + `)`)
	// kiroOwnRe 以肯定句式开头地据为己有。只认「是 / 原生使用 / 作为 Kiro」，不认泛泛的「I use」：
	// 「I use them only when you ask」是在否认原生流程。
	kiroOwnRe = regexp.MustCompile(`(?i)^(` + affirmLead + `|i\s+(?:do\s+)?natively\s+use\b|i\s+natively\b|as\s+kiro\b)`)
	// leadNegationRe 回答以否定开头（配合 answerLead 使用）。
	leadNegationRe = regexp.MustCompile(`(?i)^(no\b|nope\b|not\b|none\b|never\b|i\s+do\s+not\b|i\s+don't\b|i\s+am\s+not\b|i'm\s+not\b|i\s+have\s+no\b|不|没有|否)`)
	amazonQRe      = regexp.MustCompile(`(?i)(amazon q|aws toolkit)`)
	bedrockClaimRe = regexp.MustCompile(`(?i)(aws bedrock|amazon bedrock|\bbedrock\b)`)
	vertexClaimRe  = regexp.MustCompile(`(?i)(vertex ai|google vertex|antigravity)`)

	// foreignBrandRe 其他家的模型/IDE 品牌词。出现在系统提示或自我介绍里 = 后端不是 Claude。
	foreignBrandRe = regexp.MustCompile(`(?i)(通义|千问|qwen|deepseek|kimi|moonshot|智谱|chatglm|\bglm-|minimax|豆包|doubao|文心|ernie|讯飞|星火|spark desk|gpt-4|gpt-5|openai|gemini|llama|mistral|grok|cursor|windsurf|trae|copilot)`)

	// pathLeakRe 本机路径泄露：Claude Code 号池的模板工作区特征。
	pathLeakRe = regexp.MustCompile(`(/Users/[\w.\-]+(?:/[\w.\-]+)+|/home/[\w.\-]+(?:/[\w.\-]+)+|[A-Za-z]:\\[\w.\-\\]+|~/\.claude(?:/[\w.\-]+)+)`)

	// noWorkspaceRe 明确报告没有工作区。
	noWorkspaceRe = regexp.MustCompile(`(?i)(no_workspace|no workspace|没有工作区|无法访问文件系统)`)

	// withholdRe 拒绝复述 system 里的内容。拒答说明模型看到了那段内容，不是「没送达」。
	// 匹配前先用 normalizeQuotes 把弯引号撇号换成直引号。
	withholdRe = regexp.MustCompile(`(?i)((?:can'?t|cannot|can not|won'?t|unable to|not able to|not allowed to|shouldn'?t)\s+(?:\w+\s+){0,2}(?:share|reveal|disclose|provide|give|tell)|keep (?:it|that|this|them) (?:to myself|private|confidential|secret)|meant to (?:keep|stay)|locked up|confidential|不能(?:透露|提供|告诉|分享)|无法(?:透露|提供|告诉|分享)|不便(?:透露|提供))`)
	// absentRe 声称上下文里根本没有这项信息：这是真没送达，不能当拒答放过。
	absentRe = regexp.MustCompile(`(?i)(don'?t (?:have|see)|do not (?:have|see)|can'?t (?:find|see)|cannot (?:find|see)|there(?:'s| is| was) no|no (?:session|such)\b|none (?:was|were|is|has)|(?:isn'?t|aren'?t|wasn'?t|weren'?t)\s+(?:\w+\s+){0,2}(?:given|provided|included|present|mentioned|available)|not (?:been )?(?:given|provided|included|present|mentioned)|nothing (?:like (?:that|this) |of the sort )?(?:appears|is present|was (?:given|provided|included))|not (?:in|present in|part of) (?:my|the|this) (?:context|conversation|system prompt|instructions)|没有|并无|未提供|看不到)`)

	// modelIDRe / modelAliasRe 自报模型名。
	modelIDRe    = regexp.MustCompile(`(?i)\b(claude-(?:opus|sonnet|haiku|fable|mythos)-[\w.\-]+)\b`)
	modelAliasRe = regexp.MustCompile(`(?i)\b(claude[\s-](?:opus|sonnet|haiku|fable|mythos)[\s-][\d.]+)\b`)

	// cutoffRe 自报知识截止日期，覆盖中英文常见写法。
	cutoffRe = regexp.MustCompile(`(?i)(20\d{2}\s*年\s*\d{1,2}\s*月|(?:Jan(?:uary)?|Feb(?:ruary)?|Mar(?:ch)?|Apr(?:il)?|May|Jun(?:e)?|Jul(?:y)?|Aug(?:ust)?|Sep(?:tember)?|Oct(?:ober)?|Nov(?:ember)?|Dec(?:ember)?)\s+20\d{2}|20\d{2}-\d{2}(?:-\d{2})?)`)

	// signatureRejectRe 篡改签名被拒时的报错特征。
	signatureRejectRe = regexp.MustCompile(`(?i)(signature|thinking.*invalid|invalid.*thinking|签名)`)

	// minBudgetHintRe thinking 预算过小时官方报错的特征：旧型号提示 1024 下限，4.7 起的型号
	// 直接说 thinking.type.enabled 不受支持（中转常把字段路径打成 ***.***，只剩 enabled）。
	minBudgetHintRe = regexp.MustCompile(`(?i)(1024|enabled"?\s+is not supported|not supported for this model)`)

	// extraFieldRejectRe 未知字段被拒时的报错特征。
	extraFieldRejectRe = regexp.MustCompile(`(?i)(extra inputs are not permitted|unexpected keyword|unrecognized|additional properties|not permitted|unknown field)`)

	// forcedToolChoiceRe 型号不支持强制工具调用时的官方报错：
	// tool_choice: type "tool" and "any" are not supported for this model.
	// 报文在原文里是 JSON 转义过的（\"tool\"），中转还常把字段路径打成 ***。
	forcedToolChoiceRe = regexp.MustCompile(`(?i)(tool_choice|\\?"tool\\?"\s+and\s+\\?"any\\?")[^.]{0,80}not supported`)
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
// 带日期后缀的 id 与不带后缀的视作同一型号（与查表共用 modelKeys）；平台形态的 id
// （anthropic.claude-opus-5-v1:0）按其中最具体的主型号 id 比，不放宽到只看大版本。
// 模型自报常只说到大版本（「Claude Opus 5」），所以按前缀宽松比；回显核对用更严的 sameModel。
func modelMatchesRequested(claimed, requested string) bool {
	want := map[string]bool{}
	for _, key := range modelKeys(requested) {
		want[compactModelName(key)] = true
	}
	if keys := familyModelKeys(requested); len(keys) > 0 {
		want[compactModelName(keys[0])] = true
	}
	if c, ok := LookupCutoff(requested); ok {
		want[compactModelName(c.Name)] = true
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

// sameModel 判断响应回显的型号与请求是否同一型号。回显是 API 原样返回的 id：去掉日期后缀后
// 字面相同即一致；否则读得出族与版本号时族、大版本、小版本都要一致——平台前后缀
// （global.anthropic.…-v1:0）不影响结果，但 claude-opus-5 顶替 claude-opus-5-5 算不一致。
// 读不出版本号时只认回显以请求开头（回显只会比请求多出日期或平台后缀），不认比请求短的回显。
func sameModel(got, want string) bool {
	undated := func(model string) string {
		keys := modelKeys(model)
		return compactModelName(keys[len(keys)-1])
	}
	w := undated(want)
	if undated(got) == w {
		return true
	}
	gv, gok := parseModelVersion(got)
	wv, wok := parseModelVersion(want)
	if gok && wok {
		return gv.family == wv.family && gv.major == wv.major && gv.minor == wv.minor
	}
	return w != "" && strings.HasPrefix(compactModelName(got), w)
}

// squash 把多余空白压成单个空格，便于把长回复塞进摘要。
func squash(s string) string { return strings.TrimSpace(whitespaceOnly.ReplaceAllString(s, " ")) }

// answerLead 回答的开头：去掉前导的 Markdown、引号、标点、列表序号与「Short answer:」这类标签
// （模型常以 **No.**、「不，」、「1. No…」开头），弯引号换成直引号并小写，供 leadNegationRe /
// affirmLeadRe / kiroOwnRe 从头匹配。
func answerLead(text string) string {
	s := strings.ToLower(normalizeQuotes(text))
	for i := 0; i < 3; i++ { // 标点、序号、标签可能层层叠着
		s = strings.TrimLeftFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
		t := answerLabelRe.ReplaceAllString(listMarkerRe.ReplaceAllString(s, ""), "")
		if t == s {
			break
		}
		s = t
	}
	return s
}

// normalizeQuotes 弯引号换成直引号：模型常输出 don’t / can’t，正则按直引号写。
func normalizeQuotes(s string) string { return quoteReplacer.Replace(s) }

var (
	quoteReplacer = strings.NewReplacer("’", "'", "‘", "'", "“", `"`, "”", `"`)
	listMarkerRe  = regexp.MustCompile(`^\d+[.)、]\s*`)
	answerLabelRe = regexp.MustCompile(`(?i)^(short answer|answer|tl;?dr|in short|简短回答|简答|回答|答)\s*[:：]\s*`)
)

// affirmLead 肯定开头的几种说法（affirmLeadRe / kiroOwnRe 共用）。
const affirmLead = `yes\b|yeah\b|yep\b|correct\b|是的|(?:是|对)(?:[，,。！!\s]|$)`

// clip 截断展示文本。
func clip(s string, n int) string {
	s = squash(s)
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}
