package statusimg

import (
	"bytes"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode"

	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
)

// update 与 `go test -update` 联动：把样图写到 testdata/ 供人工肉眼核对。
// 用它而不是让测试每次都写文件，是为了让 CI 里的测试保持只读。
var update = flag.Bool("update", false, "更新 testdata 下的样图（board_0/11/40.png）")

// 样图文件名：0 张（占位）、11 张（日常全貌）、40 张（压力与 Telegram 上限）。
var sampleCounts = []int{0, 11, 40}

// sampleBoard 造一张有代表性的看板：
//   - 覆盖全部 8 档色块（保证样图能一眼验色板）
//   - 掺入 HasData=false 的灰块
//   - 覆盖四种状态档位
//   - 第一张卡片故意给一个超长中文名，用来验省略号截断
func sampleBoard(n int) Board {
	b := Board{
		Title:     "渠道状态",
		Summary:   fmt.Sprintf("%d 个渠道 · 比率近90分钟", n),
		UpdatedAt: "更新 10-12 15:30",
		Note:      "口径：健康分由成功率、延迟与缓存率加权；样本不足的时间块记灰。",
	}

	// 8 个分值各落一档，顺序打乱后按 i 平移，让每张卡片的色块都不一样。
	tiers := []int{99, 95, 85, 75, 65, 55, 40, 20}
	// 状态文案与档位必须成对给：错配的样图会让人误判渲染层配错了色。
	statuses := []struct{ text, level string }{
		{"正常", "ok"},
		{"延迟", "warn"},
		{"异常", "bad"},
		{"无流量", "none"},
	}
	names := []string{
		"超长分组名称测试用于验证省略号截断是否正确工作ABCDEFG",
		"Claude 官方",
		"Claude 逆向",
		"GPT-4o 池",
		"Gemini 池",
		"DeepSeek 池",
		"自建中转",
		"备用渠道",
	}
	rates := []string{"0.08x", "0.5x", "1x", "1.2x", "2.5x", "0.03x"}

	for i := 0; i < n; i++ {
		blocks := make([]Block, blocksPerCard)
		for j := range blocks {
			blocks[j] = Block{
				Score: tiers[(i*3+j)%len(tiers)],
				// 每隔 7 块造一个「样本不足」，让灰块也出现在样图里。
				HasData: (i+j)%7 != 3,
			}
		}
		st := statuses[i%len(statuses)]
		b.Cards = append(b.Cards, Card{
			Name:        names[i%len(names)],
			Rate:        rates[i%len(rates)],
			Status:      st.text,
			StatusLevel: st.level,
			Blocks:      blocks,
			SuccessText: fmt.Sprintf("%.1f%%", 80+float64(i%18)),
			CacheText:   fmt.Sprintf("%.1f%%", 30+float64(i%60)),
		})
	}
	return b
}

func decodePNG(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("png.Decode 失败: %v", err)
	}
	return img
}

// TestRenderSizes 是任务的核心约束：宽固定 1280，高度随卡片数单调递增，
// 且 40 张卡片时仍在 Telegram 的图片尺寸限制内。
func TestRenderSizes(t *testing.T) {
	prevH := 0
	for _, n := range []int{0, 1, 11, 40} {
		data, err := Render(sampleBoard(n))
		if err != nil {
			t.Fatalf("Render(%d 张卡片) 失败: %v", n, err)
		}
		img := decodePNG(t, data)

		if got, want := img.Bounds().Dx(), outW; got != want {
			t.Errorf("%d 张卡片：宽 = %d, 期望 %d", n, got, want)
		}
		h := img.Bounds().Dy()
		if h <= prevH {
			t.Errorf("%d 张卡片：高 = %d, 未超过上一档的 %d（应单调递增）", n, h, prevH)
		}
		prevH = h

		if n == 40 {
			// Telegram 图片：单边上限 10000px，宽高和上限 10000；需求还要求
			// 宽高比不超过 20:1（否则在聊天里会缩成一条细缝）。
			if ratio := float64(h) / float64(outW); ratio > 20 {
				t.Errorf("40 张卡片宽高比 = %.1f:1, 超过 20:1", ratio)
			}
			if outW+h > telegramMaxPhotoSum {
				t.Errorf("40 张卡片尺寸 %dx%d 超过 Telegram 的 %d 像素限制", outW, h, telegramMaxPhotoSum)
			}
			// 关键回归：40 张是当前生产规模的上限，必须在安全上限内，并且还留得下
			// 若干行的余量。要求「留余量」而不是「勉强不越界」：一旦某次改字号/
			// 行高把高度推上去，这条断言会先失败，而不是等线上被 Telegram 拒收。
			if float64(h) > maxImageHeight {
				t.Errorf("40 张卡片高 %d 已超过安全上限 %d", h, maxImageHeight)
			}
			if rows := headroomRows(float64(h)); rows < minHeadroomRows {
				t.Errorf("40 张卡片高 %d 距安全上限 %d 只剩 %d 行余量（要求 ≥%d 行）",
					h, maxImageHeight, rows, minHeadroomRows)
			}
			// 同一条规则的两个入口必须一致：40 张不该触发任何高度告警。
			if w := heightWarning(n, float64(h)); w != "" {
				t.Errorf("40 张卡片不应触发高度告警，却得到: %s", w)
			}
		}
	}
}

