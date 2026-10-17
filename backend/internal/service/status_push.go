package service

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"sub2api-account-monitor/internal/config"
	"sub2api-account-monitor/internal/notify"
	"sub2api-account-monitor/internal/repository"
	"sub2api-account-monitor/internal/statusimg"

	"github.com/robfig/cron/v3"
)

// defaultStatusPushTitle 看板默认大标题。
const defaultStatusPushTitle = "渠道状态"

// DefaultIgnoredStatusCodes 不计入失败的 HTTP 状态码，与稳定性页同一份口径：
// 400 参数错 / 403 无权限 / 429 限流 / 529 上游过载。
//
// 定义在 service 层而不是 handler：handler 只负责「用户在页面上怎么改这个集合」
// （parseIgnoredStatusCodes），而「哪几类响应不算服务故障」是业务口径，渠道状态图
// 与稳定性页必须永远一致。handler.defaultIgnoredStatusCodes 直接引用本变量。
var DefaultIgnoredStatusCodes = []int32{400, 403, 429, 529}

// 状态推送的间隔与窗口档位。前端下拉直接消费这两个列表（后端是唯一真相）。
var (
	// StatusPushIntervalOptions 间隔档位（分钟），全部能对齐整点或半点。
	StatusPushIntervalOptions = []int{30, 60, 120, 180, 240, 360, 720}
	// StatusPushWindowOptions 统计窗口档位（分钟）。
	StatusPushWindowOptions = []int{60, 90, 180, 360}
)

// statusPushFallbackIntervalMinutes 档位非法时的回落间隔（分钟）。
//
// 名字不用「min」：它不是下限夹紧，而是「不认识这个值就换成它」。取 60（整点一次）
// 而不是最小的 30：非法值多半来自手改库或老版本设置，突发多发图比漏发更难解释。
const statusPushFallbackIntervalMinutes = 60

// statusPushTimeout 单次推送的总超时（查询 + 画图 + 逐个群上传）。
const statusPushTimeout = 2 * time.Minute

// StatusPushSettings 渠道状态图定时推送配置（settings 中 status_push 域的 JSON 结构）。
type StatusPushSettings struct {
	Enabled          bool     `json:"enabled"`
	IntervalMinutes  int      `json:"interval_minutes"`
	WindowMinutes    int      `json:"window_minutes"`
	ChatIDs          []string `json:"chat_ids"`
	ExcludedGroupIDs []int64  `json:"excluded_group_ids"`
	Title            string   `json:"title"`
}

// normalize 填充默认值、夹紧非法档位、清理 chat_id 列表。
func (s *StatusPushSettings) normalize() {
	if !containsInt(StatusPushIntervalOptions, s.IntervalMinutes) {
		s.IntervalMinutes = statusPushFallbackIntervalMinutes
	}
	if !containsInt(StatusPushWindowOptions, s.WindowMinutes) {
		s.WindowMinutes = statusPushWindowMinutesDefault
	}
	s.ChatIDs = cleanChatIDs(s.ChatIDs)
	if s.ExcludedGroupIDs == nil {
		s.ExcludedGroupIDs = []int64{}
	}
	if strings.TrimSpace(s.Title) == "" {
		s.Title = defaultStatusPushTitle
	} else {
		s.Title = truncateRunes(strings.TrimSpace(s.Title), statusPushMaxTitleRunes)
	}
}

// statusPushMaxTitleRunes 标题最大字符数（按 rune 计）。
//
// 【为什么必须有上限】标题会同时进两个地方：图上（渲染层按显示宽度截断，超长只是难看）
// 和 caption（Telegram 硬限制 1024 字符）。caption 里的标题不会被截断，
// 于是一个几千字的标题会让 sendPhoto 返回 400，**所有目标群一起失败** ——
// 而失败原因看起来完全与标题无关。这里按 rune 截断（不是字节，避免切断汉字），
// 32 个字足够表达「渠道状态」这类标题，留出的余量也远超 caption 的其余部分。
const statusPushMaxTitleRunes = 32

