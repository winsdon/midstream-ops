package modeldetect

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

type qualityProbe struct {
	name   string
	body   map[string]any
	verify func(string) bool
}

type fableProbe struct {
	ok            bool
	qualityOK     bool
	thinkingChars int
}

var fableModelRe = regexp.MustCompile(`(?i)^claude-fable-5(?:[-_.].*)?$`)

func checkQualityBaseline(ctx context.Context, c *Client, st *runState) *CheckResult {
	meta, _ := checkByID("quality-baseline")
	r := newResult(meta)
	probeID := nonce("QUALITY")
	thinking := qualityThinkingParam(st.profile)
	probes := []qualityProbe{
		{
			name: "精确算术",
			body: map[string]any{"model": c.Target().Model, "max_tokens": 2048, "thinking": thinking,
				"messages": []map[string]any{{"role": "user", "content": "只输出 323。不要解释。"}}},
			verify: func(text string) bool { return qualityExactText(text, "323") },
		},
		{
			name: "指令遵循",
			body: map[string]any{"model": c.Target().Model, "max_tokens": 2048, "thinking": thinking,
				"messages": []map[string]any{{"role": "user", "content": "只原样输出校验串 " + probeID + "，不要添加任何字符。"}}},
			verify: func(text string) bool { return qualityExactText(text, probeID) },
		},
		{
			name: "结构化回答",
			body: map[string]any{"model": c.Target().Model, "max_tokens": 2048, "thinking": thinking,
				"messages": []map[string]any{{"role": "user", "content": "只输出 JSON 对象 {\"answer\":323,\"label\":\"ok\"}，不要 markdown 或解释。"}}},
			verify: func(text string) bool { return qualityJSONAnswer(text, 323, "ok") },
		},
	}

	passed, successful := 0, 0
	for _, probe := range probes {
		ex := c.Post(ctx, KindMessages, probe.body)
		r.Exchanges = append(r.Exchanges, ex)
		r.DurationMs += ex.DurationMs
		if !ex.OK() {
			r.diagnose(probe.name+" 请求成功", false, describeFailure(ex))
			continue
		}
		successful++
		text := contentText(ex.JSON)
		ok := probe.verify(text)
		passed += boolInt(ok)
		think, sig := responseThinkingStats(ex.JSON)
		r.assert(probe.name+"答案与格式正确", ok, fmt.Sprintf("thinking=%d 字符，签名=%d 字符，TTFT=%s，总耗时=%dms，输出=%d tokens，回复=%s",
			think, sig, fmtMsPtr(ex.TTFTMs), ex.DurationMs, intOf(usageOf(ex.JSON)["output_tokens"]), clip(text, 100)))
	}

	if successful == 0 {
		r.Status = StatusInconclusive
		return r.finish("质量基准请求均失败")
	}
	if successful < len(probes) {
		r.Status = StatusInconclusive
		return r.finish(fmt.Sprintf("质量基准请求成功 %d/%d，证据不足", successful, len(probes)))
	}
	r.diagnose("质量基准请求成功率", successful == len(probes), fmt.Sprintf("%d/%d", successful, len(probes)))
	r.diagnose("TTFT 与总耗时已记录", true, "速度仅作观测，不作为单次伪造判据")
	if passed == len(probes) {
		r.AuthScore = 5
	}

	if fableModelRe.MatchString(c.Target().Model) {
		fableResults := runFableEffortPairs(ctx, c, r)
		if fableTwinSuspicious(fableResults) {
			r.Status = StatusSuspicious
			r.addEvidence("fable_effort_identical", "Fable low/max 的质量与 thinking 完全一致",
				"两个固定推理题的两档请求均成功，thinking 字符数逐题相同", ClassWrapper, 4)
		} else if len(fableResults) == 4 {
			r.diagnose("Fable low/max 产生不同 thinking 证据", true, formatFableResults(fableResults))
		} else {
			r.diagnose("Fable low/max 对照完整", false, "请求失败或不支持，无法形成伪造结论")
		}
	}

	return r.finish(fmt.Sprintf("质量 %d/%d；成功 %d/%d；总耗时 %dms", passed, len(probes), successful, len(probes), r.DurationMs))
}

