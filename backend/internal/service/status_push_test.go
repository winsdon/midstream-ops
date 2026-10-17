package service

import (
	"strings"
	"testing"
	"time"

	"sub2api-account-monitor/internal/config"
	"sub2api-account-monitor/internal/notify"
	"sub2api-account-monitor/internal/repository"
	"sub2api-account-monitor/internal/statusimg"
)

// statusPushTestService 造一个只用于纯逻辑验证的 StatusPushService。
// 不给 PG：这些用例只覆盖「设置校验 / 调度注册 / 结果记录」这些不需要数据库的路径。
func statusPushTestService(t *testing.T, st StatusPushSettings) *StatusPushService {
	t.Helper()
	cfg := &config.Config{Location: cst}
	settingsRepo := &SettingsService{strategy: defaultStrategy(), statusPush: st}
	return NewStatusPushService(cfg, &repository.PG{}, settingsRepo)
}

// Start → Enabled=false 时只就绪、不排程；Enabled=true 时注册 entry。
// Reload 换间隔必须真的换掉 entry，而不是叠加第二个。
func TestStatusPushServiceScheduleLifecycle(t *testing.T) {
	disabled := defaultStatusPush()
	svc := statusPushTestService(t, disabled)
	if err := svc.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer svc.Stop()
	if svc.entryID != 0 {
		t.Errorf("未启用时不该注册 entry，实际 id=%d", svc.entryID)
	}

	// 打开并改成 30 分钟：应注册一个 entry
	enabled := disabled
	enabled.Enabled = true
	enabled.IntervalMinutes = 30
	svc.Reload(enabled)
	if svc.entryID == 0 {
		t.Fatal("启用后应注册 entry")
	}
	first := svc.entryID

	// 换间隔：entry 必须是新的（旧的一定被移除），条目总数仍为 1
	enabled.IntervalMinutes = 120
	svc.Reload(enabled)
	if svc.entryID == 0 {
		t.Fatal("换间隔后应仍有 entry")
	}
	if svc.entryID == first {
		t.Error("换间隔后 entryID 未变，说明旧 entry 没被移除（会按老间隔继续发）")
	}
	if got := len(svc.cron.Entries()); got != 1 {
		t.Errorf("cron entries = %d, want 1（移除旧 entry 后只留一个）", got)
	}

	// 关掉：entry 应被移除
	enabled.Enabled = false
	svc.Reload(enabled)
	if svc.entryID != 0 {
		t.Errorf("关闭后 entryID = %d, want 0", svc.entryID)
	}
	if got := len(svc.cron.Entries()); got != 0 {
		t.Errorf("cron entries = %d, want 0", got)
	}
}

// Stop 幂等：没 Start 过、或 Stop 两次都不该 panic / 卡住。
func TestStatusPushServiceStopIsIdempotent(t *testing.T) {
	svc := statusPushTestService(t, defaultStatusPush())
	svc.Stop() // 未 Start
	if err := svc.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	svc.Stop()
	svc.Stop()
}

// Start 之后再 Start 不该重复注册（幂等）。
func TestStatusPushServiceStartTwiceKeepsOneEntry(t *testing.T) {
	enabled := defaultStatusPush()
	enabled.Enabled = true
	svc := statusPushTestService(t, enabled)
	if err := svc.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer svc.Stop()
	first := svc.entryID
	if err := svc.Start(); err != nil {
		t.Fatalf("第二次 Start: %v", err)
	}
	if svc.entryID != first {
		t.Errorf("第二次 Start 换了 entryID（%d -> %d），应直接返回", first, svc.entryID)
	}
	if got := len(svc.cron.Entries()); got != 1 {
		t.Errorf("cron entries = %d, want 1", got)
	}
}

