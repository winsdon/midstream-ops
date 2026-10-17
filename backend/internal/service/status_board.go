package service

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"sub2api-account-monitor/internal/pkg/metrics"
	"sub2api-account-monitor/internal/repository"
	"sub2api-account-monitor/internal/statusimg"
)

// statusBlockCount 卡片上的色块数，固定 8。
//
// 【为什么固定】块数是这张图的视觉语言：看图的人靠「哪一格变红」而不是读数字
// 判断趋势。块数随窗口变化会让同一张图在不同设置下不可比，也让截图和群里的
// 历史消息对不上。窗口变化只改变每块的时长，不改变块数。
const statusBlockCount = 8

// statusMinSamples 单块样本门槛：ok+err 少于此值视为样本不足，画灰块。
//
// 一个只有 1 次请求的块，失败 1 次就是 0% 成功率、最低档红色。低频分组上这种
// 噪声会让整张图像是出了事故；门槛把「没样本」与「真的差」区分开。
const statusMinSamples = 3

// statusWindowDefaultMinutes 默认统计窗口（分钟）。
const statusWindowDefaultMinutes = 90

// statusAbnormalSLA 最近两块合并后低于这个 SLA 判「异常」。
// 与稳定性页被动口径的 PASSIVE_RATE_BANDS 首档（90）一致，两边读数才不会打架。
const statusAbnormalSLA = 90.0

// statusSlowTTFTMs 首字 P50 超过这个值判「延迟」（毫秒）。
// 与前端 ttftPenalty 的起扣线相同：10 秒以内不认为有问题。
const statusSlowTTFTMs = 10_000.0

// blockStat 单分组单色块的聚合累计。
type blockStat struct {
	ok   int64
	err  int64
	ttft *float64
}

// groupStat 单分组整窗累计（底部两个数用）。
type groupStat struct {
	ok    int64
	err   int64
	input int64
	cache int64
}

// buildStatusBoard 把分组与时间线点组装成一张看板的视图模型（纯函数）。
//
// 分层：本函数只做「算」，statusimg 只做「画」，两者之间是 statusimg.Board 这组
// 成品字符串。刻意不再造一个 service 侧的类型别名 —— 多一层同构类型只会多一处
// 需要同步改的字段。
//
//	groups   ListGroups 的结果（已按 sort_order 排好，本函数保持其顺序）
//	points   GroupStatusTimeline 的结果（bucket 由 origin=since 分桶）
//	excluded 要排除的分组 id
//	now      「现在」，调用方已转成本地时区（图上要显示北京时间）
//	window   统计窗口；<= 0 时取默认 90 分钟
//
// 公开分组的口径在本函数里落地：status == "active" 且非专属分组。专属分组是
// 管理员授权给特定用户的，分组名与倍率属于内部定价策略，不该出现在客户群里。
func buildStatusBoard(
	groups []repository.PGGroup,
	points []repository.GroupStatusTimelinePoint,
	excluded []int64,
	now time.Time,
	window time.Duration,
) statusimg.Board {
	if window <= 0 {
		window = statusWindowDefaultMinutes * time.Minute
	}
	now = now.Truncate(time.Second)
	since := now.Add(-window)
	block := window / statusBlockCount
	// block 只可能在 window 小于 8ns 时为 0。当前档位都远大于它，但这是个纯函数，
	// 除数归零会 panic；折成整窗（等于一块）让任意 window 都有定义。
	if block <= 0 {
		block = window
	}

	// 下标映射：points 里可能出现已被过滤掉的分组（专属 / 非 active / 被排除），
	// 查不到下标就跳过。也顺带把顺序定成 ListGroups 的顺序。
	// 过滤口径与设置页的勾选列表共用 publicStatusGroups，避免两处各写一遍
	// 「什么算公开分组」。
	visible := publicStatusGroups(groups, excluded)
	slotOf := make(map[int64]int, len(visible))
	for i, g := range visible {
		slotOf[g.ID] = i
	}

	blocks := make([]blockStat, len(visible)*statusBlockCount)
	totals := make([]groupStat, len(visible))
	for _, p := range points {
		slot, ok := slotOf[p.GroupID]
		if !ok {
			continue
		}
		// 只收落在窗口内的桶。窗口外的点来自 since 之前（重连后补的数据）或
		// 分桶取整溢出的边角，装进图上会让某一块代表不属于它的时间。
		//
		// 【必须用 Before/After 比较，不能靠 int(diff/block) 的截断】Go 的
		// 浮点转整数是向零截断：明显早于 since 的桶（diff = -1 分钟）会算成
		// 下标 0，从而被当成「最早那一块」的真实数据。
		diff := p.Bucket.Sub(since)
		if diff < 0 || diff >= window {
			continue
		}
		idx := int(diff / block)
		// window 不能被 8 整除时（当前档位都能整除，属于防御）最后一条缝会
		// 算成下标 8；折进最后一块而不是丢弃 —— 那批数据确实属于最新的时段。
		if idx >= statusBlockCount {
			idx = statusBlockCount - 1
		}
		b := &blocks[slot*statusBlockCount+idx]
		b.ok += p.Ok
		b.err += p.Err
		// 每（分组, 桶）在 SQL 里已聚合过，一个桶只有一行，P50 直接取即可。
		if p.FirstTokP50 != nil {
			b.ttft = p.FirstTokP50
		}
		totals[slot].ok += p.Ok
		totals[slot].err += p.Err
		totals[slot].input += p.InputTokens
		totals[slot].cache += p.CacheReadTokens
	}

	board := statusimg.Board{
		Title:     defaultStatusPushTitle,
		Summary:   fmt.Sprintf("%d 个渠道 · 比率近%d分钟", len(visible), int(window.Minutes())),
		UpdatedAt: "更新 " + now.Format("01-02 15:04"),
		Cards:     make([]statusimg.Card, 0, len(visible)),
	}
	for i, g := range visible {
		stat := totals[i]
		sla := metrics.SLAPercent(stat.ok, stat.err)
		cache := metrics.CacheHitRate(stat.cache, stat.input)

		card := statusimg.Card{
			Name:        g.Name,
			Rate:        fmt.Sprintf("%.2fx", g.RateMultiplier),
			Blocks:      make([]statusimg.Block, 0, statusBlockCount),
			SuccessText: percentText(sla),
			CacheText:   percentText(cache),
		}
		for j := 0; j < statusBlockCount; j++ {
			b := blocks[i*statusBlockCount+j]
			samples := b.ok + b.err
			if samples < statusMinSamples {
				card.Blocks = append(card.Blocks, statusimg.Block{HasData: false})
				continue
			}
			card.Blocks = append(card.Blocks, statusimg.Block{
				Score:   blockHealthScore(b.ok, b.err, b.ttft),
				HasData: true,
			})
		}
		card.Status, card.StatusLevel = currentStatus(blocks, i*statusBlockCount)
		board.Cards = append(board.Cards, card)
	}
	return board
}

