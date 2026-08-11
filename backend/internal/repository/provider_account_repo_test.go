package repository

import (
	"context"
	"testing"

	"sub2api-account-monitor/internal/pkg/secretbox"
)

// newTestLinkRepo 建临时 monitor 测试库并返回关联表 repo 与供应商 repo（建站用）。
func newTestLinkRepo(t *testing.T) (*ProviderAccountRepo, *ProviderRepo) {
	t.Helper()
	s := newTestStore(t)
	return NewProviderAccountRepo(s), NewProviderRepo(s, &secretbox.Box{})
}

func mustProvider(t *testing.T, r *ProviderRepo, name string) *Provider {
	t.Helper()
	p, err := r.Create(context.Background(), CreateParams{Name: name, BalanceType: "none"})
	if err != nil {
		t.Fatalf("建供应商失败: %v", err)
	}
	return p
}

// TestLinkAccountIsExclusive 一个账号只能归属一个供应商：关到 B 会自动从 A 解绑。
//
// 这是收益统计正确性的基石 —— 账号落进两个桶会让合计翻倍。
func TestLinkAccountIsExclusive(t *testing.T) {
	link, prov := newTestLinkRepo(t)
	ctx := context.Background()
	a := mustProvider(t, prov, "site-a")
	b := mustProvider(t, prov, "site-b")

	if err := link.Replace(ctx, a.ID, []ProviderAccount{
		{ProviderID: a.ID, AccountID: 100, AccountName: "acc-100"},
	}); err != nil {
		t.Fatal(err)
	}
	// B 抢同一个账号
	if err := link.Replace(ctx, b.ID, []ProviderAccount{
		{ProviderID: b.ID, AccountID: 100, AccountName: "acc-100"},
	}); err != nil {
		t.Fatal(err)
	}

	aLinks, err := link.ListByProvider(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(aLinks) != 0 {
		t.Errorf("账号被抢走后 A 不该还持有它，实际 %+v", aLinks)
	}
	pid, err := link.ProviderIDOf(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if pid != b.ID {
		t.Errorf("账号 100 应归属 B(%d)，实际 %d", b.ID, pid)
	}
}

// TestReplaceIsFullReplacement Replace 是全量替换而非增量追加。
func TestReplaceIsFullReplacement(t *testing.T) {
	link, prov := newTestLinkRepo(t)
	ctx := context.Background()
	p := mustProvider(t, prov, "site")

	if err := link.Replace(ctx, p.ID, []ProviderAccount{
		{ProviderID: p.ID, AccountID: 1}, {ProviderID: p.ID, AccountID: 2},
	}); err != nil {
		t.Fatal(err)
	}
	// 第二次只提交 2 和 3 → 1 应被移除
	if err := link.Replace(ctx, p.ID, []ProviderAccount{
		{ProviderID: p.ID, AccountID: 2}, {ProviderID: p.ID, AccountID: 3},
	}); err != nil {
		t.Fatal(err)
	}

	got, err := link.ListByProvider(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[int64]bool{}
	for _, l := range got {
		ids[l.AccountID] = true
	}
	if len(got) != 2 || !ids[2] || !ids[3] || ids[1] {
		t.Errorf("Replace 应为全量替换，实际 %+v", got)
	}
}

// TestReplaceEmptyClearsAll 提交空集合等于解除该供应商的全部关联。
func TestReplaceEmptyClearsAll(t *testing.T) {
	link, prov := newTestLinkRepo(t)
	ctx := context.Background()
	p := mustProvider(t, prov, "site")

	if err := link.Replace(ctx, p.ID, []ProviderAccount{{ProviderID: p.ID, AccountID: 7}}); err != nil {
		t.Fatal(err)
	}
	if err := link.Replace(ctx, p.ID, nil); err != nil {
		t.Fatal(err)
	}
	got, err := link.ListByProvider(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("提交空集合应清空关联，实际 %+v", got)
	}
	// 账号回到未归属状态
	if pid, _ := link.ProviderIDOf(ctx, 7); pid != 0 {
		t.Errorf("解除关联后应返回 0（未归属），实际 %d", pid)
	}
}

// TestDeleteProviderCascadesLinks 删除供应商时关联被 CASCADE 清掉，不留悬垂行。
func TestDeleteProviderCascadesLinks(t *testing.T) {
	link, prov := newTestLinkRepo(t)
	ctx := context.Background()
	p := mustProvider(t, prov, "gone")

	if err := link.Replace(ctx, p.ID, []ProviderAccount{{ProviderID: p.ID, AccountID: 42}}); err != nil {
		t.Fatal(err)
	}
	if err := prov.Delete(ctx, p.ID); err != nil {
		t.Fatal(err)
	}

	all, err := link.ListAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Errorf("删除供应商后关联应被 CASCADE 清掉，实际 %+v", all)
	}
}

// TestCountAndAccountToProvider 计数与反查映射。
func TestCountAndAccountToProvider(t *testing.T) {
	link, prov := newTestLinkRepo(t)
	ctx := context.Background()
	a := mustProvider(t, prov, "a")
	b := mustProvider(t, prov, "b")

	if err := link.Replace(ctx, a.ID, []ProviderAccount{
		{ProviderID: a.ID, AccountID: 1}, {ProviderID: a.ID, AccountID: 2},
	}); err != nil {
		t.Fatal(err)
	}
	if err := link.Replace(ctx, b.ID, []ProviderAccount{{ProviderID: b.ID, AccountID: 3}}); err != nil {
		t.Fatal(err)
	}

	counts, err := link.CountByProvider(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts[a.ID] != 2 || counts[b.ID] != 1 {
		t.Errorf("计数错误: %+v", counts)
	}

	m, err := link.AccountToProvider(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if m[1] != a.ID || m[3] != b.ID {
		t.Errorf("反查映射错误: %+v", m)
	}
	if _, ok := m[999]; ok {
		t.Error("未关联的账号不该出现在映射里")
	}
}

// TestLinkManyKeepsExisting LinkMany 不清除目标供应商的既有关联（与 Replace 的区别）。
func TestLinkManyKeepsExisting(t *testing.T) {
	link, prov := newTestLinkRepo(t)
	ctx := context.Background()
	p := mustProvider(t, prov, "site")

	if err := link.Replace(ctx, p.ID, []ProviderAccount{{ProviderID: p.ID, AccountID: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := link.LinkMany(ctx, []ProviderAccount{{ProviderID: p.ID, AccountID: 2}}); err != nil {
		t.Fatal(err)
	}

	got, err := link.ListByProvider(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("LinkMany 应保留既有关联，实际 %+v", got)
	}
}
