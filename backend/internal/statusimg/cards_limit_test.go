package statusimg

import (
	"testing"

	"golang.org/x/image/font"
)

// mustMetaFace 取一个底部脚注字号的 face，用于按真实字体量文案宽度。
func mustMetaFace(t *testing.T) font.Face {
	t.Helper()
	f, err := loadFont()
	if err != nil {
		t.Fatalf("加载内嵌字体失败: %v", err)
	}
	fs, err := newFaces(f)
	if err != nil {
		t.Fatalf("创建 face 失败: %v", err)
	}
	return fs.meta
}

// 单图卡片数上限必须与渲染层的安全阈值自洽。
//
// 【为什么要单独锁住这条】service 层按 MaxCardsForSafeHeight() 截断，而本包又在
// 超过 maxImageHeight 时告警。两者一旦不一致（service 的上限更高），每一次满上限
// 推送都会打一条「图高超过安全上限…请在 service 层减少分组」的告警 —— 而 service
// 已经按设计截断过了，这条告警永远无法消除，运维只能学会忽略它。
// 历史上 service 自己写死过 64，正好比这里的阈值高一张，就是这个症状。
func TestMaxCardsForSafeHeightIsExactAndSelfConsistent(t *testing.T) {
	n := MaxCardsForSafeHeight()
	if n <= 0 {
		t.Fatal("卡片数上限必须为正")
	}

	// 最坏情形：带底部口径备注 + 带「卡片被裁剪」说明行（两者都会占高，
	// 而推送时两者一定同时存在）。
	worst := func(cards int) Board {
		return Board{
			Title:     "渠道状态",
			Summary:   "99 个渠道 · 比率近90分钟",
			UpdatedAt: "更新 10-17 00:00",
			Note:      "每块 11.25 分钟 · SLA 排除业务限制与忽略状态码 · 与稳定性页同口径",
			Truncated: "共 99 个渠道，图中仅显示前 99 个（其余请在上方排除列表里去掉）",
			Cards:     make([]Card, cards),
		}
	}

	// 满上限不得告警，且必须仍低于 Telegram 的「宽 + 高 ≤ 10000」
	l := computeLayout(worst(n))
	if w := heightWarning(n, l.height); w != "" {
		t.Errorf("满上限 %d 张不该告警，却得到: %s", n, w)
	}
	if sum := l.height + float64(outW); sum > telegramMaxPhotoSum {
		t.Errorf("%d 张时宽+高 = %.0f，超过 Telegram 硬限制 %d", n, sum, telegramMaxPhotoSum)
	}

	// 多一张必须告警：否则上限比实际安全边界保守太多，白白少发分组
	over := computeLayout(worst(n + 1))
	if heightWarning(n+1, over.height) == "" {
		t.Errorf("n+1 = %d 张（高 %.0f）应触发告警，否则上限过于保守", n+1, over.height)
	}
}

// 空看板与极小看板也要落在安全高度内（上限推导的下界不能被破坏）。
func TestSmallBoardsAreWithinSafeHeight(t *testing.T) {
	for _, cards := range []int{0, 1, 2, 11, 40} {
		b := Board{
			Title: "渠道状态", Summary: "x", UpdatedAt: "y",
			Note:  "每块 11.25 分钟 · SLA 排除业务限制与忽略状态码 · 与稳定性页同口径",
			Cards: make([]Card, cards),
		}
		l := computeLayout(b)
		if w := heightWarning(cards, l.height); w != "" {
			t.Errorf("%d 张卡不该告警: %s", cards, w)
		}
	}
}

// 底部那两行脚注（口径备注 + 裁剪说明）必须放得下，一行都别被截断。
//
// 【为什么必须按真实字数量】Summary 只占半幅宽，之前把「共 70 个，图仅显示前 64」
// 塞进 Summary 就被截成了「…图仅显示前 …」—— 关键数字没了，等于没说。
// 裁剪说明改走整幅宽的一行之后，这里按真实字体把关：文案一旦被改长到放不下，
// 用例立刻红，而不是等运营在客户群里看到一句被截断的话。
func TestFooterNoticesFitFullWidth(t *testing.T) {
	// 与 service 层实际下发的文案保持一致（两边都改时必须同步改这里，
	// 所以刻意把字面量写全，而不是引用 service 的常量）
	notices := []string{
		"每块 11.25 分钟 · SLA 排除业务限制与忽略状态码 · 与稳定性页同口径",
		"共 99 个渠道，图中仅显示前 62 个（其余请在上方排除列表里去掉）",
	}
	avail := float64(outW) - s(2*pad)
	face := mustMetaFace(t)
	for _, text := range notices {
		if w := measureWidth(face, text); w > avail {
			t.Errorf("文案显示宽度 %.0f 超过可用 %.0f，会被截断: %q", w, avail, text)
		}
		// 顺带确认渲染时真的没被截断（truncateToWidth 会在超宽时加省略号）
		if got := truncateToWidth(face, text, avail); got != text {
			t.Errorf("文案被截断了: %q -> %q", text, got)
		}
	}
}
