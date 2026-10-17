package service

import (
	"strings"
	"testing"
	"time"

	"sub2api-account-monitor/internal/repository"
	"sub2api-account-monitor/internal/statusimg"
)

// cst 固定时区，让「更新 04-08 12:00」这类断言不受本机时区影响。
var cst = time.FixedZone("CST", 8*3600)

// boardNow 测试基准时间：整点整秒，便于人肉核对块边界。
func boardNow() time.Time { return time.Date(2026, 4, 8, 12, 0, 0, 0, cst) }

// boardWindow 默认窗口 90 分钟 ÷ 8 块 = 每块 11.25 分钟。
func boardWindow() time.Duration { return statusWindowDefaultMinutes * time.Minute }

// bucketAt 第 idx 块的桶起点（与 SQL 的 date_bin(origin=since) 一致）。
func bucketAt(now time.Time, window time.Duration, idx int) time.Time {
	return now.Truncate(time.Second).Add(-window + time.Duration(idx)*(window/statusBlockCount))
}

func group(id int64, name string, opts ...func(*repository.PGGroup)) repository.PGGroup {
	g := repository.PGGroup{ID: id, Name: name, Status: "active", RateMultiplier: 1}
	for _, o := range opts {
		o(&g)
	}
	return g
}

func withRate(r float64) func(*repository.PGGroup) {
	return func(g *repository.PGGroup) { g.RateMultiplier = r }
}
func exclusive() func(*repository.PGGroup) {
	return func(g *repository.PGGroup) { g.IsExclusive = true }
}
func disabled() func(*repository.PGGroup) {
	return func(g *repository.PGGroup) { g.Status = "disabled" }
}

func fp(v float64) *float64 { return &v }

// 空看板：没有任何流量时仍要给满 8 个灰块。
// 「补齐」而不是「只画有数据的块」是刻意的：块的横向位置代表时间段，
// 缺块会让看图的人以为是数据丢了，而不是那段时间没有流量。
func TestBuildStatusBoardFillsEightGreyBlocks(t *testing.T) {
	board := buildStatusBoard(
		[]repository.PGGroup{group(1, "默认分组", withRate(0.08))},
		nil, nil, boardNow(), boardWindow(),
	)

	if len(board.Cards) != 1 {
		t.Fatalf("cards = %d, want 1", len(board.Cards))
	}
	card := board.Cards[0]
	if len(card.Blocks) != statusBlockCount {
		t.Fatalf("blocks = %d, want %d", len(card.Blocks), statusBlockCount)
	}
	for i, b := range card.Blocks {
		if b.HasData {
			t.Errorf("block %d HasData = true, want false（无流量应画灰块）", i)
		}
	}
	if card.Status != "无流量" || card.StatusLevel != "none" {
		t.Errorf("status = %q/%q, want 无流量/none", card.Status, card.StatusLevel)
	}
	if card.SuccessText != "-" || card.CacheText != "-" {
		t.Errorf("success/cache = %q/%q, want -/-（无样本不是 0%%）", card.SuccessText, card.CacheText)
	}
	if board.Title != defaultStatusPushTitle {
		t.Errorf("title = %q, want %q", board.Title, defaultStatusPushTitle)
	}
	if board.Summary != "1 个渠道 · 比率近90分钟" {
		t.Errorf("summary = %q", board.Summary)
	}
	if board.UpdatedAt != "更新 04-08 12:00" {
		t.Errorf("updated_at = %q", board.UpdatedAt)
	}
}

