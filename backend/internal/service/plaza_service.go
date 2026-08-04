package service

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"sub2api-account-monitor/internal/repository"
)

// 计费模式（与 sub2api 的 channel_model_pricing.billing_mode 对齐）。
const (
	BillingModeToken      = "token"
	BillingModePerRequest = "per_request"
	BillingModeImage      = "image"
)

// PlazaGroup 模型可用的分组及倍率。
type PlazaGroup struct {
	ID               int64   `json:"id"`
	Name             string  `json:"name"`
	Platform         string  `json:"platform"`
	RateMultiplier   float64 `json:"rate_multiplier"`
	SubscriptionType string  `json:"subscription_type"`
	IsExclusive      bool    `json:"is_exclusive"`
}

// PlazaPrice 一组价格（同一来源的整组价，不逐字段混取）。
type PlazaPrice struct {
	Input        *float64 `json:"input"`
	Output       *float64 `json:"output"`
	CacheWrite   *float64 `json:"cache_write"`
	CacheRead    *float64 `json:"cache_read"`
	ImageOutput  *float64 `json:"image_output"`
	PerRequest   *float64 `json:"per_request"`
	HasIntervals bool     `json:"has_intervals"`
}

// PlazaInterval 阶梯定价区间。
type PlazaInterval struct {
	MinTokens   int      `json:"min_tokens"`
	MaxTokens   *int     `json:"max_tokens"`
	TierLabel   string   `json:"tier_label,omitempty"`
	Input       *float64 `json:"input"`
	Output      *float64 `json:"output"`
	CacheWrite  *float64 `json:"cache_write"`
	CacheRead   *float64 `json:"cache_read"`
	PerRequest  *float64 `json:"per_request"`
}

// PlazaSource 模型的一个来源（渠道 × 平台），供详情弹窗展示明细。
type PlazaSource struct {
	ChannelName string          `json:"channel_name"`
	ChannelDesc string          `json:"channel_desc"`
	Platform    string          `json:"platform"`
	BillingMode string          `json:"billing_mode"`
	Price       PlazaPrice      `json:"price"`
	Intervals   []PlazaInterval `json:"intervals"`
	Groups      []PlazaGroup    `json:"groups"`
}

// PlazaMetric 模型的近 N 小时用量指标。
type PlazaMetric struct {
	RequestCount    int64   `json:"request_count"`
	AvgDurationMs   int64   `json:"avg_duration_ms"`
	TokensPerSecond float64 `json:"tokens_per_second"`
	// SuccessRate 成功率百分比（0-100）；分母排除业务限制类错误。
	// nil 表示窗口内无任何请求，无法计算。
	SuccessRate *float64 `json:"success_rate"`
}

// PlazaProbe 模型的主动探测汇总。
type PlazaProbe struct {
	Total       int64    `json:"total"`
	SuccessCnt  int64    `json:"success_count"`
	AvgTTFTMs   *float64 `json:"avg_ttft_ms"`
	AvgTotalMs  *float64 `json:"avg_total_ms"`
	LastSuccess *bool    `json:"last_success"`
}

// 价格来源标识，供前端提示用户价格可信度。
const (
	// PriceSourceChannel 完全来自本站渠道自定义定价。
	PriceSourceChannel = "channel"
	// PriceSourceOfficial 完全来自内嵌的 LiteLLM 官方价表。
	PriceSourceOfficial = "official"
	// PriceSourceMixed 渠道定价 + 官方价表补齐缺失字段。
	PriceSourceMixed = "mixed"
	// PriceSourceUnknown 两处都没有价格。
	PriceSourceUnknown = "unknown"
)

