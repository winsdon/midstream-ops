package repository

import (
	"testing"
	"time"
)

// GroupStatusTimeline 的口径锁定：成功来自 usage_logs（只记成功），失败来自
// ops_error_logs 且排除业务限制与忽略状态码；统计只按分组，不按账号。
//
// 用临时表而不是真的迁移建表：本函数只依赖两张表的少数几列，临时表能把
// 「哪些列参与口径」写成可执行的断言，也避免和真表的其它列纠缠。
func TestGroupStatusTimeline(t *testing.T) {
	ctx, pg, pool := pelicanTestPool(t)
	origin := time.Date(2026, 4, 8, 11, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `
 CREATE TEMP TABLE usage_logs(id bigint, group_id bigint, account_id bigint, created_at timestamptz, first_token_ms double precision, input_tokens bigint, cache_read_tokens bigint);
 CREATE TEMP TABLE ops_error_logs(id bigint, group_id bigint, account_id bigint, created_at timestamptz, status_code int, is_business_limited boolean);
 INSERT INTO usage_logs VALUES
   (1,10,1,'2026-04-08 11:01:00+00',1000,100,50),
   (2,10,1,'2026-04-08 11:02:00+00',3000,100,50),
   (3,10,2,'2026-04-08 11:03:00+00',5000,200,100),
   (4,20,1,'2026-04-08 11:01:00+00',NULL,10,0),
   (5,NULL,1,'2026-04-08 11:01:00+00',1000,999,999);
 INSERT INTO ops_error_logs VALUES
   (1,10,1,'2026-04-08 11:04:00+00',500,false),
   (2,10,1,'2026-04-08 11:05:00+00',429,false),
   (3,10,1,'2026-04-08 11:06:00+00',400,false),
   (4,10,1,'2026-04-08 11:07:00+00',503,true),
   (5,20,1,'2026-04-08 11:02:00+00',502,false),
   (6,NULL,1,'2026-04-08 11:02:00+00',500,false),
   (7,10,1,'2026-04-08 11:08:00+00',500,true);
 `); err != nil {
		t.Fatal(err)
	}

	// 忽略状态码只放 400/429：503 **不**进忽略列表，这样「业务限制不算失败」
	// 必须靠 SQL 里的 is_business_limited = FALSE 独立成立 —— 否则这条分支
	// 会被 status_code 条件顺手排除，等于零覆盖（删掉那个条件测试照样绿）。
	points, err := pg.GroupStatusTimeline(ctx, origin, 15*time.Minute, []int32{400, 429})
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 2 {
		t.Fatalf("points = %+v, want 2 buckets (group 10 & 20)", points)
	}

	byGroup := map[int64]GroupStatusTimelinePoint{}
	for _, p := range points {
		if !p.Bucket.Equal(origin) {
			t.Errorf("bucket = %v, want %v (origin aligned)", p.Bucket, origin)
		}
		byGroup[p.GroupID] = p
	}

	g10 := byGroup[10]
	if g10.Ok != 3 || g10.Err != 1 {
		t.Errorf("group 10 ok/err = %d/%d, want 3/1"+
			"（500 计失败；400/429 被忽略；503 是否计入只由 is_business_limited 决定）",
			g10.Ok, g10.Err)
	}
	// 首字 P50 只来自成功请求：1000/3000/5000 的中位数是 3000。
	if g10.FirstTokP50 == nil || *g10.FirstTokP50 != 3000 {
		t.Errorf("group 10 first_tok_p50 = %v, want 3000", g10.FirstTokP50)
	}
	if g10.InputTokens != 400 || g10.CacheReadTokens != 200 {
		t.Errorf("group 10 input/cache = %d/%d, want 400/200", g10.InputTokens, g10.CacheReadTokens)
	}

	// group 20 窗口内只有失败：必须出现（FULL OUTER JOIN 的意义），且不能凭空造样本。
	g20 := byGroup[20]
	if g20.Ok != 1 || g20.Err != 1 {
		t.Errorf("group 20 ok/err = %d/%d, want 1/1", g20.Ok, g20.Err)
	}
	if g20.FirstTokP50 != nil {
		t.Errorf("group 20 first_tok_p50 = %v, want nil（该请求没有首字数据）", *g20.FirstTokP50)
	}

	// group_id 为 NULL 的行（老数据 / 无分组请求）不进结果：看板按分组出卡，
	// 没有归属的行硬塞进某个桶会让数字对不上。
	for _, p := range points {
		if p.GroupID == 0 {
			t.Fatalf("NULL group_id rows must be skipped: %+v", p)
		}
	}
}