// LastPush 返回的必须是副本：改返回值不能碰到内部记录。
func TestStatusPushServiceLastPushReturnsCopy(t *testing.T) {
	svc := statusPushTestService(t, defaultStatusPush())
	if svc.LastPush() != nil {
		t.Fatal("从未推送过应为 nil")
	}

	svc.record(&StatusPushResult{
		At:      "2026-04-08T12:00:00+08:00",
		OK:      false,
		Results: []StatusPushChatResult{{ChatID: "-1001", OK: true}},
	})

	got := svc.LastPush()
	if got == nil || len(got.Results) != 1 {
		t.Fatalf("LastPush = %+v", got)
	}
	// 篡改返回值的切片内容与结构体字段
	got.Results[0].ChatID = "MUTATED"
	got.OK = true

	again := svc.LastPush()
	if again.Results[0].ChatID != "-1001" {
		t.Errorf("内部 Results 被调用方改坏：%q", again.Results[0].ChatID)
	}
	if again.OK {
		t.Error("内部 OK 被调用方改坏")
	}
}

// 没配 Bot Token / 没配 Chat ID 时，push 必须立刻给出可操作的错误，
// 而不是先去查库（PG 抖动会把「没填 Token」覆盖成一条网络错误）。
func TestStatusPushServicePushValidatesLocalConfigFirst(t *testing.T) {
	// settings.PG 是零值 PG（不可用），一旦走到取数就会失败；
	// 因此「返回的是配置错误而不是数据库错误」本身就证明了顺序。
	svc := statusPushTestService(t, defaultStatusPush())
	svc.settings.channels = notify.ChannelConfig{}

	res := svc.PushNow(t.Context())
	if res.OK {
		t.Fatal("未配置时应失败")
	}
	if !strings.Contains(res.Error, "Bot Token") {
		t.Errorf("error = %q, want 提示未配置 Bot Token", res.Error)
	}
	if !strings.Contains(res.At, "T") {
		t.Errorf("at = %q, want RFC3339（含 T）", res.At)
	}
	if _, err := time.Parse(time.RFC3339, res.At); err != nil {
		t.Errorf("at 不是合法 RFC3339: %q (%v)", res.At, err)
	}
	if res.Results != nil {
		t.Errorf("未发送时不该有逐群结果: %+v", res.Results)
	}

	// 有 Token 但没有目标群 → 另一条本地校验
	svc.settings.channels = notify.ChannelConfig{Telegram: notify.TelegramConfig{BotToken: "123:ABC"}}
	res = svc.PushNow(t.Context())
	if !strings.Contains(res.Error, "Chat ID") {
		t.Errorf("error = %q, want 提示未配置 Chat ID", res.Error)
	}
}

// 标题过长会撑爆 caption（Telegram 上限 1024 字符）并让所有群一起失败，
// normalize 必须把它截短。
func TestStatusPushSettingsNormalizeCapsTitle(t *testing.T) {
	long := strings.Repeat("渠", 5000)
	s := StatusPushSettings{Title: long}
	s.normalize()
	if n := len([]rune(s.Title)); n > statusPushMaxTitleRunes+1 { // +1 是省略号
		t.Errorf("标题长度 = %d rune, want <= %d", n, statusPushMaxTitleRunes+1)
	}
	if !strings.HasSuffix(s.Title, "…") {
		t.Errorf("截断后应带省略号: %q", s.Title)
	}

	// 正好在边界内不动
	fits := strings.Repeat("渠", statusPushMaxTitleRunes)
	s = StatusPushSettings{Title: fits}
	s.normalize()
	if s.Title != fits {
		t.Errorf("未超限的标题不该被改动")
	}

	// 真正要守的性质：拼出来的 caption 必须远低于 Telegram 的 1024 字符上限。
	// 只断言「标题被截短」不够 —— caption 里还有 4 段文案与忽略状态码列表，
	// 日后往 caption 里加内容时这条断言才会提醒「该重新算上限了」。
	s = StatusPushSettings{Title: long}
	s.normalize()
	caption := statusPushCaption(s.Title, 64, 6*time.Hour, DefaultIgnoredStatusCodes)
	if n := len([]rune(caption)); n >= telegramCaptionMaxRunes {
		t.Errorf("caption 长度 = %d rune, 必须 < %d：%q", n, telegramCaptionMaxRunes, caption)
	}
	// 边界情况：最长的窗口与最多的分组也要安全
	caption = statusPushCaption(s.Title, 9999, 720*time.Minute, DefaultIgnoredStatusCodes)
	if n := len([]rune(caption)); n >= telegramCaptionMaxRunes {
		t.Errorf("极端 caption 长度 = %d rune, 必须 < %d", n, telegramCaptionMaxRunes)
	}
}