// PlazaModel 模型广场的一张卡片（按模型名去重）。
type PlazaModel struct {
	Name        string       `json:"name"`
	Platforms   []string     `json:"platforms"`
	Groups      []PlazaGroup `json:"groups"`
	BillingMode string       `json:"billing_mode"`
	Price       PlazaPrice   `json:"price"`
	// MultiPrice 表示多个来源的有效价不一致，前端卡面价格应加「低至」前缀。
	MultiPrice bool `json:"multi_price"`
	// PriceSource 取值见 PriceSource* 常量。
	PriceSource string `json:"price_source"`
	// OfficialPrice 是模型的官方标准价（内嵌 LiteLLM 价表），与本站渠道定价无关。
	// 详情页用它作为「基础价格」，再乘各分组倍率得到分组定价。nil 表示价表无此模型。
	OfficialPrice *PlazaPrice   `json:"official_price"`
	Description   string        `json:"description"`
	Sources       []PlazaSource `json:"sources"`
	Metric        *PlazaMetric  `json:"metric"`
	Probe         *PlazaProbe   `json:"probe"`
}

// PlazaData 模型广场完整数据。
type PlazaData struct {
	Models      []PlazaModel `json:"models"`
	MetricHours int          `json:"metric_hours"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

// plazaPGReader 是 PlazaService 依赖的线上库只读能力（小接口，便于测试替身）。
type plazaPGReader interface {
	ListGroupAvailableModels(ctx context.Context) ([]repository.PGGroupModels, error)
	ListChannelModelPricing(ctx context.Context) ([]repository.PGChannelPricingRow, error)
	ListPricingIntervals(ctx context.Context) ([]repository.PGPricingInterval, error)
	AggregateModelMetrics(ctx context.Context, start, end time.Time) ([]repository.PGModelMetric, error)
}

// plazaProbeReader 是 PlazaService 依赖的探测汇总能力。
type plazaProbeReader interface {
	SummaryByModel(ctx context.Context, since time.Time) ([]*repository.ModelProbeRow, error)
}

// PlazaService 组装模型广场数据。
type PlazaService struct {
	pg          plazaPGReader
	probe       plazaProbeReader
	metricHours int
	cacheTTL    time.Duration

	mu       sync.RWMutex
	cached   *PlazaData
	cachedAt time.Time
}

// NewPlazaService 创建服务。metricHours <= 0 回退 24；cacheTTL <= 0 表示不缓存。
func NewPlazaService(pg plazaPGReader, probe plazaProbeReader, metricHours int, cacheTTL time.Duration) *PlazaService {
	if metricHours <= 0 {
		metricHours = 24
	}
	return &PlazaService{pg: pg, probe: probe, metricHours: metricHours, cacheTTL: cacheTTL}
}

// Build 返回模型广场数据，命中缓存时直接复用。
//
// iframe 场景下同一份数据会被多个用户高频拉取，缓存避免反复打线上库。
func (s *PlazaService) Build(ctx context.Context) (*PlazaData, error) {
	if data := s.readCache(); data != nil {
		return data, nil
	}

	groups, err := s.pg.ListGroupAvailableModels(ctx)
	if err != nil {
		return nil, err
	}
	pricing, err := s.pg.ListChannelModelPricing(ctx)
	if err != nil {
		return nil, err
	}
	intervals, err := s.pg.ListPricingIntervals(ctx)
	if err != nil {
		return nil, err
	}

	end := time.Now()
	start := end.Add(-time.Duration(s.metricHours) * time.Hour)
	metrics, err := s.pg.AggregateModelMetrics(ctx, start, end)
	if err != nil {
		return nil, err
	}

	// 探测数据来自本地 SQLite，失败不阻塞整页（状态列显示「未接入监控」）。
	var probes []*repository.ModelProbeRow
	if s.probe != nil {
		probes, _ = s.probe.SummaryByModel(ctx, start)
	}

	data := &PlazaData{
		Models:      buildPlazaModels(groups, pricing, intervals, metrics, probes, LoadStaticPriceTable()),
		MetricHours: s.metricHours,
		UpdatedAt:   time.Now(),
	}
	s.writeCache(data)
	return data, nil
}

func (s *PlazaService) readCache() *PlazaData {
	if s.cacheTTL <= 0 {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cached != nil && time.Since(s.cachedAt) < s.cacheTTL {
		return s.cached
	}
	return nil
}

func (s *PlazaService) writeCache(data *PlazaData) {
	if s.cacheTTL <= 0 {
		return
	}
	s.mu.Lock()
	s.cached = data
	s.cachedAt = time.Now()
	s.mu.Unlock()
}

// modelAccumulator 聚合过程中单个模型的中间状态。
type modelAccumulator struct {
	name      string
	sources   []PlazaSource
	platforms map[string]struct{}
	groups    map[int64]PlazaGroup
}

// channelPricingIndex 把渠道定价按「分组 × 模型名（小写）」建索引，供价格覆盖查询。
type channelPricingIndex struct {
	// byGroupModel 是 groupID → 小写模型名 → 定价来源。
	byGroupModel map[int64]map[string]PlazaSource
}

// buildPlazaModels 以「分组实际可用模型」为主线聚合卡片列表（纯函数，便于单测）。
//
// 数据来源的分工：
//   - 模型清单：groups（账号 model_mapping 的 KEY 并集），空映射分组回落平台默认表
//   - 价格：渠道定价覆盖优先，缺失字段回落内嵌 LiteLLM 静态价表
//   - 指标/探测：按模型名 join
func buildPlazaModels(
	groups []repository.PGGroupModels,
	pricing []repository.PGChannelPricingRow,
	intervals []repository.PGPricingInterval,
	metrics []repository.PGModelMetric,
	probes []*repository.ModelProbeRow,
	staticPrices *StaticPriceTable,
) []PlazaModel {
	pricingIdx := buildChannelPricingIndex(pricing, groupIntervals(intervals))

	accs := make(map[string]*modelAccumulator)
	names := make([]string, 0, 64)

	for _, grp := range groups {
		models := grp.Models
		// 账号都没配 model_mapping 时，sub2api 线上会回落到硬编码默认表。
		if len(models) == 0 {
			models = DefaultModelsForPlatform(grp.Platform)
		}
		if len(models) == 0 {
			continue
		}

		plazaGroup := PlazaGroup{
			ID:               grp.GroupID,
			Name:             grp.GroupName,
			Platform:         grp.Platform,
			RateMultiplier:   grp.RateMultiplier,
			SubscriptionType: grp.SubscriptionType,
			IsExclusive:      grp.IsExclusive,
		}

		for _, raw := range models {
			name := strings.TrimSpace(raw)
			if name == "" {
				continue
			}
			acc, ok := accs[name]
			if !ok {
				acc = &modelAccumulator{
					name:      name,
					platforms: make(map[string]struct{}),
					groups:    make(map[int64]PlazaGroup),
				}
				accs[name] = acc
				names = append(names, name)
			}
			acc.groups[grp.GroupID] = plazaGroup
			if grp.Platform != "" && grp.Platform != "composite" {
				acc.platforms[grp.Platform] = struct{}{}
			}

			// 该分组对这个模型有渠道定价覆盖 → 记为一个明细来源。
			if src, ok := pricingIdx.lookup(grp.GroupID, name); ok {
				src.Groups = []PlazaGroup{plazaGroup}
				acc.sources = append(acc.sources, src)
			}
		}
	}

	metricByModel := make(map[string]repository.PGModelMetric, len(metrics))
	for _, m := range metrics {
		metricByModel[m.Model] = m
	}
	probeByModel := make(map[string]*repository.ModelProbeRow, len(probes))
	for _, p := range probes {
		probeByModel[p.Model] = p
	}

	sort.Strings(names)
	out := make([]PlazaModel, 0, len(names))
	for _, name := range names {
		acc := accs[name]
		cheapest := pickCheapestSource(acc.sources)

		// 价格：渠道定价覆盖优先，未覆盖的字段回落静态价表
		// （对齐 sub2api 计费链路「渠道覆盖 → LiteLLM 目录」的优先级）。
		official := staticPrices.Lookup(name)
		price, fromStatic := mergeWithStaticPrice(cheapest.Price, official)

		model := PlazaModel{
			Name:          name,
			Platforms:     sortedKeys(acc.platforms),
			Groups:        sortedGroups(acc.groups),
			BillingMode:   cheapest.BillingMode,
			Price:         price,
			MultiPrice:    hasDivergentPrices(acc.sources),
			Description:   cheapest.ChannelDesc,
			Sources:       acc.sources,
			PriceSource:   priceSourceLabel(len(acc.sources) > 0, fromStatic),
			OfficialPrice: officialToPlazaPrice(official),
		}
		if m, ok := metricByModel[name]; ok {
			model.Metric = &PlazaMetric{
				RequestCount:    m.RequestCount,
				AvgDurationMs:   m.AvgDurationMs,
				TokensPerSecond: m.TokensPerSecond,
				SuccessRate:     successRate(m.SuccessCount, m.ErrorCount),
			}
		}
		if p, ok := probeByModel[name]; ok {
			model.Probe = &PlazaProbe{
				Total:       p.Total,
				SuccessCnt:  p.SuccessCnt,
				AvgTTFTMs:   p.AvgTTFT,
				AvgTotalMs:  p.AvgTotal,
				LastSuccess: p.LastSuccess,
			}
		}
		out = append(out, model)
	}
	return out
}

// buildChannelPricingIndex 把渠道定价行整理成「分组 × 模型名」索引。
func buildChannelPricingIndex(
	pricing []repository.PGChannelPricingRow,
	intervalsByPricing map[int64][]PlazaInterval,
) *channelPricingIndex {
	idx := &channelPricingIndex{byGroupModel: make(map[int64]map[string]PlazaSource)}
	for _, row := range pricing {
		if row.GroupID == 0 {
			continue
		}
		perGroup, ok := idx.byGroupModel[row.GroupID]
		if !ok {
			perGroup = make(map[string]PlazaSource)
			idx.byGroupModel[row.GroupID] = perGroup
		}
		src := PlazaSource{
			ChannelName: row.ChannelName,
			ChannelDesc: row.ChannelDesc,
			Platform:    row.Platform,
			BillingMode: normalizeBillingMode(row.BillingMode),
			Price: PlazaPrice{
				Input:        row.InputPrice,
				Output:       row.OutputPrice,
				CacheWrite:   row.CacheWritePrice,
				CacheRead:    row.CacheReadPrice,
				ImageOutput:  row.ImageOutputPrice,
				PerRequest:   row.PerRequestPrice,
				HasIntervals: len(intervalsByPricing[row.PricingID]) > 0,
			},
			Intervals: intervalsByPricing[row.PricingID],
		}
		for _, m := range row.Models {
			key := strings.ToLower(strings.TrimSpace(m))
			if key == "" || strings.HasSuffix(key, "*") {
				continue
			}
			if _, exists := perGroup[key]; !exists {
				perGroup[key] = src
			}
		}
	}
	return idx
}

func (idx *channelPricingIndex) lookup(groupID int64, model string) (PlazaSource, bool) {
	perGroup, ok := idx.byGroupModel[groupID]
	if !ok {
		return PlazaSource{}, false
	}
	src, ok := perGroup[strings.ToLower(strings.TrimSpace(model))]
	return src, ok
}

// mergeWithStaticPrice 用静态价表补齐渠道定价未覆盖的字段。
// 第二个返回值报告是否用到了静态表（供前端标注价格来源）。
func mergeWithStaticPrice(base PlazaPrice, static *StaticModelPrice) (PlazaPrice, bool) {
	if static == nil {
		return base, false
	}
	used := false
	if base.Input == nil && static.Input != nil {
		base.Input = static.Input
		used = true
	}
	if base.Output == nil && static.Output != nil {
		base.Output = static.Output
		used = true
	}
	if base.CacheWrite == nil && static.CacheWrite != nil {
		base.CacheWrite = static.CacheWrite
		used = true
	}
	if base.CacheRead == nil && static.CacheRead != nil {
		base.CacheRead = static.CacheRead
		used = true
	}
	return base, used
}

// officialToPlazaPrice 把静态价表条目转成对外的价格结构。nil 入参返回 nil。
func officialToPlazaPrice(p *StaticModelPrice) *PlazaPrice {
	if p == nil {
		return nil
	}
	return &PlazaPrice{
		Input:      p.Input,
		Output:     p.Output,
		CacheWrite: p.CacheWrite,
		CacheRead:  p.CacheRead,
	}
}

// successRate 计算成功率百分比（0-100）。总数为 0 时返回 nil（无数据而非 0%）。
func successRate(success, errors int64) *float64 {
	total := success + errors
	if total <= 0 {
		return nil
	}
	rate := float64(success) / float64(total) * 100
	return &rate
}

// priceSourceLabel 描述价格的来源，供前端提示用户价格可信度。
func priceSourceLabel(hasChannel, usedStatic bool) string {
	switch {
	case hasChannel && usedStatic:
		return PriceSourceMixed
	case hasChannel:
		return PriceSourceChannel
	case usedStatic:
		return PriceSourceOfficial
	default:
		return PriceSourceUnknown
	}
}

func groupIntervals(intervals []repository.PGPricingInterval) map[int64][]PlazaInterval {
	out := make(map[int64][]PlazaInterval)
	for _, iv := range intervals {
		out[iv.PricingID] = append(out[iv.PricingID], PlazaInterval{
			MinTokens:  iv.MinTokens,
			MaxTokens:  iv.MaxTokens,
			TierLabel:  iv.TierLabel,
			Input:      iv.InputPrice,
			Output:     iv.OutputPrice,
			CacheWrite: iv.CacheWritePrice,
			CacheRead:  iv.CacheReadPrice,
			PerRequest: iv.PerRequestPrice,
		})
	}
	return out
}

func normalizeBillingMode(mode string) string {
	switch mode {
	case BillingModePerRequest, BillingModeImage:
		return mode
	default:
		return BillingModeToken
	}
}

// effectivePrice 返回来源的可比较有效价：per_request 比每次价，其余比输入价
// （缺省回退输出价）。nil 表示无价可比，排序时恒排末尾。
func effectivePrice(src PlazaSource) *float64 {
	if src.BillingMode == BillingModePerRequest {
		return src.Price.PerRequest
	}
	if src.Price.Input != nil {
		return src.Price.Input
	}
	return src.Price.Output
}

// pickCheapestSource 选出最便宜的来源。整组价格取自同一来源，避免逐字段混取
// 拼出一个实际不存在的价格组合。全部无价时取第一个。
func pickCheapestSource(sources []PlazaSource) PlazaSource {
	if len(sources) == 0 {
		return PlazaSource{BillingMode: BillingModeToken}
	}
	best := sources[0]
	bestPrice := effectivePrice(best)
	for i := 1; i < len(sources); i++ {
		p := effectivePrice(sources[i])
		if p == nil {
			continue
		}
		if bestPrice == nil || *p < *bestPrice {
			best = sources[i]
			bestPrice = p
		}
	}
	return best
}

// hasDivergentPrices 判断多个来源的有效价是否不一致。
func hasDivergentPrices(sources []PlazaSource) bool {
	var first *float64
	for _, src := range sources {
		p := effectivePrice(src)
		if p == nil {
			continue
		}
		if first == nil {
			first = p
			continue
		}
		if *p != *first {
			return true
		}
	}
	return false
}

func sortedGroups(m map[int64]PlazaGroup) []PlazaGroup {
	out := make([]PlazaGroup, 0, len(m))
	for _, g := range m {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
