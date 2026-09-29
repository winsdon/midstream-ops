package repository

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// 智商测试历史：写入回填 id、列表不带报告、详情带报告、按时间清理。
func TestModelIQRepoRoundTrip(t *testing.T) {
	repo := NewModelIQRepo(newTestStore(t))
	ctx := context.Background()

	accountID := int64(42)
	first := &ModelIQRun{
		AccountID: &accountID, AccountName: "账号A", TargetFP: "fp-a", TargetName: "账号A",
		BaseURL: "https://a.example.com", Model: "claude-opus-5", Passed: 1, Total: 2,
		Results: json.RawMessage(`[{"id":"iq-candy","title":"糖果题测试","status":"passed","summary":"答对 21"}]`),
		Report:  json.RawMessage(`{"suite":"iq","checks":[{"id":"iq-pelican","output":{"document":"<svg></svg>"}}]}`),
	}
	if err := repo.Insert(ctx, first); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if first.ID <= 0 || first.CreatedAt.IsZero() {
		t.Fatalf("写入后应回填 id 与 created_at，实际 %+v", first)
	}
	// 缺省的 results / report 落成空 JSON 而不是 NULL
	second := &ModelIQRun{TargetFP: "fp-b", BaseURL: "https://b.example.com", Model: "claude-opus-5"}
	if err := repo.Insert(ctx, second); err != nil {
		t.Fatalf("写入空结论失败: %v", err)
	}

	items, total, err := repo.List(ctx, 1, 10)
	if err != nil {
		t.Fatalf("列表失败: %v", err)
	}
	if total != 2 || len(items) != 2 || items[0].ID != second.ID {
		t.Fatalf("列表应按时间倒序返回 2 条，实际 total=%d items=%+v", total, items)
	}
	if items[1].Passed != 1 || items[1].Total != 2 || items[1].Report != nil {
		t.Fatalf("列表应带结论计数、不带报告，实际 %+v", items[1])
	}
	var results []map[string]any
	if err := json.Unmarshal(items[1].Results, &results); err != nil || len(results) != 1 {
		t.Fatalf("结论 JSON 应可解析，实际 %s（%v）", items[1].Results, err)
	}

	got, err := repo.Get(ctx, first.ID)
	if err != nil {
		t.Fatalf("详情失败: %v", err)
	}
	if got.AccountID == nil || *got.AccountID != accountID || len(got.Report) == 0 {
		t.Fatalf("详情应带账号与报告，实际 %+v", got)
	}
	if _, err := repo.Get(ctx, first.ID+second.ID+100); !errors.Is(err, ErrIQRunNotFound) {
		t.Fatalf("不存在的 id 应返回 ErrIQRunNotFound，实际 %v", err)
	}

	n, err := repo.DeleteOlderThan(ctx, time.Now().Add(time.Hour))
	if err != nil || n != 2 {
		t.Fatalf("清理应删掉 2 条，实际 n=%d err=%v", n, err)
	}
}

// 智商测试历史只留最新若干条，更早的删除；保留顺序与列表一致。
func TestModelIQRepoDeleteBeyondLatest(t *testing.T) {
	repo := NewModelIQRepo(newTestStore(t))
	ctx := context.Background()

	const total = 5
	const keep = 3
	ids := make([]int64, 0, total)
	for i := 0; i < total; i++ {
		run := &ModelIQRun{
			TargetFP: "fp", TargetName: "渠道", BaseURL: "https://a.example.com", Model: "claude-opus-5",
		}
		if err := repo.Insert(ctx, run); err != nil {
			t.Fatalf("写入第 %d 条失败: %v", i, err)
		}
		ids = append(ids, run.ID)
	}

	n, err := repo.DeleteBeyondLatest(ctx, keep)
	if err != nil {
		t.Fatalf("裁剪失败: %v", err)
	}
	if n != int64(total-keep) {
		t.Fatalf("应删除 %d 条，实际 %d", total-keep, n)
	}

	items, got, err := repo.List(ctx, 1, 100)
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
	if _, err := repo.Get(ctx, ids[0]); !errors.Is(err, ErrIQRunNotFound) {
		t.Fatalf("最早的记录 %d 应已被删除，实际 %v", ids[0], err)
	}

	if err := repo.Delete(ctx, ids[0]); !errors.Is(err, ErrIQRunNotFound) {
		t.Fatalf("再删已不存在的记录应返回 ErrIQRunNotFound，实际 %v", err)
	}
	kept := ids[total-1]
	if err := repo.Delete(ctx, kept); err != nil {
		t.Fatalf("删除最新一条失败: %v", err)
	}
	if _, err := repo.Get(ctx, kept); !errors.Is(err, ErrIQRunNotFound) {
		t.Fatalf("已删除的记录 %d 仍能读到: %v", kept, err)
	}
}