func TestTruncateRunes(t *testing.T) {
	cases := []struct {
		in    string
		limit int
		want  string
	}{
		{"abc", 5, "abc"},
		{"abc", 3, "abc"},
		{"abcd", 3, "abc…"},
		{"渠道状态", 2, "渠道…"},
		{"渠道状态", 0, ""},
		{"", 3, ""},
	}
	for _, c := range cases {
		if got := truncateRunes(c.in, c.limit); got != c.want {
			t.Errorf("truncateRunes(%q, %d) = %q, want %q", c.in, c.limit, got, c.want)
		}
	}
}

// publicStatusGroups 是「什么算公开分组」的唯一实现，状态图与设置页共用。
func TestPublicStatusGroupsKeepsOrderAndFilters(t *testing.T) {
	groups := []repository.PGGroup{
		group(10, "A"),
		group(11, "B-exclusive", exclusive()),
		group(12, "C-disabled", disabled()),
		group(13, "D"),
		group(14, "E"),
	}
	got := publicStatusGroups(groups, nil)
	if len(got) != 3 || got[0].ID != 10 || got[1].ID != 13 || got[2].ID != 14 {
		t.Fatalf("groups = %+v, want [10 13 14]（保持输入顺序）", got)
	}
	got = publicStatusGroups(groups, []int64{13})
	if len(got) != 2 || got[0].ID != 10 || got[1].ID != 14 {
		t.Fatalf("excluded groups = %+v, want [10 14]", got)
	}
}

// 图过宽高限制时截断，且必须让看图的人知道被截了。
func TestStatusPushMaxCardsWithinTelegramLimit(t *testing.T) {
	// 上限必须来自渲染层：本包自己写死一个数字就会与渲染层的告警阈值漂移
	// （历史教训：写过 64，比阈值高一张，满上限推送永远打一条消不掉的告警）。
	if statusPushMaxCards != statusimg.MaxCardsForSafeHeight() {
		t.Errorf("statusPushMaxCards = %d, 必须等于 statusimg.MaxCardsForSafeHeight() = %d",
			statusPushMaxCards, statusimg.MaxCardsForSafeHeight())
	}
	// 至少要能容纳常见规模
	if statusPushMaxCards < 40 {
		t.Errorf("上限 %d 太小，正常站点会有几十个公开分组", statusPushMaxCards)
	}
}

// 裁剪说明必须走独立一行（Board.Truncated），不能塞进 Summary。
//
// 【为什么】Summary 只占画布右半边（约 624px），渲染层按宽度截断，
// 「共 70 个，图仅显示前 62」这类文案会被截到看不见关键数字 —— 那恰好让
// 「悄悄少发分组」在客户可见面上依然成立，正是上限本身想防的事。
// 说明文案的显示宽度由 statusimg 的用例按真实字体量（见
// TestMaxCardsForSafeHeightIsExactAndSelfConsistent 同包内的宽度断言）；
// 这里只锁语义：两个数字都要出现，且顺序是「共 N 个 / 前 M 个」。
func TestTruncationNoticeMentionsBothNumbers(t *testing.T) {
	got := truncationNotice(99, 62)
	for _, want := range []string{"共 99 个", "前 62 个"} {
		if !strings.Contains(got, want) {
			t.Errorf("说明 = %q, 必须含 %q", got, want)
		}
	}

	// caption 也要带上总数（小屏缩略图可能盖住图上的说明行）
	caption := statusPushCaption("渠道状态", 62, 90*time.Minute, DefaultIgnoredStatusCodes)
	caption += "\n（公开渠道共 99 个，图仅显示前 62 个）"
	if n := len([]rune(caption)); n >= telegramCaptionMaxRunes {
		t.Errorf("caption 长度 %d 必须 < %d", n, telegramCaptionMaxRunes)
	}
}