// TestRenderPixels 抽查像素：底色、卡片白底、以及色块颜色真的按色板落到了图上。
func TestRenderPixels(t *testing.T) {
	board := sampleBoard(1)
	data, err := Render(board)
	if err != nil {
		t.Fatalf("Render 失败: %v", err)
	}
	img := decodePNG(t, data)
	l := computeLayout(board)

	at := func(x, y float64) color.RGBA {
		t.Helper()
		return color.RGBAModel.Convert(img.At(int(x), int(y))).(color.RGBA)
	}

	// 左上角 2px 处必须是浅灰底。
	if got, want := at(2, 2), colorBackground; got != want {
		t.Errorf("背景色 = %v, 期望 %v", got, want)
	}

	// 卡片底部（底部内边距区间）必须是纯白 —— 那里不会有文字。
	cardX, cardY := s(pad), l.gridTop
	if got, want := at(cardX+l.cardW/2, cardY+l.cardH-s(6)), colorCard; got != want {
		t.Errorf("卡片白底 = %v, 期望 %v", got, want)
	}

	// 第一个色块的中心必须等于 blockColor 算出的颜色。
	contentX := cardX + s(cardPad)
	contentW := l.cardW - s(2*cardPad)
	blockW := (contentW - s(float64(blocksPerCard-1)*blockGap)) / blocksPerCard
	blocksTop := cardY + s(cardPad+nameLineH+rateLineH+blockTopGap)
	blk := board.Cards[0].Blocks[0]
	want := blockColor(blk.Score, blk.HasData)
	got := at(contentX+blockW/2, blocksTop+s(blockH)/2)
	if got != want {
		t.Errorf("首色块颜色 = %v, 期望 %v (score=%d hasData=%v)", got, want, blk.Score, blk.HasData)
	}
}

// TestBlockColorPalette 把八档色板的边界钉死。
//
// 每个档位都同时测「下边界」与「上边界」两个值：只测档位内任一点的话，
// 把 >= 写成 > 也能通过。HasData=false 必须忽略 score 一律画灰。
func TestBlockColorPalette(t *testing.T) {
	gray := color.RGBA{0xd1, 0xd5, 0xdb, 0xff}
	cases := []struct {
		score int
		want  color.RGBA
	}{
		{100, color.RGBA{0x15, 0x80, 0x3d, 0xff}}, // 深绿
		{98, color.RGBA{0x15, 0x80, 0x3d, 0xff}},  // 深绿下边界
		{97, color.RGBA{0x22, 0xc5, 0x5e, 0xff}},  // 绿上边界
		{90, color.RGBA{0x22, 0xc5, 0x5e, 0xff}},  // 绿下边界
		{89, color.RGBA{0x4a, 0xde, 0x80, 0xff}},  // 浅绿上边界
		{80, color.RGBA{0x4a, 0xde, 0x80, 0xff}},  // 浅绿下边界
		{79, color.RGBA{0xa3, 0xe6, 0x35, 0xff}},  // 黄绿上边界
		{70, color.RGBA{0xa3, 0xe6, 0x35, 0xff}},  // 黄绿下边界
		{69, color.RGBA{0xfa, 0xcc, 0x15, 0xff}},  // 黄上边界
		{60, color.RGBA{0xfa, 0xcc, 0x15, 0xff}},  // 黄下边界
		{59, color.RGBA{0xfb, 0x92, 0x3c, 0xff}},  // 橙上边界
		{50, color.RGBA{0xfb, 0x92, 0x3c, 0xff}},  // 橙下边界
		{49, color.RGBA{0xf4, 0x72, 0xb6, 0xff}},  // 粉上边界
		{30, color.RGBA{0xf4, 0x72, 0xb6, 0xff}},  // 粉下边界
		{29, color.RGBA{0xdc, 0x26, 0x26, 0xff}},  // 红上边界
		{0, color.RGBA{0xdc, 0x26, 0x26, 0xff}},   // 红
	}
	for _, tc := range cases {
		if got := blockColor(tc.score, true); got != tc.want {
			t.Errorf("blockColor(%d, true) = %v, 期望 %v", tc.score, got, tc.want)
		}
	}
	for _, score := range []int{0, 29, 50, 100} {
		if got := blockColor(score, false); got != gray {
			t.Errorf("blockColor(%d, false) = %v, 期望灰 %v", score, got, gray)
		}
	}
}