// 样本门槛：一块里 ok+err < 3 视为样本不足（灰块），够 3 个才算分。
// 这条规则防的是「低频分组一次失败就整块标红」。
func TestBuildStatusBoardSampleThreshold(t *testing.T) {
	now, window := boardNow(), boardWindow()
	points := []repository.GroupStatusTimelinePoint{
		// 第 0 块：2 个成功，样本不足
		{GroupID: 1, Bucket: bucketAt(now, window, 0), Ok: 2},
		// 第 1 块：2 成功 1 失败 = 3，刚够门槛
		{GroupID: 1, Bucket: bucketAt(now, window, 1), Ok: 2, Err: 1},
		// 第 2 块：0 成功 3 失败，样本够
		{GroupID: 1, Bucket: bucketAt(now, window, 2), Err: 3},
	}
	board := buildStatusBoard([]repository.PGGroup{group(1, "g")}, points, nil, now, window)
	blocks := board.Cards[0].Blocks

	if blocks[0].HasData {
		t.Errorf("block 0 = %+v, want 样本不足（2 < 3）", blocks[0])
	}
	if !blocks[1].HasData || blocks[1].Score != 50 {
		t.Errorf("block 1 = %+v, want HasData + score 50（错误率 1/3 约 33%%，超 10%% 固定扣 50）", blocks[1])
	}
	if !blocks[2].HasData || blocks[2].Score != 50 {
		t.Errorf("block 2 = %+v, want HasData + score 50（全失败扣 50）", blocks[2])
	}
	for i := 3; i < statusBlockCount; i++ {
		if blocks[i].HasData {
			t.Errorf("block %d has data, want grey", i)
		}
	}
}

// 健康分与前端 accountHealthScore 同算法，边界逐档钉住。
func TestBlockHealthScore(t *testing.T) {
	cases := []struct {
		name    string
		ok, err int64
		ttftMs  *float64
		want    int
	}{
		{name: "完美", ok: 100, err: 0, ttftMs: fp(1000), want: 100},
		{name: "错误率 1% 不扣分", ok: 99, err: 1, ttftMs: fp(1000), want: 100},
		{name: "首字 10s 不扣分", ok: 100, err: 0, ttftMs: fp(10_000), want: 100},
		{name: "首字 20s 扣一半（15 分）", ok: 100, err: 0, ttftMs: fp(20_000), want: 85},
		{name: "首字 30s 扣满 30 分", ok: 100, err: 0, ttftMs: fp(30_000), want: 70},
		{name: "首字 60s 仍扣 40 分封顶", ok: 100, err: 0, ttftMs: fp(60_000), want: 60},
		{name: "错误率 10% 扣 40 分", ok: 90, err: 10, ttftMs: fp(1000), want: 60},
		{name: "错误率 5.5% 扣 20 分", ok: 189, err: 11, ttftMs: fp(1000), want: 80},
		{name: "错误率超 10% 固定扣 50", ok: 50, err: 50, ttftMs: fp(1000), want: 50},
		{name: "两项叠加", ok: 90, err: 10, ttftMs: fp(30_000), want: 30},
		{name: "缺首字样本不扣分", ok: 80, err: 20, ttftMs: nil, want: 50},
		{name: "负首字不扣分", ok: 100, err: 0, ttftMs: fp(-1), want: 100},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := blockHealthScore(c.ok, c.err, c.ttftMs); got != c.want {
				t.Errorf("blockHealthScore(%d,%d,%v) = %d, want %d", c.ok, c.err, c.ttftMs, got, c.want)
			}
		})
	}
}

