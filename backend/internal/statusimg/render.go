package statusimg

import (
	"bytes"
	"fmt"
	"image/color"
	"log"
	"sort"
	"strings"
	"unicode"

	"github.com/fogleman/gg"
	"golang.org/x/image/font"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// ── 渲染倍率与布局常量 ─────────────────────────────────────────────
//
// 所有常量都以「逻辑像素」书写（画布逻辑宽 640），绘制时统一乘 scale，
// 于是实际输出宽 1280px —— 手机上点开是清晰的 2× 图。
//
// 【为什么不给 gg 设置变换矩阵】gg 的 Scale 只作用于字形位图和路径顶点，
// 不作用于字形步进（advance）与行高（见 gg 的 drawString：advance 直接加在
// Dot.X 上，没有过矩阵）。直接 dc.Scale(2,2) 会把文字挤在一起、行高也不翻倍。
// 所以这里不设矩阵，改为把每个坐标显式换算后再画（见 s()），
// 文字与图形因此始终处在同一个坐标系里。
const (
	scale = 2 // 渲染倍率

	logicalW = 640 // 逻辑画布宽度
	outW     = logicalW * scale

	pad         = 16 // 画布外边距
	headerH     = 52 // 头部高度（大标题 + 右侧两行摘要）
	headerGap   = 10 // 头部与卡片区之间
	colCount    = 2  // 卡片列数
	colGap      = 12 // 卡片列间距 / 行间距
	cardRadius  = 10 // 卡片圆角
	cardPad     = 12 // 卡片内边距
	blockRadius = 3  // 色块圆角
	blockGap    = 4  // 色块间距

	nameLineH   = 20 // 名称/状态行
	rateLineH   = 16 // 倍率行
	blockTopGap = 8  // 倍率行与色块之间
	blockH      = 22 // 色块高度
	footerGap   = 10 // 色块与底部成功率之间
	metaLineH   = 15 // 底部成功率/缓存率行高、备注行高

	emptyH  = 80 // 无卡片时占位框高度
	noteGap = 10 // 卡片区与底部备注之间

	// 卡片高度由内部各行相加得出，避免改了字号/行高后卡片高度悄悄不够用。
	cardH = cardPad*2 + nameLineH + rateLineH + blockTopGap + blockH + footerGap + metaLineH

	blocksPerCard = 8 // 固定 8 个时间块

	// 状态文字前的圆点直径、点与文字间距、名称与状态之间的最小留白。
	statusDotD    = 6
	statusDotGap  = 5
	nameStatusGap = 12
	labelValueGap = 4 // 底部「成功率 99.2%」里标签与数值之间
	headerSideGap = 8 // 大标题与右侧摘要之间的最小留白（按画布中线切分实现）
)

// ── 图片尺寸安全上限 ───────────────────────────────────────────────
//
// 【为什么要有上限】图片高度只由卡片行数决定（每行 cardH + colGap），而分组
// 数量没有上界。Telegram sendPhoto 的硬限制是「宽 + 高 ≤ 10000」（另外单边
// 也 ≤ 10000）：40 个分组时输出 1280×5294，宽+高 = 6574 还安全；到约 67 个
// 分组（34 行）时宽+高就突破 10000，Telegram 会直接拒收 —— 而且是在把图上传
// 完之后才报错，运营只会看到「推送失败」而不知道是图太高。
//
// 本包不在此处裁剪卡片：要发多少分组是业务决策（见 service 层），渲染层擅自
// 少画几张等于静默丢数据，比推送失败更糟。所以这里只负责在越界时告警。
const (
	// telegramMaxPhotoSum 是 Telegram sendPhoto 对「宽 + 高」的硬限制。
	telegramMaxPhotoSum = 10000

	// maxImageHeight 是本包认为安全的图片高度上限：硬限制减去输出宽度，再留
	// 5% 余量。留余量是因为 Telegram 的限制可能在别处（如文件体积、聊天类型）
	// 更严，卡在 8720 这种临界值上出问题最难排查。
	maxImageHeight = (telegramMaxPhotoSum - outW) * 95 / 100 // 8284
)

// maxSafeCards 是总高不超过 maxImageHeight 的最大卡片数，由布局反推。
//
// 【为什么由本包导出这个数】「单图最多几张」是渲染约束（画布尺寸 + Telegram
// 限制），不是业务偏好，所以数字必须由知道布局的人推导。service 层曾自己写死
// 一个 64 —— 比本包的告警阈值还高一张，结果每次满上限推送都会打一条「图高超过
// 安全上限」的告警，而 service 已经按设计截断过了，这条告警永远消不掉。
// 反推而不是手写公式：公式一改（字号、行高、列数）这里自动跟上。
//
// 按最坏情形算：带底部备注行 + 带「卡片被裁剪」说明行，两者都会占高。
var maxSafeCards = computeMaxSafeCards()

// MaxCardsForSafeHeight 返回单图安全的卡片数上限，供 service 层截断使用。
func MaxCardsForSafeHeight() int { return maxSafeCards }

// computeMaxSafeCards 二分出不超过 maxImageHeight 的最大卡片数。
func computeMaxSafeCards() int {
	boardWith := func(n int) Board {
		b := Board{
			Title:   "渠道状态",
			Summary: "99 个渠道 · 比率近90分钟",
			// 最坏情形：两行脚注都在（推送时一定会有 Note，裁剪时还会有 Truncated）
			Note:      "每块 11.25 分钟 · SLA 排除业务限制与忽略状态码 · 与稳定性页同口径",
			Truncated: "共 99 个渠道，图中仅显示前 99 个（其余请在上方排除列表里去掉）",
			Cards:     make([]Card, n),
		}
		return b
	}
	lo, hi := 0, 1
	for computeLayout(boardWith(hi)).height <= float64(maxImageHeight) {
		lo = hi
		hi *= 2
		if hi > 1<<20 {
			break
		}
	}
	for lo+1 < hi {
		mid := (lo + hi) / 2
		if computeLayout(boardWith(mid)).height <= float64(maxImageHeight) {
			lo = mid
		} else {
			hi = mid
		}
	}
	return lo
}

// 字号（逻辑像素）。
const (
	fsTitle   = 22
	fsSummary = 13
	fsUpdated = 12
	fsName    = 15
	fsRate    = 12
	fsStatus  = 12
	fsMeta    = 11
)

// 配色。底色与卡片色取 Tailwind gray 系，保证浅灰底上的白卡有足够对比。
var (
	colorBackground = color.RGBA{0xf3, 0xf4, 0xf6, 0xff} // 浅灰底 gray-100
	colorCard       = color.RGBA{0xff, 0xff, 0xff, 0xff} // 卡片白底
	colorCardEdge   = color.RGBA{0xe5, 0xe7, 0xeb, 0xff} // 卡片描边 gray-200
	colorTitle      = color.RGBA{0x11, 0x18, 0x27, 0xff} // 标题/名称 gray-900
	colorMuted      = color.RGBA{0x6b, 0x72, 0x80, 0xff} // 次要文字 gray-500
	colorFaint      = color.RGBA{0x9c, 0xa3, 0xaf, 0xff} // 最弱文字 gray-400
)

// emptyHint 是无卡片时的占位文案。
//
// 它只是排版占位，不表达任何业务结论：「一张卡片都没有」是 service 层给出
// 的事实，本包照实画一个空框 + 一句中性提示，避免出现一片可疑的空白。
const emptyHint = "暂无数据"

// 卡片底部两个比率的标签。
//
// 提成常量而不是散在 drawCard 里写字面量：缺字形检测要按「图上实际画的字」
// 来扫，用同一份常量才能保证以后改文案时检测不会漏掉。
const (
	labelSuccess = "成功率"
	labelCache   = "缓存率"
)

// s 把逻辑像素换算成输出像素。
func s(v float64) float64 { return v * scale }

// Render 把看板视图模型渲染成 PNG 字节（1280px 宽，即逻辑 640 × 2 倍率）。
//
// 高度随卡片行数增长（0 张卡片时画一个占位框）。字体默认用内嵌的
// Noto Sans SC，可用 SetFontPath 覆盖。
//
// 两个只告警、不改渲染的行为（本包不替业务层做决定）：
//   - 字体缺字形：把缺的字符打一条日志，图照画（gg 会把缺字画成空白/方框）。
//   - 图高越过安全上限：打一条日志提醒可能被 Telegram 拒收，不裁剪卡片。
func Render(b Board) ([]byte, error) {
	// 先取字体：字体有问题就在画之前失败，而不是画出半张图。
	f, err := loadFont()
	if err != nil {
		return nil, err
	}

	// 先算布局：图高越界告警需要总高这个数。
	l := computeLayout(b)

	// 缺字形与图高越界都只写日志、不参与控制流：推送该不该发是 service 层的
	// 判断，渲染层只负责把「画出来会有豆腐块 / 图会被拒收」留下痕迹，
	// 否则运营永远不知道客户看到一个方框，或为什么推送失败。
	warnBoardIssues(f, b, l)

	fs, err := newFaces(f)
	if err != nil {
		return nil, err
	}

	dc := gg.NewContext(int(l.width), int(l.height))
	dc.SetColor(colorBackground)
	dc.Clear()

	drawHeader(dc, fs, b, l)
	if len(b.Cards) == 0 {
		drawEmptyState(dc, fs, l)
	} else {
		for i, card := range b.Cards {
			drawCard(dc, fs, card, l, i)
		}
	}
	drawTruncated(dc, fs, b, l)
	drawNote(dc, fs, b, l)

	var buf bytes.Buffer
	if err := dc.EncodePNG(&buf); err != nil {
		return nil, fmt.Errorf("statusimg: PNG 编码失败: %w", err)
	}
	return buf.Bytes(), nil
}

// boardTexts 列出一次渲染会画到图上的全部文本。
//
// 【为什么要集中列出来】缺字形检测必须覆盖「图上每一个字」，漏掉任何一处
// 都可能让豆腐块溜进客户群。这里刻意包含本包自己补的标签（成功率/缓存率）
// 与空态文案，因为它们同样会被画出来。
func boardTexts(b Board) []string {
	texts := []string{
		b.Title,
		b.Summary,
		b.UpdatedAt,
		b.Note,
		b.Truncated,
		labelSuccess, // 本包补画的标签
		labelCache,
	}
	if len(b.Cards) == 0 {
		texts = append(texts, emptyHint)
	}
	for _, card := range b.Cards {
		texts = append(texts, card.Name, card.Rate, card.Status, card.SuccessText, card.CacheText)
	}
	return texts
}

// missingGlyphs 返回 texts 中在字体 f 里没有字形的 rune，去重后按码位升序。
//
// 【判据】sfnt 的约定是「没有对应字形的字符映射到 glyph index 0」（即 .notdef），
// 所以 GlyphIndex 返回 0（或报错）就是缺字形。
//
// 空白与控制字符跳过：换行/制表符本来就不是「字形缺失」，而且裁剪字体时
// 已经把它们删了，不跳过会把每个多行备注都误报成一片缺字。
func missingGlyphs(f *sfnt.Font, texts []string) []rune {
	if f == nil {
		return nil
	}

	// 每次调用用一个自己的 Buffer：sfnt 的方法只在「各调用方各持 Buffer」时
	// 才并发安全，而 Render 可能被定时推送和手工预览同时调用。
	var buf sfnt.Buffer
	seen := make(map[rune]struct{})
	var missing []rune

	for _, text := range texts {
		for _, r := range text {
			if unicode.IsSpace(r) || unicode.IsControl(r) {
				continue
			}
			if _, ok := seen[r]; ok {
				continue
			}
			seen[r] = struct{}{}

			idx, err := f.GlyphIndex(&buf, r)
			if err != nil || idx == 0 {
				missing = append(missing, r)
			}
		}
	}

	sort.Slice(missing, func(i, j int) bool { return missing[i] < missing[j] })
	return missing
}

// formatMissingGlyphs 把缺字形字符写成「字符(U+码位)」形式。
//
// 带上码位是必须的：像 U+20BB7(𠮷) 这种字符在终端里本身就可能显示成方框，
// 光看字符分辨不出到底缺了谁；emoji 与变体选择符更是肉眼分不清。
func formatMissingGlyphs(missing []rune) string {
	parts := make([]string, 0, len(missing))
	for _, r := range missing {
		parts = append(parts, fmt.Sprintf("%c(U+%04X)", r, r))
	}
	return strings.Join(parts, " ")
}

// warnBoardIssues 发出绘图前的两条告警日志（缺字形、图高越界）。
//
// 两件事都只记日志：不返回错误、不 panic、不影响渲染输出。缺一个字符不应该
// 让整张图发不出去，图太高也不该由渲染层擅自少画几张卡片 —— 但两者都必须
// 留下可检索的痕迹（「哪个分组名带了这个字符」「多少分组会超限」）。
//
// 集中成一个函数是为了能直接测「日志真的发出去了」，不必真去渲染 64 个分组
// 那种又慢又占内存的场景。
func warnBoardIssues(f *sfnt.Font, b Board, l layout) {
	if missing := missingGlyphs(f, boardTexts(b)); len(missing) > 0 {
		log.Printf(
			"statusimg: 字体缺 %d 个字形，这些字符会画成空白或方框（分组名/标题里请去掉它们）: %s",
			len(missing), formatMissingGlyphs(missing),
		)
	}
	if warning := heightWarning(len(b.Cards), l.height); warning != "" {
		log.Printf("statusimg: %s", warning)
	}
}

// heightWarning 在图高超过安全上限时返回告警文案，否则返回空字符串。
//
// 抽成纯函数是为了能直接测「越界这一支」：真去渲染 100 个分组要走完整条
// 绘制路径，测试又慢又占内存，而这个判断本身只跟数字有关。
//
// 文案区分「刚越过安全上限」与「连 Telegram 硬上限都超了」两种程度：安全上限
// 留了 5% 余量，所以刚越线时宽+高可能还没到 10000。把 9565 说成「超过 10000」
// 会让运营拿数字去核对时对不上，之后就不再相信这条告警了。
func heightWarning(cardCount int, height float64) string {
	if height <= maxImageHeight {
		return ""
	}

	sum := height + float64(outW)
	consequence := fmt.Sprintf(
		"宽+高 %.0f 尚未超过硬上限 %d，但余量已不足", sum, telegramMaxPhotoSum)
	if sum > float64(telegramMaxPhotoSum) {
		consequence = fmt.Sprintf(
			"宽+高 %.0f 已超过硬上限 %d，Telegram 会拒收", sum, telegramMaxPhotoSum)
	}

	return fmt.Sprintf(
		"图高 %.0fpx 超过本包安全上限 %dpx（%d 个分组，宽 %dpx）：%s。"+
			"渲染层不裁剪卡片，请在 service 层减少分组或分批推送。",
		height, maxImageHeight, cardCount, outW, consequence,
	)
}

// layout 是一次渲染算好的几何量（单位：输出像素）。
type layout struct {
	width   float64
	height  float64
	header  float64 // 头部高度
	gridTop float64 // 卡片区顶部
	cardW   float64
	cardH   float64
	noteTop float64 // 备注行顶部；noteH 为 0 时不画
	noteH   float64
	// 卡片被裁剪时的说明行（整幅宽）。与备注各自独立占行。
	warnTop float64
	warnH   float64
}

// computeLayout 先算总高再画 —— gg 必须在 NewContext 时就知道画布尺寸。
func computeLayout(b Board) layout {
	// 名字按「显示宽度」截断而不是按字符数，卡片宽度才是真正的约束。
	cardW := s(logicalW-2*pad-colGap) / colCount

	rows := (len(b.Cards) + colCount - 1) / colCount

	gridH := s(emptyH) // 没有卡片时卡片区就是一块占位框
	if rows > 0 {
		gridH = float64(rows)*s(cardH) + float64(rows-1)*s(colGap)
	}

	noteH := 0.0
	if b.Note != "" {
		noteH = s(noteGap + metaLineH)
	}
	warnH := 0.0
	if b.Truncated != "" {
		warnH = s(noteGap + metaLineH)
	}

	l := layout{
		width:   s(logicalW),
		header:  s(headerH),
		gridTop: s(pad + headerH + headerGap),
		cardW:   cardW,
		cardH:   s(cardH),
		noteH:   noteH,
		warnH:   warnH,
	}
	l.warnTop = l.gridTop + gridH + s(noteGap)
	// 裁剪说明在口径备注之前：它解释的是「图上的卡片不全」，比口径更该先被看到。
	l.noteTop = l.warnTop + warnH
	l.height = l.gridTop + gridH + warnH + noteH + s(pad)
	return l
}

// drawTruncated 画「卡片被裁剪」的说明行（整幅宽，空字符串不画）。
//
// 用比备注更深的颜色：这条不是口径脚注，而是「你看到的不是全部」的提示。
func drawTruncated(dc *gg.Context, fs *faces, b Board, l layout) {
	if b.Truncated == "" {
		return
	}
	dc.SetFontFace(fs.meta)
	dc.SetColor(colorMuted)
	dc.DrawStringAnchored(
		truncateToWidth(fs.meta, b.Truncated, l.width-s(2*pad)),
		s(pad), bandBaseline(fs.meta, l.warnTop, s(metaLineH)), 0, 0,
	)
}

// drawHeader 画左上大标题与右侧两行摘要。
//
// 标题与摘要按画布中线分工，各自截断，因此无论标题多长都不会压到摘要上。
func drawHeader(dc *gg.Context, fs *faces, b Board, l layout) {
	halfW := s(logicalW / 2)

	dc.SetFontFace(fs.title)
	dc.SetColor(colorTitle)
	title := truncateToWidth(fs.title, b.Title, halfW-s(pad+headerSideGap))
	dc.DrawStringAnchored(title, s(pad), bandBaseline(fs.title, s(pad), l.header), 0, 0)

	right := s(logicalW - pad)
	lineH := l.header / 2

	dc.SetFontFace(fs.summary)
	dc.SetColor(colorMuted)
	summary := truncateToWidth(fs.summary, b.Summary, halfW-s(headerSideGap))
	dc.DrawStringAnchored(summary, right, bandBaseline(fs.summary, s(pad), lineH), 1, 0)

	// UpdatedAt 为空时第二行整个不画（文档约定）。
	if b.UpdatedAt != "" {
		dc.SetFontFace(fs.updated)
		dc.SetColor(colorFaint)
		updated := truncateToWidth(fs.updated, b.UpdatedAt, halfW-s(headerSideGap))
		dc.DrawStringAnchored(updated, right, bandBaseline(fs.updated, s(pad)+lineH, lineH), 1, 0)
	}
}

// drawCard 画一张卡片：白底圆角，内含名称/倍率/状态/8 色块/底部两个比率。
func drawCard(dc *gg.Context, fs *faces, card Card, l layout, idx int) {
	x := s(pad) + float64(idx%colCount)*(l.cardW+s(colGap))
	y := l.gridTop + float64(idx/colCount)*(l.cardH+s(colGap))

	drawPanel(dc, x, y, l.cardW, l.cardH)

	contentX := x + s(cardPad)
	contentW := l.cardW - s(2*cardPad)

	nameTop := y + s(cardPad)
	rateTop := nameTop + s(nameLineH)
	blocksTop := rateTop + s(rateLineH) + s(blockTopGap)
	footerTop := blocksTop + s(blockH) + s(footerGap)

	// 名称先按「卡片宽度 - 状态占位」截断，保证省略号出现在状态左侧而不是叠上去。
	nameMax := contentW
	if card.Status != "" {
		nameMax -= measureWidth(fs.status, card.Status) + s(statusDotD+statusDotGap+nameStatusGap)
	}
	dc.SetFontFace(fs.name)
	dc.SetColor(colorTitle)
	dc.DrawStringAnchored(
		truncateToWidth(fs.name, card.Name, nameMax),
		contentX, bandBaseline(fs.name, nameTop, s(nameLineH)), 0, 0,
	)

	if card.Status != "" {
		drawStatus(dc, fs, card, x, nameTop, contentW)
	}

	dc.SetFontFace(fs.rate)
	dc.SetColor(colorMuted)
	dc.DrawStringAnchored(
		truncateToWidth(fs.rate, card.Rate, contentW),
		contentX, bandBaseline(fs.rate, rateTop, s(rateLineH)), 0, 0,
	)

	drawBlocks(dc, card, contentX, contentW, blocksTop)

	base := bandBaseline(fs.meta, footerTop, s(metaLineH))
	drawLabelValue(dc, fs.meta, contentX, base, labelSuccess, card.SuccessText, false)
	drawLabelValue(dc, fs.meta, contentX+contentW, base, labelCache, card.CacheText, true)
}

// drawStatus 画右上角状态：一个状态色圆点 + 同色文字。
//
// 白底上只用浅灰文字会看不清「无流量」这类弱状态，加一个圆点既补了对比度，
// 也让状态在两张相邻卡片间可以一眼扫过。
func drawStatus(dc *gg.Context, fs *faces, card Card, cardX, nameTop, contentW float64) {
	base := bandBaseline(fs.status, nameTop, s(nameLineH))
	textX := cardX + s(cardPad) + contentW - measureWidth(fs.status, card.Status)
	centerY := nameTop + s(nameLineH)/2
	dot := statusColor(card.StatusLevel)

	dc.SetColor(dot)
	dc.DrawCircle(textX-s(statusDotGap)-s(statusDotD)/2, centerY, s(statusDotD)/2)
	dc.Fill()

	// 显式再设一次文字色：不依赖「Fill 之后当前色还是圆点色」这种隐式状态，
	// 以后在圆点和文字之间插一行绘制也不会让状态文字串色。
	dc.SetFontFace(fs.status)
	dc.SetColor(dot)
	dc.DrawStringAnchored(card.Status, cardX+s(cardPad)+contentW, base, 1, 0)
}

// drawBlocks 画 8 个时间块（从左到右由旧到新）。
//
// 只画 Board 真的给了的块：缺项留空而不是补灰块 —— 补灰等于替 service 层
// 断言「该时间块无数据」，那是业务判断，不该由渲染层做。
func drawBlocks(dc *gg.Context, card Card, contentX, contentW, top float64) {
	blockW := (contentW - s(float64(blocksPerCard-1)*blockGap)) / blocksPerCard
	for i, blk := range card.Blocks {
		if i >= blocksPerCard {
			break
		}
		dc.SetColor(blockColor(blk.Score, blk.HasData))
		dc.DrawRoundedRectangle(contentX+float64(i)*(blockW+s(blockGap)), top, blockW, s(blockH), s(blockRadius))
		dc.Fill()
	}
}

// drawLabelValue 画「标签 + 数值」，right 为 true 时整组右对齐到 x。
//
// 标签（成功率/缓存率）由渲染层补上：Board 里的 SuccessText 只是「99.2%」这种
// 纯数值，光看数字分不清是成功率还是缓存率。
func drawLabelValue(dc *gg.Context, face font.Face, x, baseline float64, label, value string, right bool) {
	dc.SetFontFace(face)
	labelW := measureWidth(face, label)
	gap := s(labelValueGap)
	if right {
		x -= labelW + gap + measureWidth(face, value)
	}

	dc.SetColor(colorFaint)
	dc.DrawStringAnchored(label, x, baseline, 0, 0)
	dc.SetColor(colorMuted)
	dc.DrawStringAnchored(value, x+labelW+gap, baseline, 0, 0)
}

// drawEmptyState 在没有任何卡片时画一个占位框。
func drawEmptyState(dc *gg.Context, fs *faces, l layout) {
	boxW := l.width - s(2*pad)
	drawPanel(dc, s(pad), l.gridTop, boxW, s(emptyH))

	dc.SetFontFace(fs.summary)
	dc.SetColor(colorFaint)
	dc.DrawStringAnchored(
		emptyHint,
		s(pad)+boxW/2, bandBaseline(fs.summary, l.gridTop, s(emptyH)), 0.5, 0,
	)
}

// drawNote 画底部口径备注（空字符串则不画）。
func drawNote(dc *gg.Context, fs *faces, b Board, l layout) {
	if b.Note == "" {
		return
	}
	dc.SetFontFace(fs.meta)
	dc.SetColor(colorFaint)
	dc.DrawStringAnchored(
		truncateToWidth(fs.meta, b.Note, l.width-s(2*pad)),
		s(pad), bandBaseline(fs.meta, l.noteTop, s(metaLineH)), 0, 0,
	)
}

// drawPanel 画白底圆角面板：先填充再描一圈浅边。
//
// 浅灰底上的纯白卡片边界很弱，1px 描边是让卡片「站」起来的必要成本。
func drawPanel(dc *gg.Context, x, y, w, h float64) {
	dc.SetColor(colorCard)
	dc.DrawRoundedRectangle(x, y, w, h, s(cardRadius))
	dc.Fill()

	dc.SetColor(colorCardEdge)
	dc.SetLineWidth(1)
	// 半像素内缩：描边落在像素中心，2× 下边缘才不会是糊的一圈。
	dc.DrawRoundedRectangle(x+0.5, y+0.5, w-1, h-1, s(cardRadius))
	dc.Stroke()
}

// bandBaseline 给出「文字视觉垂直居中于 [top, top+h)」所需的基线 y。
//
// gg 的 DrawString 把 y 当基线，而布局是自上而下按行高排的，这里统一换算，
// 免得每个调用点自己凑 ascent/descent。Metrics 是 26.6 定点数，除 64 换成像素。
func bandBaseline(face font.Face, top, h float64) float64 {
	m := face.Metrics()
	ascent := float64(m.Ascent) / 64
	descent := float64(m.Descent) / 64
	return top + (h+ascent-descent)/2
}

// blockColor 把健康分映射到八档色板。
//
// 【为什么要单独一个函数】色板边界属于「口径」，service 层只传 Score/HasData，
// 不知道颜色；边界一旦写错（比如写成 > 而不是 >=）肉眼很难发现，所以抽出来
// 由表格测试钉死每一个边界值。
func blockColor(score int, hasData bool) color.RGBA {
	if !hasData {
		// 样本不足与无流量都画同一个灰：区分它们需要业务信息，而本包没有。
		return color.RGBA{0xd1, 0xd5, 0xdb, 0xff}
	}
	switch {
	case score >= 98:
		return color.RGBA{0x15, 0x80, 0x3d, 0xff}
	case score >= 90:
		return color.RGBA{0x22, 0xc5, 0x5e, 0xff}
	case score >= 80:
		return color.RGBA{0x4a, 0xde, 0x80, 0xff}
	case score >= 70:
		return color.RGBA{0xa3, 0xe6, 0x35, 0xff}
	case score >= 60:
		return color.RGBA{0xfa, 0xcc, 0x15, 0xff}
	case score >= 50:
		return color.RGBA{0xfb, 0x92, 0x3c, 0xff}
	case score >= 30:
		return color.RGBA{0xf4, 0x72, 0xb6, 0xff}
	default:
		return color.RGBA{0xdc, 0x26, 0x26, 0xff}
	}
}

// statusColor 把状态档位映射成颜色。
//
// StatusLevel 是约定好的四个值；出现别的值（比如 service 以后新增档位）时
// 按最弱的灰处理，宁可看起来「没有状态」，也不要画出一个误导性的颜色。
// none 用 gray-400 而不是色块缺数据的 gray-300：它是白底上的文字，需要可读性。
func statusColor(level string) color.RGBA {
	switch level {
	case "ok":
		return color.RGBA{0x22, 0xc5, 0x5e, 0xff}
	case "warn":
		return color.RGBA{0xfb, 0x92, 0x3c, 0xff}
	case "bad":
		return color.RGBA{0xdc, 0x26, 0x26, 0xff}
	default:
		return color.RGBA{0x9c, 0xa3, 0xaf, 0xff}
	}
}

// measureWidth 返回文字渲染后的宽度（像素，向下取整）。
//
// 与 gg 的 MeasureString 用同一套算法（font.Drawer.MeasureString >> 6），
// 这样「量出来的宽度」和「画出来的宽度」在同一口径上，右对齐才真的贴边。
func measureWidth(face font.Face, text string) float64 {
	if face == nil || text == "" {
		return 0
	}
	d := &font.Drawer{Face: face}
	return float64(d.MeasureString(text) >> 6)
}

// truncateToWidth 按显示宽度截断文字，超长部分换成省略号。
//
// 不能按字符数截断：中文是 ASCII 的两倍宽，同样 10 个字符能差出一倍。
// 逐字累加字形步进，因此中英混排、全角标点都能正确处理。
// 缺字时 GlyphAdvance 返回 0 且 ok=false —— 与 gg 绘制时跳过该字形、
// 步进为 0 的行为一致，所以量出来的宽度仍然可信。
func truncateToWidth(face font.Face, text string, maxWidth float64) string {
	if face == nil || maxWidth <= 0 {
		return ""
	}
	d := &font.Drawer{Face: face}
	if float64(d.MeasureString(text)>>6) <= maxWidth {
		return text
	}

	const ellipsis = "…"
	ellipsisW := d.MeasureString(ellipsis)
	limit := fixed.Int26_6(maxWidth * 64)

	kept := make([]rune, 0, len(text))
	var width fixed.Int26_6
	for _, r := range text {
		advance, _ := face.GlyphAdvance(r)
		if width+advance+ellipsisW > limit {
			break
		}
		kept = append(kept, r)
		width += advance
	}
	return string(kept) + ellipsis
}
