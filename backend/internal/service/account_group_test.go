package service

import (
	"testing"

	"sub2api-account-monitor/internal/repository"
)

func TestNormalizeBaseURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"尾斜杠归一", "https://x.com/", "https://x.com"},
		{"去路径", "https://x.com/v1", "https://x.com"},
		{"去深层路径", "https://x.com/v1/messages", "https://x.com"},
		{"host 转小写", "https://X.COM/v1", "https://x.com"},
		{"https 默认端口等价于省略", "https://x.com:443", "https://x.com"},
		{"http 默认端口等价于省略", "http://x.com:80/v1", "http://x.com"},
		{"非默认端口保留", "https://x.com:8443/v1", "https://x.com:8443"},
		{"协议不合并", "http://x.com", "http://x.com"},
		{"空串", "", ""},
		{"仅空白", "   ", ""},
		// 无 scheme 时 url.Parse 会把整串当路径，Host 为空 → 走原样返回分支
		{"裸 host 原样返回", "x.com/v1", "x.com/v1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := NormalizeBaseURL(c.in); got != c.want {
				t.Errorf("NormalizeBaseURL(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestNormalizeBaseURLGroupsSameSite 同一个站的不同写法必须归进同一组 ——
// 这是「按 URL 归组」能用的前提。
func TestNormalizeBaseURLGroupsSameSite(t *testing.T) {
	variants := []string{
		"https://api.example.com",
		"https://api.example.com/",
		"https://api.example.com/v1",
		"https://api.example.com/v1/",
		"https://API.Example.com/v1/messages",
		"https://api.example.com:443/v1",
	}
	want := NormalizeBaseURL(variants[0])
	for _, v := range variants {
		if got := NormalizeBaseURL(v); got != want {
			t.Errorf("%q 应与 %q 归为同一组，实际 %q vs %q", v, variants[0], got, want)
		}
	}
}

func TestGroupAccountsByURL(t *testing.T) {
	accs := []repository.PGAccount{
		{ID: 1, Name: "【walk】gpt", BaseURL: "https://a.com/v1", Platform: "openai", Status: "active"},
		{ID: 2, Name: "【walk】claude", BaseURL: "https://a.com/", Platform: "anthropic", Status: "active"},
		{ID: 3, Name: "no-prefix", BaseURL: "https://b.com", Platform: "openai", Status: "active"},
		{ID: 4, Name: "空地址账号", BaseURL: "", Platform: "gemini", Status: "active"},
	}
	linkedTo := map[int64]string{2: "walk"}
	providerByURL := map[string]string{"https://b.com": "bee"}

	groups := GroupAccountsByURL(accs, linkedTo, providerByURL)

	byURL := make(map[string]URLGroup, len(groups))
	for _, g := range groups {
		byURL[g.BaseURL] = g
	}

	// a.com 的两个账号归一到同一组
	a := byURL["https://a.com"]
	if a.AccountCount != 2 {
		t.Errorf("a.com 应有 2 个账号，实际 %d", a.AccountCount)
	}
	// 建议名取【】前缀
	if a.SuggestedName != "walk" {
		t.Errorf("a.com 建议名应为 walk，实际 %q", a.SuggestedName)
	}
	// 已关联的账号带出归属，让用户知道勾选它等于抢过来
	var acc2 GroupedAcc
	for _, x := range a.Accounts {
		if x.ID == 2 {
			acc2 = x
		}
	}
	if acc2.LinkedTo != "walk" {
		t.Errorf("账号 2 应标记已关联到 walk，实际 %q", acc2.LinkedTo)
	}

	// 无前缀时建议名回退 host
	b := byURL["https://b.com"]
	if b.SuggestedName != "b.com" {
		t.Errorf("b.com 建议名应回退 host，实际 %q", b.SuggestedName)
	}
	if b.ExistingProvider != "bee" {
		t.Errorf("b.com 应标记已建站 bee，实际 %q", b.ExistingProvider)
	}

	// base_url 为空的账号不能被丢掉，否则它们永远进不了任何供应商
	empty, ok := byURL[""]
	if !ok || empty.AccountCount != 1 {
		t.Errorf("空 base_url 的账号应自成一组，实际 %+v", byURL)
	}

	// 未建站的排前面（那是待处理项）
	if groups[0].ExistingProvider != "" {
		t.Errorf("未建站的组应排前面，实际首组 %+v", groups[0])
	}
}