// 右上角状态只看最近 2 块 —— 反映「当前」，不是整窗平均。
func TestCurrentStatusRules(t *testing.T) {
	now, window := boardNow(), boardWindow()
	at := func(i int) time.Time { return bucketAt(now, window, i) }

	cases := []struct {
		name       string
		points     []repository.GroupStatusTimelinePoint
		wantStatus string
		wantLevel  string
	}{
		{
			name:       "无流量",
			points:     nil,
			wantStatus: "无流量",
			wantLevel:  "none",
		},
		{
			name: "正常",
			points: []repository.GroupStatusTimelinePoint{
				{GroupID: 1, Bucket: at(6), Ok: 10, FirstTokP50: fp(3000)},
				{GroupID: 1, Bucket: at(7), Ok: 10, FirstTokP50: fp(3500)},
			},
			wantStatus: "正常",
			wantLevel:  "ok",
		},
		{
			name: "SLA 低于 90 判异常",
			points: []repository.GroupStatusTimelinePoint{
				{GroupID: 1, Bucket: at(7), Ok: 8, Err: 2},
			},
			wantStatus: "异常",
			wantLevel:  "bad",
		},
		{
			name: "首字超 10s 判延迟",
			points: []repository.GroupStatusTimelinePoint{
				{GroupID: 1, Bucket: at(7), Ok: 10, FirstTokP50: fp(12_000)},
			},
			wantStatus: "延迟",
			wantLevel:  "warn",
		},
		{
			name: "异常优先于延迟",
			points: []repository.GroupStatusTimelinePoint{
				{GroupID: 1, Bucket: at(7), Ok: 5, Err: 5, FirstTokP50: fp(12_000)},
			},
			wantStatus: "异常",
			wantLevel:  "bad",
		},
		{
			name: "早先的故障不影响当前状态",
			points: []repository.GroupStatusTimelinePoint{
				{GroupID: 1, Bucket: at(0), Err: 50},
				{GroupID: 1, Bucket: at(5), Ok: 1, Err: 9},
				{GroupID: 1, Bucket: at(6), Ok: 10},
				{GroupID: 1, Bucket: at(7), Ok: 10},
			},
			wantStatus: "正常",
			wantLevel:  "ok",
		},
		{
			name: "窗口内有流量但最近两块没有也算无流量",
			points: []repository.GroupStatusTimelinePoint{
				{GroupID: 1, Bucket: at(3), Ok: 100},
			},
			wantStatus: "无流量",
			wantLevel:  "none",
		},
		{
			// 门槛必须同时管住状态：否则会出现「8 块全灰 + 右上角红异常」的自相矛盾卡片。
			name: "最近两块样本不足判样本不足而非异常",
			points: []repository.GroupStatusTimelinePoint{
				{GroupID: 1, Bucket: at(7), Err: 1},
			},
			wantStatus: "样本不足",
			wantLevel:  "none",
		},
		{
			name: "最近两块合计刚好到门槛就正常判定",
			points: []repository.GroupStatusTimelinePoint{
				{GroupID: 1, Bucket: at(6), Ok: 2},
				{GroupID: 1, Bucket: at(7), Ok: 1},
			},
			wantStatus: "正常",
			wantLevel:  "ok",
		},
		{
			// 89.95% 显示成 89.9%（异常），而 89.96% 显示成 90.0% 就不该是异常 ——
			// 判定与显示必须同一个舍入值，否则图上会写「成功率 90.0%」同时标红。
			name: "显示为 90.0% 时不得判异常（舍入一致）",
			points: []repository.GroupStatusTimelinePoint{
				{GroupID: 1, Bucket: at(7), Ok: 8996, Err: 1004},
			},
			wantStatus: "正常",
			wantLevel:  "ok",
		},
		{
			name: "显示为 89.9% 才判异常",
			points: []repository.GroupStatusTimelinePoint{
				{GroupID: 1, Bucket: at(7), Ok: 8994, Err: 1006},
			},
			wantStatus: "异常",
			wantLevel:  "bad",
		},
		{
			name: "首字取最近一个有样本的块",
			points: []repository.GroupStatusTimelinePoint{
				{GroupID: 1, Bucket: at(6), Ok: 10, FirstTokP50: fp(20_000)},
				{GroupID: 1, Bucket: at(7), Ok: 10, FirstTokP50: fp(3_000)},
			},
			wantStatus: "正常",
			wantLevel:  "ok",
		},
		{
			name: "最近一块没有首字样本时回退到上一块",
			points: []repository.GroupStatusTimelinePoint{
				{GroupID: 1, Bucket: at(6), Ok: 10, FirstTokP50: fp(20_000)},
				{GroupID: 1, Bucket: at(7), Ok: 10},
			},
			wantStatus: "延迟",
			wantLevel:  "warn",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			board := buildStatusBoard([]repository.PGGroup{group(1, "g")}, c.points, nil, now, window)
			card := board.Cards[0]
			if card.Status != c.wantStatus || card.StatusLevel != c.wantLevel {
				t.Errorf("status = %q/%q, want %q/%q", card.Status, card.StatusLevel, c.wantStatus, c.wantLevel)
			}
		})
	}
}

