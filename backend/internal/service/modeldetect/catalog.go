package modeldetect

import "context"

// 检测项状态。与参考测试台一致：区分「协议失败」「目标不支持」「证据不足」「结果可疑」，
// 不把临时故障当能力否定。
const (
	StatusRunning      = "running"      // 正在执行（仅作业快照，不落库）
	StatusPassed       = "passed"       // 观察通过
	StatusFailed       = "failed"       // 协议失败
	StatusUnsupported  = "unsupported"  // 目标不支持
	StatusInconclusive = "inconclusive" // 证据不足
	StatusSuspicious   = "suspicious"   // 结果可疑
)

// 渠道类别。分数落在这六类上，最高者即主判定。
const (
	ClassMaxPool  = "claude_max_pool" // 官方 Claude Max/Pro 订阅号池（OAuth）
	ClassOfficial = "official_api"    // 官方 Messages API（普通 API Key）
	ClassBedrock  = "aws_bedrock"     // AWS Bedrock
	ClassKiro     = "kiro"            // Kiro / AWS IDE 反代
	ClassVertex   = "vertex"          // Google Vertex / Antigravity
	ClassWrapper  = "wrapper"         // 洗过的包装 / 伪装渠道
	ClassInfo     = "info"            // 仅展示，不参与打分
)

// 检测项分组。
const (
	GroupGateway    = "gateway"    // 连通与网关指纹
	GroupProtocol   = "protocol"   // 协议严格性
	GroupIdentity   = "identity"   // 身份与人设
	GroupCapability = "capability" // 能力与采样
	GroupIQ         = "iq"         // 智商测试
)

// 测试套件。真伪检测回答「渠道背后是谁、后端是不是真 Claude」，产出分类与真实性评分；
// 智商测试回答「有没有降智」，只看答题结果，不出判定也不跑跨项审计。
// 两套分开勾选、分开执行、分开留痕，一次作业只跑一个套件。
const (
	SuiteAuthenticity = "authenticity"
	SuiteIQ           = "iq"
)

// 智商题的判分方式（前端据此决定结果怎么摆）。
const (
	GradingVisual = "visual" // 交出作品，画面好坏人工评判
	GradingAnswer = "answer" // 有标准答案，自动核对
)

// 成本档位（前端展示，用于提醒会消耗多少额度）。
const (
	CostLow    = "low"
	CostMedium = "medium"
	CostHigh   = "high"
)

// Assertion 一条断言。Diagnostic 为 true 表示不满足也不算失败，只作诊断线索。
type Assertion struct {
	Label      string `json:"label"`
	OK         bool   `json:"ok"`
	Detail     string `json:"detail,omitempty"`
	Diagnostic bool   `json:"diagnostic,omitempty"`
}

// Evidence 一条指向某个渠道类别的证据。Weight 为该类别加分。
type Evidence struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
	Class  string `json:"class"`
	Weight int    `json:"weight"`
}

// CheckResult 单个检测项的结果。
//
// AuthScore 与 AuthCapReason 是真实性评分的输入：前者是本项对「后端确实是 Claude」
// 的正向贡献，后者非空表示观察到了造假硬证据，会把总分封顶。
type CheckResult struct {
	ID            string      `json:"id"`
	Title         string      `json:"title"`
	Group         string      `json:"group"`
	Status        string      `json:"status"`
	Summary       string      `json:"summary"`
	DurationMs    int64       `json:"duration_ms"`
	Assertions    []Assertion `json:"assertions,omitempty"`
	Evidence      []Evidence  `json:"evidence,omitempty"`
	Exchanges     []*Exchange `json:"exchanges,omitempty"`
	AuthScore     int         `json:"auth_score,omitempty"`
	AuthCapReason string      `json:"auth_cap_reason,omitempty"`
	// Informational 只做检测、不参与分类与真实性评分（也不进跨项审计）。
	Informational bool `json:"informational,omitempty"`
	// Prompt 智商题本轮实际发送的提示词；自定义题目需要随报告留痕。
	Prompt string `json:"prompt,omitempty"`
	// Output 需要人工复核的模型产出（智商测试）。
	Output *ModelOutput `json:"output,omitempty"`
}

