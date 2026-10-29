package service

import (
	"strings"
	"testing"
)

func TestExtractFinalAnswer(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		want  int
		found bool
	}{
		{"格式行", "推理……\n最终答案：21", 21, true},
		{"半角冒号与加粗", "**最终答案: 21**", 21, true},
		{"只加粗标签", "推理……\n**最终答案**：21", 21, true},
		{"LaTeX 包裹", "最终答案：$21$", 21, true},
		{"行内代码包裹", "最终答案：`21`", 21, true},
		{"格式行取最后一次", "若形状摸不出来，最终答案：29；\n但题目说形状摸得出来\n最终答案：21", 21, true},
		{"格式行优先于其他写法", "答案是 29？不对。\n最终答案：21", 21, true},
		{"算式里的中间数不算答案", "……所以答案是 12+9=21。\n\n**最终答案**：21", 21, true},
		{"格式行后面换行接列表不算算式", "最终答案：21\n- 验证见上", 21, true},
		{"boxed 兜底", `所以 \boxed{21}`, 21, true},
		{"答案是 兜底", "综上，答案是 **21** 个。", 21, true},
		{"兜底只看末尾几行", "开头猜一下：答案是 5？\n一\n二\n三\n四\n五\n六\n结论见上", 0, false},
		{"兜底也不认算式", "所以答案是 12+9=21", 0, false},
		{"推理里的中间数不算答案", "至少要摸 9 颗圆的，再加上 12 颗五角星……", 0, false},
		{"没有答案", "这道题需要更多信息。", 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, found := extractFinalAnswer(tc.text)
			if got != tc.want || found != tc.found {
				t.Fatalf("得到 (%d, %v)，期望 (%d, %v)", got, found, tc.want, tc.found)
			}
		})
	}
}

func TestAnswerOnLastLine(t *testing.T) {
	if n, ok := answerOnLastLine("推理……\n\n**最终答案**：21\n"); !ok || n != 21 {
		t.Fatalf("格式行在最后一行应取出答案，实际 (%d, %v)", n, ok)
	}
	if _, ok := answerOnLastLine("最终答案：16？不对，再想想……\n如果先摸圆形"); ok {
		t.Fatal("被截断的推理里出现过格式行，但最后一行不是它，不应算答案")
	}
}

func TestGradeCandy(t *testing.T) {
	cases := []struct {
		name      string
		text      string
		truncated bool
		status    string
		answer    string
	}{
		{"答对", "推理……\n最终答案：21", false, CandyCorrect, "21"},
		{"答错", "按总数算\n最终答案：29", false, CandyWrong, "29"},
		{"没给答案", "这道题需要更多信息。", false, CandyNoAnswer, ""},
		{"截断但最后一行是格式行", "推理……\n最终答案：21", true, CandyCorrect, "21"},
		{"截断时不认推理里的中间结论", "最终答案：16？不对\n如果先摸圆形", true, CandyNoAnswer, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := gradeCandy(tc.text, tc.truncated)
			if got.Status != tc.status || got.Answer != tc.answer || got.Expected != "21" {
				t.Fatalf("得到 %+v，期望 status=%s answer=%s", got, tc.status, tc.answer)
			}
		})
	}
}

// 标准答案按题目数据穷举验证，不靠手算：能摸出形状时最少 21 颗；摸不出形状时是陷阱答案 29。
func TestCandyAnswerMatchesPuzzle(t *testing.T) {
	round := [3]int{7, 9, 8} // 苹果、桃子、西瓜
	star := [3]int{7, 6, 4}
	// paired 手里的圆形 r 与五角星 s 能否凑出「圆苹果 + 星桃子」或「圆桃子 + 星苹果」
	paired := func(r, s [3]int) bool { return (r[0] > 0 && s[1] > 0) || (r[1] > 0 && s[0] > 0) }
	// draws 从 pool 里摸出 n 颗的所有口味组合
	draws := func(pool [3]int, n int) [][3]int {
		var out [][3]int
		for a := 0; a <= pool[0] && a <= n; a++ {
			for b := 0; b <= pool[1] && a+b <= n; b++ {
				if c := n - a - b; c <= pool[2] {
					out = append(out, [3]int{a, b, c})
				}
			}
		}
		return out
	}
	guaranteed := func(x, y int) bool {
		for _, r := range draws(round, x) {
			for _, s := range draws(star, y) {
				if !paired(r, s) {
					return false
				}
			}
		}
		return true
	}
	roundTotal, starTotal := round[0]+round[1]+round[2], star[0]+star[1]+star[2]

	byShape := -1
	for x := 0; x <= roundTotal; x++ {
		for y := 0; y <= starTotal; y++ {
			if (byShape < 0 || x+y < byShape) && guaranteed(x, y) {
				byShape = x + y
			}
		}
	}
	if byShape != candyAnswer {
		t.Fatalf("能摸出形状时最少要摸 %d 颗，常量 candyAnswer = %d", byShape, candyAnswer)
	}

	blind := -1
	for n := 1; n <= roundTotal+starTotal && blind < 0; n++ {
		all := true
		for x := n - starTotal; x <= n && all; x++ {
			if x >= 0 && x <= roundTotal {
				all = guaranteed(x, n-x)
			}
		}
		if all {
			blind = n
		}
	}
	if blind != 29 {
		t.Fatalf("摸不出形状时应得到陷阱答案 29，实际 %d", blind)
	}
	if !strings.Contains(candyPrompt, "最终答案：数字") {
		t.Fatal("糖果题必须规定作答格式，自动判分只认格式行")
	}
}
