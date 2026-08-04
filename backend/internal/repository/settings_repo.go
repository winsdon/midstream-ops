package repository

import (
	"context"
	"database/sql"
	"errors"
)

// SettingsRepo 系统设置存储（key → JSON value）。
type SettingsRepo struct {
	db *sql.DB
}

// NewSettingsRepo 创建 SettingsRepo。
func NewSettingsRepo(s *SQLite) *SettingsRepo { return &SettingsRepo{db: s.DB()} }

// Get 读取设置 JSON；不存在时返回空串（非错误）。
func (r *SettingsRepo) Get(ctx context.Context, key string) (string, error) {
	var value string
	err := r.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

// Set 写入设置 JSON（upsert）。
func (r *SettingsRepo) Set(ctx context.Context, key, value string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at) VALUES (?,?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		key, value, nowUTC())
	return err
}
