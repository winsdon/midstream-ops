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

func TestAccountName(t *testing.T) {
	cases := []struct {
		platform string
		provider string
		rate     float64
		wantPre  string
	}{
		{"openai", "walk", 1.5, "A-"},
		{"anthropic", "walk", 0.6, "B-"},
		{"gemini", "pet", 1, "C-"},
		{"antigravity", "pet", 2.25, "D-"},
		{"weird", "pet", 1, "X-"}, // 未知平台兜底
	}
	for _, c := range cases {
		got := AccountName(c.platform, c.provider, c.rate)
		if !strings.HasPrefix(got, c.wantPre) {
			t.Errorf("%s 应以 %s 开头，实际 %s", c.platform, c.wantPre, got)
		}
		// 必须含【供应商名】—— 本项目靠该前缀把账号归属到供应商
		if !strings.Contains(got, "【"+c.provider+"】") {
			t.Errorf("账号名应含【%s】，实际 %s", c.provider, got)
		}
	}
}
