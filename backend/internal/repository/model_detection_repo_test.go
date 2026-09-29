package repository

import (
	"context"
	"errors"
	"testing"
)

// 真伪检测历史只留最新若干条，更早的删除；保留顺序与列表一致。
func TestModelDetectionRepoDeleteBeyondLatest(t *testing.T) {
	repo := NewModelDetectionRepo(newTestStore(t))
	ctx := context.Background()

	const total = 5
	const keep = 3
	ids := make([]int64, 0, total)
	for i := 0; i < total; i++ {
		d := &ModelDetection{
			TargetFP: "fp", TargetName: "渠道", BaseURL: "https://a.example.com",
			Model: "claude-opus-5", Label: "unknown", Confidence: "low",
		}
		if err := repo.Insert(ctx, d); err != nil {
			t.Fatalf("写入第 %d 条失败: %v", i, err)
		}
		ids = append(ids, d.ID)
	}

	n, err := repo.DeleteBeyondLatest(ctx, 0)
	if err != nil || n != 0 {
		t.Fatalf("keep<=0 不应删除，实际 n=%d err=%v", n, err)
	}

	n, err = repo.DeleteBeyondLatest(ctx, keep)
	if err != nil {
		t.Fatalf("裁剪失败: %v", err)
	}
	if n != int64(total-keep) {
		t.Fatalf("应删除 %d 条，实际 %d", total-keep, n)
	}

	items, got, err := repo.List(ctx, DetectionFilter{Page: 1, PageSize: 100})
	if err != nil {
		t.Fatalf("列表失败: %v", err)
	}
	if got != int64(keep) || len(items) != keep {
		t.Fatalf("应剩 %d 条，实际 total=%d len=%d", keep, got, len(items))
	}
	for i, item := range items {
		want := ids[total-1-i]
		if item.ID != want {
			t.Fatalf("第 %d 条应为最新 id %d，实际 %d", i, want, item.ID)
		}
	}
	if _, err := repo.Get(ctx, ids[0]); !errors.Is(err, ErrDetectionNotFound) {
		t.Fatalf("最早的记录 %d 应已被删除，实际 %v", ids[0], err)
	}

	if err := repo.Delete(ctx, ids[0]); !errors.Is(err, ErrDetectionNotFound) {
		t.Fatalf("再删已不存在的记录应返回 ErrDetectionNotFound，实际 %v", err)
	}
	kept := ids[total-1]
	if err := repo.Delete(ctx, kept); err != nil {
		t.Fatalf("删除最新一条失败: %v", err)
	}
	if _, err := repo.Get(ctx, kept); !errors.Is(err, ErrDetectionNotFound) {
		t.Fatalf("已删除的记录 %d 仍能读到: %v", kept, err)
	}
}