// 公开分组的口径：active 且非专属；排除集生效；顺序沿用 ListGroups（sort_order）。
func TestBuildStatusBoardFiltersAndKeepsOrder(t *testing.T) {
	groups := []repository.PGGroup{
		group(10, "A"),
		group(11, "B-exclusive", exclusive()),
		group(12, "C-disabled", disabled()),
		group(13, "D"),
		group(14, "E"),
	}
	now, window := boardNow(), boardWindow()
	points := []repository.GroupStatusTimelinePoint{
		{GroupID: 10, Bucket: bucketAt(now, window, 7), Ok: 10},
		{GroupID: 11, Bucket: bucketAt(now, window, 7), Ok: 10},
		{GroupID: 12, Bucket: bucketAt(now, window, 7), Ok: 10},
		{GroupID: 13, Bucket: bucketAt(now, window, 7), Ok: 10},
		{GroupID: 14, Bucket: bucketAt(now, window, 7), Ok: 10},
	}

	board := buildStatusBoard(groups, points, nil, now, window)
	if len(board.Cards) != 3 {
		t.Fatalf("cards = %d, want 3（专属与 disabled 都不算公开）", len(board.Cards))
	}
	names := []string{board.Cards[0].Name, board.Cards[1].Name, board.Cards[2].Name}
	if names[0] != "A" || names[1] != "D" || names[2] != "E" {
		t.Errorf("order = %v, want [A D E]（保持 ListGroups 的顺序）", names)
	}
	if board.Summary != "3 个渠道 · 比率近90分钟" {
		t.Errorf("summary = %q", board.Summary)
	}

	// 排除 D（13）：卡片消失，且它那些点也不会漏进别的卡片。
	board = buildStatusBoard(groups, points, []int64{13}, now, window)
	if len(board.Cards) != 2 || board.Cards[0].Name != "A" || board.Cards[1].Name != "E" {
		t.Fatalf("cards after exclusion = %+v", board.Cards)
	}
	if board.Cards[1].Blocks[7].Score != 100 {
		t.Errorf("E block 7 = %+v, want 只有 E 自己的数据", board.Cards[1].Blocks[7])
	}
}

// 底部两个数按整窗算：SLA 与缓存率。
func TestBuildStatusBoardWindowTotals(t *testing.T) {
	now, window := boardNow(), boardWindow()
	points := []repository.GroupStatusTimelinePoint{
		{GroupID: 1, Bucket: bucketAt(now, window, 0), Ok: 90, Err: 10, InputTokens: 300, CacheReadTokens: 100},
		{GroupID: 1, Bucket: bucketAt(now, window, 3), Ok: 100, InputTokens: 0, CacheReadTokens: 100},
	}
	board := buildStatusBoard([]repository.PGGroup{group(1, "g")}, points, nil, now, window)
	card := board.Cards[0]
	if card.SuccessText != "95.0%" {
		t.Errorf("success = %q, want 95.0%%（190/200）", card.SuccessText)
	}
	// 缓存率 = 200 / (300 + 200) = 40%
	if card.CacheText != "40.0%" {
		t.Errorf("cache = %q, want 40.0%%", card.CacheText)
	}
}

// 窗口外的桶（since 之前、或分桶溢出的边角）不能落进任何一块。
func TestBuildStatusBoardIgnoresOutOfWindowBuckets(t *testing.T) {
	now, window := boardNow(), boardWindow()
	since := now.Truncate(time.Second).Add(-window)
	points := []repository.GroupStatusTimelinePoint{
		{GroupID: 1, Bucket: since.Add(-time.Minute), Ok: 100, Err: 100}, // 窗口之前（早 1 分钟：整除法会把它截断成下标 0）
		{GroupID: 1, Bucket: since.Add(-window), Ok: 100, Err: 100},      // 更早一个整窗
		{GroupID: 1, Bucket: since.Add(window), Ok: 100, Err: 100},       // 窗口之后（下一个 origin）
		{GroupID: 1, Bucket: bucketAt(now, window, 7), Ok: 10},
	}
	board := buildStatusBoard([]repository.PGGroup{group(1, "g")}, points, nil, now, window)
	card := board.Cards[0]
	if card.SuccessText != "100.0%" {
		t.Errorf("success = %q, want 100.0%%（窗口外的 100 个失败必须被丢弃）", card.SuccessText)
	}
	if card.Status != "正常" {
		t.Errorf("status = %q, want 正常", card.Status)
	}
}

