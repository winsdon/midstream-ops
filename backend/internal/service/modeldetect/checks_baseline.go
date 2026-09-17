package modeldetect

import (
	"context"
	"fmt"
)

func checkBaselineQuality(ctx context.Context, c *Client, st *runState) *CheckResult {
	meta, _ := checkByID("baseline-quality")
	r := newResult(meta)
	if st.baseline == nil {
		r.Status = StatusInconclusive
		return r.finish("未生成 CCMax 基准")
	}
	if !SameBaselineModel(st.baseline.Model, c.Target().Model) {
		r.Status = StatusInconclusive
		r.diagnose("基准模型与检测模型一致", false,
			fmt.Sprintf("基准=%s 检测=%s", st.baseline.Model, c.Target().Model))
		return r.finish("基准模型与检测模型不一致，对照未生效")
	}
	ex := c.Post(ctx, KindMessages, BaselineRequest(c.Target().Model))
	r.Exchanges = []*Exchange{ex}
	r.DurationMs = ex.DurationMs
	if !ex.OK() {
		r.Status = StatusInconclusive
		r.diagnose("基准对照请求成功", false, describeFailure(ex))
		return r.finish("基准对照请求失败")
	}
	actual := StatsFromExchange(ex)
	actual.QualityOK = actual.ResponseSummary != "" && str(ex.JSON["stop_reason"]) == "end_turn"
	qualityOK, tokenOK, thinkingOK, detail := compareBaseline(actual, st.baseline)
	r.assert("基准题回复质量通过", qualityOK, detail)
	r.assert("输出 token 在基准范围内", tokenOK, detail)
	r.assert("thinking 在基准范围内", thinkingOK, detail)
	r.diagnose("TTFT 与总耗时已记录", true, fmt.Sprintf("基准 TTFT=%dms 实际 TTFT=%dms；基准总耗时=%dms 实际总耗时=%dms", st.baseline.TTFTMs, valueInt64(ex.TTFTMs), st.baseline.DurationMs, ex.DurationMs))
	if qualityOK && tokenOK && thinkingOK {
		r.AuthScore = 30
		return r.finish("CCMax 基准对照通过")
	}
	if qualityOK && (!tokenOK || !thinkingOK) {
		r.Status = StatusSuspicious
		r.addEvidence("baseline_stats_outlier", "CCMax 基准 token/thinking 偏差", detail, ClassWrapper, 8)
		return r.finish("CCMax 基准统计偏差")
	}
	return r.finish("CCMax 基准质量不匹配")
}
