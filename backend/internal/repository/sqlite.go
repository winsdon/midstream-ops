package repository

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// SQLite 本地存储（供应商/快照/倍率历史/探测结果）。
type SQLite struct {
	db *sql.DB
}

// NewSQLite 打开 SQLite（WAL + 单连接），并执行迁移。
func NewSQLite(path string) (*SQLite, error) {
	// 确保目录存在
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := ensureDir(dir); err != nil {
			return nil, err
		}
	}

	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开 SQLite 失败: %w", err)
	}
	// 单连接，避免 SQLITE_BUSY
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	s := &SQLite{db: db}
	if err := s.Migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// DB 暴露底层 *sql.DB。
func (s *SQLite) DB() *sql.DB { return s.db }

// Close 关闭连接。
func (s *SQLite) Close() error { return s.db.Close() }

// Migrate 执行内嵌 SQL 迁移（按文件名排序，记录 schema_migrations）。
func (s *SQLite) Migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		filename TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
	)`); err != nil {
		return fmt.Errorf("创建 schema_migrations 失败: %w", err)
	}

	entries, err := migrationsFS.ReadDir("migrations")
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
		if err := s.db.QueryRow(`SELECT COUNT(1) FROM schema_migrations WHERE filename = ?`, name).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			continue
		}
		content, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(content)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("执行迁移 %s 失败: %w", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(filename) VALUES(?)`, name); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// Ping 验证 SQLite 可用。
func (s *SQLite) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }
