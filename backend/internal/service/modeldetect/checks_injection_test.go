package modeldetect

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// ziUpstream 按 claude-opus-5 的官方基线（裸请求 18、带 system 48）计量的假渠道。
// 数字序列按 slope·n + intercept 计，缺省 2n+7：opus-5 的数字截距没有官方实测，7 只是个合理的常数。
type ziUpstream struct {
	mu        sync.Mutex
	bareCalls int
	replies   int

	inject         int  // 每个请求额外多出的输入（网关注入）
	injectCount    bool // count_tokens 也带上注入
	noCount        bool // count_tokens 不可用
	countEstimated bool // count_tokens 是网关按字符数估算的
	poolEvery      int  // 每 poolEvery 个裸请求有一个落在注入号上
	poolExtra      int  // 注入号多出的缓存读取
	estimated      bool // token 数是网关估算的，不随数字个数线性增长
	slope          int  // 每个数字的 token 数，缺省 2
	intercept      int  // 数字序列的截距，缺省 7
	disobey        bool // 不遵循调用方 system
	hidden         string
	hiddenThink    string
	replayBare     string // 非空时所有裸请求都回这个消息 id
	bareSignature  string // 非空时所有裸请求都带这份 thinking 签名（回放缓存的响应）
	delayTokens    time.Duration
	tokenDoneAt    time.Time   // 慢请求（max-tokens-strict）结束的时间
	probeStarts    []time.Time // 本项专属请求（数字序列 / 隐藏注入）开始的时间
}

// estimateTokens 网关常见的估算：按字符数折算，不随数字个数线性增长。
func estimateTokens(text string) int { return len(strings.TrimSpace(text))*10/37 + 9 }

// isDigitProbe 是不是「是否 0 注入」的数字序列探针（全是单个数字）。
func isDigitProbe(text string) bool {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return false
	}
	for _, f := range fields {
		if len(f) != 1 || f[0] < '0' || f[0] > '9' {
			return false
		}
	}
	return true
}

func (u *ziUpstream) serve(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		if r.URL.Path == "/v1/messages/count_tokens" {
			if u.noCount {
				writeJSON(w, 404, map[string]any{"type": "error",
					"error": map[string]any{"type": "not_found_error", "message": "count_tokens endpoint is not supported"}})
				return
			}
			n := u.promptTokens(body)
			if u.countEstimated {
				n = estimateTokens(userText(body))
			}
			if u.injectCount {
				n += u.inject
			}
			writeJSON(w, 200, map[string]any{"input_tokens": n})
			return
		}
		u.reply(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// promptTokens 官方口径下这段请求的输入 token 数（不含注入）。
func (u *ziUpstream) promptTokens(body map[string]any) int {
	if str(body["system"]) == pirateSystem {
		return 48
	}
	text := userText(body)
	switch {
	case strings.Contains(text, "PONG"):
		return 18
	case strings.Contains(text, "YES or NO"):
		return 30
	case strings.Contains(text, "TOKEN"):
		return 55
	}
	if u.estimated {
		return estimateTokens(text)
	}
	slope, intercept := u.slope, u.intercept
	if slope == 0 {
		slope = 2
	}
	if intercept == 0 {
		intercept = 7
	}
	return slope*len(strings.Fields(text)) + intercept
}

func (u *ziUpstream) reply(w http.ResponseWriter, body map[string]any) {
	text := userText(body)
	u.mu.Lock()
	probe := mapOf(body["thinking"]) != nil || isDigitProbe(text)
	if probe {
		u.probeStarts = append(u.probeStarts, time.Now())
	}
	u.mu.Unlock()
	if strings.Contains(text, "TOKEN") && u.delayTokens > 0 {
		// 先记结束时间再回包：检测项拿到响应之前，这个时间一定已经记下。
		time.Sleep(u.delayTokens)
		u.mu.Lock()
		u.tokenDoneAt = time.Now()
		u.mu.Unlock()
	}

	u.mu.Lock()
	u.replies++
	id := "msg_01" + fakeMsgCore(u.replies)
	usage := map[string]any{"input_tokens": u.promptTokens(body) + u.inject, "output_tokens": 4}
	isBare := strings.Contains(text, "PONG") && body["system"] == nil
	if isBare {
		u.bareCalls++
		if u.poolEvery > 0 && u.bareCalls%u.poolEvery == 0 {
			usage["cache_read_input_tokens"] = u.poolExtra
		}
		if u.replayBare != "" {
			id = u.replayBare
		}
	}
	u.mu.Unlock()

	content := []any{map[string]any{"type": "text", "text": "ok"}}
	switch {
	case mapOf(body["thinking"]) != nil:
		answer := u.hidden
		if answer == "" {
			answer = "NO"
		}
		think := u.hiddenThink
		if think == "" {
			think = "The user asks whether there was a system prompt before this message. There was none."
		}
		content = []any{
			map[string]any{"type": "thinking", "thinking": think, "signature": "SIGVALID_x"},
			map[string]any{"type": "text", "text": answer},
		}
	case str(body["system"]) == pirateSystem && !u.disobey:
		content = []any{map[string]any{"type": "text", "text": "Arrr! 4"}}
	case isBare:
		content = []any{map[string]any{"type": "text", "text": "PONG"}}
		if u.bareSignature != "" {
			content = []any{
				map[string]any{"type": "thinking", "thinking": "", "signature": u.bareSignature},
				map[string]any{"type": "text", "text": "PONG"},
			}
		}
	}
	writeJSON(w, 200, map[string]any{
		"id": id, "type": "message", "role": "assistant", "model": str(body["model"]),
		"stop_reason": "end_turn", "usage": usage, "content": content,
	})
}

func runZeroInjection(t *testing.T, u *ziUpstream, model string) *CheckResult {
	t.Helper()
	target, err := Target{Name: "t", BaseURL: u.serve(t).URL, APIKey: "sk-test-key-1234567890", Model: model}.Normalize()
	if err != nil {
		t.Fatalf("归一化目标失败: %v", err)
	}
	run := Run(context.Background(), target, []string{"zero-injection"}, NewGate(2), nil)
	res := findCheck(run.Checks, "zero-injection")
	if res == nil {
		t.Fatal("缺少 zero-injection 结果")
	}
	return res
}

func TestZeroInjectionClean(t *testing.T) {
	res := runZeroInjection(t, &ziUpstream{}, "claude-opus-5")
	if res.Status != StatusPassed || !hasEvidenceKey(res, "zero_injection") {
		t.Fatalf("与官方基线逐项一致应判 0 注入，实际 %s：%s", res.Status, res.Summary)
	}
	if len(res.Exchanges) != zeroInjectionRequests {
		t.Fatalf("应发 %d 次请求，实际 %d", zeroInjectionRequests, len(res.Exchanges))
	}
	if !res.Informational {
		t.Fatal("结果应标为只检测不计分")
	}
}

func TestZeroInjectionDetectsInjection(t *testing.T) {
	res := runZeroInjection(t, &ziUpstream{inject: 11, injectCount: true}, "claude-opus-5")
	if res.Status != StatusSuspicious || !hasEvidenceKey(res, "injection_detected") {
		t.Fatalf("输入比官方基线多应判检出注入，实际 %s：%s", res.Status, res.Summary)
	}
	if !strings.Contains(res.Summary, "约 11 tokens") {
		t.Fatalf("摘要应给出注入量，实际 %s", res.Summary)
	}
}

func TestZeroInjectionDetectsMixedPool(t *testing.T) {
	res := runZeroInjection(t, &ziUpstream{poolEvery: 4, poolExtra: 3500}, "claude-opus-5")
	if res.Status != StatusSuspicious || !hasEvidenceKey(res, "injection_pool_mixed") {
		t.Fatalf("部分采样多出缓存读取应判号池混用，实际 %s：%s", res.Status, res.Summary)
	}
	if !strings.Contains(res.Summary, "比官方多 3500") {
		t.Fatalf("摘要应写出注入号多出的量，实际 %s", res.Summary)
	}
}

func TestZeroInjectionFlagsEstimatedTokens(t *testing.T) {
	res := runZeroInjection(t, &ziUpstream{estimated: true}, "claude-opus-5")
	if res.Status != StatusSuspicious || !hasEvidenceKey(res, "injection_metering_anomaly") {
		t.Fatalf("数字序列不线性应判计量异常，实际 %s：%s", res.Status, res.Summary)
	}
}

func TestZeroInjectionFlagsTokenizerMismatch(t *testing.T) {
	// opus-4-8 有官方 2n+5 实测：每个数字 1 token 说明 tokenizer 不是它的。
	res := runZeroInjection(t, &ziUpstream{slope: 1}, "claude-opus-4-8")
	if res.Status != StatusSuspicious || !strings.Contains(res.Summary, "tokenizer 与型号不符") {
		t.Fatalf("斜率与官方不符应判计量异常，实际 %s：%s", res.Status, res.Summary)
	}
}

func TestZeroInjectionReconcilesDigitsBaseline(t *testing.T) {
	// 假渠道的数字截距是 7，比官方 2n+5 多 2：没有裸请求基线的型号靠数字序列对账。
	res := runZeroInjection(t, &ziUpstream{}, "claude-opus-4-8")
	if res.Status != StatusSuspicious || !strings.Contains(res.Summary, "约 2 tokens") {
		t.Fatalf("数字序列恒比官方多 2 应判检出注入，实际 %s：%s", res.Status, res.Summary)
	}
}

func TestZeroInjectionWithoutBaseline(t *testing.T) {
	res := runZeroInjection(t, &ziUpstream{}, "claude-sonnet-5")
	if res.Status != StatusInconclusive {
		t.Fatalf("没有官方基线且未见注入应记证据不足，实际 %s：%s", res.Status, res.Summary)
	}
	// 推理与计数在两档长度上恒差 30：两边是同一个 tokenizer，差值只能是注入。
	res = runZeroInjection(t, &ziUpstream{inject: 30}, "claude-sonnet-5")
	if res.Status != StatusSuspicious || !strings.Contains(res.Summary, "推理比 count_tokens 恒多 30") {
		t.Fatalf("推理比计数恒多应判检出注入，实际 %s：%s", res.Status, res.Summary)
	}
}

// TestZeroInjectionIgnoresEstimatedCountTokens count_tokens 是网关估算的时候，
// 它与推理的差值不是常数，不能据此判注入（审查里实测过的误报）。
func TestZeroInjectionIgnoresEstimatedCountTokens(t *testing.T) {
	// opus-4-8 的数字序列与官方 2n+5 对上账，count_tokens 却按字符估算：应判 0 注入。
	res := runZeroInjection(t, &ziUpstream{intercept: 5, countEstimated: true}, "claude-opus-4-8")
	if res.Status != StatusPassed {
		t.Fatalf("数字序列与官方一致、count_tokens 是估算的，应判 0 注入，实际 %s：%s", res.Status, res.Summary)
	}
	// 没有任何官方基线的型号：估算的 count_tokens 也不能把结论推成检出注入。
	res = runZeroInjection(t, &ziUpstream{countEstimated: true}, "claude-sonnet-5")
	if res.Status != StatusInconclusive {
		t.Fatalf("只有估算的 count_tokens 可比时应记证据不足，实际 %s：%s", res.Status, res.Summary)
	}
}

func TestZeroInjectionSoftSignalDowngrades(t *testing.T) {
	res := runZeroInjection(t, &ziUpstream{hidden: "YES"}, "claude-opus-5")
	if res.Status != StatusSuspicious || !hasEvidenceKey(res, "injection_soft_signal") {
		t.Fatalf("计量 0 注入但模型自述有指令，应降为可疑，实际 %s：%s", res.Status, res.Summary)
	}
	res = runZeroInjection(t, &ziUpstream{disobey: true}, "claude-opus-5")
	if res.Status != StatusSuspicious || !strings.Contains(res.Summary, "调用方 system 未生效") {
		t.Fatalf("调用方 system 未生效应降为可疑，实际 %s：%s", res.Status, res.Summary)
	}
}

func TestZeroInjectionCountUnavailableDoesNotBlock(t *testing.T) {
	res := runZeroInjection(t, &ziUpstream{noCount: true}, "claude-opus-5")
	if res.Status != StatusPassed {
		t.Fatalf("count_tokens 不可用不影响有基线的结论，实际 %s：%s", res.Status, res.Summary)
	}
}

// TestZeroInjectionIsNotScored 本项只检测：加不加它，判定与跨项审计必须一模一样。
// 裸请求全部回放同一个 id——审计若读了本项的 20 次采样，就会报 id 重复。
func TestZeroInjectionIsNotScored(t *testing.T) {
	up := &ziUpstream{inject: 400, poolEvery: 3, poolExtra: 3000, replayBare: "msg_011CetJZkDjw9DiCC1x7RKgh"}
	srv := up.serve(t)
	target, _ := Target{Name: "t", BaseURL: srv.URL, APIKey: "sk-test-key-1234567890", Model: "claude-opus-5"}.Normalize()

	without := Run(context.Background(), target, []string{"ping"}, NewGate(2), nil)
	with := Run(context.Background(), target, []string{"ping", "zero-injection"}, NewGate(2), nil)

	if zi := findCheck(with.Checks, "zero-injection"); zi == nil || zi.Status != StatusSuspicious {
		t.Fatalf("本项应检出注入，实际 %+v", zi)
	}
	if !reflect.DeepEqual(without.Verdict.Scores, with.Verdict.Scores) ||
		!reflect.DeepEqual(without.Verdict.Authenticity, with.Verdict.Authenticity) ||
		without.Verdict.Label != with.Verdict.Label || len(without.Verdict.Reasons) != len(with.Verdict.Reasons) {
		t.Fatalf("加入本项后判定变了：\n无 %+v\n有 %+v", without.Verdict, with.Verdict)
	}
	if res := findCheck(with.Checks, AuditCheckID); res != nil && hasEvidenceKey(res, "msg_id_duplicate") {
		t.Fatalf("审计不应读本项的采样：%+v", res.Evidence)
	}
}

func TestZeroInjectionCatalog(t *testing.T) {
	meta, ok := checkByID("zero-injection")
	if !ok || !meta.Informational || handlers["zero-injection"] == nil {
		t.Fatalf("目录信息不完整：%+v", meta)
	}
	if meta.Requests != len(zeroInjectionProbes("claude-opus-5", ResolveProfile("claude-opus-5"))) {
		t.Fatalf("目录里的请求数 %d 与实际探针数不符", meta.Requests)
	}
}

// TestRunDefersInformationalChecks 只检测的项要等计分项全部结束才开跑：它的并发采样
// 会把请求挤到号池里别的账号上，与缓存链、签名回传同时跑会改掉它们的结论。
func TestRunDefersInformationalChecks(t *testing.T) {
	up := &ziUpstream{delayTokens: 300 * time.Millisecond}
	target, _ := Target{Name: "t", BaseURL: up.serve(t).URL, APIKey: "sk-test-key-1234567890",
		Model: "claude-opus-5"}.Normalize()
	Run(context.Background(), target, []string{"max-tokens-strict", "zero-injection"}, NewGate(4), nil)

	up.mu.Lock()
	defer up.mu.Unlock()
	if up.tokenDoneAt.IsZero() || len(up.probeStarts) == 0 {
		t.Fatalf("两项都应跑到：tokenDoneAt=%v probes=%d", up.tokenDoneAt, len(up.probeStarts))
	}
	for _, at := range up.probeStarts {
		if at.Before(up.tokenDoneAt) {
			t.Fatalf("「是否 0 注入」在计分项结束前就开跑了（%v < %v）", at, up.tokenDoneAt)
		}
	}
}

// TestRunWithoutScoredChecksHasNoVerdict 只勾不计分的项时不出判定，也就不会落进真伪历史。
func TestRunWithoutScoredChecksHasNoVerdict(t *testing.T) {
	up := &ziUpstream{}
	target, _ := Target{Name: "t", BaseURL: up.serve(t).URL, APIKey: "sk-test-key-1234567890",
		Model: "claude-opus-5"}.Normalize()
	run := Run(context.Background(), target, []string{"zero-injection"}, NewGate(2), nil)
	if run.Verdict != nil {
		t.Fatalf("只有不计分的项时不应出判定，实际 %+v", run.Verdict)
	}
	if findCheck(run.Checks, AuditCheckID) != nil {
		t.Fatal("没有计分项时不应跑跨项审计")
	}
}

// TestRequestFailedInformational 只检测的项零星 429 不算请求失败，一个都没成功才算。
func TestRequestFailedInformational(t *testing.T) {
	partial := &CheckResult{Informational: true, Status: StatusPassed,
		Exchanges: []*Exchange{{Status: 200}, {Status: 429}}}
	if RequestFailed(partial) {
		t.Fatal("有请求成功时不应算请求失败")
	}
	allFailed := &CheckResult{Informational: true, Status: StatusInconclusive,
		Exchanges: []*Exchange{{Status: 429}, {Status: 502}}}
	if !RequestFailed(allFailed) {
		t.Fatal("全部请求失败时应算请求失败")
	}
	scored := &CheckResult{Status: StatusInconclusive, Exchanges: []*Exchange{{Status: 200}, {Status: 429}}}
	if !RequestFailed(scored) {
		t.Fatal("计分项沿用原口径：出现 429 即算请求失败")
	}
}

// TestHasEvidenceSkipsInformational 置信度的「铁证」清单不能被只检测的项触发。
func TestHasEvidenceSkipsInformational(t *testing.T) {
	res := &CheckResult{Informational: true, Evidence: []Evidence{{Key: "unified_ratelimit"}}}
	if hasEvidence([]*CheckResult{res}, "unified_ratelimit") {
		t.Fatal("只检测的项的证据不应被 hasEvidence 看到")
	}
}

// TestRunProbesStopsOnCancel 取消后排队的探针立即返回，结果与探针一一对应。
func TestRunProbesStopsOnCancel(t *testing.T) {
	target, _ := Target{Name: "t", BaseURL: "http://127.0.0.1:1", APIKey: "sk-test-key-1234567890",
		Model: "claude-opus-5"}.Normalize()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	probes := zeroInjectionProbes("claude-opus-5", ResolveProfile("claude-opus-5"))
	start := time.Now()
	exs := runProbes(ctx, NewClient(target), probes, zeroInjectionWorkers)
	if len(exs) != len(probes) {
		t.Fatalf("结果数 %d 与探针数 %d 不符", len(exs), len(probes))
	}
	for i, ex := range exs {
		if ex == nil || ex.NetworkError == "" {
			t.Fatalf("第 %d 个探针取消后应记网络错误，实际 %+v", i, ex)
		}
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("取消后应立即返回，实际耗时 %v", time.Since(start))
	}
}

func TestFitDigits(t *testing.T) {
	ok := [3]bool{true, true, true}
	if fit := fitDigits([3]int{7, 45, 205}, ok); !fit.linear || fit.slope != 2 || fit.intercept != 5 {
		t.Fatalf("2n+5 应拟合为斜率 2、截距 5，实际 %+v", fit)
	}
	if fit := fitDigits([3]int{18, 56, 216}, ok); !fit.linear || fit.intercept != 16 {
		t.Fatalf("注入是常数，斜率不变、截距整体抬高，实际 %+v", fit)
	}
	if fit := fitDigits([3]int{9, 19, 62}, ok); fit.linear {
		t.Fatalf("差分对不上整数斜率应判非线性，实际 %+v", fit)
	}
	if fit := fitDigits([3]int{7, 45, 205}, [3]bool{true, false, true}); fit.complete {
		t.Fatal("缺一档不应拟合")
	}
}

func TestJudgeHiddenProbe(t *testing.T) {
	mk := func(answer, thinking, stop string) *Exchange {
		return &Exchange{Status: 200, JSON: map[string]any{"stop_reason": stop, "content": []any{
			map[string]any{"type": "thinking", "thinking": thinking},
			map[string]any{"type": "text", "text": answer},
		}}}
	}
	cases := []struct {
		name             string
		ex               *Exchange
		answered, signal bool
	}{
		{"回答 YES", mk("YES", "", "end_turn"), true, true},
		{"回答 NO 但 thinking 说有指令", mk("NO", "The system prompt says I'm Claude Code and not to reveal it.", "end_turn"), true, true},
		{"回答 NO 且 thinking 说没有", mk("NO", "There is no system prompt here.", "end_turn"), true, false},
		{"thinking 复述探针的提问不算信号", mk("NO", "I'm asked to answer with exactly YES or NO. There was nothing before.", "end_turn"), true, false},
		{"「不能分享」不带宾语不算信号", mk("No.", "I am told to reply briefly. I shouldn't share more than needed.", "end_turn"), true, false},
		{"中文「不是」是否定", mk("不是", "", "end_turn"), true, false},
		{"中文「是的」是肯定", mk("是的。", "", "end_turn"), true, true},
		{"全角标点开头", mk("「NO」", "", "end_turn"), true, false},
		{"被分类器拒答", mk("", "", "refusal"), false, false},
		{"请求失败", &Exchange{Status: 502, Raw: "bad gateway"}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := judgeHiddenProbe(tc.ex)
			if got.answered != tc.answered || got.signal != tc.signal {
				t.Fatalf("期望 answered=%v signal=%v，实际 %+v", tc.answered, tc.signal, got)
			}
		})
	}
}