// 倍率显示固定两位小数，与截图里的「0.08x」一致。
func TestBuildStatusBoardRateFormat(t *testing.T) {
	cases := []struct {
		rate float64
		want string
	}{
		{0.08, "0.08x"},
		{1, "1.00x"},
		{0.0833, "0.08x"},
		{2.5, "2.50x"},
		{0, "0.00x"},
	}
	for _, c := range cases {
		board := buildStatusBoard([]repository.PGGroup{group(1, "g", withRate(c.rate))}, nil, nil, boardNow(), boardWindow())
		if got := board.Cards[0].Rate; got != c.want {
			t.Errorf("rate %v = %q, want %q", c.rate, got, c.want)
		}
	}
}

// 窗口参数非法时退回默认 90 分钟，不能让除零或空图漏出去。
func TestBuildStatusBoardFallsBackToDefaultWindow(t *testing.T) {
	board := buildStatusBoard([]repository.PGGroup{group(1, "g")}, nil, nil, boardNow(), 0)
	if !strings.Contains(board.Summary, "90分钟") {
		t.Errorf("summary = %q, want 默认 90 分钟窗口", board.Summary)
	}
	if len(board.Cards[0].Blocks) != statusBlockCount {
		t.Errorf("blocks = %d, want %d", len(board.Cards[0].Blocks), statusBlockCount)
	}
}

// 空分组列表：图仍然能画（标题 + 摘要 + 0 张卡），不能 panic。
func TestBuildStatusBoardZeroGroups(t *testing.T) {
	board := buildStatusBoard(nil, nil, nil, boardNow(), boardWindow())
	if len(board.Cards) != 0 {
		t.Fatalf("cards = %d, want 0", len(board.Cards))
	}
	if !strings.Contains(board.Summary, "0 个渠道") {
		t.Errorf("summary = %q", board.Summary)
	}
	var _ statusimg.Board = board
}

func TestStatusPushCronSpecAlignsToClock(t *testing.T) {
	cases := map[int]string{
		30:  "*/30 * * * *",
		60:  "0 * * * *",
		120: "0 */2 * * *",
		180: "0 */3 * * *",
		240: "0 */4 * * *",
		360: "0 */6 * * *",
		720: "0 */12 * * *",
		// 未登记的档位一律回落到整点每小时，而不是 @every（重启会换相位）
		45: "0 * * * *",
		0:  "0 * * * *",
	}
	for minutes, want := range cases {
		if got := statusPushCronSpec(minutes); got != want {
			t.Errorf("statusPushCronSpec(%d) = %q, want %q", minutes, got, want)
		}
	}
}

func TestStatusPushSettingsNormalize(t *testing.T) {
	// 非法间隔回落到 60（而不是最小值 30）：非法值多半来自手改库，保守取慢。
	s := StatusPushSettings{IntervalMinutes: 7, WindowMinutes: 1, Title: "  "}
	s.normalize()
	if s.IntervalMinutes != statusPushFallbackIntervalMinutes {
		t.Errorf("interval = %d, want %d", s.IntervalMinutes, statusPushFallbackIntervalMinutes)
	}
	if s.WindowMinutes != statusPushWindowMinutesDefault {
		t.Errorf("window = %d, want %d", s.WindowMinutes, statusPushWindowMinutesDefault)
	}
	if s.Title != defaultStatusPushTitle {
		t.Errorf("title = %q, want %q", s.Title, defaultStatusPushTitle)
	}
	if s.ChatIDs == nil || s.ExcludedGroupIDs == nil {
		t.Errorf("nil slices must become empty: %+v", s)
	}

	s = StatusPushSettings{
		IntervalMinutes:  120,
		WindowMinutes:    180,
		ChatIDs:          []string{" -1001 ", "", "-1001", "@chan", "  "},
		ExcludedGroupIDs: []int64{5},
		Title:            " 渠道状态 ",
	}
	s.normalize()
	if s.IntervalMinutes != 120 || s.WindowMinutes != 180 {
		t.Errorf("legal values must be kept: %+v", s)
	}
	want := []string{"-1001", "@chan"}
	if len(s.ChatIDs) != len(want) || s.ChatIDs[0] != want[0] || s.ChatIDs[1] != want[1] {
		t.Errorf("chat_ids = %v, want %v（trim + 去重 + 丢空行）", s.ChatIDs, want)
	}
	if s.Title != "渠道状态" {
		t.Errorf("title = %q, want trimmed", s.Title)
	}
}

