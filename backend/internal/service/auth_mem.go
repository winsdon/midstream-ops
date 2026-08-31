package service

import (
	"context"
	"sync"

	"sub2api-account-monitor/internal/repository"
)

// MemRefreshTokens 内存版 refresh token 存储，供单测与不连 PG 的路由测试使用。
type MemRefreshTokens struct {
	mu sync.Mutex
	m  map[string]repository.RefreshToken
}

// NewMemRefreshTokens 创建空的内存存储。
func NewMemRefreshTokens() *MemRefreshTokens {
	return &MemRefreshTokens{m: make(map[string]repository.RefreshToken)}
}

// Insert 写入一行。
func (s *MemRefreshTokens) Insert(_ context.Context, rec repository.RefreshToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[rec.TokenHash] = rec
	return nil
}

// Get 按 hash 读取。
func (s *MemRefreshTokens) Get(_ context.Context, tokenHash string) (*repository.RefreshToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.m[tokenHash]
	if !ok {
		return nil, repository.ErrRefreshTokenNotFound
	}
	cp := rec
	return &cp, nil
}

// Delete 按 hash 删除。
func (s *MemRefreshTokens) Delete(_ context.Context, tokenHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, tokenHash)
	return nil
}

// Rotate 删旧插新；旧行不存在返回 ErrRefreshTokenNotFound。
func (s *MemRefreshTokens) Rotate(_ context.Context, oldHash string, rec repository.RefreshToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[oldHash]; !ok {
		return repository.ErrRefreshTokenNotFound
	}
	delete(s.m, oldHash)
	s.m[rec.TokenHash] = rec
	return nil
}