// TestStatusColor 钉住四个状态档位；未知档位必须退化成灰而不是某个误导性的颜色。
func TestStatusColor(t *testing.T) {
	cases := map[string]color.RGBA{
		"ok":   {0x22, 0xc5, 0x5e, 0xff},
		"warn": {0xfb, 0x92, 0x3c, 0xff},
		"bad":  {0xdc, 0x26, 0x26, 0xff},
		"none": {0x9c, 0xa3, 0xaf, 0xff},
		"":     {0x9c, 0xa3, 0xaf, 0xff},
		"???":  {0x9c, 0xa3, 0xaf, 0xff},
	}
	for level, want := range cases {
		if got := statusColor(level); got != want {
			t.Errorf("statusColor(%q) = %v, 期望 %v", level, got, want)
		}
	}
}

// serviceTexts 是 service 层真实会放进 Board、从而会被画到图上的文案。
//
// 【为什么在这硬编码而不是 import service】本包刻意保持「零业务依赖」（见
// board.go 的包注释），import service 会让渲染包反向依赖业务层、并可能在
// 测试里拉起数据库配置。所以把文案抄一份钉在这里：service 改了文案而这个
// 用例没跟上，只是少测了一个字符串，不会让编译或运行出错；而一旦新文案用了
// 字体没有的字，只要有人同步到这里就会被抓住。
//
// 值取自 service/status_board.go 与 status_push.go：
//   - 大标题 defaultStatusPushTitle
//   - 摘要 fmt.Sprintf("%d 个渠道 · 比率近%d分钟", ...)
//   - 更新时间 "更新 " + now.Format("01-02 15:04")
//   - 状态 currentStatus 的四个返回值
//   - 比率 percentText 的两种形态
//   - 倍率 fmt.Sprintf("%.2fx", ...)
//   - DefaultIgnoredStatusCodes = 400/403/429/529
var serviceTexts = []struct {
	name string
	text string
}{
	{"大标题", "渠道状态"},
	{"摘要形态", "22 个渠道 · 比率近90分钟"},
	{"更新时间", "更新 10-12 15:30"},
	{"状态-正常", "正常"},
	{"状态-延迟", "延迟"},
	{"状态-异常", "异常"},
	{"状态-无流量", "无流量"},
	{"卡内标签", "成功率" + labelCache + "倍率"},
	{"比率-有样本", "99.2%"},
	{"比率-无样本", "-"},
	{"倍率形态", "0.08x"},
	{"空态文案", emptyHint},
	{"截断省略号", "超长分组名称…"},
	{"忽略状态码", "400/403/429/529"},
	{"符号", "%/·×"},
}

