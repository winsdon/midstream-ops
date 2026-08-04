package service

import (
	"net/url"
	"sort"
	"strings"

	"sub2api-account-monitor/internal/repository"
)

// NormalizeBaseURL 把账号的 base_url 归一到「站点身份」，用于按 URL 归组。
//
// 去路径与尾斜杠：写入侧本就不统一 —— sub2api_provision.go 写 credentials.base_url
// 时是 TrimRight + "/"，而 providers.base_url 是 TrimRight 不带斜杠；同一个站还可能
// 一处填到 /v1 一处填到根。不归一就会把同一个站拆成好几组。
//
// host 转小写（RFC 规定大小写不敏感）；去掉协议默认端口（:443 / :80 等价于省略）。
// 协议保留不合并：http 与 https 可能确实指向两个不同后端，猜错的代价比多一组更高，
// 界面上会展示完整 URL 供人判断。
// 无法解析的输入原样返回（仅去空白与尾斜杠），让它自成一组而不是全部塌进空串。
func NormalizeBaseURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return strings.TrimRight(s, "/")
	}
	host := strings.ToLower(u.Host)
	scheme := strings.ToLower(u.Scheme)
	if (scheme == "https" && strings.HasSuffix(host, ":443")) ||
		(scheme == "http" && strings.HasSuffix(host, ":80")) {
		host = host[:strings.LastIndex(host, ":")]
	}
	if scheme == "" {
		return host
	}
	return scheme + "://" + host
}

// GroupedAcc 归组内的账号（含已归属信息，避免用户误抢）。
type GroupedAcc struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
	Status   string `json:"status"`
	// LinkedTo 已关联到的供应商名（空 = 未关联）。勾选它等于把它从原供应商抢过来。
	LinkedTo string `json:"linked_to"`
}

// URLGroup 按 base_url 归组的账号集合（扫描弹窗「按 URL」页签）。
type URLGroup struct {
	BaseURL      string       `json:"base_url"`   // 规范化后的站点身份
	SampleURL    string       `json:"sample_url"` // 组内首个原始 URL，便于人工核对
	AccountCount int          `json:"account_count"`
	Accounts     []GroupedAcc `json:"accounts"`
	// SuggestedName 建议的供应商名：优先取组内账号名出现最多的【】前缀
	// （沿用历史命名约定），无前缀时回退 host。仅作输入预填，用户可改。
	SuggestedName string `json:"suggested_name"`
	// ExistingProvider 该 URL 已对应的供应商名（按 providers.base_url 匹配；空 = 无）
	ExistingProvider string `json:"existing_provider"`
}

// GroupAccountsByURL 按规范化 base_url 归组账号。
//
// base_url 为空的账号归入一个空串组：它们无从判断归属，但仍需能被手动关联 ——
// 丢掉它们等于让这些账号永远进不了任何供应商。
func GroupAccountsByURL(
	accs []repository.PGAccount,
	linkedTo map[int64]string,
	providerByURL map[string]string,
) []URLGroup {
	type agg struct {
		sample   string
		accounts []GroupedAcc
		prefixes map[string]int
	}
	buckets := make(map[string]*agg)
	for _, a := range accs {
		key := NormalizeBaseURL(a.BaseURL)
		b, ok := buckets[key]
		if !ok {
			b = &agg{sample: a.BaseURL, prefixes: map[string]int{}}
			buckets[key] = b
		}
		b.accounts = append(b.accounts, GroupedAcc{
			ID: a.ID, Name: a.Name, Platform: a.Platform,
			Status: a.Status, LinkedTo: linkedTo[a.ID],
		})
		// 沿用历史【】命名约定猜站点名 —— 它已不是归属真相，只作输入预填
		if p, ok := ParseProviderName(a.Name); ok {
			b.prefixes[p]++
		}
	}

	out := make([]URLGroup, 0, len(buckets))
	for key, b := range buckets {
		sort.Slice(b.accounts, func(i, j int) bool { return b.accounts[i].ID < b.accounts[j].ID })
		out = append(out, URLGroup{
			BaseURL:          key,
			SampleURL:        b.sample,
			AccountCount:     len(b.accounts),
			Accounts:         b.accounts,
			SuggestedName:    suggestProviderName(key, b.prefixes),
			ExistingProvider: providerByURL[key],
		})
	}
	// 未建站的排前面（那是待处理项），再按账号数降序 —— 与 ScanPrefixes 的排序习惯一致
	sort.Slice(out, func(i, j int) bool {
		if (out[i].ExistingProvider == "") != (out[j].ExistingProvider == "") {
			return out[i].ExistingProvider == ""
		}
		if out[i].AccountCount != out[j].AccountCount {
			return out[i].AccountCount > out[j].AccountCount
		}
		return out[i].BaseURL < out[j].BaseURL
	})
	return out
}

// suggestProviderName 取出现次数最多的【】前缀作建议名；无前缀时回退 host。
func suggestProviderName(normalizedURL string, prefixes map[string]int) string {
	best, bestN := "", 0
	for p, n := range prefixes {
		// 计数相同时按名称定序，避免 map 遍历顺序让建议名每次请求都变
		if n > bestN || (n == bestN && p < best) {
			best, bestN = p, n
		}
	}
	if best != "" {
		return best
	}
	host := strings.TrimPrefix(strings.TrimPrefix(normalizedURL, "https://"), "http://")
	if i := strings.Index(host, ":"); i > 0 {
		host = host[:i]
	}
	return host
}