// ModelOutput 智商题的完整作答。
//
// exchange 原文截断到 8000 字符，流式时还是 SSE 分片，既看不出完整回答也渲染不了作品，
// 而这类题的价值恰恰在全文，所以单独保存。
type ModelOutput struct {
	// Text 可见回复（不含 thinking），超长截断。
	Text string `json:"text,omitempty"`
	// Document 从回复里取出的完整 SVG / HTML 作品，前端只在沙箱 iframe 里渲染。
	Document string `json:"document,omitempty"`
	// DocumentKind 取 svg / html。
	DocumentKind string `json:"document_kind,omitempty"`
	// Answer 从回复里解析出的最终答案；Expected 为标准答案。只有自动判分的题目才有。
	Answer   string `json:"answer,omitempty"`
	Expected string `json:"expected,omitempty"`
}

// addEvidence 追加一条证据。
func (r *CheckResult) addEvidence(key, label, detail, class string, weight int) {
	r.Evidence = append(r.Evidence, Evidence{Key: key, Label: label, Detail: detail, Class: class, Weight: weight})
}

// assert 追加一条断言。
func (r *CheckResult) assert(label string, ok bool, detail string) {
	r.Assertions = append(r.Assertions, Assertion{Label: label, OK: ok, Detail: detail})
}

// diagnose 追加一条诊断断言（不满足不判失败）。
func (r *CheckResult) diagnose(label string, ok bool, detail string) {
	r.Assertions = append(r.Assertions, Assertion{Label: label, OK: ok, Detail: detail, Diagnostic: true})
}

// finish 按断言结果决定状态：非诊断断言全过为 passed，
// 出现网络错误 / 429 / 5xx 则记「证据不足」，不当能力失败。
func (r *CheckResult) finish(summary string) *CheckResult {
	r.Summary = summary
	if r.Status != "" {
		return r
	}
	allOK := true
	for _, a := range r.Assertions {
		if !a.Diagnostic && !a.OK {
			allOK = false
			break
		}
	}
	if allOK {
		r.Status = StatusPassed
		return r
	}
	if transient(r.Exchanges) {
		r.Status = StatusInconclusive
		return r
	}
	r.Status = StatusFailed
	return r
}

// transient 判断失败是否来自临时故障。
func transient(exchanges []*Exchange) bool {
	for _, ex := range exchanges {
		if ex == nil {
			continue
		}
		if ex.NetworkError != "" || ex.Status == 408 || ex.Status == 429 || ex.Status >= 500 {
			return true
		}
	}
	return false
}

// RequestFailed 该项失败来自请求本身（网络错误 / 超时 / 429 / 5xx），
// 而不是协议断言未通过。这类结果值得重试；协议失败再打一遍没有新信息。
//
// 只检测的项内部请求多（「是否 0 注入」一次 27 个），零星 429 不影响结论，
// 只有一个请求都没成功时才算请求失败——否则每点一次「重试失败项」都要把 27 个请求重打一遍。
func RequestFailed(r *CheckResult) bool {
	if r == nil || r.Status == StatusRunning {
		return false
	}
	if r.Informational && anyExchangeSucceeded(r.Exchanges) {
		return false
	}
	return transient(r.Exchanges)
}

// newResult 按目录元信息初始化结果。
func newResult(meta Check) *CheckResult {
	return &CheckResult{ID: meta.ID, Title: meta.Title, Group: meta.Group, Informational: meta.Informational}
}