// telegramCaptionMaxRunes Telegram sendPhoto 的 caption 上限（字符）。
// 标题上限就是按它反推的，测试里也会拿它当断言上界。
const telegramCaptionMaxRunes = 1024

// truncateRunes 按 rune 截断，超长时保留省略号。
func truncateRunes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	rs := []rune(s)
	if len(rs) <= limit {
		return s
	}
	return string(rs[:limit]) + "…"
}

// defaultStatusPush 缺省配置：默认关闭。
//
// 与预警一样默认关闭而不是默认开启：往客户群里发图是「对外可见」的动作，
// 必须由运营显式确认 chat_id 之后再打开。间隔默认 60 分钟，窗口默认 90 分钟。
func defaultStatusPush() StatusPushSettings {
	return StatusPushSettings{
		Enabled:          false,
		IntervalMinutes:  60,
		WindowMinutes:    statusPushWindowMinutesDefault,
		ChatIDs:          []string{},
		ExcludedGroupIDs: []int64{},
		Title:            defaultStatusPushTitle,
	}
}

// statusPushWindowMinutesDefault 默认统计窗口：90 分钟。
//
// 8 块 × 11.25 分钟：足够覆盖一小时内的抖动，又短到「现在能不能用」还有意义。
const statusPushWindowMinutesDefault = 90

// chatIDRe 合法的 Telegram chat_id：数字（群/频道，超级群形如 -100…）或 @频道用户名。
//
// 在保存时就挡住格式错误，而不是等整点推送失败：图上一次失败可能过半小时才有人
// 发现，而表单里立刻报错能马上改。
var chatIDRe = regexp.MustCompile(`^(-?\d+|@[A-Za-z][A-Za-z0-9_]{4,31})$`)

// ValidateStatusPushChatIDs 校验 chat_id 列表格式，返回第一个非法项的错误。
// 空列表合法（未配置 = 不发），由调用方决定是否提示。
func ValidateStatusPushChatIDs(ids []string) error {
	for _, id := range cleanChatIDs(ids) {
		if !chatIDRe.MatchString(id) {
			return fmt.Errorf("chat_id 格式不正确: %q（应为 -1001234567890 这样的数字，或 @channel 这样的频道用户名）", id)
		}
	}
	return nil
}

// cleanChatIDs 去掉空白与重复项（保持原顺序）。
// 换行粘贴是最常见的输入方式，重复项则会让同一个群收到两张一样的图。
func cleanChatIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func containsInt(list []int, v int) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

// statusPushCronSpec 把间隔换算成对齐整点的 cron 表达式（标准 5 字段）。
//
// 【为什么不用 @every Nm】@every 从进程启动时刻起算，重启一次就换一个相位：
// 09:07 发、重启后 09:52 发，客户群里看起来毫无规律。对齐整点后每张图的
// 时间戳都是 :00，运营和客户都能对上「这是几点的数据」。
func statusPushCronSpec(intervalMinutes int) string {
	switch intervalMinutes {
	case 30:
		return "*/30 * * * *"
	case 120:
		return "0 */2 * * *"
	case 180:
		return "0 */3 * * *"
	case 240:
		return "0 */4 * * *"
	case 360:
		return "0 */6 * * *"
	case 720:
		return "0 */12 * * *"
	default:
		// 60 与任何未登记的值都按整点每小时。
		return "0 * * * *"
	}
}

