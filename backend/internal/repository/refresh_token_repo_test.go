package repository

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRefreshTokenInsertGetDelete(t *testing.T) {
	repo := NewRefreshTokenRepo(newTestStore(t))
	ctx := context.Background()
	exp := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Microsecond)
	rec := RefreshToken{
		TokenHash:  "hash-1",
		Username:   "admin",
		PasswordFP: "fp-1",
		ExpiresAt:  exp,
	}
	if err := repo.Insert(ctx, rec); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := repo.Get(ctx, "hash-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Username != "admin" || got.PasswordFP != "fp-1" {
		t.Errorf("Get = %+v", got)
	}
	if !got.ExpiresAt.Equal(exp) && !got.ExpiresAt.Truncate(time.Microsecond).Equal(exp) {
		t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, exp)
	}

	if err := repo.Delete(ctx, "hash-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, "hash-1"); !errors.Is(err, ErrRefreshTokenNotFound) {
		t.Errorf("删除后 Get err = %v, want ErrRefreshTokenNotFound", err)
	}
}

func TestRefreshTokenGetMissing(t *testing.T) {
	repo := NewRefreshTokenRepo(newTestStore(t))
	_, err := repo.Get(context.Background(), "nope")
	if !errors.Is(err, ErrRefreshTokenNotFound) {
		t.Errorf("err = %v, want ErrRefreshTokenNotFound", err)
	}
}

func TestRefreshTokenRotate(t *testing.T) {
	repo := NewRefreshTokenRepo(newTestStore(t))
	ctx := context.Background()
	old := RefreshToken{TokenHash: "old", Username: "admin", PasswordFP: "fp", ExpiresAt: time.Now().Add(time.Hour)}
	if err := repo.Insert(ctx, old); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	next := RefreshToken{TokenHash: "new", Username: "admin", PasswordFP: "fp", ExpiresAt: time.Now().Add(48 * time.Hour)}
	if err := repo.Rotate(ctx, "old", next); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if _, err := repo.Get(ctx, "old"); !errors.Is(err, ErrRefreshTokenNotFound) {
		t.Errorf("旧 hash 应失效, err=%v", err)
	}
	got, err := repo.Get(ctx, "new")
	if err != nil {
		t.Fatalf("新 hash Get: %v", err)
	}
	if got.Username != "admin" {
		t.Errorf("username = %q", got.Username)
	}
}

func TestRefreshTokenRotateMissing(t *testing.T) {
	repo := NewRefreshTokenRepo(newTestStore(t))
	err := repo.Rotate(context.Background(), "ghost", RefreshToken{
		TokenHash: "n", Username: "admin", PasswordFP: "fp", ExpiresAt: time.Now().Add(time.Hour),
	})
	if !errors.Is(err, ErrRefreshTokenNotFound) {
		t.Errorf("err = %v, want ErrRefreshTokenNotFound", err)
	}
}
