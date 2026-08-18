package service

import (
	"reflect"
	"strings"
	"testing"
)

func TestBuildAccountPayload(t *testing.T) {
	cases := []struct {
		name            string
		platform        string
		wantPoolMode    bool
		wantExtraKey    string
		wantConcurrency int
		wantTierID      bool
	}{
		{name: "anthropic", platform: "anthropic", wantPoolMode: true, wantExtraKey: "anthropic_passthrough", wantConcurrency: 1000},
		{name: "openai", platform: "openai", wantPoolMode: true, wantExtraKey: "openai_passthrough", wantConcurrency: 1000},
		{name: "gemini 带 tier_id", platform: "gemini", wantPoolMode: true, wantConcurrency: 1000, wantTierID: true},
		{name: "antigravity 低并发", platform: "antigravity", wantConcurrency: 10},
		{name: "未知平台走兜底", platform: "unknown-x", wantConcurrency: 100},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := BuildAccountPayload(c.platform, "https://up.example.com/", "sk-test", "acc", []int64{72, 73})

			if p["platform"] != c.platform {
				t.Errorf("platform 应为 %q，实际 %v", c.platform, p["platform"])
			}
			if p["type"] != "apikey" {
				t.Errorf("type 应固定为 apikey，实际 %v", p["type"])
			}
			if p["concurrency"] != c.wantConcurrency {
				t.Errorf("concurrency 应为 %d，实际 %v", c.wantConcurrency, p["concurrency"])
			}

			// group_ids 必须是 []int64（数字数组）—— 实测传字符串数组上游会 400
			ids, ok := p["group_ids"].([]int64)
			if !ok {
				t.Fatalf("group_ids 必须是 []int64，实际类型 %T", p["group_ids"])
			}
			if !reflect.DeepEqual(ids, []int64{72, 73}) {
				t.Errorf("group_ids 应为 [72 73]，实际 %v", ids)
			}

			creds, ok := p["credentials"].(map[string]any)
			if !ok {
				t.Fatal("credentials 缺失或类型错误")
			}
			if creds["api_key"] != "sk-test" {
				t.Errorf("api_key 未正确透传，实际 %v", creds["api_key"])
			}
			// base_url 结尾应恰好一个斜杠（入参已带斜杠时不重复）
			if creds["base_url"] != "https://up.example.com/" {
				t.Errorf("base_url 应规范化为单斜杠结尾，实际 %v", creds["base_url"])
			}
			if c.wantPoolMode && creds["pool_mode"] != true {
				t.Errorf("%s 应设置 pool_mode", c.platform)
			}
			if !c.wantPoolMode && creds["pool_mode"] != nil {
				t.Errorf("%s 不应设置 pool_mode，实际 %v", c.platform, creds["pool_mode"])
			}
			if c.wantTierID && creds["tier_id"] != "aistudio_free" {
				t.Errorf("gemini 应设置 tier_id，实际 %v", creds["tier_id"])
			}

			if c.wantExtraKey != "" {
				extra, ok := p["extra"].(map[string]any)
				if !ok {
					t.Fatalf("%s 应有 extra", c.platform)
				}
				if extra[c.wantExtraKey] != true {
					t.Errorf("extra.%s 应为 true，实际 %v", c.wantExtraKey, extra[c.wantExtraKey])
				}
			} else if _, has := p["extra"]; has {
				t.Errorf("%s 不应有 extra 字段", c.platform)
			}
		})
	}
}

func TestBuildAccountPayloadBaseURLNormalize(t *testing.T) {
	// 入参不带斜杠时应补上，避免拼出 //v1 之类的路径
	p := BuildAccountPayload("anthropic", "https://up.example.com", "k", "n", nil)
	creds := p["credentials"].(map[string]any)
	if creds["base_url"] != "https://up.example.com/" {
		t.Errorf("base_url 应补斜杠，实际 %v", creds["base_url"])
	}
}

func TestBuildAccountPayloadNilGroupIDs(t *testing.T) {
	// nil 应规范化为空数组，避免 JSON 序列化成 null 被上游拒绝
	p := BuildAccountPayload("openai", "https://x.com", "k", "n", nil)
	ids, ok := p["group_ids"].([]int64)
	if !ok || ids == nil {
		t.Fatalf("group_ids 应为空数组而非 nil，实际 %#v", p["group_ids"])
	}
	if len(ids) != 0 {
		t.Errorf("group_ids 应为空，实际 %v", ids)
	}
}

func TestFormatRate(t *testing.T) {
	cases := []struct {
		rate float64
		want string
	}{
		{1, "1"},
		{1.0, "1"},
		{0.5, "0.5"},
		{2.25, "2.25"},
		{0, "0"},
	}
	for _, c := range cases {
		if got := FormatRate(c.rate); got != c.want {
			t.Errorf("FormatRate(%v) = %q, want %q", c.rate, got, c.want)
		}
	}
}

func TestUpstreamKeyName(t *testing.T) {
	if got := UpstreamKeyName("default", 0.5); got != "【kaola】default-0.5" {
		t.Errorf("got %q", got)
	}
	if got := UpstreamKeyName("gpt", 1); got != "【kaola】gpt-1" {
		t.Errorf("got %q", got)
	}
}

func TestLocalAccountName(t *testing.T) {
	if got := LocalAccountName("walk", "default", 0.5); got != "【walk】default-0.5" {
		t.Errorf("got %q", got)
	}
	if !strings.Contains(LocalAccountName("tongba", "claude", 2.25), "【tongba】") {
		t.Error("账号名必须含【上游名】")
	}
}

func TestPickAccountBaseURL(t *testing.T) {
	if got := PickAccountBaseURL(nil); got != "" {
		t.Errorf("空列表应返回空串，实际 %q", got)
	}
	if got := PickAccountBaseURL([]string{"", "  "}); got != "" {
		t.Errorf("全空应返回空串，实际 %q", got)
	}
	if got := PickAccountBaseURL([]string{"https://only.example/"}); got != "https://only.example/" {
		t.Errorf("单元素应原样返回，实际 %q", got)
	}
	got := PickAccountBaseURL([]string{"https://a.example", "", "https://b.example", "https://a.example"})
	if got != "https://a.example" && got != "https://b.example" {
		t.Errorf("多元素应落在去重后的集合里，实际 %q", got)
	}
}
