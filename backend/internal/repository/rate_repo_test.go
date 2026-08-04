package repository

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// newTestRateRepo 建一个临时 SQLite（跑全部迁移）并返回 RateRepo。
func newTestRateRepo(t *testing.T) *RateRepo {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := NewSQLite(path)
	if err != nil {
		t.Fatalf("初始化 SQLite 失败: %v", err)
	}
	t.Cleanup(func() {
		_ = s.Close()
		_ = os.Remove(path)
	})
	return NewRateRepo(s)
}

// TestCurrentRowsFiltersByEntityType 回归测试：CurrentRows 必须按 entity_type 过滤。
//
// 曾出现的 bug：不过滤时，Reconcile("group") 会把所有 account 行看作「本轮未出现」
// 而标记 deleted，下一轮 Reconcile("account") 又将其「复活」插新行，两类实体每轮
// 互踩，导致倍率未变也不断插行（快照表疯长、历史里全是 1 -> 1 的假变化）。
func TestCurrentRowsFiltersByEntityType(t *testing.T) {
	repo := newTestRateRepo(t)
	ctx := context.Background()

	if err := repo.Insert(ctx, "local", 0, "group", "1", "g1", 1.5, ""); err != nil {
		t.Fatalf("插入 group 失败: %v", err)
	}
	if err := repo.Insert(ctx, "local", 0, "account", "1", "a1", 2.5, ""); err != nil {
		t.Fatalf("插入 account 失败: %v", err)
	}

	groups, err := repo.CurrentRows(ctx, "local", 0, "group")
	if err != nil {
		t.Fatalf("查询 group 失败: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("group 查询应只返回 1 行，实际 %d 行: %v", len(groups), groups)
	}
	if row, ok := groups["group:1"]; !ok || row.Rate != 1.5 {
		t.Fatalf("group:1 缺失或倍率错误: %v", groups)
	}
	if _, leaked := groups["account:1"]; leaked {
		t.Fatal("group 查询泄漏了 account 行 —— 会导致 account 被误标 deleted")
	}

	accounts, err := repo.CurrentRows(ctx, "local", 0, "account")
	if err != nil {
		t.Fatalf("查询 account 失败: %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("account 查询应只返回 1 行，实际 %d 行", len(accounts))
	}
	if _, leaked := accounts["group:1"]; leaked {
		t.Fatal("account 查询泄漏了 group 行")
	}
}

// TestCurrentRowsScopeIsolation 不同 scope / provider 的快照互不干扰。
func TestCurrentRowsScopeIsolation(t *testing.T) {
	repo := newTestRateRepo(t)
	ctx := context.Background()

	if err := repo.Insert(ctx, "local", 0, "group", "1", "本站分组", 1, ""); err != nil {
		t.Fatal(err)
	}
	if err := repo.Insert(ctx, "upstream", 8, "group", "vip", "上游VIP", 2, "anthropic"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Insert(ctx, "upstream", 9, "group", "vip", "另一站VIP", 3, "openai"); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		scope      string
		providerID int64
		wantRate   float64
	}{
		{"local", 0, 1},
		{"upstream", 8, 2},
		{"upstream", 9, 3},
	}
	for _, c := range cases {
		rows, err := repo.CurrentRows(ctx, c.scope, c.providerID, "group")
		if err != nil {
			t.Fatalf("%s/%d 查询失败: %v", c.scope, c.providerID, err)
		}
		if len(rows) != 1 {
			t.Fatalf("%s/%d 应返回 1 行，实际 %d", c.scope, c.providerID, len(rows))
		}
		for _, row := range rows {
			if row.Rate != c.wantRate {
				t.Fatalf("%s/%d 倍率应为 %v，实际 %v", c.scope, c.providerID, c.wantRate, row.Rate)
			}
		}
	}
}

// TestSnapshotLifecycle 快照生命周期：touch 不插行、变化插行、标删与复活。
func TestSnapshotLifecycle(t *testing.T) {
	repo := newTestRateRepo(t)
	ctx := context.Background()

	if err := repo.Insert(ctx, "local", 0, "group", "1", "g1", 1.0, "anthropic"); err != nil {
		t.Fatal(err)
	}
	rows, _ := repo.CurrentRows(ctx, "local", 0, "group")
	first := rows["group:1"]

	// touch：倍率未变，不应产生新行
	if err := repo.Touch(ctx, first.ID, "g1-renamed", "openai"); err != nil {
		t.Fatal(err)
	}
	all, _, err := repo.History(ctx, SnapshotFilter{Scope: "local", PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("touch 后应仍为 1 行，实际 %d 行", len(all))
	}
	if all[0].Name != "g1-renamed" {
		t.Fatalf("touch 应同步名称，实际 %q", all[0].Name)
	}
	// platform 与 name 同待遇：就地更新，不得插新行
	if all[0].Platform != "openai" {
		t.Fatalf("touch 应同步平台，实际 %q", all[0].Platform)
	}

	// 变化：插新行，LAG 推导出 prev_rate
	if err := repo.Insert(ctx, "local", 0, "group", "1", "g1-renamed", 2.0, "openai"); err != nil {
		t.Fatal(err)
	}
	all, _, err = repo.History(ctx, SnapshotFilter{Scope: "local", PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("变化后应为 2 行，实际 %d 行", len(all))
	}
	// History 按时间倒序，首行是最新的 2.0，其 prev 应为 1.0
	if all[0].Rate != 2.0 {
		t.Fatalf("最新行倍率应为 2.0，实际 %v", all[0].Rate)
	}
	if all[0].PrevRate == nil || *all[0].PrevRate != 1.0 {
		t.Fatalf("最新行 prev_rate 应为 1.0，实际 %v", all[0].PrevRate)
	}

	// 标删 → CurrentRows 仍可见（供复活判定），deleted=true
	rows, _ = repo.CurrentRows(ctx, "local", 0, "group")
	if err := repo.MarkDeleted(ctx, rows["group:1"].ID); err != nil {
		t.Fatal(err)
	}
	rows, _ = repo.CurrentRows(ctx, "local", 0, "group")
	if !rows["group:1"].Deleted {
		t.Fatal("标删后 deleted 应为 true")
	}
	// CurrentList 默认过滤已删除
	list, err := repo.CurrentList(ctx, "local", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("CurrentList 应过滤已删除行，实际返回 %d 行", len(list))
	}
	if list, err = repo.CurrentList(ctx, "local", nil, true); err != nil || len(list) != 1 {
		t.Fatalf("include_deleted 应返回 1 行，实际 %d 行 err=%v", len(list), err)
	}
}
