package modeldetect

import (
	"fmt"
	"sort"
	"strings"
)

// 主判定标签。
const (
	LabelMaxPool     = "claude_max_pool"
	LabelOfficial    = "official_api"
	LabelBedrock     = "aws_bedrock"
	LabelKiro        = "kiro"
	LabelVertex      = "vertex"
	LabelWrapper     = "wrapper"
	LabelUnknown     = "unknown"
	LabelUnavailable = "unavailable"
)

// 置信度。
const (
	ConfidenceHigh   = "high"
	ConfidenceMedium = "medium"
	ConfidenceLow    = "low"
)

// 真实性等级。
const (
	GradeGenuine  = "genuine"  // 观察到真 Claude 后端的硬证据
	GradeLikely   = "likely"   // 大概率为真
	GradeDoubtful = "doubtful" // 存疑
	GradeFake     = "fake"     // 疑似伪装
)

// authCapScore 触发造假硬证据后真实性评分的上限。
const authCapScore = 30

// Authenticity 后端真实性评估，独立于渠道分类。
//
// 与分类分开是刻意的：Max 号池注入 CC 人设不影响它是真 Claude；
// 反过来一条「干净的官方 API 直连」也可能被中间人换成别家模型。
type Authenticity struct {
	Score     int      `json:"score"`
	Grade     string   `json:"grade"`
	Capped    bool     `json:"capped"`
	CapReason []string `json:"cap_reason,omitempty"`
}

// Verdict 一个目标的最终判定。
type Verdict struct {
	Label        string         `json:"label"`
	Title        string         `json:"title"`
	Confidence   string         `json:"confidence"`
	Scores       map[string]int `json:"scores"`
	Authenticity Authenticity   `json:"authenticity"`
	Reasons      []Evidence     `json:"reasons"`
}

// classOrder 分数展示顺序，同时决定同分时的优先级。
var classOrder = []string{ClassMaxPool, ClassOfficial, ClassBedrock, ClassKiro, ClassVertex, ClassWrapper}

var labelTitles = map[string]string{
	LabelMaxPool:     "官方 Claude Max / Pro 订阅号池（经网关）",
	LabelOfficial:    "官方 Messages API（普通 API Key）",
	LabelBedrock:     "AWS Bedrock 承载的 Claude",
	LabelKiro:        "Kiro / AWS IDE 反代",
	LabelVertex:      "Google Vertex / Antigravity",
	LabelWrapper:     "包装或改写过的渠道，不像干净官方口",
	LabelUnknown:     "证据不足，仅能确认可返回 Claude 兼容响应",
	LabelUnavailable: "渠道不可用",
}

// Classify 汇总所有检测结果，给出渠道分类与真实性评分。
func Classify(results []*CheckResult) Verdict {
	scores := map[string]int{}
	for _, cls := range classOrder {
		scores[cls] = 0
	}
	var reasons []Evidence
	seenEvidence := map[string]bool{}
	auth := Authenticity{}
	usable := false

	for _, res := range results {
		if res == nil || res.Status == StatusRunning {
			continue
		}
		if res.Status != StatusUnsupported && anyExchangeSucceeded(res.Exchanges) {
			usable = true
		}
		for _, ev := range res.Evidence {
			// 同一个指纹被观察到多次（例如两个工具调用都返回非官方 id）只算一次：
			// 它是一条证据的重复观察，不是两条独立证据，重复计分会把分数吹起来。
			if seenEvidence[ev.Key] {
				continue
			}
			seenEvidence[ev.Key] = true
			if ev.Weight > 0 {
				scores[ev.Class] += ev.Weight
			}
			reasons = append(reasons, ev)
		}
		auth.Score += res.AuthScore
		if res.AuthCapReason != "" {
			auth.Capped = true
			auth.CapReason = append(auth.CapReason, res.AuthCapReason)
		}
	}

	if auth.Score > 100 {
		auth.Score = 100
	}
	if auth.Capped && auth.Score > authCapScore {
		auth.Score = authCapScore
	}
	auth.Grade = gradeOf(auth.Score, auth.Capped)

	// 证据按权重降序，前端「为什么这么判」直接取前几条
	sort.SliceStable(reasons, func(i, j int) bool { return reasons[i].Weight > reasons[j].Weight })

	if !usable {
		return Verdict{
			Label: LabelUnavailable, Title: labelTitles[LabelUnavailable],
			Confidence: ConfidenceHigh, Scores: scores, Authenticity: auth,
			Reasons: reasons,
		}
	}

	top, topScore, second := rank(scores)
	label := pickLabel(top, topScore, scores)
	confidence := confidenceOf(label, topScore, second, results)

	return Verdict{
		Label: label, Title: labelTitles[label],
		Confidence: confidence, Scores: scores, Authenticity: auth, Reasons: reasons,
	}
}

