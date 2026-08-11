package service

import (
	"regexp"
	"testing"
)

func TestParseProviderName(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"【walk】gpt pro", "walk", true},
		{"【哈基米】pro 0.08", "哈基米", true},
		{"【a】【b】x", "a", true},
		{"plain account", "", false},
		{"", "", false},
		{"【】empty", "", false},       // 空前缀（[^】]+ 至少一个字符，但】立即结束 → 不匹配）
		{"prefix【walk】x", "", false}, // 前缀不在开头
	}
	re := regexp.MustCompile(`^【([^】]+)】`)
	_ = re
	for _, c := range cases {
		got, ok := ParseProviderName(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("ParseProviderName(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}