// runState 检测项之间共享的上下文。
//
// 只有真正互相依赖的项才读它：签名篡改要复用上一步拿到的 thinking 块，
// count_tokens 要和基础请求的 input_tokens 对齐。
type runState struct {
	profile  ThinkingProfile
	baseline *BaselineStats
	// pelicanPrompt 是本轮鹈鹕测试的提示词。空值时由 checkPelican 回退内置原题。
	pelicanPrompt string

	// pingUsage 基础请求的 usage，用于估算注入量与缓存命中。
	pingUsage      map[string]any
	pingInput      int
	pingCacheWrite int
	pingSeen       bool

	// thinkingContent 是带签名的 assistant content 原样快照（供签名回传）。
	thinkingContent []any
	thinkingIndex   int
	thinkingParam   map[string]any
	thinkingPrompt  string
	// thinkingUsage 取签名那次请求的用量，签名回传时用来确认 thinking 进了上下文。
	thinkingUsage thinkingUsage
}

// thinkingUsage 取签名那次请求的用量。
type thinkingUsage struct {
	input  int // 输入总量（含缓存读写）
	output int // output_tokens
	// thoughts 其中的 thinking token（output_tokens_details.thinking_tokens）；split 为 false 表示渠道没拆分。
	thoughts int
	split    bool
}

// thinkingUsageOf 从一次响应的 usage 读出签名回传要用的用量。
func thinkingUsageOf(usage map[string]any) thinkingUsage {
	u := thinkingUsage{output: intOf(usage["output_tokens"])}
	u.input, _, _ = inputTotal(usage)
	u.thoughts, u.split = thinkingTokens(usage)
	return u
}

// CheckFunc 单个检测项的执行体。
type CheckFunc func(ctx context.Context, c *Client, st *runState) *CheckResult

// Check 检测项元信息（前端据此渲染勾选面板）。
type Check struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Group    string `json:"group"`
	Default  bool   `json:"default"`
	Cost     string `json:"cost"`
	Requests int    `json:"requests"`
	Note     string `json:"note,omitempty"`
	// Requires 声明前置检测项。缺前置时 runner 会自动补跑。
	Requires []string `json:"requires,omitempty"`
	// Suite 所属套件。空值即真伪检测——目录里绝大多数项都是它，只有智商题显式标注。
	Suite string `json:"suite,omitempty"`
	// Informational 只做检测、不参与分类与真实性评分。
	Informational bool `json:"informational,omitempty"`
	// Grading 智商题的判分方式；Prompt 题目原文，供前端「查看题目」。
	Grading string `json:"grading,omitempty"`
	Prompt  string `json:"prompt,omitempty"`
}

// InSuite 判断检测项是否属于指定套件。
func (c Check) InSuite(suite string) bool {
	if c.Suite == "" {
		return suite == SuiteAuthenticity
	}
	return c.Suite == suite
}

