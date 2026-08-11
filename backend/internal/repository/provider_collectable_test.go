package repository

import (
	"context"
	"testing"

	"sub2api-account-monitor/internal/pkg/secretbox"
)

func newTestProviderRepo(t *testing.T) *ProviderRepo {
	t.Helper()
	s := newTestStore(t)
	return NewProviderRepo(s, &secretbox.Box{})
}

// collectableCases 覆盖三种认证模式的凭据齐备判据。
//
// want 同时是 ListCollectable（SQL）与 CredentialsReady（内存）的期望值 ——
// 两份实现必须等价，任一漂移都会让「排了班却采不动」或「能采却不排班」重现。
var collectableCases = []struct {
	name string
	cp   CreateParams
	want bool
}{
	{
		name: "password 凭据齐备",
		cp: CreateParams{
			BalanceType: "sub2api", Platform: "sub2api", AuthMode: "password",
			BaseURL: "https://a.example.com", LoginEmail: "u@x.com", LoginPassword: "pw",
		},
		want: true,
	},
	{
		name: "password 缺密码",
		cp: CreateParams{
			BalanceType: "sub2api", Platform: "sub2api", AuthMode: "password",
			BaseURL: "https://b.example.com", LoginEmail: "u@x.com",
		},
		want: false,
	},
	{
		name: "快捷导入产物：只有地址无凭据",
		cp: CreateParams{
			BalanceType: "sub2api", Platform: "sub2api", AuthMode: "password",
			BaseURL: "https://c.example.com",
		},
		want: false,
	},
	{
		name: "缺地址",
		cp: CreateParams{
			BalanceType: "sub2api", Platform: "sub2api", AuthMode: "password",
			LoginEmail: "u@x.com", LoginPassword: "pw",
		},
		want: false,
	},
	{
		name: "token 只有 refresh_token 也够（refreshSub2apiToken 可换新）",
		cp: CreateParams{
			BalanceType: "sub2api", Platform: "sub2api", AuthMode: "token",
			BaseURL: "https://d.example.com", RefreshToken: "rt",
		},
		want: true,
	},
	{
		name: "token 只有 access_token 也够（401 前可直接用）",
		cp: CreateParams{
			BalanceType: "sub2api", Platform: "sub2api", AuthMode: "token",
			BaseURL: "https://e.example.com", AccessToken: "at",
		},
		want: true,
	},
	{
		name: "token 两个令牌都没有",
		cp: CreateParams{
			BalanceType: "sub2api", Platform: "sub2api", AuthMode: "token",
			BaseURL: "https://f.example.com",
		},
		want: false,
	},
	{
		name: "user_key 令牌+用户 ID 齐备",
		cp: CreateParams{
			BalanceType: "sub2api", Platform: "new-api", AuthMode: "user_key",
			BaseURL: "https://g.example.com", AccessToken: "pat", UpstreamUserID: "7",
		},
		want: true,
	},
	{
		name: "user_key 缺用户 ID",
		cp: CreateParams{
			BalanceType: "sub2api", Platform: "new-api", AuthMode: "user_key",
			BaseURL: "https://h.example.com", AccessToken: "pat",
		},
		want: false,
	},
	{
		name: "凭据齐备但不采集（balance_type=none）",
		cp: CreateParams{
			BalanceType: "none", Platform: "sub2api", AuthMode: "password",
			BaseURL: "https://i.example.com", LoginEmail: "u@x.com", LoginPassword: "pw",
		},
		want: false,
	},
}

// TestListCollectableRequiresCredentials 缺凭据的站点不进采集队列。
//
// 快捷导入默认建成 balance_type=sub2api，但扫描拿不到账密：若仅凭
// balance_type 就排班，每轮都在登录处失败，健康点全红会淹没真正坏掉的站。
func TestListCollectableRequiresCredentials(t *testing.T) {
	repo := newTestProviderRepo(t)
	ctx := context.Background()

	want := map[string]bool{}
	for i, c := range collectableCases {
		cp := c.cp
		cp.Name = c.name
		if _, err := repo.Create(ctx, cp); err != nil {
			t.Fatalf("case %d 建站失败: %v", i, err)
		}
		if c.want {
			want[c.name] = true
		}
	}

	got, err := repo.ListCollectable(ctx)
	if err != nil {
		t.Fatalf("ListCollectable 失败: %v", err)
	}
	gotNames := map[string]bool{}
	for _, p := range got {
		gotNames[p.Name] = true
	}
	for name := range want {
		if !gotNames[name] {
			t.Errorf("%q 应可采集但被挡在门外", name)
		}
	}
	for name := range gotNames {
		if !want[name] {
			t.Errorf("%q 不该进采集队列（缺凭据或未开启采集）", name)
		}
	}
}

// TestCredentialsReadyMatchesSQL 内存判据与 SQL 判据必须等价。
//
// 两份实现分居 Go 与 SQL，改一处忘另一处会让 DTO 显示「待配置」的站照常被排班
// （或反之）。本用例是它们之间唯一的契约。
func TestCredentialsReadyMatchesSQL(t *testing.T) {
	repo := newTestProviderRepo(t)
	ctx := context.Background()

	for _, c := range collectableCases {
		cp := c.cp
		cp.Name = c.name
		p, err := repo.Create(ctx, cp)
		if err != nil {
			t.Fatalf("%q 建站失败: %v", c.name, err)
		}
		// want 含 balance_type 维度，凭据判据须单独比对
		wantCred := c.want || cp.BalanceType != "sub2api" && credsFilled(cp)
		if got := p.CredentialsReady(); got != wantCred {
			t.Errorf("%q CredentialsReady() = %v，期望 %v", c.name, got, wantCred)
		}
	}
}

// credsFilled 与 credentialsReadySQL 同构的用例侧判据（独立实现，避免自证）。
func credsFilled(cp CreateParams) bool {
	if cp.BaseURL == "" {
		return false
	}
	switch cp.AuthMode {
	case "password":
		return cp.LoginEmail != "" && cp.LoginPassword != ""
	case "token":
		return cp.RefreshToken != "" || cp.AccessToken != ""
	case "user_key":
		return cp.AccessToken != "" && cp.UpstreamUserID != ""
	}
	return false
}

// TestUpdateBaseURLFillsBlank 快捷导入为历史空地址站点补写地址。
func TestUpdateBaseURLFillsBlank(t *testing.T) {
	repo := newTestProviderRepo(t)
	ctx := context.Background()

	p, err := repo.Create(ctx, CreateParams{Name: "legacy", BalanceType: "sub2api", AuthMode: "password"})
	if err != nil {
		t.Fatal(err)
	}
	if p.BaseURL != "" {
		t.Fatalf("前置条件不成立：BaseURL = %q，期望空", p.BaseURL)
	}

	updated, err := repo.UpdateBaseURL(ctx, p.ID, "https://legacy.example.com")
	if err != nil {
		t.Fatalf("UpdateBaseURL 失败: %v", err)
	}
	if updated.BaseURL != "https://legacy.example.com" {
		t.Errorf("BaseURL = %q，期望已补写", updated.BaseURL)
	}
	// 补地址不等于配好凭据，仍不该进采集队列
	if updated.CredentialsReady() {
		t.Error("只补了地址就判为凭据齐备，门禁形同虚设")
	}
}
