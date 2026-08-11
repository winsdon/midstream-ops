package repository

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

// ErrNoTestDSN 表示未配置测试库。调用方应 t.Skip 而非 t.Fatal。
var ErrNoTestDSN = fmt.Errorf("未设置 MONITOR_TEST_PG_DSN")

// NewTestStore 建一个 schema 隔离的测试库并跑完整迁移，返回 Store 与清理函数。
//
// 放在非 _test.go 文件里是因为 internal/service 的测试也要用它 —— Go 不允许
// 跨包引用 _test.go 里的符号。它只读 MONITOR_TEST_PG_DSN，未配置时返回
// ErrNoTestDSN，不会在生产路径上被误用。
//
// 隔离靠「每个测试一个 schema」而非「每个测试一个数据库」：CREATE DATABASE 要
// 独占连接且不能在事务内执行，耗时是 CREATE SCHEMA 的十倍以上。
//
// 【为什么不用事务回滚做隔离】迁移里有 DDL，且被测代码自己开事务（11 处
// BeginTx），嵌套要 savepoint 模拟，会掩盖真实的事务边界问题——而事务行为正是
// 本次 SQLite→PG 迁移最需要测的东西。
func NewTestStore() (*Store, func(), error) {
	base := os.Getenv("MONITOR_TEST_PG_DSN")
	if base == "" {
		return nil, nil, ErrNoTestDSN
	}

	// schema 名用随机后缀而非测试名：后者含 `/` 与大小写（子测试），消毒比随机串
	// 麻烦，且 t.Parallel() 下同名子测试会撞。
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return nil, nil, fmt.Errorf("生成 schema 名失败: %w", err)
	}
	schema := "t_" + hex.EncodeToString(buf)

	admin, err := sql.Open("pgx/v5", base)
	if err != nil {
		return nil, nil, fmt.Errorf("连接测试库失败: %w", err)
	}
	if _, err := admin.Exec(fmt.Sprintf(`CREATE SCHEMA %q`, schema)); err != nil {
		_ = admin.Close()
		return nil, nil, fmt.Errorf("创建 schema %s 失败: %w", schema, err)
	}

	// search_path 通过 DSN 参数传：pgx 把它当 RuntimeParam 透传给服务端，
	// 连接池里每条连接自动带上，无需手动 SET。
	sep := "?"
	if strings.ContainsRune(base, '?') {
		sep = "&"
	}
	raw, err := openRaw(base + sep + "search_path=" + schema)
	if err != nil {
		_, _ = admin.Exec(fmt.Sprintf(`DROP SCHEMA %q CASCADE`, schema))
		_ = admin.Close()
		return nil, nil, err
	}
	s := &Store{db: &DB{raw: raw}}
	if err := s.Migrate(); err != nil {
		_ = raw.Close()
		_, _ = admin.Exec(fmt.Sprintf(`DROP SCHEMA %q CASCADE`, schema))
		_ = admin.Close()
		return nil, nil, err
	}

	cleanup := func() {
		_ = s.Close()
		_, _ = admin.Exec(fmt.Sprintf(`DROP SCHEMA %q CASCADE`, schema))
		_ = admin.Close()
	}
	return s, cleanup, nil
}
