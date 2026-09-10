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
	applyAudit(run, st.profile)
	verdict := Classify(run.Checks)
	run.Verdict = &verdict
	finish(run)
	return run
}

// RetryChecks 重跑 previous 里指定的检测项，其余结果保留，最后重算判定。
//
// ids 应已是目录顺序。同一批里仍按 Requires 等待；未纳入本批的前置项
// 从 previous 的成功结果里恢复共享状态（签名块、ping usage），避免为了
// 重试一项把已经通过的前置再打一遍。
func RetryChecks(ctx context.Context, target Target, checkIDs []string, previous *TargetRun, gate *Gate, onProgress func(*CheckResult)) *TargetRun {
	run := cloneTargetRun(previous)
	run.Error = ""
	run.FinishedAt = nil
	if run.StartedAt.IsZero() {
		run.StartedAt = time.Now()
	}

	ids := knownCheckIDs(checkIDs)
	if len(ids) == 0 {
		applyAudit(run, ResolveProfile(target.Model))
		verdict := Classify(run.Checks)
		run.Verdict = &verdict
		finish(run)
		return run
	}

	client := NewClient(target)
	st := &runState{profile: ResolveProfile(target.Model)}
	hydrateRunState(st, run.Checks, ids)
	if gate == nil {
		gate = NewGate(1)
	}

	retrying := map[string]struct{}{}
	for _, id := range ids {
		retrying[id] = struct{}{}
	}
	pending := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		pending[id] = struct{}{}
	}
	finished := map[string]struct{}{}

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
			if _, inBatch := retrying[dep]; !inBatch {
				continue
			}
			if _, done := finished[dep]; !done {
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
			upsertCheck(run, ev.res)
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
	applyAudit(run, st.profile)
	verdict := Classify(run.Checks)
	run.Verdict = &verdict
	finish(run)
	return run
}

// applyAudit 跑跨项审计并把结果并入 run。
//
// 必须在 Classify 之前：审计产出的证据与 AuthCapReason 要参与打分。用 upsertCheck
// 而不是 append，保证重试路径下替换旧审计结果而不是叠一份。
func applyAudit(run *TargetRun, profile ThinkingProfile) {
	if run == nil {
		return
	}
	if res := AuditRun(run.Checks, profile); res != nil {
		upsertCheck(run, res)
	}
}

// ExpandRetryIDs 以用户点的请求失败项为种子，带上因前置失败而没真正发出请求的依赖项。
// 已经通过的后续项不重跑。
func ExpandRetryIDs(seed []string, existing []*CheckResult, allowed []string) []string {
	allow := map[string]bool{}
	for _, id := range allowed {
		allow[id] = true
	}
	want := map[string]bool{}
	for _, id := range seed {
		if allow[id] {
			want[id] = true
		}
	}
	byID := map[string]*CheckResult{}
	for _, c := range existing {
		if c != nil {
			byID[c.ID] = c
		}
	}
	changed := true
	for changed {
		changed = false
		for _, meta := range Checks {
			if want[meta.ID] || !allow[meta.ID] {
				continue
			}
			if !shouldFollowRetry(byID[meta.ID]) {
				continue
			}
			for _, dep := range meta.Requires {
				if want[dep] {
					want[meta.ID] = true
					changed = true
					break
				}
			}
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

func shouldFollowRetry(c *CheckResult) bool {
	if c == nil {
		return false
	}
	if RequestFailed(c) {
		return true
	}
	return c.Status == StatusInconclusive && !anyExchangeSucceeded(c.Exchanges)
}

func knownCheckIDs(ids []string) []string {
	want := map[string]bool{}
	for _, id := range ids {
		if _, ok := checkByID(id); ok {
			want[id] = true
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

func cloneTargetRun(src *TargetRun) *TargetRun {
	if src == nil {
		return &TargetRun{StartedAt: time.Now()}
	}
	cp := *src
	cp.Checks = append([]*CheckResult(nil), src.Checks...)
	return &cp
}

func upsertCheck(run *TargetRun, res *CheckResult) {
	if run == nil || res == nil {
		return
	}
	for i, c := range run.Checks {
		if c != nil && c.ID == res.ID {
			run.Checks[i] = res
			return
		}
	}
	run.Checks = append(run.Checks, res)
}

// hydrateRunState 用已完成项的进程内响应体恢复共享状态。
// skip 里的项即将重跑，它们的旧状态不灌进去，避免 ping-again 读到即将被替换的 ping。
func hydrateRunState(st *runState, checks []*CheckResult, skip []string) {
	skipping := map[string]bool{}
	for _, id := range skip {
		skipping[id] = true
	}
	for _, c := range checks {
		if c == nil || skipping[c.ID] {
			continue
		}
		switch c.ID {
		case "ping":
			hydratePingState(st, c)
		case "thinking-sig":
			hydrateThinkingState(st, c)
		}
	}
}

func hydratePingState(st *runState, c *CheckResult) {
	ex := firstOKExchange(c)
	if ex == nil {
		return
	}
	usage := usageOf(ex.JSON)
	st.pingUsage = usage
	st.pingInput = intOf(usage["input_tokens"])
	st.pingCacheWrite = cacheCreationTokens(usage)
	st.pingSeen = true
}

func hydrateThinkingState(st *runState, c *CheckResult) {
	ex := firstOKExchange(c)
	if ex == nil {
		return
	}
	blocks := contentBlocks(ex.JSON)
	idx := -1
	for i, raw := range blocks {
		blk := mapOf(raw)
		if str(blk["type"]) == "thinking" && str(blk["signature"]) != "" {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}
	st.thinkingContent = blocks
	st.thinkingIndex = idx
	st.thinkingParam = st.profile.ThinkingParam("summarized")
	st.thinkingPrompt = thinkingPrompt
}

func firstOKExchange(c *CheckResult) *Exchange {
	if c == nil {
		return nil
	}
	for _, ex := range c.Exchanges {
		if ex != nil && ex.OK() {
			return ex
		}
	}
	return nil
}

// sortChecks 按目录顺序排列结果。不在目录里的项（跨项审计）排到末尾。
func sortChecks(checks []*CheckResult) {
	order := make(map[string]int, len(Checks))
	for i, c := range Checks {
		order[c.ID] = i
	}
	rank := func(id string) int {
		if i, ok := order[id]; ok {
			return i
		}
		return len(Checks)
	}
	sort.SliceStable(checks, func(i, j int) bool {
		return rank(checks[i].ID) < rank(checks[j].ID)
	})
}

func finish(run *TargetRun) {
	now := time.Now()
	run.FinishedAt = &now
}