// anyExchangeSucceeded 该检测项里是否有请求真的通了。
func anyExchangeSucceeded(exchanges []*Exchange) bool {
	for _, ex := range exchanges {
		if ex != nil && ex.OK() {
			return true
		}
	}
	return false
}

// rank 返回最高分类别、其分数与次高分。
func rank(scores map[string]int) (string, int, int) {
	type kv struct {
		k string
		v int
	}
	list := make([]kv, 0, len(scores))
	for _, cls := range classOrder {
		list = append(list, kv{cls, scores[cls]})
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].v > list[j].v })
	second := 0
	if len(list) > 1 {
		second = list[1].v
	}
	return list[0].k, list[0].v, second
}

// pickLabel 把最高分类别映射成主判定。
//
// 阈值沿用参考脚本的经验值：分数太低时宁可报 unknown 也不硬套一个类别，
// 因为网关能改写的字段太多，孤证不足以定性。
func pickLabel(top string, topScore int, scores map[string]int) string {
	if topScore <= 2 {
		return LabelUnknown
	}
	switch top {
	case ClassKiro:
		if topScore >= 5 {
			return LabelKiro
		}
	case ClassMaxPool:
		if topScore >= 5 {
			return LabelMaxPool
		}
	case ClassBedrock:
		if topScore >= 4 {
			return LabelBedrock
		}
	case ClassVertex:
		if topScore >= 4 {
			return LabelVertex
		}
	case ClassOfficial:
		if topScore >= 4 {
			return LabelOfficial
		}
	case ClassWrapper:
		return LabelWrapper
	}
	// 分数够不到本类阈值：包装分若同时不低，按包装处理，否则证据不足
	if scores[ClassWrapper] >= 3 {
		return LabelWrapper
	}
	return LabelUnknown
}

// confidenceOf 置信度：领先幅度是主判据，另有几组「铁证」直接给 high。
func confidenceOf(label string, topScore, second int, results []*CheckResult) string {
	if label == LabelUnknown {
		return ConfidenceLow
	}
	if hasEvidence(results, "msg_id_bedrock", "msg_id_vertex", "sig_prefix_vertex", "unified_ratelimit") {
		return ConfidenceHigh
	}
	if topScore-second >= 3 {
		return ConfidenceHigh
	}
	return ConfidenceMedium
}

// hasEvidence 判断结果集中是否出现指定证据键。
func hasEvidence(results []*CheckResult, keys ...string) bool {
	want := make(map[string]bool, len(keys))
	for _, k := range keys {
		want[k] = true
	}
	for _, res := range results {
		if res == nil {
			continue
		}
		for _, ev := range res.Evidence {
			if want[ev.Key] {
				return true
			}
		}
	}
	return false
}

// gradeOf 真实性等级。
func gradeOf(score int, capped bool) string {
	switch {
	case capped:
		return GradeFake
	case score >= 80:
		return GradeGenuine
	case score >= 50:
		return GradeLikely
	case score >= 30:
		return GradeDoubtful
	default:
		return GradeDoubtful
	}
}

// Summary 一句话结论，用于日志与通知。
func (v Verdict) Summary() string {
	parts := make([]string, 0, len(classOrder))
	for _, cls := range classOrder {
		if v.Scores[cls] > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", cls, v.Scores[cls]))
		}
	}
	return fmt.Sprintf("%s（%s 置信度）｜真实性 %d/%s｜%s",
		v.Title, v.Confidence, v.Authenticity.Score, v.Authenticity.Grade, strings.Join(parts, " "))
}