func qualityThinkingParam(profile ThinkingProfile) map[string]any {
	if profile.Adaptive {
		return map[string]any{"type": "adaptive"}
	}
	return profile.ThinkingParam("summarized")
}

func runFableEffortPairs(ctx context.Context, c *Client, r *CheckResult) []fableProbe {
	tasks := []struct {
		prompt string
		want   string
	}{
		{"只输出 323。计算 17×19，不要解释。", "323"},
		{"只输出 15。计算 7+8，不要解释。", "15"},
	}
	results := make([]fableProbe, 0, 4)
	for _, task := range tasks {
		for _, effort := range []string{"low", "max"} {
			ex := c.Post(ctx, KindMessages, map[string]any{
				"model": c.Target().Model, "max_tokens": 2048,
				"thinking":      map[string]any{"type": "adaptive"},
				"output_config": map[string]any{"effort": effort},
				"messages":      []map[string]any{{"role": "user", "content": task.prompt}},
			})
			r.Exchanges = append(r.Exchanges, ex)
			r.DurationMs += ex.DurationMs
			if !ex.OK() {
				results = append(results, fableProbe{})
				r.diagnose("Fable "+effort+" 请求成功", false, describeFailure(ex))
				continue
			}
			think, sig := responseThinkingStats(ex.JSON)
			qualityOK := qualityExactText(contentText(ex.JSON), task.want)
			results = append(results, fableProbe{ok: true, qualityOK: qualityOK, thinkingChars: think})
			r.diagnose(fmt.Sprintf("Fable %s：答案正确", effort), qualityOK,
				fmt.Sprintf("thinking=%d 字符，签名=%d 字符，TTFT=%s，总耗时=%dms，输出=%d tokens",
					think, sig, fmtMsPtr(ex.TTFTMs), ex.DurationMs, intOf(usageOf(ex.JSON)["output_tokens"])))
		}
	}
	return results
}

func responseThinkingStats(body map[string]any) (thinkingChars, signatureChars int) {
	for _, raw := range contentBlocks(body) {
		block := mapOf(raw)
		if str(block["type"]) == "thinking" {
			thinkingChars += len(str(block["thinking"]))
			signatureChars += len(str(block["signature"]))
		}
	}
	return
}

func cacheChainOutcome(firstRead, firstCreation, secondRead int) (string, bool) {
	if firstRead == 0 && firstCreation == 0 && secondRead == 0 {
		return StatusInconclusive, false
	}
	if secondRead != firstRead+firstCreation {
		return StatusSuspicious, false
	}
	return StatusPassed, true
}

func fableTwinSuspicious(probes []fableProbe) bool {
	if len(probes) != 4 {
		return false
	}
	for i := 0; i < len(probes); i += 2 {
		low, max := probes[i], probes[i+1]
		if !low.ok || !max.ok || !low.qualityOK || !max.qualityOK || low.thinkingChars <= 0 ||
			low.thinkingChars != max.thinkingChars {
			return false
		}
	}
	return true
}

func qualityExactText(got, want string) bool {
	return strings.TrimSpace(got) == want
}

func qualityJSONAnswer(raw string, answer int, label string) bool {
	var value map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &value); err != nil {
		return false
	}
	return intOf(value["answer"]) == answer && str(value["label"]) == label && len(value) == 2
}

func formatFableResults(probes []fableProbe) string {
	parts := make([]string, 0, len(probes))
	for i, p := range probes {
		parts = append(parts, fmt.Sprintf("%d:%v/%v/%d", i+1, p.ok, p.qualityOK, p.thinkingChars))
	}
	return strings.Join(parts, " ")
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