// currentStatus 用**最近 2 块**判定右上角状态，反映「当前」而不是整窗平均。
//
// 整窗 SLA 会把 10 分钟前的一场故障稀释到看不出来，而这张图在群里就是用来
// 回答「现在能不能用」。取 2 块而不是 1 块：单块样本常不足，2 块是能给出
// 稳定判断的最小窗口（与 frontend stabilityModel 的 PASSIVE_RATE_BANDS 同档）。
//
// 【状态同样受样本门槛约束】这是刻意的：门槛（statusMinSamples）的目的写在它的
// 注释里 ——「低频分组上一次失败就让整张图像是出了事故」。若状态判定绕过门槛，
// 就会出现「8 个色块全灰、右上角却标红异常、成功率 0.0%」的自相矛盾卡片，
// 恰好把门槛想挡的噪声从状态那一格放回来。所以最近两块样本不足时给
// 「样本不足」（灰），与色块的灰是同一个判断。
//
// 返回状态文本与配色档位（ok / warn / bad / none）。
func currentStatus(blocks []blockStat, base int) (string, string) {
	recent := blocks[base+statusBlockCount-2 : base+statusBlockCount]

	var ok, err int64
	for _, b := range recent {
		ok += b.ok
		err += b.err
	}
	switch {
	case ok+err == 0:
		return "无流量", "none"
	case ok+err < statusMinSamples:
		return "样本不足", "none"
	}
	if sla := metrics.SLAPercent(ok, err); sla != nil && displayPercent(*sla) < statusAbnormalSLA {
		return "异常", "bad"
	}
	// 首字取最近一个有样本的块而不是两块的加权平均：中位数不可加，两个桶的
	// P50 平均不出合并后的 P50；而「当前延迟」就该看最近的那次读数。
	for i := len(recent) - 1; i >= 0; i-- {
		if recent[i].ttft != nil {
			if *recent[i].ttft > statusSlowTTFTMs {
				return "延迟", "warn"
			}
			break
		}
	}
	return "正常", "ok"
}