// TestEmbeddedFontHasChineseGlyphs 直接验内嵌字体本身：
// 只要字体被换成拉丁字体（或嵌入路径失效退回 basicfont），这里就会失败 ——
// 这是「没有中文字体就视为任务未完成」这条验收的自动化防线。
//
// 用 service 层真实文案做表驱动（见 serviceTexts）：固定一小串字符只能证明
// 「这几个字有」，覆盖不到「换个分组名就缺字」。
func TestEmbeddedFontHasChineseGlyphs(t *testing.T) {
	f, err := opentype.Parse(embeddedFontData)
	if err != nil {
		t.Fatalf("内嵌字体解析失败: %v", err)
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: 16, DPI: 72})
	if err != nil {
		t.Fatalf("创建 face 失败: %v", err)
	}

	for _, tc := range serviceTexts {
		t.Run(tc.name, func(t *testing.T) {
			for _, r := range tc.text {
				if unicode.IsSpace(r) {
					continue
				}
				advance, ok := face.GlyphAdvance(r)
				if !ok || advance <= 0 {
					t.Errorf("内嵌字体缺字形 %q (U+%04X)，文案 %q 会画成豆腐块: ok=%v advance=%d",
						r, r, tc.text, ok, advance)
				}
			}
		})
	}

	// 嵌入的必须是裁剪后的子集（8203 字形 / 1.6MB），不是 8MB 的完整字体。
	if n := f.NumGlyphs(); n > 15000 {
		t.Errorf("内嵌字体有 %d 个字形，像是完整字体而非 GB2312 子集", n)
	}
	if len(embeddedFontData) > 4<<20 {
		t.Errorf("内嵌字体 %d 字节，超过子集字体应有的体积", len(embeddedFontData))
	}
}

// TestMissingGlyphs 验证缺字形检测本身（L1 的核心）。
//
// 内嵌子集是「GB2312 + ASCII + 常用标点」，所以 emoji、CJK 扩展 B（𠮷）
// 和拉丁扩展（ç）都真的不在里面 —— 这几个字符就是审查者实测缺失的那批，
// 用它们当样例可以证明检测真的在读字形表，而不是恒返回空。
func TestMissingGlyphs(t *testing.T) {
	f, err := loadFont()
	if err != nil {
		t.Fatalf("loadFont 失败: %v", err)
	}

	t.Run("缺字形必须被检出", func(t *testing.T) {
		got := missingGlyphs(f, []string{"渠道状态🚀", "𠮷", "façade"})
		want := []rune{'ç', '🚀', '𠮷'}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("缺字形 = %v, 期望 %v", got, want)
		}
	})

	t.Run("齐全的文案返回空", func(t *testing.T) {
		if got := missingGlyphs(f, boardTexts(sampleBoard(3))); len(got) != 0 {
			t.Errorf("样图文案不该缺字形，却检出 %s", formatMissingGlyphs(got))
		}
	})

	t.Run("去重且按码位升序", func(t *testing.T) {
		got := missingGlyphs(f, []string{"🚀🚀ç", "ç🚀"})
		if !reflect.DeepEqual(got, []rune{'ç', '🚀'}) {
			t.Errorf("缺字形 = %v, 期望去重升序 [ç 🚀]", got)
		}
	})

	t.Run("空白与控制字符不算缺字形", func(t *testing.T) {
		// 裁剪字体时删掉了 \n \t \r，若不跳过会把多行备注整段误报。
		if got := missingGlyphs(f, []string{"第一行\n\t第二行\r\n  渠道状态"}); len(got) != 0 {
			t.Errorf("空白不该算缺字形，却检出 %s", formatMissingGlyphs(got))
		}
	})

	t.Run("nil 字体不 panic", func(t *testing.T) {
		if got := missingGlyphs(nil, []string{"任意"}); got != nil {
			t.Errorf("nil 字体应返回 nil, 得到 %v", got)
		}
	})
}

// TestRenderWithMissingGlyphs 验证缺字形的 Board 仍然能出图：
// 不得报错、不得 panic、PNG 必须能解码（L1 要求「只告警不改行为」）。
func TestRenderWithMissingGlyphs(t *testing.T) {
	board := sampleBoard(2)
	board.Title = "渠道状态 🚀"
	board.Note = "备注里带 ç 与 𠮷"
	board.Cards[0].Name = "ç 分组🚀"

	data, err := Render(board)
	if err != nil {
		t.Fatalf("缺字形时 Render 不应报错, 得到: %v", err)
	}
	if img := decodePNG(t, data); img.Bounds().Dx() != outW {
		t.Errorf("宽 = %d, 期望 %d", img.Bounds().Dx(), outW)
	}

	// 检测函数要覆盖到 Title / Note / Card.Name 三处（含本包补画的标签）。
	missing := missingGlyphs(mustLoadFont(t), boardTexts(board))
	for _, want := range []rune{'ç', '🚀', '𠮷'} {
		if !slices.Contains(missing, want) {
			t.Errorf("缺字形集合 %v 未包含 %q", missing, want)
		}
	}
}

