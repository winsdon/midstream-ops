package service

import (
	"regexp"
	"strconv"
	"strings"

	"sub2api-account-monitor/internal/repository"
)

// candyPrompt 糖果题。原题的表格在转述时被压成了一行，这里按行排成 Markdown 表格；
// 末尾的格式行只规定作答格式、不含任何解题提示，自动判分只认它。
const candyPrompt = "在一个黑色的袋子里放有三种口味的糖果，每种糖果有两种不同的形状（圆形和五角星形，不同的形状靠手感可以分辨）。" +
	"现已知不同口味的糖和不同形状的数量统计如下表。参赛者需要在活动前决定摸出的糖果数目，那么，" +
	"最少取出多少个糖果才能保证手中同时拥有不同形状的苹果味和桃子味的糖？" +
	"（同时手中有圆形苹果味匹配五角星桃子味糖果，或者有圆形桃子味匹配五角星苹果味糖果都满足要求）\n\n" +
	"| | 苹果味 | 桃子味 | 西瓜味 |\n" +
	"|---|---|---|---|\n" +
	"| 圆形 | 7 | 9 | 8 |\n" +
	"| 五角星形 | 7 | 6 | 4 |\n\n" +
	"禁止联网,禁止写代码算出答案.禁止使用外部工具\n\n" +
	"请在回答最后单独一行按「最终答案：数字」的格式给出答案。"

// candyAnswer 糖果题标准答案。关键在题干「不同的形状靠手感可以分辨」：口味摸不出、形状摸得出，
// 摸的人能自己决定圆的、五角星各摸几颗。摸 12 颗五角星——不是苹果的五角星最多 6+4=10 颗、
// 不是桃子的最多 7+4=11 颗，所以苹果、桃子各至少 1 颗；再摸 9 颗圆的——圆形西瓜只有 8 颗，
// 至少 1 颗是苹果或桃子。圆的那颗不论是哪种都能和五角星配成一对，共 21 颗，且没有更少的分法。
// 把形状当成摸不出来、只按总数算会得到 29，正是这道题要筛掉的漏读题干的答案。
const candyAnswer = 21

const (
	// candyMaxTokens 糖果题的输出上限（含 thinking）：推理通常几千字，留足余量。
	candyMaxTokens = 16000
	// candyMaxTextRunes 糖果题回复全文的保存上限。
	candyMaxTextRunes = 20000
)

// 糖果题判定结果。
const (
	CandyQueued   = "queued"
	CandyCorrect  = "correct"
	CandyWrong    = "wrong"
	CandyNoAnswer = "no_answer"
	CandyFailed   = "failed"
)

// gradeCandy 核对糖果题回复。truncated 表示回复没有正常结束（被 max_tokens 截断或中途断流）：
// 被截断的回答没有真正的结尾，推理里的中间数不能当结论，只认恰好落在最后一行的格式行。
func gradeCandy(text string, truncated bool) repository.PelicanCandy {
	out := repository.PelicanCandy{Expected: strconv.Itoa(candyAnswer)}
	got, found := extractFinalAnswer(text)
	if truncated {
		got, found = answerOnLastLine(text)
	}
	if !found {
		out.Status = CandyNoAnswer
		return out
	}
	out.Answer = strconv.Itoa(got)
	out.Status = CandyWrong
	if got == candyAnswer {
		out.Status = CandyCorrect
	}
	return out
}

// answerNoise 模型常把答案包在 Markdown / LaTeX 记号里（**最终答案**：21、`21`、$21$），匹配前先剥掉。
var answerNoise = strings.NewReplacer("*", "", "_", "", "`", "", "$", "")

// formatLineRe 题目要求的「最终答案：N」格式行。
// 第二个分组捕获数字后紧跟的运算符（只跨空格、不跨行）：「12+9=21」里的 12 是算式的一部分，不是结论。
var formatLineRe = regexp.MustCompile(`最终答案\s*(?:是|为)?\s*[:：]?\s*(\d+)[ \t]*([+\-×*/=＋－＝]?)`)

// fallbackAnswerRes 模型没照格式作答时的兜底：\boxed{N} 与「答案是 N」。
// 「最少……N 个」这类写法不认——推理过程里的「至少 1 个」会被误抽。
var fallbackAnswerRes = []*regexp.Regexp{
	regexp.MustCompile(`\\boxed\{\s*(\d+)\s*\}`),
	regexp.MustCompile(`答案\s*(?:是|为)?\s*[:：]?\s*(\d+)[ \t]*([+\-×*/=＋－＝]?)`),
}

// answerTailLines 兜底规则只看回复最后几行：结论在末尾，前面的推理里满是中间数。
const answerTailLines = 5

// extractFinalAnswer 取回复里的最终数字答案。格式行取最后一次出现（推理里常先写中间结果）；
// 没有格式行才在末尾几行里找兜底写法。
func extractFinalAnswer(text string) (int, bool) {
	clean := answerNoise.Replace(text)
	if n, ok := lastAnswer(formatLineRe, clean); ok {
		return n, true
	}
	tail := lastLines(clean, answerTailLines)
	for _, re := range fallbackAnswerRes {
		if n, ok := lastAnswer(re, tail); ok {
			return n, true
		}
	}
	return 0, false
}

// answerOnLastLine 最后一个非空行就是格式行时返回其中的答案。
// 输出被截断时只认它：截断的回答没有真正的结尾，「取最后一次出现」的前提不成立。
func answerOnLastLine(text string) (int, bool) {
	return lastAnswer(formatLineRe, lastLines(answerNoise.Replace(text), 1))
}

// lastAnswer 取最后一个有效匹配里的数字；数字后紧跟运算符的是算式里的中间数，跳过。
func lastAnswer(re *regexp.Regexp, text string) (int, bool) {
	matches := re.FindAllStringSubmatch(text, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		m := matches[i]
		if len(m) > 2 && m[2] != "" {
			continue
		}
		if n, err := strconv.Atoi(m[1]); err == nil {
			return n, true
		}
	}
	return 0, false
}

// lastLines 取最后 n 个非空行，保持原顺序。
func lastLines(text string, n int) string {
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, n)
	for i := len(lines) - 1; i >= 0 && len(kept) < n; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			kept = append([]string{line}, kept...)
		}
	}
	return strings.Join(kept, "\n")
}
