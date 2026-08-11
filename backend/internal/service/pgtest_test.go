package service

import (
	"errors"
	"testing"

	"sub2api-account-monitor/internal/repository"
)

// newTestStore 建一个 schema 隔离的测试库。未配 MONITOR_TEST_PG_DSN 时 Skip。
//
// 与 repository 包里的同名辅助一职，因为 Go 不允许跨包引用 _test.go 符号，
// 真正的实现放在 repository.NewTestStore（非测试文件），两边共用。
func newTestStore(t *testing.T) *repository.Store {
	t.Helper()
	s, cleanup, err := repository.NewTestStore()
	if errors.Is(err, repository.ErrNoTestDSN) {
		t.Skip("未设置 MONITOR_TEST_PG_DSN，跳过需要 PG 的测试")
	}
	if err != nil {
		t.Fatalf("初始化测试 Store 失败: %v", err)
	}
	t.Cleanup(cleanup)
	return s
}
