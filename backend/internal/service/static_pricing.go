package service

import (
	_ "embed"
	"encoding/json"
	"regexp"
	"strings"
	"sync"
)

// staticPricingJSON 是 LiteLLM 模型价目表的本地副本。
//
// sub2api 计费时的价格解析优先级是「渠道定价覆盖 → LiteLLM 目录 → 硬编码兜底」，
// 而 LiteLLM 目录存在 sub2api 服务器的磁盘 JSON 里、不在 Postgres。本站只读 PG
// 拿不到它，因此内嵌一份同源副本作为价格兜底。
//
// 更新方式（价表滞后会导致新模型无价，属预期降级、不影响其它数据）：
//
//	curl -o internal/service/pricingdata/model_prices.json \
//	  https://raw.githubusercontent.com/Wei-Shaw/model-price-repo/main/model_prices_and_context_window.json
//
//go:embed pricingdata/model_prices.json
var staticPricingJSON []byte

// StaticModelPrice 单个模型的官方价（USD / token）。
type StaticModelPrice struct {
	Input      *float64 `json:"input_cost_per_token"`
	Output     *float64 `json:"output_cost_per_token"`
	CacheWrite *float64 `json:"cache_creation_input_token_cost"`
	CacheRead  *float64 `json:"cache_read_input_token_cost"`
}

// hasAnyPrice 报告该条目是否含有任何有效价格。
func (p *StaticModelPrice) hasAnyPrice() bool {
	return p != nil && (p.Input != nil || p.Output != nil || p.CacheWrite != nil || p.CacheRead != nil)
}

// versionSuffixPattern 匹配模型名尾部的日期版本号，如 -20251101。
var versionSuffixPattern = regexp.MustCompile(`-\d{6,8}$`)

// StaticPriceTable 内嵌价表的只读查询索引。
type StaticPriceTable struct {
	// byName 是小写模型名 → 价格。
	byName map[string]*StaticModelPrice
	// byBase 是去掉版本号后缀的基础名 → 价格，用于模糊匹配。
	byBase map[string]*StaticModelPrice
}

var (
	staticPriceOnce  sync.Once
	staticPriceTable *StaticPriceTable
)

// LoadStaticPriceTable 返回内嵌价表（进程内只解析一次）。
func LoadStaticPriceTable() *StaticPriceTable {
	staticPriceOnce.Do(func() {
		staticPriceTable = parseStaticPriceTable(staticPricingJSON)
	})
	return staticPriceTable
}

func parseStaticPriceTable(raw []byte) *StaticPriceTable {
	t := &StaticPriceTable{
		byName: make(map[string]*StaticModelPrice, 256),
		byBase: make(map[string]*StaticModelPrice, 256),
	}
	var parsed map[string]StaticModelPrice
	if err := json.Unmarshal(raw, &parsed); err != nil {
		// 内嵌文件损坏不应导致服务不可用：退化为空表，所有模型显示无价。
		return t
	}
	for name, price := range parsed {
		p := price
		if !p.hasAnyPrice() {
			continue
		}
		lower := strings.ToLower(strings.TrimSpace(name))
		if lower == "" {
			continue
		}
		t.byName[lower] = &p
		// 先到先得：同一基础名的多个版本保留首个，避免旧版本覆盖新版本。
		if base := baseModelName(lower); base != "" && base != lower {
			if _, exists := t.byBase[base]; !exists {
				t.byBase[base] = &p
			}
		}
	}
	return t
}

// baseModelName 去掉日期版本号后缀，如 claude-opus-4-5-20251101 → claude-opus-4-5。
func baseModelName(lower string) string {
	return versionSuffixPattern.ReplaceAllString(lower, "")
}

// Lookup 按模型名查价，逐级放宽匹配（对齐 sub2api PricingService.GetModelPricing 的前几层）：
//  1. 精确名
//  2. 剥离 -thinking 后缀（同一模型的思考变体与基础模型同价）
//  3. -4-5- → -4.5- 的写法变体
//  4. 去掉日期版本号后缀
//
// 未命中返回 nil，调用方据此显示「-」。
func (t *StaticPriceTable) Lookup(model string) *StaticModelPrice {
	if t == nil {
		return nil
	}
	lower := strings.ToLower(strings.TrimSpace(model))
	if lower == "" {
		return nil
	}

	candidates := []string{lower}
	if trimmed := strings.TrimSuffix(lower, "-thinking"); trimmed != lower {
		candidates = append(candidates, trimmed)
	}
	// 数字分隔写法变体：claude-opus-4-5-x → claude-opus-4.5-x
	for _, c := range append([]string{}, candidates...) {
		if v := strings.ReplaceAll(c, "-4-5-", "-4.5-"); v != c {
			candidates = append(candidates, v)
		}
	}

	for _, c := range candidates {
		if p, ok := t.byName[c]; ok {
			return p
		}
	}
	for _, c := range candidates {
		if base := baseModelName(c); base != "" {
			if p, ok := t.byName[base]; ok {
				return p
			}
			if p, ok := t.byBase[base]; ok {
				return p
			}
		}
	}
	return nil
}

// Size 返回价表条目数（供启动日志与诊断用）。
func (t *StaticPriceTable) Size() int {
	if t == nil {
		return 0
	}
	return len(t.byName)
}