// TestHeightWarning 钉住高度上限这一支的判定。
//
// 不去真的渲染 100 个分组：那要走完整条绘制路径，慢且占内存，
// 而「超没超上限」只跟一个数字有关。
func TestHeightWarning(t *testing.T) {
	// 40 张卡片（当前生产上限）必须不告警，且要有像样的余量。
	forty := computeLayout(sampleBoard(40))
	if w := heightWarning(40, forty.height); w != "" {
		t.Errorf("40 个分组不应告警, 得到: %s", w)
	}
	if rows := headroomRows(forty.height); rows < minHeadroomRows {
		t.Errorf("40 个分组高 %.0f 距上限 %d 只剩 %d 行余量（要求 ≥%d 行）",
			forty.height, maxImageHeight, rows, minHeadroomRows)
	}

	// 上限之内不告警；越界才告警（边界值本身算超）。
	if w := heightWarning(1, float64(maxImageHeight)); w != "" {
		t.Errorf("恰好等于上限不应告警, 得到: %s", w)
	}
	if w := heightWarning(1, float64(maxImageHeight)+1); w == "" {
		t.Error("超过上限 1px 必须告警")
	}

	// 用真实布局推到越界那一支：不断加卡片直到高度超过上限。
	over := 0
	for i := 1; i <= 200; i++ {
		if computeLayout(sampleBoard(i)).height > maxImageHeight {
			over = i
			break
		}
	}
	if over == 0 {
		t.Fatal("加到 200 个分组仍未超过安全上限，说明上限或布局常量不对")
	}
	w := heightWarning(over, float64(computeLayout(sampleBoard(over)).height))
	for _, want := range []string{"超过本包安全上限", "不裁剪卡片"} {
		if !strings.Contains(w, want) {
			t.Errorf("告警文案 %q 未包含 %q", w, want)
		}
	}
	t.Logf("在 %d 个分组处越过安全上限 %dpx", over, maxImageHeight)

	// 刚越过安全上限时宽+高还没到硬上限：文案必须如实说「余量不足」，
	// 不能声称已超过 10000（否则运营拿数字核对会对不上）。
	justOver := heightWarning(over, float64(maxImageHeight+1))
	if !strings.Contains(justOver, "尚未超过硬上限") {
		t.Errorf("刚越线时不应声称超过硬上限: %q", justOver)
	}
	if strings.Contains(justOver, "会拒收") {
		t.Errorf("刚越线（宽+高 %.0f < 10000）不应出现「会拒收」: %q", float64(maxImageHeight+1)+float64(outW), justOver)
	}

	// 连硬上限都超了的那一档必须明确说会被拒收。
	overHard := heightWarning(200, float64(telegramMaxPhotoSum-outW+1))
	if !strings.Contains(overHard, "会拒收") {
		t.Errorf("超过硬上限的文案必须说明会被拒收: %q", overHard)
	}

	// 30 个分组（60 行 → 15 行）应当仍在限内，确认上限不是过紧。
	if h := computeLayout(sampleBoard(30)).height; h > maxImageHeight {
		t.Errorf("30 个分组高 %.0f 已超上限 %d，上限过紧", h, maxImageHeight)
	}
}

