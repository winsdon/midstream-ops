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
	// Informational 只做检测、不参与分类与真实性评分。
	Informational bool `json:"informational,omitempty"`
	// AuthMax 本项对真实性评分的满分（0 = 不加分）。前端据此把没拿到分的计分项也列进证据链。
	AuthMax int `json:"auth_max,omitempty"`
}

// Checks 全部检测项，顺序即执行顺序。
var Checks = []Check{
	{ID: "ping", Title: "基础请求与网关指纹", Group: GroupGateway, Default: true, Cost: CostLow, Requests: 1,
		Note: "消息 id 形态、注入量、限流头、service_tier"},
	{ID: "ping-again", Title: "官方缓存链路", Group: GroupGateway, Default: true, Cost: CostLow, Requests: 3,
		Note: "3 轮追加的多轮对话（同一会话标识）：每次都必须读到上次写入并写入新段（readₙ₊₁ = readₙ + writeₙ）", Requires: []string{"ping"}},
	{ID: "zero-injection", Title: "是否 0 注入", Group: GroupGateway, Default: true, Cost: CostMedium,
		Requests: zeroInjectionRequests, Informational: true,
		Note: "裸请求 / 2n+5 / 带 system / count_tokens 对账，外加 20 次并发采样看号池；只做检测，不参与分类与真实性评分"},

	{ID: "param-strict", Title: "参数校验严格性", Group: GroupProtocol, Default: true, Cost: CostLow, Requests: 4, AuthMax: 15,
		Note: "四个应被官方拒绝的非法请求；只作真实性加分项，被放行不记包装 / 伪装"},
	{ID: "max-tokens-strict", Title: "max_tokens 硬上限", Group: GroupProtocol, Default: true, Cost: CostLow, Requests: 1, AuthMax: 10},
	{ID: "tool-use", Title: "强制工具调用", Group: GroupProtocol, Default: true, Cost: CostMedium, Requests: 2, AuthMax: 10,
		Note: "tool_use id 前缀是区分 Bedrock / Vertex 的强指纹"},
	{ID: "thinking-sig", Title: "thinking 签名", Group: GroupProtocol, Default: true, Cost: CostMedium, Requests: 1},
	{ID: "sig-tamper", Title: "签名完整性", Group: GroupProtocol, Default: true, Cost: CostMedium, Requests: 2, AuthMax: 10,
		Note: "只能证明后端会校验它自己签发的签名（伪造后端也做得到），真伪看「官方签名交叉验证」；原样回传可续写、改一个字符必须被拒；识别网关回放缓存与剥离 thinking 块", Requires: []string{"thinking-sig"}},
	{ID: "sig-crossverify", Title: "官方签名交叉验证", Group: GroupProtocol, Default: true, Cost: CostMedium, Requests: 5, AuthMax: 40,
		Note: "正向：内置的已知真签名交给目标续写；反向（需先生成基准）：目标签的签名交给真实基准账号续写。真 Anthropic 互认签名，伪造后端会以签名无效拒绝；只对第一方渠道成立"},
	{ID: "tokenizer", Title: "分词器指纹", Group: GroupProtocol, Default: true, Cost: CostLow, Requests: 4, AuthMax: 10,
		Note: "四段固定文本的输入量差值必须与该代 Claude 一致（只比差值，注入量不影响）"},

	{ID: "persona-cc", Title: "Claude Code 人设", Group: GroupIdentity, Default: true, Cost: CostLow, Requests: 1},
	{ID: "sys-dump", Title: "系统提示泄露", Group: GroupIdentity, Default: true, Cost: CostMedium, Requests: 1,
		Note: "泄露出别家品牌词即判伪装", Requires: []string{"ping"}},
	{ID: "model-meta", Title: "型号与知识截止", Group: GroupIdentity, Default: true, Cost: CostMedium, Requests: 1},

	{ID: "image", Title: "图片识别", Group: GroupCapability, Default: false, Cost: CostMedium, Requests: 1, AuthMax: 5,
		Note: "纯文本套壳会直接失败"},
	{ID: "pdf", Title: "PDF 识别", Group: GroupCapability, Default: false, Cost: CostMedium, Requests: 1, AuthMax: 5},
	{ID: "strict-schema", Title: "严格 JSON Schema", Group: GroupCapability, Default: false, Cost: CostMedium, Requests: 1, AuthMax: 5},
	{ID: "hello-entropy", Title: "采样熵", Group: GroupCapability, Default: false, Cost: CostHigh, Requests: 10, AuthMax: 1,
		Note: "10 次 Hello/Hi，去重回复过少说明是模板响应"},
	{ID: "baseline-quality", Title: "CCMax 基准对照", Group: GroupCapability, Default: true, Cost: CostHigh, Requests: 1, AuthMax: 30, Note: "与已保存的真实 CCMax 基准比较质量、输出 token 与 thinking"},
}

// handlers 检测项 id → 执行体。
var handlers = map[string]CheckFunc{
	"ping":              checkPing,
	"ping-again":        checkPingAgain,
	"zero-injection":    checkZeroInjection,
	"param-strict":      checkParamStrict,
	"max-tokens-strict": checkMaxTokens,
	"tool-use":          checkToolUse,
	"thinking-sig":      checkThinkingSignature,
	"sig-tamper":        checkSignatureTamper,
	"sig-crossverify":   checkSignatureCrossVerify,
	"tokenizer":         checkTokenizer,
	"persona-cc":        checkPersonaClaudeCode,
	"sys-dump":          checkSystemDump,
	"model-meta":        checkModelMeta,
	"image":             checkImage,
	"pdf":               checkPDF,
	"strict-schema":     checkStrictSchema,
	"hello-entropy":     checkHelloEntropy,
	"baseline-quality":  checkBaselineQuality,
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

// DefaultCheckIDs 推荐勾选的检测项。
func DefaultCheckIDs() []string {
	var out []string
	for _, c := range Checks {
		if c.Default {
			out = append(out, c.ID)
		}
	}
	return out
}

// Preset 场景预设：一键勾选一组针对某类渠道的检测项。
type Preset struct {
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	Checks []string `json:"checks"`
}

// Presets 运营常用的两套真伪检测场景。高成本能力项不进预设，要跑再去自定义勾。
var Presets = []Preset{
	{
		ID:    "cc_max",
		Title: "CC Max",
		Checks: []string{
			"ping", "ping-again", "zero-injection",
			"param-strict", "max-tokens-strict", "thinking-sig", "sig-tamper", "sig-crossverify", "tokenizer",
			"persona-cc", "sys-dump", "model-meta", "baseline-quality",
		},
	},
	{
		ID:    "aws_bedrock",
		Title: "AWS Bedrock",
		Checks: []string{
			"ping",
			"param-strict", "max-tokens-strict", "tool-use", "thinking-sig", "sig-tamper", "tokenizer",
			"persona-cc", "model-meta",
		},
	},
}
