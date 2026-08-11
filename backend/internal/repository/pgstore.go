// Package repository 提供数据访问层（上游 sub2api PG 只读 + 本地 monitor PG 读写）。
package repository

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strings"
	"time"

	"sub2api-account-monitor/internal/config"

	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed migrations_pg/*.sql
var migrationsFS embed.FS

// Store 本地 monitor 库（供应商/快照/倍率/成本/授信台账/KYC）。
//
// 与 PG（上游 sub2api 只读库）的区别：这个库是本项目自己的数据，可写。
// 两者都是外部 PG，本项目不负责起数据库。
type Store struct {
	db *DB
}

// openRaw 打开一个配置好连接池的 *sql.DB。生产走 NewStore，测试走 DSN 直连，
// 池参数只在这里定义一处。
func openRaw(dsn string) (*sql.DB, error) {
	raw, err := sql.Open("pgx/v5", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开本地 PG 失败: %w", err)
	}
	// SQLite 时代是 SetMaxOpenConns(1)（规避 SQLITE_BUSY，代价是全库写入串行）。
	// PG 是 MVCC，没有这个限制。
	raw.SetMaxOpenConns(10)
	raw.SetMaxIdleConns(2)
	raw.SetConnMaxLifetime(30 * time.Minute)
	return raw, nil
}

// NewStore 连接本地 monitor 库并执行迁移。
func NewStore(cfg config.StoreDB) (*Store, error) {
	raw, err := openRaw(cfg.DSN())
	if err != nil {
		return nil, err
	}

	s := &Store{db: &DB{raw: raw}}
	if err := s.Migrate(); err != nil {
		_ = raw.Close()
		return nil, err
	}
	return s, nil
}

// DB 暴露带 rebind 的连接壳。
func (s *Store) DB() *DB { return s.db }

// Close 关闭连接。
func (s *Store) Close() error { return s.db.Close() }

// Ping 验证本地库可用。
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// migrateLockID 迁移咨询锁的键。多实例同时启动时避免并发执行同一迁移
// —— SQLite 时代靠单文件 + 单连接天然串行，PG 下没有这个保护。
const migrateLockID = 20260810

// Migrate 执行内嵌 SQL 迁移（按文件名排序，记录 schema_migrations）。
//
// 【依赖 pgx 的 simple protocol】每个迁移文件是整体一次 tx.Exec，靠的是
// pgx 在无参数时强制走 QueryExecModeSimpleProtocol（conn.go），而 PG 的
// Simple Query 协议原生支持分号分隔的多语句。代价是错误不指明是文件里第几条
// 语句失败。若某个迁移大到难以定位，把它拆成 NNN_a.sql / NNN_b.sql 即可
// ——文件名排序天然保证顺序，不需要写 SQL 分号切分器。
//
// 迁移文件内不能写 BEGIN;/COMMIT;：simple protocol 下 PG 把整个多语句串当作
// 一个隐式事务，显式事务语句会与外层 tx 冲突。
func (s *Store) Migrate() error {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启迁移事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// 事务版咨询锁：随事务结束自动释放。不用 pg_advisory_lock —— 后者必须在
	// 同一连接上取和放，而 *sql.DB 是连接池，拿不到这个保证。
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, migrateLockID); err != nil {
		return fmt.Errorf("获取迁移锁失败: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		filename   TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("创建 schema_migrations 失败: %w", err)
	}

	entries, err := migrationsFS.ReadDir("migrations_pg")
	if err != nil {
		return fmt.Errorf("读取迁移目录失败: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		var exists int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(1) FROM schema_migrations WHERE filename = ?`, name).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			continue
		}
		content, err := migrationsFS.ReadFile("migrations_pg/" + name)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(content)); err != nil {
			return fmt.Errorf("执行迁移 %s 失败: %w\n--- 文件开头 ---\n%s", name, err, head(string(content), 200))
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations(filename) VALUES(?)`, name); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// head 截取字符串前 n 个字符，供迁移失败时定位文件。
func head(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
