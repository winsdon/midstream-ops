package health

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)

func probeAt(success bool, code int, at time.Time) ProbeResult {
	return ProbeResult{Success: success, StatusCode: code, At: at}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		name string
		r    ProbeResult
		want Kind
	}{
		{"成功", probeAt(true, 200, t0), KindSuccess},
		{"网络失败", probeAt(false, 0, t0), KindSoftFailure},
		{"限流 429", probeAt(false, 429, t0), KindSoftFailure},
		{"鉴权 401", probeAt(false, 401, t0), KindHardFailure},
		{"禁止 403", probeAt(false, 403, t0), KindHardFailure},
		{"模型不存在 404", probeAt(false, 404, t0), KindHardFailure},
		{"服务端 500", probeAt(false, 500, t0), KindHardFailure},
		{"参数错误 400", probeAt(false, 400, t0), KindInvalidResponse},
		{"422", probeAt(false, 422, t0), KindInvalidResponse},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Classify(tt.r); got != tt.want {
				t.Errorf("Classify() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHardFailureSuspendsImmediately(t *testing.T) {
	cur := Snapshot{State: StateHealthy, WeightPercent: 100}
	next, tr := Step(Config{}, cur, probeAt(false, 500, t0))
	if next.State != StateSuspended {
		t.Fatalf("硬失败应直接 suspended，got %v", next.State)
	}
	if next.CooldownUntil == nil || !next.CooldownUntil.Equal(t0.Add(5*time.Minute)) {
		t.Fatalf("冷却截止应为 +5m，got %v", next.CooldownUntil)
	}
	if !tr.Changed || tr.Reason != KindHardFailure {
		t.Fatalf("转移记录错误: %+v", tr)
	}
}

func TestSoftFailureDegradesThenSuspends(t *testing.T) {
	cfg := Config{FailureThreshold: 3}
	cur := Snapshot{State: StateHealthy, WeightPercent: 100}

	// 第 1 次软失败 → degraded 75%
	cur, _ = Step(cfg, cur, probeAt(false, 0, t0))
	if cur.State != StateDegraded || cur.WeightPercent != 75 {
		t.Fatalf("第 1 次软失败后应 degraded/75，got %v/%d", cur.State, cur.WeightPercent)
	}
	// 第 2 次 → degraded 50%
	cur, _ = Step(cfg, cur, probeAt(false, 429, t0.Add(time.Minute)))
	if cur.State != StateDegraded || cur.WeightPercent != 50 {
		t.Fatalf("第 2 次软失败后应 degraded/50，got %v/%d", cur.State, cur.WeightPercent)
	}
	// 第 3 次达阈值 → suspended
	cur, tr := Step(cfg, cur, probeAt(false, 0, t0.Add(2*time.Minute)))
	if cur.State != StateSuspended || !tr.Changed {
		t.Fatalf("第 3 次软失败应 suspended，got %v", cur.State)
	}
}

func TestInvalidResponseDoesNotSuspend(t *testing.T) {
	cur := Snapshot{State: StateHealthy, WeightPercent: 100, ConsecutiveSuccesses: 5}
	next, tr := Step(Config{}, cur, probeAt(false, 400, t0))
	if next.State != StateHealthy {
		t.Fatalf("参数类 4xx 不应改变可用性，got %v", next.State)
	}
	if next.ConsecutiveSuccesses != 0 {
		t.Fatalf("连续成功计数应清零，got %d", next.ConsecutiveSuccesses)
	}
	if tr.Changed {
		t.Fatalf("不应有状态转移")
	}
}

func TestRecoveryPath(t *testing.T) {
	cfg := Config{SuccessThreshold: 2, ObservingDuration: 5 * time.Minute, RecoveryStepPercent: 25}
	cooldown := t0.Add(5 * time.Minute)
	cur := Snapshot{State: StateSuspended, CooldownUntil: &cooldown, ConsecutiveFailures: 3}

	// 冷却后首次成功 → observing（权重 0）
	cur, tr := Step(cfg, cur, probeAt(true, 200, t0.Add(6*time.Minute)))
	if cur.State != StateObserving || cur.WeightPercent != 0 {
		t.Fatalf("应进入 observing/0，got %v/%d", cur.State, cur.WeightPercent)
	}
	if tr.Reason != KindCooldownExpired {
		t.Fatalf("reason 应为 cooldown_expired，got %v", tr.Reason)
	}

	// 观察窗内第 2 次成功（未满窗）→ 停留 observing
	cur, _ = Step(cfg, cur, probeAt(true, 200, t0.Add(8*time.Minute)))
	if cur.State != StateObserving {
		t.Fatalf("观察窗未满应停留 observing，got %v", cur.State)
	}

	// 窗满 + 连续成功达阈值 → recovering（25%）
	cur, tr = Step(cfg, cur, probeAt(true, 200, t0.Add(12*time.Minute)))
	if cur.State != StateRecovering || cur.WeightPercent != 25 {
		t.Fatalf("应 recovering/25，got %v/%d", cur.State, cur.WeightPercent)
	}
	if tr.Reason != KindObservingDone {
		t.Fatalf("reason 应为 observing_done，got %v", tr.Reason)
	}

	// 阶梯回升：50 → 75 → 100 回 healthy
	cur, _ = Step(cfg, cur, probeAt(true, 200, t0.Add(13*time.Minute)))
	cur, _ = Step(cfg, cur, probeAt(true, 200, t0.Add(14*time.Minute)))
	cur, tr = Step(cfg, cur, probeAt(true, 200, t0.Add(15*time.Minute)))
	if cur.State != StateHealthy || cur.WeightPercent != 100 {
		t.Fatalf("应回 healthy/100，got %v/%d", cur.State, cur.WeightPercent)
	}
	if !tr.Changed {
		t.Fatalf("recovering→healthy 应记为转移")
	}
}

func TestRecoveryFailureFallsBack(t *testing.T) {
	until := t0.Add(5 * time.Minute)
	cur := Snapshot{State: StateObserving, ObservingUntil: &until, ConsecutiveSuccesses: 1}
	next, _ := Step(Config{}, cur, probeAt(false, 0, t0.Add(time.Minute)))
	if next.State != StateSuspended {
		t.Fatalf("观察期失败应打回 suspended，got %v", next.State)
	}
}

func TestDisabledIgnoresProbes(t *testing.T) {
	cur := Snapshot{State: StateDisabled}
	next, tr := Step(Config{}, cur, probeAt(true, 200, t0))
	if next.State != StateDisabled || tr.Changed {
		t.Fatalf("disabled 不应响应探测，got %v", next.State)
	}
	next, _ = Step(Config{}, cur, probeAt(false, 500, t0))
	if next.State != StateDisabled {
		t.Fatalf("disabled 不应响应失败，got %v", next.State)
	}
}

func TestProbeBackoff(t *testing.T) {
	tests := []struct {
		failures int
		want     time.Duration
	}{
		{0, 0},
		{1, 2 * time.Minute},
		{2, 5 * time.Minute},
		{3, 10 * time.Minute},
		{10, 10 * time.Minute},
	}
	for _, tt := range tests {
		if got := ProbeBackoff(tt.failures); got != tt.want {
			t.Errorf("ProbeBackoff(%d) = %v, want %v", tt.failures, got, tt.want)
		}
	}
}