// StatusPushChatResult 单个群的推送结果。
type StatusPushChatResult struct {
	ChatID string `json:"chat_id"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
}

// StatusPushResult 一次推送的整体结果（只留内存，供设置页显示）。
//
// 【为什么不落库】YAGNI，且落库要配套保留期清理。设置页要回答的只是
// 「上一次推成功了吗、几点的、失败原因是什么」，进程重启后从头开始即可。
type StatusPushResult struct {
	// At 为 RFC3339（带时区偏移）。刻意不用「2006-01-02 15:04:05」：
	// 那种格式没有时区，浏览器的 new Date() 会按**浏览器本地时区**解释，
	// 服务端与用户不在同一时区时显示出来的时间就是错的。
	At      string                 `json:"at"`
	OK      bool                   `json:"ok"`
	Error   string                 `json:"error,omitempty"`
	Sent    int                    `json:"sent"`
	Failed  int                    `json:"failed"`
	Groups  int                    `json:"groups"`
	Results []StatusPushChatResult `json:"results,omitempty"`
}

// StatusPushService 渠道状态图的定时推送。
//
// 四个职责依次是：取数（PG）→ 组装（buildStatusBoard）→ 画图（statusimg）→
// 发送（notify）。本服务自身只管调度与编排，每一步的实现都在对应的包里。
type StatusPushService struct {
	cfg      *config.Config
	pg       *repository.PG
	settings *SettingsService

	mu      sync.Mutex
	cron    *cron.Cron
	entryID cron.EntryID
	started bool
	last    *StatusPushResult
}

// NewStatusPushService 创建推送服务（不启动；Start 才注册定时任务）。
func NewStatusPushService(cfg *config.Config, pg *repository.PG, settings *SettingsService) *StatusPushService {
	return &StatusPushService{
		cfg:      cfg,
		pg:       pg,
		settings: settings,
		// 自带 cron 实例（与全局 Scheduler 分离）：本功能的启停随设置热更新，
		// 混进全局调度器会让「移除/重注册」影响别的定时任务。
		cron: cron.New(cron.WithLocation(cfg.Location)),
	}
}

// Start 启动定时器并按当前设置注册任务。
func (s *StatusPushService) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return nil
	}
	if err := s.rescheduleLocked(s.settings.StatusPush()); err != nil {
		return err
	}
	s.cron.Start()
	s.started = true
	st := s.settings.StatusPush()
	if st.Enabled {
		log.Printf("[status-push] 已启动：间隔=%s 窗口=%d 分钟 群数=%d", statusPushCronSpec(st.IntervalMinutes), st.WindowMinutes, len(st.ChatIDs))
	} else {
		log.Printf("[status-push] 已就绪（未启用）")
	}
	return nil
}

// Stop 停止定时器（等待运行中的推送结束）。
func (s *StatusPushService) Stop() {
	s.mu.Lock()
	started := s.started
	s.started = false
	c := s.cron
	s.mu.Unlock()
	if !started {
		return
	}
	<-c.Stop().Done()
	log.Println("[status-push] 已停止")
}

// Reload 设置变化时重新注册定时任务（由 SettingsService 回调）。
func (s *StatusPushService) Reload(st StatusPushSettings) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		return
	}
	// 启用状态或间隔都可能变，统一「先移除旧 entry 再按新 spec 注册」——
	// 只改间隔时若不移除，旧 entry 会继续按老间隔发。
	if err := s.rescheduleLocked(st); err != nil {
		log.Printf("[status-push] 重新注册定时任务失败: %v", err)
		return
	}
	if st.Enabled {
		log.Printf("[status-push] 定时任务已更新：间隔=%s 窗口=%d 分钟 群数=%d",
			statusPushCronSpec(st.IntervalMinutes), st.WindowMinutes, len(st.ChatIDs))
	} else {
		log.Printf("[status-push] 定时任务已移除（未启用）")
	}
}

// rescheduleLocked 移除旧 entry 并按设置注册新 entry（调用方持锁）。
func (s *StatusPushService) rescheduleLocked(st StatusPushSettings) error {
	if s.entryID != 0 {
		s.cron.Remove(s.entryID)
		s.entryID = 0
	}
	if !st.Enabled {
		return nil
	}
	id, err := s.cron.AddFunc(statusPushCronSpec(st.IntervalMinutes), s.runScheduled)
	if err != nil {
		return fmt.Errorf("注册渠道推送定时任务失败: %w", err)
	}
	s.entryID = id
	return nil
}

// runScheduled cron 回调：读一次当前设置，PG 不可用则跳过本轮。
func (s *StatusPushService) runScheduled() {
	st := s.settings.StatusPush()
	if !st.Enabled {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), statusPushTimeout)
	defer cancel()
	// 与 Scheduler.withPG 同款降级：不可用时先尝试重连，仍不可用就跳过本轮。
	// 跳过而不是报错：整点触发一次，下一个整点自然重试，没必要制造噪声。
	if !s.pg.Available() {
		if err := s.pg.Ping(ctx); err != nil || !s.pg.Available() {
			log.Printf("[status-push] 跳过本轮：PG 不可用")
			return
		}
	}
	s.push(ctx, st, "定时")
}

// StatusPushPreviewOverride 只用于预览：让「还没保存的改动」也能出图。
//
// 字段用指针区分「未指定」（nil → 用已保存的设置）与「显式指定为空」
// （ExcludedGroupIDs 指向空切片 = 本次预览不排除任何分组）。
type StatusPushPreviewOverride struct {
	WindowMinutes    *int
	ExcludedGroupIDs *[]int64
}

// Preview 用给定设置渲染一张图，不发送。
//
// 设置由调用方传入（叠加预览覆盖）而不是内部直接读：预览要能反映**表单里
// 还没保存的改动**，而推送必须用已保存的设置 —— 两者取值时机不同，
// 所以由调用方决定，本函数只负责渲染。
func (s *StatusPushService) Preview(ctx context.Context, override StatusPushPreviewOverride) ([]byte, error) {
	st := s.settings.StatusPush()
	if override.WindowMinutes != nil {
		st.WindowMinutes = *override.WindowMinutes
		// 走一遍 normalize：预览窗口同样只接受合法档位，非法值回落默认，
		// 免得预览出一个真实设置里永远不会出现的窗口。
		st.normalize()
	}
	if override.ExcludedGroupIDs != nil {
		st.ExcludedGroupIDs = *override.ExcludedGroupIDs
	}
	board, _, _, err := s.buildBoard(ctx, st)
	if err != nil {
		return nil, err
	}
	return statusimg.Render(board)
}

// PushNow 立即推送一次（设置页的「立即推送测试」，忽略 enabled 开关）。
func (s *StatusPushService) PushNow(ctx context.Context) StatusPushResult {
	return s.push(ctx, s.settings.StatusPush(), "手动")
}

// PGAvailable 报告远端库当前是否可用，供 handler 区分 503（可重试）与 500（本站出错）。
// 与其它只读页面的降级语义保持一致。
func (s *StatusPushService) PGAvailable() bool {
	return s.pg != nil && s.pg.Available()
}

// LastPush 返回最近一次推送结果的**副本**（从未推送过为 nil）。
// Results 必须深拷贝：`cp := *s.last` 只复制切片头，调用方改一下
// results[0].ChatID 就改到了服务内部的记录上。当前唯一调用方只做 JSON 序列化，
// 但这是个纯读取接口，不该把内部状态的可写别名交出去。
func (s *StatusPushService) LastPush() *StatusPushResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last == nil {
		return nil
	}
	cp := *s.last
	if s.last.Results != nil {
		cp.Results = append([]StatusPushChatResult(nil), s.last.Results...)
	}
	return &cp
}

// PublicGroups 返回可推送的公开分组（active 且非专属，已按 sort_order 排）。
// 设置页的排除勾选列表用它 —— 这里**不**应用当前排除集，否则被排除的组会从
// 列表里消失，运营再也无法取消勾选。
func (s *StatusPushService) PublicGroups(ctx context.Context) ([]repository.PGGroup, error) {
	groups, err := s.pg.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	return publicStatusGroups(groups, nil), nil
}

// push 执行一次完整推送并记录结果。
func (s *StatusPushService) push(ctx context.Context, st StatusPushSettings, source string) StatusPushResult {
	res := StatusPushResult{At: time.Now().In(s.cfg.Location).Format(time.RFC3339)}

	// 【先做本地配置校验，再去查库】两处配置缺失是最常见的失败原因，也是唯一
	// 「一看就能改」的原因。放在取数之前有两个好处：不白跑一次远端 percentile_cont，
	// 以及不会让「没填 Bot Token」被一次 PG 抖动覆盖成一条看不懂的网络超时。
	//
	// Bot Token 取自「通知渠道 → Telegram」已配置的值，不单独再填一份：
	// 同一个机器人，填两处必然漂移。
	token := strings.TrimSpace(s.settings.Channels().Telegram.BotToken)
	if token == "" {
		res.Error = "未配置 Telegram Bot Token，请在「通知与渠道」里填写"
		s.record(&res)
		log.Printf("[status-push] %s推送失败: %s", source, res.Error)
		return res
	}
	if len(st.ChatIDs) == 0 {
		res.Error = "未配置目标 Chat ID"
		s.record(&res)
		log.Printf("[status-push] %s推送失败: %s", source, res.Error)
		return res
	}

	board, total, truncated, err := s.buildBoard(ctx, st)
	if err != nil {
		res.Error = err.Error()
		s.record(&res)
		log.Printf("[status-push] %s推送失败: %v", source, err)
		return res
	}
	res.Groups = len(board.Cards)

	png, err := statusimg.Render(board)
	if err != nil {
		res.Error = "渲染图片失败: " + err.Error()
		s.record(&res)
		log.Printf("[status-push] %s推送失败: %v", source, err)
		return res
	}

	// caption 里带上「图不全」的实情：图上的说明行可能被小屏缩略图盖住，
	// 而消息正文是必然可见的那一面。caption 有 1024 字符，放得下。
	caption := statusPushCaption(st.Title, len(board.Cards), time.Duration(st.WindowMinutes)*time.Minute, DefaultIgnoredStatusCodes)
	if truncated {
		caption += fmt.Sprintf("\n（公开渠道共 %d 个，图仅显示前 %d 个）", total, statusPushMaxCards)
	}
	res.Results = make([]StatusPushChatResult, 0, len(st.ChatIDs))
	for _, chatID := range st.ChatIDs {
		item := StatusPushChatResult{ChatID: chatID}
		if err := notify.TelegramSendPhoto(ctx, token, chatID, png, caption); err != nil {
			item.Error = err.Error()
			res.Failed++
			log.Printf("[status-push] %s推送失败 chat_id=%s: %v", source, chatID, err)
		} else {
			item.OK = true
			res.Sent++
		}
		res.Results = append(res.Results, item)
	}
	res.OK = res.Failed == 0
	s.record(&res)
	if res.OK {
		log.Printf("[status-push] %s推送完成：%d 个渠道 → %d 个群", source, res.Groups, res.Sent)
	} else {
		log.Printf("[status-push] %s推送部分失败：成功 %d 个群，失败 %d 个群", source, res.Sent, res.Failed)
	}
	return res
}

// record 保存最近一次推送结果。
func (s *StatusPushService) record(res *StatusPushResult) {
	s.mu.Lock()
	s.last = res
	s.mu.Unlock()
}

// buildBoard 取数 + 组装出看板视图模型。
//
// 返回公开分组总数（裁剪前）与「是否裁剪过」：caption 要写出「共 N 个」，
// 而 board.Cards 已经被截短，光看它拿不到真实总数。
func (s *StatusPushService) buildBoard(ctx context.Context, st StatusPushSettings) (statusimg.Board, int, bool, error) {
	window := time.Duration(st.WindowMinutes) * time.Minute
	// 与 buildStatusBoard 内部完全一致地推导 since：那边的分桶下标是
	// (bucket - since) / block，origin 必须与它逐秒对齐，否则整张图的色块
	// 会整体错位一格。
	now := time.Now().In(s.cfg.Location).Truncate(time.Second)
	since := now.Add(-window)

	groups, err := s.pg.ListGroups(ctx)
	if err != nil {
		return statusimg.Board{}, 0, false, fmt.Errorf("读取分组失败: %w", err)
	}
	points, err := s.pg.GroupStatusTimeline(ctx, since, window/statusBlockCount, DefaultIgnoredStatusCodes)
	if err != nil {
		return statusimg.Board{}, 0, false, fmt.Errorf("读取分组状态时间线失败: %w", err)
	}

	board := buildStatusBoard(groups, points, st.ExcludedGroupIDs, now, window)

	// Telegram sendPhoto 的硬限制是「宽 + 高 ≤ 10000」（本站固定宽 1280）。
	// 每张卡约占 254px 高、每行 2 张，40 张约 5294px（宽高比 4.1:1）安全；
	// 超过约 66 个公开分组时整图会被 Telegram 直接拒收，且报错与分组数毫无关系。
	// 所以在这里截断，并把「不全」这件事同时写进图上的独立一行与 caption ——
	// 悄悄少发几个分组比整张图发不出去更难发现，而截断只发生在一处就够。
	//
	// 【为什么不改写 Summary】Summary 只占画布右半边（约 624px），
	// 「共 70 个，图仅显示前 64」会被渲染层按宽度截掉关键数字，
	// 恰好让「悄悄少发」在客户可见面上依然成立。
	total := visibleCardCount(groups, st)
	truncated := total > statusPushMaxCards
	if truncated {
		log.Printf("[status-push] 公开分组 %d 个，超过单图上限 %d，只画前 %d 个（其余请在设置里排除）",
			total, statusPushMaxCards, statusPushMaxCards)
		board.Cards = board.Cards[:statusPushMaxCards]
		board.Truncated = truncationNotice(total, statusPushMaxCards)
	}

	if t := strings.TrimSpace(st.Title); t != "" {
		board.Title = truncateRunes(t, statusPushMaxTitleRunes)
	}
	board.Note = fmt.Sprintf("每块 %s · SLA 排除业务限制与忽略状态码 · 与稳定性页同口径", blockDurationText(window))
	return board, total, truncated, nil
}

// statusPushMaxCards 单图的卡片数上限。
//
// 数字来自渲染层（statusimg.MaxCardsForSafeHeight）而不是在这里写死：
// 「单图最多几张」由画布布局与 Telegram 的「宽 + 高 ≤ 10000」共同决定，
// 是本包不该复述的约束。此前这里写过 64 —— 比渲染层的告警阈值高一张，
// 于是每次满上限推送都会打一条「图高超过安全上限」的告警，且永远无法消除。
var statusPushMaxCards = statusimg.MaxCardsForSafeHeight()

// truncationNotice 卡片被裁剪时图上的说明文案。
//
// 抽成纯函数是为了能被宽度断言覆盖：文案会画在图上独立一行（整幅宽约 1232px），
// 一旦写长了就会被渲染层截掉关键数字，而「共几个 / 显示了几个」恰恰是唯一
// 能让看图的人察觉「图不全」的信息。statusimg 的测试会按真实字体量它的宽度。
func truncationNotice(total, shown int) string {
	return fmt.Sprintf("共 %d 个渠道，图中仅显示前 %d 个（其余请在上方排除列表里去掉）", total, shown)
}

// visibleCardCount 复算一次公开分组数，用于超额时的说明文案。
func visibleCardCount(groups []repository.PGGroup, st StatusPushSettings) int {
	return len(publicStatusGroups(groups, st.ExcludedGroupIDs))
}

// publicStatusGroups 过滤出公开分组并保持输入顺序（ListGroups 已按 sort_order 排）。
//
// 顺序刻意不在这里重排：分组顺序是 sub2api 后台的属性，不是本功能的偏好；
// 复制一份排序规则必然和后台漂移。
func publicStatusGroups(groups []repository.PGGroup, excluded []int64) []repository.PGGroup {
	skip := make(map[int64]bool, len(excluded))
	for _, id := range excluded {
		skip[id] = true
	}
	out := make([]repository.PGGroup, 0, len(groups))
	for _, g := range groups {
		if g.Status != "active" || g.IsExclusive || skip[g.ID] {
			continue
		}
		out = append(out, g)
	}
	return out
}
