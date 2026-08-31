package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ErrRefreshTokenNotFound 表示 hash 对应的 refresh token 不存在（已轮转、已登出或从未签发）。
var ErrRefreshTokenNotFound = errors.New("refresh token not found")

// RefreshToken 是落库的 refresh token 行。TokenHash 是明文 token 的 SHA-256 hex。
type RefreshToken struct {
	TokenHash  string
	Username   string
	PasswordFP string
	ExpiresAt  time.Time
	CreatedAt  time.Time
}

// RefreshTokenRepo 管理员 refresh token 存储。
type RefreshTokenRepo struct {
	db *DB
}

// NewRefreshTokenRepo 创建 RefreshTokenRepo。
func NewRefreshTokenRepo(s *Store) *RefreshTokenRepo {
	return &RefreshTokenRepo{db: s.DB()}
}

// Insert 写入一行。token_hash 冲突视为调用方重复签发，原样返回驱动错误。
func (r *RefreshTokenRepo) Insert(ctx context.Context, rec RefreshToken) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO refresh_tokens (token_hash, username, password_fp, expires_at)
		VALUES (?,?,?,?)`,
		rec.TokenHash, rec.Username, rec.PasswordFP, rec.ExpiresAt.UTC())
	return err
}

// Get 按 hash 读取。找不到返回 ErrRefreshTokenNotFound。
func (r *RefreshTokenRepo) Get(ctx context.Context, tokenHash string) (*RefreshToken, error) {
	var rec RefreshToken
	err := r.db.QueryRowContext(ctx, `
		SELECT token_hash, username, password_fp, expires_at, created_at
		FROM refresh_tokens WHERE token_hash = ?`, tokenHash).
		Scan(&rec.TokenHash, &rec.Username, &rec.PasswordFP, &rec.ExpiresAt, &rec.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrRefreshTokenNotFound
	}
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

// Delete 按 hash 删除。行不存在不算错误。
func (r *RefreshTokenRepo) Delete(ctx context.Context, tokenHash string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM refresh_tokens WHERE token_hash = ?`, tokenHash)
	return err
}

// Rotate 在同一事务里删旧插新。旧行不存在返回 ErrRefreshTokenNotFound（并发轮转的失败方）。
func (r *RefreshTokenRepo) Rotate(ctx context.Context, oldHash string, rec RefreshToken) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `DELETE FROM refresh_tokens WHERE token_hash = ?`, oldHash)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrRefreshTokenNotFound
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO refresh_tokens (token_hash, username, password_fp, expires_at)
		VALUES (?,?,?,?)`,
		rec.TokenHash, rec.Username, rec.PasswordFP, rec.ExpiresAt.UTC()); err != nil {
		return err
	}
	return tx.Commit()
}
