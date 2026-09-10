package handler

import (
	"net/http/httptest"
	"testing"
	"time"

	"sub2api-account-monitor/internal/repository"

	"github.com/gin-gonic/gin"
)

// newQueryContext 构造一个只带 query string 的 gin.Context，供纯解析函数测试用。
func newQueryContext(rawQuery string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/?"+rawQuery, nil)
	return c
}

func TestParseWindow(t *testing.T) {
	const def = 24 * 60

	tests := []struct {
		name        string
		query       string
		wantMinutes int
	}{
		{"最短档位 5 分钟", "minutes=5", 5},
		{"30 分钟", "minutes=30", 30},
		{"6 小时", "minutes=360", 360},
		{"1440 分钟", "minutes=1440", 1440},
		{"无参数取默认", "", def},
		{"minutes=0 非法回退", "minutes=0", def},
		{"minutes 负数回退", "minutes=-5", def},
		{"minutes 非数字回退", "minutes=abc", def},
		{"minutes 超上限回退", "minutes=43201", def},
		{"minutes 恰好上限", "minutes=43200", 43200},
		{"hours 兼容分支", "hours=24", 1440},
		{"hours 超上限回退", "hours=721", def},
		// minutes 存在即走 minutes 分支，hours 不参与——否则前端切到 5 分钟时
		// 若 URL 里残留旧的 hours 参数，窗口会被悄悄拉回 24 小时。
		{"minutes 优先于 hours", "minutes=5&hours=24", 5},
		// minutes 非法时不回落到 hours：非法值代表调用方意图有误，
		// 静默改用另一个参数会让排查更困难，直接给默认值。
		{"minutes 非法时不回落 hours", "minutes=abc&hours=1", def},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			window, minutes := parseWindow(newQueryContext(tt.query), def)

			if minutes != tt.wantMinutes {
				t.Errorf("minutes = %d, want %d", minutes, tt.wantMinutes)
			}
			if want := time.Duration(tt.wantMinutes) * time.Minute; window != want {
				t.Errorf("window = %v, want %v", window, want)
			}
		})
	}
}

func TestDefaultWindowMinutesIsOneHour(t *testing.T) {
	if defaultWindowMinutes != 60 {
		t.Errorf("defaultWindowMinutes = %d, want 60", defaultWindowMinutes)
	}
}

func TestTotalPassiveRequestsIncludesErrors(t *testing.T) {
	if got := totalPassiveRequests(2, 16); got != 18 {
		t.Errorf("2 成功 + 16 失败 = %d, want 18", got)
	}
	if got := totalPassiveRequests(0, 5); got != 5 {
		t.Errorf("仅失败 = %d, want 5", got)
	}
	if got := totalPassiveRequests(3, 0); got != 3 {
		t.Errorf("仅成功 = %d, want 3", got)
	}
}

func TestSlaPercent(t *testing.T) {
	if got := slaPercent(99, 1); got != float64(99) {
		t.Errorf("99/100 = %v, want 99", got)
	}
	if got := slaPercent(100, 0); got != float64(100) {
		t.Errorf("100/0err = %v, want 100", got)
	}
	if got := slaPercent(0, 5); got != float64(0) {
		t.Errorf("0/5 = %v, want 0", got)
	}
	if got := slaPercent(0, 0); got != nil {
		t.Errorf("0/0 = %v, want nil", got)
	}
}

func TestAttachGroupsEmptyBucketIsEmptySlice(t *testing.T) {
	item := gin.H{}
	attachGroups(item, 999, map[int64][]string{1: {"pro"}})
	gs, ok := item["groups"].([]string)
	if !ok {
		t.Fatalf("groups type = %T, want []string", item["groups"])
	}
	if len(gs) != 0 {
		t.Errorf("unassigned groups = %v, want empty slice", gs)
	}
}

func TestAttachGroupsSortsNames(t *testing.T) {
	item := gin.H{}
	attachGroups(item, 1, map[int64][]string{1: {"pro", "default"}})
	gs := item["groups"].([]string)
	if len(gs) != 2 || gs[0] != "default" || gs[1] != "pro" {
		t.Errorf("groups = %v, want sorted [default pro]", gs)
	}
}

func TestTimelineBucketSplitsWindowInto60(t *testing.T) {
	cases := []struct {
		minutes int
		want    time.Duration
	}{
		{5, 5 * time.Second},
		{30, 30 * time.Second},
		{60, time.Minute},
		{360, 6 * time.Minute},
		{1440, 24 * time.Minute},
	}
	for _, tt := range cases {
		if got := timelineBucket(tt.minutes); got != tt.want {
			t.Errorf("minutes=%d bucket=%v, want %v", tt.minutes, got, tt.want)
		}
	}
}

func TestTimelineBucketInvalidFallsBackToDefault(t *testing.T) {
	want := time.Duration(defaultWindowMinutes) * time.Minute / timelineBarCount
	if got := timelineBucket(0); got != want {
		t.Errorf("minutes=0 bucket=%v, want %v", got, want)
	}
	if got := timelineBucket(-5); got != want {
		t.Errorf("minutes=-5 bucket=%v, want %v", got, want)
	}
}

