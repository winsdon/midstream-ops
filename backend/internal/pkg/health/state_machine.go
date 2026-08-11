// Package health 六状态健康状态机（纯函数，无 IO —— 移植 transit-hub connection_health）。
//
//	healthy → degraded → suspended → observing → recovering → healthy
//	                        ↑____________ disabled 仅人工进出 ____________
//
// 判定规则：
//   - 硬失败（5xx / 401/403 / 404 模型不存在）→ 直接 suspended + 冷却；
//   - 软失败（网络错误 / 429 / 响应不可解析）→ 降权，连续达阈值才 suspended；
//   - 其余 4xx 归 invalid_response，不判定上游不可用（防参数错误误伤）；
//   - 恢复：冷却结束探测成功 → observing（观察窗）→ 连续成功达阈值 → recovering
//     （权重阶梯回升）→ 100% 回 healthy。
package health

import "time"

// State 健康状态。
type State string

// 六状态。
const (
	StateHealthy    State = "healthy"
	StateDegraded   State = "degraded"
	StateSuspended  State = "suspended"
	StateObserving  State = "observing"
	StateRecovering State = "recovering"
	StateDisabled   State = "disabled" // 仅人工进出
)

// Config 状态机参数（零值时取默认）。
type Config struct {
	FailureThreshold    int           // 软失败连续次数 → suspended（默认 3）
	SuccessThreshold    int           // observing 中连续成功次数 → recovering（默认 2）
	CooldownDuration    time.Duration // suspended 冷却（默认 5 分钟）
	ObservingDuration   time.Duration // 观察窗（默认 5 分钟）
	RecoveryStepPercent int           // 降权/回升步长（默认 25）
}

// withDefaults 填充默认参数。
func (c Config) withDefaults() Config {
	if c.FailureThreshold <= 0 {
		c.FailureThreshold = 3
	}
	if c.SuccessThreshold <= 0 {
		c.SuccessThreshold = 2
	}
	if c.CooldownDuration <= 0 {
		c.CooldownDuration = 5 * time.Minute
	}
	if c.ObservingDuration <= 0 {
		c.ObservingDuration = 5 * time.Minute
	}
	if c.RecoveryStepPercent <= 0 {
		c.RecoveryStepPercent = 25
	}
	return c
}

// Snapshot 状态机的当前快照（输入/输出）。
type Snapshot struct {
	State                State
	ConsecutiveFailures  int
	ConsecutiveSuccesses int
	WeightPercent        int // 100 = 全量
	CooldownUntil        *time.Time
	ObservingUntil       *time.Time
}

// ProbeResult 一次探测的判定输入。
type ProbeResult struct {
	Success    bool
	StatusCode int // 0 = 网络层失败（无响应）
	At         time.Time
}

// Kind 失败类别。
type Kind string

// 失败类别（Transition.Reason 用）。
const (
	KindHardFailure     Kind = "hard_failure"     // 5xx / 401 / 403 / 404
	KindSoftFailure     Kind = "soft_failure"     // 网络错误 / 429
	KindInvalidResponse Kind = "invalid_response" // 其余 4xx：不判定不可用
	KindSuccess         Kind = "probe_success"
	KindCooldownExpired Kind = "cooldown_expired"
	KindObservingDone   Kind = "observing_done"
	KindRecoveryStep    Kind = "recovery_step"
)

// Classify 判定探测结果类别。
func Classify(r ProbeResult) Kind {
	if r.Success {
		return KindSuccess
	}
	switch {
	case r.StatusCode == 0:
		return KindSoftFailure // 网络层失败
	case r.StatusCode == 429:
		return KindSoftFailure
	case r.StatusCode == 401 || r.StatusCode == 403 || r.StatusCode == 404:
		return KindHardFailure
	case r.StatusCode >= 500:
		return KindHardFailure
	case r.StatusCode >= 400:
		return KindInvalidResponse
	default:
		return KindSoftFailure
	}
}

// Transition 一次状态转移结果。
type Transition struct {
	From    State
	To      State
	Reason  Kind
	Changed bool // From != To
}