// TestWarningsAreLogged 验证两条告警**真的写进了日志**（而不只是纯函数返回了值）。
//
// 审查发现的问题是「文档承诺检测+告警，代码里一个 log 都没有」，所以只测
// missingGlyphs 的返回值还不够：必须证明 Render 这条路径上确实会 log。
func TestWarningsAreLogged(t *testing.T) {
	f := mustLoadFont(t)

	captureLog := func(fn func()) string {
		var buf bytes.Buffer
		prevOut := log.Writer()
		prevFlags := log.Flags()
		log.SetOutput(&buf)
		// 去掉时间前缀，断言只看内容。
		log.SetFlags(0)
		defer func() {
			log.SetOutput(prevOut)
			log.SetFlags(prevFlags)
		}()
		fn()
		return buf.String()
	}

	t.Run("缺字形告警", func(t *testing.T) {
		board := sampleBoard(1)
		board.Cards[0].Name = "ç 分组🚀𠮷"
		out := captureLog(func() { warnBoardIssues(f, board, computeLayout(board)) })

		for _, want := range []string{"缺", "字形", "U+00E7", "U+1F680", "U+20BB7"} {
			if !strings.Contains(out, want) {
				t.Errorf("缺字形告警未包含 %q，实际输出: %q", want, out)
			}
		}
		t.Logf("告警原文: %s", strings.TrimSpace(out))
	})

	t.Run("图高越界告警", func(t *testing.T) {
		// 直接构造一个「连硬上限都超了」的布局，避免真渲染 64 个分组（慢且占内存）。
		over := layout{width: s(logicalW), height: float64(telegramMaxPhotoSum - outW + 1)}
		out := captureLog(func() { warnBoardIssues(f, sampleBoard(64), over) })

		for _, want := range []string{"超过本包安全上限", "拒收", "不裁剪卡片"} {
			if !strings.Contains(out, want) {
				t.Errorf("高度告警未包含 %q，实际输出: %q", want, out)
			}
		}
		t.Logf("告警原文: %s", strings.TrimSpace(out))
	})

	t.Run("正常看板不产生任何告警", func(t *testing.T) {
		board := sampleBoard(11)
		out := captureLog(func() {
			if _, err := Render(board); err != nil {
				t.Fatalf("Render 失败: %v", err)
			}
		})
		if out != "" {
			t.Errorf("正常看板不该有任何告警日志，却输出: %q", out)
		}
	})
}

func mustLoadFont(t *testing.T) *sfnt.Font {
	t.Helper()
	f, err := loadFont()
	if err != nil {
		t.Fatalf("loadFont 失败: %v", err)
	}
	return f
}

// minHeadroomRows 是 40 个分组（当前生产规模上限）必须留出的余量，单位「卡片行」。
//
// 取 5 行（约 10 个分组）：既不会因为正常微调就误报，又能在字号/行高被改动
// 到接近上限时提前失败。
const minHeadroomRows = 5

// headroomRows 返回在安全上限之下还能再加多少行卡片。
func headroomRows(height float64) int {
	return int((maxImageHeight - height) / (s(cardH) + s(colGap)))
}

// mustFaces 给需要 face 的用例取一套字面。
func mustFaces(t *testing.T) *faces {
	t.Helper()
	fs, err := newFaces(mustLoadFont(t))
	if err != nil {
		t.Fatalf("newFaces 失败: %v", err)
	}
	return fs
}

// TestTruncateToWidth 验证按显示宽度截断：中文两倍宽，不能按字符数算。
func TestTruncateToWidth(t *testing.T) {
	face := mustFaces(t).name

	if got := truncateToWidth(face, "短名", 1000); got != "短名" {
		t.Errorf("未超长时不应改动: %q", got)
	}
	if got := truncateToWidth(face, "", 100); got != "" {
		t.Errorf("空串应原样返回: %q", got)
	}
	if got := truncateToWidth(face, "任意", 0); got != "" {
		t.Errorf("宽度为 0 时应返回空串: %q", got)
	}

	long := "超长分组名称测试用于验证省略号截断是否正确工作"
	limit := measureWidth(face, "超长分组")
	got := truncateToWidth(face, long, limit)
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("被截断的文本应以省略号结尾: %q", got)
	}
	if w := measureWidth(face, got); w > limit {
		t.Errorf("截断后宽度 %v 超过上限 %v: %q", w, limit, got)
	}
	if len([]rune(got)) >= len([]rune(long)) {
		t.Errorf("截断后字符数未减少: %q", got)
	}
}

