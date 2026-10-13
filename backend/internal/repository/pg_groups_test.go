package repository

import (
	"context"
	"os"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestListGroupsOrdersBySortOrder 守住「本站列出分组时跟随 sub2api 后台的排序」——
// 「上游管理 → 导入账号」的关联本站分组勾选框按它渲染，Rates 页的本站分组下拉同理。
//
// 用 TEMP TABLE 顶替线上 groups：不碰任何持久数据，还能精确构造「物理顺序、id 顺序、
// 名称顺序都与 sort_order 顺序不同」的场景 —— 旧实现没有 ORDER BY，返回的是插入顺序。
func TestListGroupsOrdersBySortOrder(t *testing.T) {
	dsn := os.Getenv("MONITOR_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("MONITOR_TEST_PG_DSN is required")
	}
	ctx := context.Background()

	// TEMP TABLE 只存在于创建它的会话，所以每个用例都要自己的单连接池。
	newPool := func(t *testing.T) *pgxpool.Pool {
		t.Helper()
		cfg, err := pgxpool.ParseConfig(dsn)
		if err != nil {
			t.Fatal(err)
		}
		cfg.MaxConns = 1
		pool, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pool.Close)
		return pool
	}

	t.Run("按 sort_order 升序、同序号按 id", func(t *testing.T) {
		pool := newPool(t)
		_, err := pool.Exec(ctx, `
    CREATE TEMP TABLE groups (
        id bigint, name text, platform text, status text,
        rate_multiplier numeric, sort_order integer, deleted_at timestamptz);
    INSERT INTO groups VALUES
        (7, 'z 已删除',   'openai',    'active', 1.0, 0,    now()),
        (5, 'e 最靠后',   'openai',    'active', 1.0, 20,   NULL),
        (2, 'b 排第二',   'anthropic', 'active', 1.0, 0,    NULL),
        (9, 'i 未排过序', 'anthropic', 'active', 1.0, NULL, NULL),
        (1, 'a 排第一',   'anthropic', 'active', 0.5, 0,    NULL),
        (6, 'f 同序号',   'openai',    'active', 1.0, 10,   NULL),
        (3, 'c 排第四',   'openai',    'active', 1.0, 10,   NULL);
   `)
		if err != nil {
			t.Fatal(err)
		}

		groups, err := (&PG{pool: pool}).ListGroups(ctx)
		if err != nil {
			t.Fatalf("ListGroups: %v", err)
		}

		got := make([]int64, 0, len(groups))
		for _, g := range groups {
			got = append(got, g.ID)
		}
		// sort_order 升序（NULL 折成 0），同序号按 id 兜底；已删除的 7 不出现。
		want := []int64{1, 2, 9, 3, 6, 5}
		if !slices.Equal(got, want) {
			t.Fatalf("分组顺序 = %v，期望 %v（sort_order 升序，同序号按 id）", got, want)
		}
		if groups[2].SortOrder != 0 {
			t.Fatalf("NULL sort_order 应折成 0，实际 %d", groups[2].SortOrder)
		}
		if groups[3].SortOrder != 10 {
			t.Fatalf("sort_order 应随行返回，实际 %d", groups[3].SortOrder)
		}
	})

	// sort_order 是 sub2api 后加的列。老 schema 没有它时，排序退化成「顺序不保证」，
	// 但绝不能连带把分组列表整个变成 500 —— 那会让「导入账号」根本没法选分组。
	t.Run("老 schema 没有 sort_order 列", func(t *testing.T) {
		pool := newPool(t)
		_, err := pool.Exec(ctx, `
    CREATE TEMP TABLE groups (
        id bigint, name text, platform text, status text,
        rate_multiplier numeric, deleted_at timestamptz);
    INSERT INTO groups VALUES
        (5, 'e', 'openai',    'active', 1.0, NULL),
        (1, 'a', 'anthropic', 'active', 0.5, NULL),
        (7, 'z 已删除', 'openai', 'active', 1.0, now());
   `)
		if err != nil {
			t.Fatal(err)
		}

		groups, err := (&PG{pool: pool}).ListGroups(ctx)
		if err != nil {
			t.Fatalf("老 schema 下 ListGroups 不应报错: %v", err)
		}
		got := make([]int64, 0, len(groups))
		for _, g := range groups {
			got = append(got, g.ID)
			if g.SortOrder != 0 {
				t.Fatalf("缺列时 SortOrder 应为 0，实际 %d", g.SortOrder)
			}
		}
		slices.Sort(got)
		if !slices.Equal(got, []int64{1, 5}) {
			t.Fatalf("老 schema 下分组 = %v，期望 [1 5]（未删除的全部返回）", got)
		}
	})
}
