package repository

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestRebind(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"SELECT 1", "SELECT 1"},
		{"SELECT * FROM t WHERE a=?", "SELECT * FROM t WHERE a=$1"},
		{"INSERT INTO t VALUES (?,?,?)", "INSERT INTO t VALUES ($1,$2,$3)"},
		{"UPDATE t SET a=?, b=? WHERE id=?", "UPDATE t SET a=$1, b=$2 WHERE id=$3"},
		// 动态拼接的 WHERE：编号随分支变化，正是 rebind 存在的理由
		{"SELECT 1 FROM t WHERE 1=1 AND a=? AND b=? LIMIT ? OFFSET ?",
			"SELECT 1 FROM t WHERE 1=1 AND a=$1 AND b=$2 LIMIT $3 OFFSET $4"},
	}
	for _, c := range cases {
		if got := rebind(c.in); got != c.want {
			t.Errorf("rebind(%q) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}

// TestRebindPreservesDollarPlaceholders 已是 $N 的 SQL 不应被改动
// —— pg_*.go 里的只读查询本来就写成 $N。
func TestRebindPreservesDollarPlaceholders(t *testing.T) {
	q := "SELECT * FROM accounts WHERE id = $1 AND name = $2"
	if got := rebind(q); got != q {
		t.Errorf("rebind 改动了已有的 $N 查询: %q", got)
	}
}

// TestNoQuestionMarkInsideSQLLiterals 守住 rebind 的前提假设。
//
// rebind 不解析字符串字面量。一旦有人写出 WHERE note LIKE '%?%' 这类 SQL，
// 占位符编号就会错位。本测试扫描全包源码里的单引号字面量，把这个前提变成
// 可执行的约束——而不是留在注释里等人遗忘。
func TestNoQuestionMarkInsideSQLLiterals(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	// 匹配 SQL 里的单引号字面量。Go 的字符引面量用单引号但不会出现在
	// 反引号 SQL 串里，误报可接受（宁可误报也不漏报）。
	lit := regexp.MustCompile(`'[^'\n]*'`)
	for _, f := range files {
		// rebind.go 自身含 Go 字符字面量 '?'（不是 SQL）；testsupport.go 只有 DSN 拼接。
		if strings.HasSuffix(f, "_test.go") || f == "rebind.go" || f == "testsupport.go" {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			for _, m := range lit.FindAllString(line, -1) {
				if strings.Contains(m, "?") {
					t.Errorf("%s:%d SQL 字面量内含 ?，会让 rebind 编号错位: %s", f, i+1, m)
				}
			}
		}
	}
}