// 按窗口起点分桶：落在 since 之前的行不进结果（SQL 的 created_at >= $1 保证），
// 且不同桶的行不会被合并。
func TestGroupStatusTimelineSplitsBuckets(t *testing.T) {
	ctx, pg, pool := pelicanTestPool(t)
	origin := time.Date(2026, 4, 8, 11, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `
 CREATE TEMP TABLE usage_logs(id bigint, group_id bigint, account_id bigint, created_at timestamptz, first_token_ms double precision, input_tokens bigint, cache_read_tokens bigint);
 CREATE TEMP TABLE ops_error_logs(id bigint, group_id bigint, account_id bigint, created_at timestamptz, status_code int, is_business_limited boolean);
 INSERT INTO usage_logs VALUES
   (1,10,1,'2026-04-08 11:01:00+00',1000,1,0),
   (2,10,1,'2026-04-08 11:04:00+00',1000,1,0),
   (3,10,1,'2026-04-08 11:05:00+00',1000,1,0),
   (4,10,1,'2026-04-08 10:59:00+00',1000,1,0);
 INSERT INTO ops_error_logs VALUES (1,10,1,'2026-04-08 11:04:30+00',500,false);
 `); err != nil {
		t.Fatal(err)
	}
	points, err := pg.GroupStatusTimeline(ctx, origin, 5*time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 11:00-11:05 桶：11:01、11:04 两条成功 + 11:04:30 一条失败；11:05-11:10 桶：一条成功。
	if len(points) != 2 {
		t.Fatalf("points = %d, want 2 buckets", len(points))
	}
	// ORDER BY group_id, bucket：第一个桶 2 成功 1 失败，第二个桶 1 成功 0 失败。
	if points[0].Ok != 2 || points[0].Err != 1 {
		t.Errorf("bucket 1 = %+v, want ok=2 err=1", points[0])
	}
	if points[1].Ok != 1 || points[1].Err != 0 {
		t.Errorf("bucket 2 = %+v, want ok=1 err=0", points[1])
	}
	// since 之前的行被排除后，聚合总数必须对得上：1+2（成功）+1（失败）。
	var ok, errs int64
	for _, p := range points {
		ok += p.Ok
		errs += p.Err
	}
	if ok != 3 || errs != 1 {
		t.Errorf("totals ok/err = %d/%d, want 3/1", ok, errs)
	}
}

// ignoredStatusCodes 传 nil 时必须是「不排除任何状态码」，而不是把所有错误滤掉。
//
// nil 切片会被 pgx 编码成 SQL NULL，而 `x <> ALL(NULL)` 求值为 NULL —— 失败数会
// 静默恒为 0、SLA 恒为 100。normalizeIgnoredStatusCodes 就是为这条守的，
// 本用例把它的效果钉在真实 SQL 上。
func TestGroupStatusTimelineNilIgnoredKeepsErrors(t *testing.T) {
	ctx, pg, pool := pelicanTestPool(t)
	origin := time.Date(2026, 4, 8, 11, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `
 CREATE TEMP TABLE usage_logs(id bigint, group_id bigint, account_id bigint, created_at timestamptz, first_token_ms double precision, input_tokens bigint, cache_read_tokens bigint);
 CREATE TEMP TABLE ops_error_logs(id bigint, group_id bigint, account_id bigint, created_at timestamptz, status_code int, is_business_limited boolean);
 INSERT INTO ops_error_logs VALUES
   (1,10,1,'2026-04-08 11:01:00+00',400,false),
   (2,10,1,'2026-04-08 11:02:00+00',500,false);
 `); err != nil {
		t.Fatal(err)
	}
	points, err := pg.GroupStatusTimeline(ctx, origin, 15*time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 1 || points[0].Err != 2 {
		t.Fatalf("points = %+v, want 单桶 err=2（nil 不应滤掉任何错误）", points)
	}
}

// ListGroups 必须带出 is_exclusive，否则 service 层无从区分公开/专属分组。
// 同时锁定：老 schema 没有 sort_order / is_exclusive 列时也不能报错（走 to_jsonb）。
func TestListGroupsIncludesIsExclusive(t *testing.T) {
	ctx, pg, pool := pelicanTestPool(t)
	if _, err := pool.Exec(ctx, `
 CREATE TEMP TABLE groups(id bigint, name text, platform text, status text, deleted_at timestamptz, rate_multiplier numeric, sort_order int, is_exclusive boolean);
 INSERT INTO groups VALUES
   (10,'public','anthropic','active',NULL,0.08,1,false),
   (11,'exclusive','openai','active',NULL,1,2,true),
   (12,'disabled','openai','disabled',NULL,1,3,false),
   (13,'deleted','openai','active',now(),1,4,false);
 `); err != nil {
		t.Fatal(err)
	}
	groups, err := pg.ListGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 3 {
		t.Fatalf("groups = %+v, want 3（未删除的全部，含 disabled 与 exclusive）", groups)
	}
	if groups[0].ID != 10 || groups[0].SortOrder != 1 || groups[0].IsExclusive {
		t.Errorf("groups[0] = %+v, want id=10 sort=1 exclusive=false", groups[0])
	}
	if groups[1].ID != 11 || !groups[1].IsExclusive {
		t.Errorf("groups[1] = %+v, want id=11 exclusive=true", groups[1])
	}
	if groups[0].RateMultiplier != 0.08 {
		t.Errorf("rate_multiplier = %v, want 0.08", groups[0].RateMultiplier)
	}
}

// 老 schema 兼容：temp 表没有 sort_order / is_exclusive 列时，to_jsonb 取不到键，
// COALESCE 回落到默认值而不是整条查询 500。
func TestListGroupsToleratesOldSchema(t *testing.T) {
	ctx, pg, pool := pelicanTestPool(t)
	if _, err := pool.Exec(ctx, `
 CREATE TEMP TABLE groups(id bigint, name text, platform text, status text, deleted_at timestamptz, rate_multiplier numeric);
 INSERT INTO groups VALUES (10,'legacy','anthropic','active',NULL,1);
 `); err != nil {
		t.Fatal(err)
	}
	groups, err := pg.ListGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 {
		t.Fatalf("groups = %+v, want 1", groups)
	}
	if groups[0].SortOrder != 0 || groups[0].IsExclusive {
		t.Errorf("groups[0] = %+v, want sort_order=0 exclusive=false", groups[0])
	}
}
