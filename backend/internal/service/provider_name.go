// Package service 业务逻辑层。
package service

import "regexp"

// providerPrefixRe 匹配账号名的【供应商】前缀（第一个【...】）。
var providerPrefixRe = regexp.MustCompile(`^【([^】]+)】`)

// ParseProviderName 从账号名解析【】前缀。
//
// 【这已不是归属真相】供应商与账号的归属由 provider_accounts 表决定，
// 见 013_provider_accounts.sql。本函数仅用于「猜建议名」：历史账号名大多带
// 前缀，扫描建站时据此预填站点名，省去逐个手打。改名不再影响任何统计归属。
//
// 例：`【walk】gpt pro` → ("walk", true)；无前缀 → ("", false)；`【a】【b】x` → ("a", true)。
func ParseProviderName(accountName string) (string, bool) {
	m := providerPrefixRe.FindStringSubmatch(accountName)
	if len(m) < 2 {
		return "", false
	}
	return m[1], true
}