// Checks 全部检测项，顺序即执行顺序。
var Checks = []Check{
	{ID: "models", Title: "模型列表", Group: GroupGateway, Default: true, Cost: CostLow, Requests: 1,
		Note: "拉 /v1/models，看模型 id 是否泄露 Bedrock 前缀"},
	{ID: "ping", Title: "基础请求与网关指纹", Group: GroupGateway, Default: true, Cost: CostLow, Requests: 1,
		Note: "消息 id 形态、注入量、限流头、service_tier"},
	{ID: "ping-again", Title: "官方缓存链路", Group: GroupGateway, Default: true, Cost: CostLow, Requests: 3,
		Note: "3 次前缀逐级增长的探针：每次都必须读到上次写入并写入新段（readₙ₊₁ = readₙ + writeₙ）", Requires: []string{"ping"}},
	{ID: "zero-injection", Title: "是否 0 注入", Group: GroupGateway, Default: true, Cost: CostMedium,
		Requests: zeroInjectionRequests, Informational: true,
		Note: "裸请求 / 2n+5 / 带 system / count_tokens 对账，外加 20 次并发采样看号池；只做检测，不参与分类与真实性评分"},
	{ID: "quality-baseline", Title: "回复质量 / 速度 / thinking", Group: GroupCapability, Default: true, Cost: CostHigh, Requests: 7,
		Note: "固定题目校验质量并记录 TTFT；Fable 额外对照 low/max", Requires: []string{}},

	{ID: "param-strict", Title: "参数校验严格性", Group: GroupProtocol, Default: true, Cost: CostLow, Requests: 4,
		Note: "四个应被官方拒绝的非法请求"},
	{ID: "max-tokens-strict", Title: "max_tokens 硬上限", Group: GroupProtocol, Default: true, Cost: CostLow, Requests: 1},
	{ID: "tool-use", Title: "强制工具调用", Group: GroupProtocol, Default: true, Cost: CostMedium, Requests: 2,
		Note: "tool_use id 前缀是区分 Bedrock / Vertex 的强指纹"},
	{ID: "thinking-sig", Title: "thinking 签名", Group: GroupProtocol, Default: true, Cost: CostMedium, Requests: 1},
	{ID: "sig-tamper", Title: "签名完整性", Group: GroupProtocol, Default: true, Cost: CostMedium, Requests: 2,
		Note: "原样回传可续写、改一个字符必须被拒；识别网关回放缓存与剥离 thinking 块", Requires: []string{"thinking-sig"}},
	{ID: "stream", Title: "SSE 流式协议", Group: GroupProtocol, Default: true, Cost: CostLow, Requests: 1},
	{ID: "count-tokens", Title: "count_tokens 一致性", Group: GroupProtocol, Default: true, Cost: CostLow, Requests: 3,
		Note: "稳定性、单调性，并与基础请求的 input_tokens 对齐", Requires: []string{"ping"}},

	{ID: "persona-cc", Title: "Claude Code 人设", Group: GroupIdentity, Default: true, Cost: CostLow, Requests: 1},
	{ID: "persona-kiro", Title: "非 Kiro 归属", Group: GroupIdentity, Default: true, Cost: CostMedium, Requests: 2,
		Note: "排除 CC Max 是 Kiro 逆向；命中 Kiro 特征时失败"},
	{ID: "env-leak", Title: "工作区泄露", Group: GroupIdentity, Default: true, Cost: CostLow, Requests: 1},
	{ID: "sys-dump", Title: "系统提示泄露", Group: GroupIdentity, Default: true, Cost: CostMedium, Requests: 1,
		Note: "泄露出别家品牌词即判伪装", Requires: []string{"ping"}},
	{ID: "model-meta", Title: "型号与知识截止", Group: GroupIdentity, Default: true, Cost: CostMedium, Requests: 1},
	{ID: "caller-system", Title: "system 穿透", Group: GroupIdentity, Default: true, Cost: CostLow, Requests: 2},

	{ID: "image", Title: "图片识别", Group: GroupCapability, Default: false, Cost: CostMedium, Requests: 1,
		Note: "纯文本套壳会直接失败"},
	{ID: "pdf", Title: "PDF 识别", Group: GroupCapability, Default: false, Cost: CostMedium, Requests: 1},
	{ID: "strict-schema", Title: "严格 JSON Schema", Group: GroupCapability, Default: false, Cost: CostMedium, Requests: 1},
	{ID: "prompt-cache", Title: "Prompt Cache 正负对照", Group: GroupCapability, Default: false, Cost: CostHigh, Requests: 3},
	{ID: "hello-entropy", Title: "采样熵", Group: GroupCapability, Default: false, Cost: CostHigh, Requests: 10,
		Note: "10 次 Hello/Hi，去重回复过少说明是模板响应"},
	{ID: "baseline-quality", Title: "CCMax 基准对照", Group: GroupCapability, Default: true, Cost: CostHigh, Requests: 1, Note: "与已保存的真实 CCMax 基准比较质量、输出 token 与 thinking"},
	{ID: "slope", Title: "输出斜率", Group: GroupCapability, Default: false, Cost: CostMedium, Requests: 1,
		Note: "扣掉 thinking_tokens 后的正文 token/词 比例；偏高说明有看不见的输出被计费"},

	{ID: "iq-pelican", Title: "鹈鹕测试", Group: GroupIQ, Suite: SuiteIQ, Default: true, Cost: CostHigh, Requests: 1,
		Grading: GradingVisual, Prompt: pelicanPrompt,
		Note: "SVG 动画作品：自动核对完整与动画，画面好坏人工评判"},
	{ID: "iq-candy", Title: "糖果题测试", Group: GroupIQ, Suite: SuiteIQ, Default: true, Cost: CostMedium, Requests: 1,
		Grading: GradingAnswer, Prompt: candyPrompt, Note: candyNote},
}