func TestGroupTimelineEmptyIsEmptySliceNotNil(t *testing.T) {
	item := gin.H{}
	attachTimeline(item, nil)
	pts, ok := item["timeline"].([]timelineDTO)
	if !ok {
		t.Fatalf("timeline type = %T, want []timelineDTO", item["timeline"])
	}
	if pts == nil {
		t.Fatal("timeline is nil, want empty slice so JSON is []")
	}
	if len(pts) != 0 {
		t.Errorf("timeline len = %d, want 0", len(pts))
	}
}

func TestGroupTimelineByAccountKeepsTimeOrder(t *testing.T) {
	t1 := time.Date(2026, 4, 8, 11, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Minute)
	grouped := groupTimeline([]repository.PassiveTimelinePoint{
		{AccountID: 1, Bucket: t1, Ok: 2, Err: 1},
		{AccountID: 1, Bucket: t2, Ok: 4, Err: 0},
		{AccountID: 2, Bucket: t1, Ok: 0, Err: 3},
	})
	if len(grouped[1]) != 2 {
		t.Fatalf("account 1 points = %d, want 2", len(grouped[1]))
	}
	if grouped[1][0].T != t1.UTC().Format(time.RFC3339) || grouped[1][0].Ok != 2 || grouped[1][0].Err != 1 {
		t.Errorf("first point = %+v", grouped[1][0])
	}
	if grouped[1][1].T != t2.UTC().Format(time.RFC3339) || grouped[1][1].Ok != 4 {
		t.Errorf("second point = %+v", grouped[1][1])
	}
	if len(grouped[2]) != 1 || grouped[2][0].Err != 3 {
		t.Errorf("account 2 = %+v", grouped[2])
	}
}

func TestGroupTimelineCopiesLatencyFields(t *testing.T) {
	p50 := 8000.0
	grouped := groupTimeline([]repository.PassiveTimelinePoint{
		{AccountID: 1, Bucket: time.Unix(0, 0).UTC(), Ok: 3, Err: 0, FirstTokP50: &p50, OutputTokens: 90, DurationMsSum: 3000, CacheReadTokens: 10, InputTokens: 40},
	})
	got := grouped[1][0]
	if got.FirstTokenP50 == nil || *got.FirstTokenP50 != 8000 {
		t.Errorf("first_token_p50 = %v, want 8000", got.FirstTokenP50)
	}
	if got.OutputTokens != 90 || got.DurationMsSum != 3000 {
		t.Errorf("output/duration = %d/%v", got.OutputTokens, got.DurationMsSum)
	}
	if got.CacheReadTokens != 10 || got.InputTokens != 40 {
		t.Errorf("cache/input = %d/%d", got.CacheReadTokens, got.InputTokens)
	}
}

func TestAttachProviderResolvesCurrentOwnership(t *testing.T) {
	linkMap := map[int64]int64{101: 7}
	nameByID := map[int64]string{7: "供应商甲"}

	item := gin.H{}
	attachProvider(item, 101, linkMap, nameByID)

	if item["provider_id"] != int64(7) {
		t.Errorf("provider_id = %v, want 7", item["provider_id"])
	}
	if item["provider_name"] != "供应商甲" {
		t.Errorf("provider_name = %v, want 供应商甲", item["provider_name"])
	}
}

func TestAttachProviderUnlinkedAccountGetsEmptyBucket(t *testing.T) {
	// 未关联账号必须落到「未归属」桶（0 / ""）而不是被丢弃，
	// 否则新接入还没关联的账号会从稳定性页凭空消失。
	item := gin.H{}
	attachProvider(item, 999, map[int64]int64{101: 7}, map[int64]string{7: "供应商甲"})

	if item["provider_id"] != int64(0) {
		t.Errorf("provider_id = %v, want 0", item["provider_id"])
	}
	if item["provider_name"] != "" {
		t.Errorf("provider_name = %v, want empty", item["provider_name"])
	}
}

func TestAttachProviderToleratesNilLookup(t *testing.T) {
	// providerLookup 查询失败时返回 nil map —— 此时仍应产出「未归属」而非 panic，
	// 因为分位数本身有价值，归属只是筛选维度。
	item := gin.H{}
	attachProvider(item, 101, nil, nil)

	if item["provider_id"] != int64(0) || item["provider_name"] != "" {
		t.Errorf("nil lookup got %v / %v, want 0 / empty", item["provider_id"], item["provider_name"])
	}
}

func TestTokensPerSecond(t *testing.T) {
	got := tokensPerSecond(1_200_000, 1000)
	f, ok := got.(float64)
	if !ok || f != 1_200_000 {
		t.Errorf("1.2M tokens in 1s = %v, want 1200000", got)
	}
	if tokensPerSecond(100, 0) != nil {
		t.Errorf("duration 0 should be nil")
	}
	if tokensPerSecond(100, -1) != nil {
		t.Errorf("negative duration should be nil")
	}
}

func TestCacheHitRate(t *testing.T) {
	got := cacheHitRate(912, 88)
	f, ok := got.(float64)
	if !ok || f < 91.19 || f > 91.21 {
		t.Errorf("912/(912+88) = %v, want 91.2", got)
	}
	if cacheHitRate(0, 0) != nil {
		t.Errorf("empty sample should be nil")
	}
	zero := cacheHitRate(0, 100)
	zf, ok := zero.(float64)
	if !ok || zf != 0 {
		t.Errorf("no cache reads = %v, want 0", zero)
	}
}