// blockHealthScore 单块健康分 0–100。
//
// 【必须与前端同算法】前端 frontend/src/utils/stabilityModel.ts 的
// accountHealthScore 是同一口径的另一个实现（稳定性页的色点用它）。两处注释
// 互相指向：改一处必须改另一处，否则同一个账号在稳定性页是绿的、在群里是红的。
//
// 与前端一致的两条宽容线：错误率 ≤1% 不扣分（1–10% 线性扣到 40，>10% 扣 50），
// 首字 ≥10s 才开始扣分（10–30s 线性扣到 30，>30s 扣 40）。
// 前端允许传 errorRate 或 sla，这里直接由 ok/err 计数算错误率 —— 计数是精确值，
// 比先转成 SLA 再倒推少一次舍入。
func blockHealthScore(ok, err int64, ttftP50Ms *float64) int {
	total := ok + err
	if total <= 0 {
		return 0
	}
	errorRate := float64(err) / float64(total) * 100
	penalty := errorPenalty(errorRate) + ttftPenalty(ttftP50Ms)
	score := int(math.Round(100 - penalty))
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

// errorPenalty 错误率扣分：≤1% 不扣，1–10% 线性到 40，更高固定 50。
func errorPenalty(errorRate float64) float64 {
	if math.IsNaN(errorRate) || errorRate <= 1 {
		return 0
	}
	if errorRate <= 10 {
		return (errorRate - 1) / 9 * 40
	}
	return 50
}

// ttftPenalty 首字扣分：<10s 不扣，10–30s 线性到 30，>30s 固定 40。
// 缺值（该块没有任何成功的首字样本）不扣分，而不是按最差处理。
func ttftPenalty(ttftP50Ms *float64) float64 {
	if ttftP50Ms == nil || math.IsNaN(*ttftP50Ms) || *ttftP50Ms < 0 {
		return 0
	}
	v := *ttftP50Ms
	if v < statusSlowTTFTMs {
		return 0
	}
	if v <= 30_000 {
		return (v - statusSlowTTFTMs) / 20_000 * 30
	}
	return 40
}

// percentText 百分比显示：无样本显示「-」，否则保留 1 位小数。
//
// 无样本必须是「-」而不是「0%」：0% 缓存率表示「完全没命中」，
// 与「这批请求根本没走 token」是两件事，混在一起会误导客户。
func percentText(v *float64) string {
	if v == nil {
		return "-"
	}
	return strconv.FormatFloat(displayPercent(*v), 'f', 1, 64) + "%"
}

// displayPercent 把百分比舍入到 1 位小数 —— 图上与 caption 里显示的精度。
//
// 【为什么单独抽出来】状态判定（currentStatus）与数字显示必须用同一个舍入值，
// 否则会出现「图上写成功率 90.0%、右上角却标异常」这种自相矛盾的卡片：
// 89.96 用原始值比 90 是「异常」，用 %.1f 显示出来却是 90.0%。判定与显示都走
// 本函数，两者在定义上就不可能不一致。
func displayPercent(v float64) float64 {
	return math.Round(v*10) / 10
}

// statusPushCaption 图片下方的一句话说明（群消息里显示）。
//
// 图上只有数字，口径必须跟着消息走：客户看到红块会来问，运营要能直接回答
// 「这是怎么算的」。标题为空时用默认标题。
func statusPushCaption(title string, cardCount int, window time.Duration, ignored []int32) string {
	title = strings.TrimSpace(title)
	if title == "" {
		title = defaultStatusPushTitle
	}
	excluded := "无"
	if len(ignored) > 0 {
		parts := make([]string, 0, len(ignored))
		for _, c := range ignored {
			parts = append(parts, fmt.Sprintf("%d", c))
		}
		excluded = strings.Join(parts, "/")
	}
	return fmt.Sprintf("%s｜%d 个渠道｜近 %d 分钟真实流量｜色块=每 %s 健康分｜SLA 排除业务限制与 %s",
		title, cardCount, int(window.Minutes()), blockDurationText(window), excluded)
}

// blockDurationText 每块时长的可读文本（11.25 分钟这类非整数要说清）。
//
// 默认 90 分钟窗口 ÷ 8 块 = 11.25 分钟，必须写出来：看图的人会按「一块=10 分钟」
// 估算，差 12.5% 会让「刚才那波抖动了多久」判断错。
func blockDurationText(window time.Duration) string {
	minutes := window.Minutes() / statusBlockCount
	return strconv.FormatFloat(minutes, 'f', -1, 64) + " 分钟"
}