func TestValidateStatusPushChatIDs(t *testing.T) {
	ok := [][]string{
		{"-1001234567890"},
		{"-1001234567890", "@my_channel"},
		{"123456"},
		{},
		{""},
	}
	for _, ids := range ok {
		if err := ValidateStatusPushChatIDs(ids); err != nil {
			t.Errorf("ValidateStatusPushChatIDs(%v) = %v, want nil", ids, err)
		}
	}
	bad := [][]string{
		{"abc"},
		{"@ab"},
		{"123abc"},
		{"-1001234567890", "oops"},
		{"@channel name"},
	}
	for _, ids := range bad {
		if err := ValidateStatusPushChatIDs(ids); err == nil {
			t.Errorf("ValidateStatusPushChatIDs(%v) = nil, want error", ids)
		}
	}
}

func TestBlockDurationText(t *testing.T) {
	cases := map[time.Duration]string{
		90 * time.Minute:  "11.25 分钟",
		60 * time.Minute:  "7.5 分钟",
		180 * time.Minute: "22.5 分钟",
		360 * time.Minute: "45 分钟",
	}
	for window, want := range cases {
		if got := blockDurationText(window); got != want {
			t.Errorf("blockDurationText(%v) = %q, want %q", window, got, want)
		}
	}
}

func TestStatusPushCaption(t *testing.T) {
	got := statusPushCaption("渠道状态", 11, 90*time.Minute, []int32{400, 403, 429, 529})
	for _, want := range []string{"渠道状态", "11 个渠道", "近 90 分钟", "11.25 分钟", "400/403/429/529"} {
		if !strings.Contains(got, want) {
			t.Errorf("caption = %q, want 含 %q", got, want)
		}
	}
	// 标题留空时回落默认标题，而不是发出以「｜」开头的怪句子。
	if got := statusPushCaption("   ", 1, time.Hour, nil); !strings.HasPrefix(got, defaultStatusPushTitle) {
		t.Errorf("caption = %q, want 以默认标题开头", got)
	}
}

// 判定与显示必须用同一个舍入值，这里把两者的关系直接钉死：
// 图上显示的字符串数值 -> 就是 currentStatus 拿来比阈值的那个数。
func TestDisplayPercentMatchesPercentText(t *testing.T) {
	cases := []struct {
		raw      float64
		wantText string
		wantNum  float64
	}{
		{89.96, "90.0%", 90.0},
		{89.94, "89.9%", 89.9},
		{90, "90.0%", 90},
		{100, "100.0%", 100},
		{0, "0.0%", 0},
		{91.25, "91.3%", 91.3},
	}
	for _, c := range cases {
		raw := c.raw
		if got := percentText(&raw); got != c.wantText {
			t.Errorf("percentText(%v) = %q, want %q", c.raw, got, c.wantText)
		}
		if got := displayPercent(c.raw); got != c.wantNum {
			t.Errorf("displayPercent(%v) = %v, want %v", c.raw, got, c.wantNum)
		}
	}
	if percentText(nil) != "-" {
		t.Errorf("无样本必须显示 - 而不是 0%%")
	}
}

// window 小到 block 为 0 时不能 panic（纯函数应对任意输入有定义）。
func TestBuildStatusBoardTinyWindowDoesNotPanic(t *testing.T) {
	board := buildStatusBoard(
		[]repository.PGGroup{group(1, "g")},
		[]repository.GroupStatusTimelinePoint{{GroupID: 1, Bucket: boardNow(), Ok: 10}},
		nil, boardNow(), time.Nanosecond,
	)
	if len(board.Cards) != 1 || len(board.Cards[0].Blocks) != statusBlockCount {
		t.Fatalf("tiny window board = %+v", board)
	}
}
