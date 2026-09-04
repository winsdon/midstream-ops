package modeldetect

import (
	"context"
	"sort"
	"time"
)

// Gate 限制同时执行的检测项数量。多个目标共享同一把闸门，
// 这样页面上的「并发」就是全局在途检测项上限，而不是「目标数 × 每目标并发」。
type Gate struct {
	ch chan struct{}
}

// NewGate 创建容量为 n 的闸门。n < 1 时按 1 处理。
func NewGate(n int) *Gate {
	if n < 1 {
		n = 1
	}
	return &Gate{ch: make(chan struct{}, n)}
}

// Acquire 占用一个槽位。ctx 取消时返回 false，此时没有占到槽，不必 Release。
func (g *Gate) Acquire(ctx context.Context) bool {
	if g == nil {
		return ctx.Err() == nil
	}
	select {
	case <-ctx.Done():
		return false
	case g.ch <- struct{}{}:
		return true
	}
}

// Release 归还槽位。必须与一次成功的 Acquire 配对。
func (g *Gate) Release() {
	if g == nil {
		return
	}
	<-g.ch
}

// TargetRun 一个目标的完整检测结果（脱敏后可直接落库）。
type TargetRun struct {
	Name       string         `json:"name"`
	BaseURL    string         `json:"base_url"`
	Model      string         `json:"model"`
	AuthMode   string         `json:"auth_mode"`
	AccountID  *int64         `json:"account_id,omitempty"`
	ProviderID *int64         `json:"provider_id,omitempty"`
	Checks     []*CheckResult `json:"checks"`
	Verdict    *Verdict       `json:"verdict,omitempty"`
	StartedAt  time.Time      `json:"started_at"`
	FinishedAt *time.Time     `json:"finished_at,omitempty"`
	Error      string         `json:"error,omitempty"`
}

// ResolveCheckIDs 把用户勾选的项补上前置依赖，并按目录顺序排列。
//
// 补依赖是必要的：签名篡改必须先拿到签名，缓存复现必须先有第一次请求。
// 与其在结果里报「跳过」，不如自动把前置项跑掉。
func ResolveCheckIDs(selected []string) []string {
	want := map[string]bool{}
	var add func(id string)
	add = func(id string) {
		if want[id] {
			return
		}
		meta, ok := checkByID(id)
		if !ok {
			return
		}
		want[id] = true
		for _, dep := range meta.Requires {
			add(dep)
		}
	}
	for _, id := range selected {
		add(id)
	}
	if len(want) == 0 {
		for _, id := range DefaultCheckIDs() {
			add(id)
		}
	}

	out := make([]string, 0, len(want))
	for _, c := range Checks {
		if want[c.ID] {
			out = append(out, c.ID)
		}
	}
	return out
}

// TotalRequests 估算所选检测项的请求数（前端用于提示成本）。
func TotalRequests(ids []string) int {
	total := 0
	for _, id := range ids {
		if c, ok := checkByID(id); ok {
			total += c.Requests
		}
	}
	return total
}

// runningResult 检测项开始执行时的占位结果，供前端格子显示「执行中」。
func runningResult(meta Check) *CheckResult {
	return &CheckResult{ID: meta.ID, Title: meta.Title, Group: meta.Group, Status: StatusRunning}
}

// Run 对单个目标执行检测项。
//
// 无 Requires 的项会竞争 gate 并行执行；有前置依赖的项等前置完成后才入队。
// 这样 ping-again / 签名篡改仍能读到上一档写入的状态，同时互不依赖的项不再排队。
// onProgress 在检测项开始（status=running）和结束时各回调一次，供上层增量推送。
func Run(ctx context.Context, target Target, checkIDs []string, gate *Gate, onProgress func(*CheckResult)) *TargetRun {
	run := &TargetRun{
		Name: target.Name, BaseURL: target.BaseURL, Model: target.Model,
		AuthMode: target.AuthMode, AccountID: target.AccountID, ProviderID: target.ProviderID,
		StartedAt: time.Now(),
	}

	client := NewClient(target)
	st := &runState{profile: ResolveProfile(target.Model)}
	ids := ResolveCheckIDs(checkIDs)
	if gate == nil {
		gate = NewGate(1)
	}

	pending := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		pending[id] = struct{}{}
	}
	finished := make(map[string]struct{}, len(ids))

	type outcome struct {
		id  string
		res *CheckResult
	}
	outcomes := make(chan outcome, len(ids))
	inflight := 0

	depsReady := func(id string) bool {
		meta, ok := checkByID(id)
		if !ok {
			return false
		}
		for _, dep := range meta.Requires {
			if _, ok := finished[dep]; !ok {
				return false
			}
		}
		return true
	}

	startReady := func() {
		if ctx.Err() != nil {
			return
		}
		for _, id := range ids {
			if _, wait := pending[id]; !wait {
				continue
			}
			if !depsReady(id) {
				continue
			}
			delete(pending, id)
			inflight++
			go func(id string) {
				var res *CheckResult
				defer func() { outcomes <- outcome{id: id, res: res} }()
				if !gate.Acquire(ctx) {
					return
				}
				defer gate.Release()
				if ctx.Err() != nil {
					return
				}
				meta, ok := checkByID(id)
				fn, hasFn := handlers[id]
				if !ok || !hasFn {
					return
				}
				if onProgress != nil {
					onProgress(runningResult(meta))
				}
				res = fn(ctx, client, st)
			}(id)
		}
	}

	startReady()
	for inflight > 0 {
		ev := <-outcomes
		inflight--
		finished[ev.id] = struct{}{}
		if ev.res != nil {
			run.Checks = append(run.Checks, ev.res)
			if onProgress != nil {
				onProgress(ev.res)
			}
		}
		if ctx.Err() != nil {
			run.Error = "检测已取消"
			continue
		}
		startReady()
	}

	if ctx.Err() != nil && run.Error == "" {
		run.Error = "检测已取消"
	}
	sortChecks(run.Checks)
	verdict := Classify(run.Checks)
	run.Verdict = &verdict
	finish(run)
	return run
}

func sortChecks(checks []*CheckResult) {
	order := make(map[string]int, len(Checks))
	for i, c := range Checks {
		order[c.ID] = i
	}
	sort.SliceStable(checks, func(i, j int) bool {
		return order[checks[i].ID] < order[checks[j].ID]
	})
}

func finish(run *TargetRun) {
	now := time.Now()
	run.FinishedAt = &now
}