// TestSetFontPath 验证外部字体覆盖与恢复。
//
// 用内嵌字体的字节写一个临时 OTF 当外部字体：它一定可解析，
// 于是这个用例只测「覆盖/恢复/报错」的流程，不依赖机器上装了什么字体。
func TestSetFontPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "external.otf")
	if err := os.WriteFile(path, embeddedFontData, 0o644); err != nil {
		t.Fatalf("写临时字体失败: %v", err)
	}

	t.Cleanup(func() {
		if err := SetFontPath(""); err != nil {
			t.Errorf("恢复内嵌字体失败: %v", err)
		}
	})

	if err := SetFontPath(path); err != nil {
		t.Fatalf("SetFontPath(%q) 失败: %v", path, err)
	}
	if _, err := Render(sampleBoard(1)); err != nil {
		t.Fatalf("用外部字体渲染失败: %v", err)
	}

	if err := SetFontPath(filepath.Join(t.TempDir(), "not-exist.otf")); err == nil {
		t.Error("字体文件不存在时应返回错误")
	}
	bad := filepath.Join(t.TempDir(), "bad.otf")
	if err := os.WriteFile(bad, []byte("not a font"), 0o644); err != nil {
		t.Fatalf("写坏字体失败: %v", err)
	}
	if err := SetFontPath(bad); err == nil {
		t.Error("字体内容非法时应返回错误")
	}

	// 非法路径失败后必须仍能出图：configured 字体坏了不应该让整个推送挂掉。
	if err := SetFontPath(""); err != nil {
		t.Fatalf("恢复内嵌字体失败: %v", err)
	}
	if _, err := Render(sampleBoard(1)); err != nil {
		t.Fatalf("恢复内嵌字体后渲染失败: %v", err)
	}
}

// TestRenderConcurrent 验证「解析后的字体全局共享、face 每次新建」这套并发设计：
// 定时推送与手工预览可能同时出图，用 -race 跑这个用例才有意义。
func TestRenderConcurrent(t *testing.T) {
	board := sampleBoard(11)
	want, err := Render(board)
	if err != nil {
		t.Fatalf("Render 失败: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := Render(board)
			if err != nil {
				t.Errorf("并发 Render 失败: %v", err)
				return
			}
			// 渲染必须是确定性的：同样的输入必须产出逐字节相同的图，
			// 否则「样图」作为回归基线就没有意义。
			if !bytes.Equal(got, want) {
				t.Error("并发渲染结果与首次渲染不一致")
			}
		}()
	}
	wg.Wait()
}

// TestTestdataSamples 把 testdata 下的样图当成**回归基线**用。
//
// 两种模式：
//   - `-update`：重新生成样图（.png 的 hash 变了就是在改基线，必须人工
//     用 read_image 看过再提交）。
//   - 默认（CI）：逐字节比对本次渲染与已提交的样图。不一致就失败 —— 说明
//     渲染输出被动过，要么是预期的（那就跑 -update 并肉眼复核），要么是回归。
//
// 文件不存在时跳过该档而不是让整个测试失败：首次 clone 可能没有 testdata
// （git 不留空目录，也可能被 .gitignore 之类拦下），那种情况下仍然要有用。
func TestTestdataSamples(t *testing.T) {
	if *update {
		// testdata 可能还不存在（git 不留空目录），这里顺手建出来。
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("创建 testdata 失败: %v", err)
		}
	}

	for _, n := range sampleCounts {
		data, err := Render(sampleBoard(n))
		if err != nil {
			t.Fatalf("Render(%d) 失败: %v", n, err)
		}
		path := filepath.Join("testdata", fmt.Sprintf("board_%d.png", n))

		if *update {
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatalf("写样图 %s 失败: %v", path, err)
			}
			t.Logf("已写 %s (%d 字节)", path, len(data))
			continue
		}

		want, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			t.Logf("样图 %s 不存在，跳过比对（跑 -update 可生成）", path)
			continue
		}
		if err != nil {
			t.Fatalf("读样图 %s 失败: %v", path, err)
		}
		if !bytes.Equal(data, want) {
			t.Errorf("样图 %s 与本次渲染不一致（%d 字节 vs %d 字节）："+
				"渲染输出变了。确认是有意改动后跑 `go test ./internal/statusimg/... -update` "+
				"重建基线，并用 read_image 肉眼复核新样图。", path, len(want), len(data))
		}
	}
}