// handlers 检测项 id → 执行体。
var handlers = map[string]CheckFunc{
	"models":            checkModels,
	"ping":              checkPing,
	"ping-again":        checkPingAgain,
	"zero-injection":    checkZeroInjection,
	"param-strict":      checkParamStrict,
	"max-tokens-strict": checkMaxTokens,
	"tool-use":          checkToolUse,
	"thinking-sig":      checkThinkingSignature,
	"sig-tamper":        checkSignatureTamper,
	"stream":            checkStream,
	"count-tokens":      checkCountTokens,
	"persona-cc":        checkPersonaClaudeCode,
	"persona-kiro":      checkPersonaKiro,
	"env-leak":          checkEnvLeak,
	"sys-dump":          checkSystemDump,
	"model-meta":        checkModelMeta,
	"caller-system":     checkCallerSystem,
	"image":             checkImage,
	"pdf":               checkPDF,
	"strict-schema":     checkStrictSchema,
	"prompt-cache":      checkPromptCache,
	"hello-entropy":     checkHelloEntropy,
	"slope":             checkSlope,
	"baseline-quality":  checkBaselineQuality,
	"quality-baseline":  checkQualityBaseline,
	"iq-pelican":        checkPelican,
	"iq-candy":          checkCandy,
}

// checkByID 按 id 查元信息。
func checkByID(id string) (Check, bool) {
	for _, c := range Checks {
		if c.ID == id {
			return c, true
		}
	}
	return Check{}, false
}

// NormalizeSuite 归一套件名，未知值按真伪检测处理（与历史请求兼容）。
func NormalizeSuite(suite string) string {
	if suite == SuiteIQ {
		return SuiteIQ
	}
	return SuiteAuthenticity
}

// DefaultCheckIDs 指定套件里推荐勾选的检测项。
func DefaultCheckIDs(suite string) []string {
	var out []string
	for _, c := range Checks {
		if c.Default && c.InSuite(suite) {
			out = append(out, c.ID)
		}
	}
	return out
}

// SuiteOf 一组检测项所属的套件（调用方保证同属一个套件）。
func SuiteOf(ids []string) string {
	for _, id := range ids {
		if meta, ok := checkByID(id); ok && meta.InSuite(SuiteIQ) {
			return SuiteIQ
		}
	}
	return SuiteAuthenticity
}

// Preset 场景预设：一键勾选一组针对某类渠道的检测项。
type Preset struct {
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	Checks []string `json:"checks"`
}

// Presets 运营常用的两套真伪检测场景（不含智商题）。高成本能力项不进预设，要跑再去自定义勾。
var Presets = []Preset{
	{
		ID:    "cc_max",
		Title: "CC Max",
		Checks: []string{
			"models", "ping", "ping-again", "zero-injection",
			"param-strict", "max-tokens-strict", "thinking-sig", "sig-tamper", "stream", "count-tokens",
			"persona-cc", "env-leak", "sys-dump", "caller-system", "model-meta", "quality-baseline", "baseline-quality",
		},
	},
	{
		ID:    "aws_bedrock",
		Title: "AWS Bedrock",
		Checks: []string{
			"models", "ping",
			"param-strict", "max-tokens-strict", "tool-use", "thinking-sig", "sig-tamper", "stream",
			"persona-cc", "persona-kiro", "model-meta",
		},
	},
}