// Step 用一次探测结果推进状态机，返回新快照与转移记录。
// disabled 状态不响应任何探测（仅人工进出）。
func Step(cfg Config, cur Snapshot, r ProbeResult) (Snapshot, Transition) {
	cfg = cfg.withDefaults()
	next := cur
	kind := Classify(r)
	tr := Transition{From: cur.State, Reason: kind}

	if cur.State == StateDisabled {
		tr.To = StateDisabled
		return next, tr
	}
	// 初始态兜底
	if next.State == "" {
		next.State = StateHealthy
		next.WeightPercent = 100
	}

	switch kind {
	case KindSuccess:
		next.ConsecutiveFailures = 0
		next.ConsecutiveSuccesses++
		switch next.State {
		case StateSuspended:
			// 冷却结束后的成功 → observing；冷却未到也提前进入观察（探测已证明可用）
			next.State = StateObserving
			until := r.At.Add(cfg.ObservingDuration)
			next.ObservingUntil = &until
			next.ConsecutiveSuccesses = 1
			next.WeightPercent = 0
			tr.Reason = KindCooldownExpired
		case StateObserving:
			// 观察窗内累计成功；窗满且达阈值 → recovering
			if next.ObservingUntil != nil && !r.At.Before(*next.ObservingUntil) &&
				next.ConsecutiveSuccesses >= cfg.SuccessThreshold {
				next.State = StateRecovering
				next.ObservingUntil = nil
				next.WeightPercent = cfg.RecoveryStepPercent
				tr.Reason = KindObservingDone
			}
		case StateRecovering:
			// 权重阶梯回升；到 100% 回 healthy
			next.WeightPercent += cfg.RecoveryStepPercent
			if next.WeightPercent >= 100 {
				next.WeightPercent = 100
				next.State = StateHealthy
			}
			tr.Reason = KindRecoveryStep
		case StateDegraded:
			// 降权后恢复成功 → 权重回满即 healthy
			next.WeightPercent += cfg.RecoveryStepPercent
			if next.WeightPercent >= 100 {
				next.WeightPercent = 100
				next.State = StateHealthy
			}
		default: // healthy
			next.WeightPercent = 100
		}

	case KindHardFailure:
		// 硬失败：直接 suspended + 冷却
		next.ConsecutiveSuccesses = 0
		next.ConsecutiveFailures++
		next.State = StateSuspended
		until := r.At.Add(cfg.CooldownDuration)
		next.CooldownUntil = &until
		next.ObservingUntil = nil
		next.WeightPercent = 0

	case KindSoftFailure:
		next.ConsecutiveSuccesses = 0
		next.ConsecutiveFailures++
		switch next.State {
		case StateObserving, StateRecovering:
			// 恢复路径上的失败 → 打回 suspended
			next.State = StateSuspended
			until := r.At.Add(cfg.CooldownDuration)
			next.CooldownUntil = &until
			next.ObservingUntil = nil
			next.WeightPercent = 0
		case StateSuspended:
			// 冷却中继续失败：刷新冷却
			until := r.At.Add(cfg.CooldownDuration)
			next.CooldownUntil = &until
		default: // healthy / degraded
			if next.ConsecutiveFailures >= cfg.FailureThreshold {
				next.State = StateSuspended
				until := r.At.Add(cfg.CooldownDuration)
				next.CooldownUntil = &until
				next.WeightPercent = 0
			} else {
				next.State = StateDegraded
				next.WeightPercent -= cfg.RecoveryStepPercent
				if next.WeightPercent < 0 {
					next.WeightPercent = 0
				}
			}
		}

	case KindInvalidResponse:
		// 参数类 4xx：不改变可用性判定，只清连续成功计数
		next.ConsecutiveSuccesses = 0
	}

	tr.To = next.State
	tr.Changed = tr.From != tr.To
	return next, tr
}

// ProbeBackoff 按连续失败次数返回额外探测退避（叠加在基础间隔上）。
func ProbeBackoff(consecutiveFailures int) time.Duration {
	switch {
	case consecutiveFailures <= 0:
		return 0
	case consecutiveFailures == 1:
		return 2 * time.Minute
	case consecutiveFailures == 2:
		return 5 * time.Minute
	default:
		return 10 * time.Minute
	}
}
