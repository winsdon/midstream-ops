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

// newResult 按目录元信息初始化结果。
func newResult(meta Check) *CheckResult {
	return &CheckResult{ID: meta.ID, Title: meta.Title, Group: meta.Group}
}

// runState 检测项之间共享的上下文。
//
// 只有真正互相依赖的项才读它：签名篡改要复用上一步拿到的 thinking 块，
// count_tokens 要和基础请求的 input_tokens 对齐。
type runState struct {
	profile ThinkingProfile

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
}

// Checks 全部检测项，顺序即执行顺序。
var Checks = []Check{
	{ID: "models", Title: "模型列表", Group: GroupGateway, Default: true, Cost: CostLow, Requests: 1,
		Note: "拉 /v1/models，看模型 id 是否泄露 Bedrock 前缀"},
	{ID: "ping", Title: "基础请求与网关指纹", Group: GroupGateway, Default: true, Cost: CostLow, Requests: 1,
		Note: "消息 id 形态、注入量、限流头、service_tier"},
	{ID: "ping-again", Title: "官方缓存链路", Group: GroupGateway, Default: true, Cost: CostLow, Requests: 1,
		Note: "严格验证 cache_read₁ + cache_creation₁ = 下一次 cache_read", Requires: []string{"ping"}},
	{ID: "quality-baseline", Title: "回复质量 / 速度 / thinking", Group: GroupCapability, Default: true, Cost: CostHigh, Requests: 7,
		Note: "固定题目校验质量并记录 TTFT；Fable 额外对照 low/max", Requires: []string{}},

	{ID: "param-strict", Title: "参数校验严格性", Group: GroupProtocol, Default: true, Cost: CostLow, Requests: 4,
		Note: "四个应被官方拒绝的非法请求"},
	{ID: "max-tokens-strict", Title: "max_tokens 硬上限", Group: GroupProtocol, Default: true, Cost: CostLow, Requests: 1},
	{ID: "tool-use", Title: "强制工具调用", Group: GroupProtocol, Default: true, Cost: CostMedium, Requests: 2,
		Note: "tool_use id 前缀是区分 Bedrock / Vertex 的强指纹"},
	{ID: "thinking-sig", Title: "thinking 签名", Group: GroupProtocol, Default: true, Cost: CostMedium, Requests: 1},
	{ID: "sig-tamper", Title: "签名完整性", Group: GroupProtocol, Default: true, Cost: CostMedium, Requests: 2,
		Note: "原样回传可续写、改一个字符必须被拒", Requires: []string{"thinking-sig"}},
	{ID: "stream", Title: "SSE 流式协议", Group: GroupProtocol, Default: true, Cost: CostLow, Requests: 1},
	{ID: "count-tokens", Title: "count_tokens 一致性", Group: GroupProtocol, Default: true, Cost: CostLow, Requests: 3,
		Note: "稳定性、单调性，并与基础请求的 input_tokens 对齐", Requires: []string{"ping"}},

	{ID: "persona-cc", Title: "Claude Code 人设", Group: GroupIdentity, Default: true, Cost: CostLow, Requests: 1},
	{ID: "persona-kiro", Title: "Kiro 归属", Group: GroupIdentity, Default: true, Cost: CostMedium, Requests: 2},
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
	{ID: "thinking-gradient", Title: "thinking 难度梯度", Group: GroupCapability, Default: false, Cost: CostHigh, Requests: 3},
	{ID: "slope", Title: "输出斜率", Group: GroupCapability, Default: false, Cost: CostMedium, Requests: 1,
		Note: "token/词 比例异常说明被强制注入 thinking 或 tokenizer 不同"},
}

// handlers 检测项 id → 执行体。
var handlers = map[string]CheckFunc{
	"models":            checkModels,
	"ping":              checkPing,
	"ping-again":        checkPingAgain,
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
	"thinking-gradient": checkThinkingGradient,
	"slope":             checkSlope,
	"quality-baseline":  checkQualityBaseline,
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

// Presets 运营常用的两套场景。高成本能力项不进预设，要跑再去自定义勾。
var Presets = []Preset{
	{
		ID:    "cc_max",
		Title: "CC Max",
		Checks: []string{
			"models", "ping", "ping-again",
			"param-strict", "max-tokens-strict", "thinking-sig", "sig-tamper", "stream", "count-tokens",
			"persona-cc", "env-leak", "sys-dump", "caller-system", "model-meta", "quality-baseline",
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
