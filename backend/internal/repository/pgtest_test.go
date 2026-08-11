package repository

import (
	"errors"
	"fmt"
	"os"
	"testing"
)

// newTestStore 是 NewTestStore 的测试内封装：未配 DSN 时 Skip 而非 Fail。
//
// Skip 而非 Fail 保住了「任何机器上都能 go test ./... 跑通」。代价是没配时
// 这些测试静默跳过，由 TestMain 打印提示补偿。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, cleanup, err := NewTestStore()
	if errors.Is(err, ErrNoTestDSN) {
		t.Skip("未设置 MONITOR_TEST_PG_DSN，跳过需要 PG 的测试")
	}
	if err != nil {
		t.Fatalf("初始化测试 Store 失败: %v", err)
	}
	t.Cleanup(cleanup)
	return s
}

// TestMain 在没配 DSN 时打印醒目提示。
//
// 不这么做的话，「一批测试全 skip、go test 显示绿色」会给出虚假的安全感
// —— 绿色但什么都没测到，比红色更危险。
func TestMain(m *testing.M) {
	code := m.Run()
	if os.Getenv("MONITOR_TEST_PG_DSN") == "" {
		fmt.Fprintln(os.Stderr,
			"\n[跳过] 需要 PostgreSQL 的测试未运行。设置 MONITOR_TEST_PG_DSN 后重跑，例如：\n"+
				"  MONITOR_TEST_PG_DSN='postgres://user:pass@localhost:5432/monitor_test?sslmode=disable' go test -race ./...")
	}
	os.Exit(code)
}
