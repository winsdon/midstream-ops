package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sub2api-account-monitor/internal/config"
	"sub2api-account-monitor/internal/pkg/secretbox"
	"sub2api-account-monitor/internal/repository"
)

// newTestOpCostService 建临时 SQLite + OperatingCostService，并预置一个自营站与一个普通站。
// 返回 (service, 自营站 id, 普通站 id)。
func newTestOpCostService(t *testing.T) (*OperatingCostService, int64, int64) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := repository.NewSQLite(path)
	if err != nil {
		t.Fatalf("初始化 SQLite 失败: %v", err)
	}
	t.Cleanup(func() {
		_ = s.Close()
		_ = os.Remove(path)
	})

	providerRepo := repository.NewProviderRepo(s, &secretbox.Box{})
	cfg := &config.Config{Location: time.UTC}

	selfRun, err := providerRepo.Create(context.Background(), repository.CreateParams{
		Name: "自营站", BalanceType: "none", SelfOperated: true,
	})
	if err != nil {
		t.Fatalf("建自营站失败: %v", err)
	}
	// 回读校验 self_operated 落库并被 scanProvider 正确读出 ——
	// providerCols 与 Scan 顺序是手工契约，错位时这里会先炸
	if !selfRun.SelfOperated {
		t.Fatal("自营站 SelfOperated 应为 true，检查 providerCols 与 scanProvider 是否同序")
	}
	normal, err := providerRepo.Create(context.Background(), repository.CreateParams{
		Name: "普通站", BalanceType: "none",
	})
	if err != nil {
		t.Fatalf("建普通站失败: %v", err)
	}
	if normal.SelfOperated {
		t.Fatal("普通站 SelfOperated 应为 false")
	}

	svc := NewOperatingCostService(repository.NewOperatingCostRepo(s), providerRepo, cfg)
	return svc, selfRun.ID, normal.ID
}

func TestCreateValidatesInput(t *testing.T) {
	tests := []struct {
		name    string
		params  repository.OperatingCostParams
		wantErr error
		// wantCategory / wantDate 仅在 wantErr 为 nil 时校验归一结果
		wantCategory string
		wantAmount   float64
	}{
		{
			name:         "正常录入",
			params:       repository.OperatingCostParams{Category: "account", Amount: 200, OccurredOn: "2026-07-15"},
			wantCategory: "account", wantAmount: 200,
		},
		{
			name:         "类别留空落 other",
			params:       repository.OperatingCostParams{Amount: 10, OccurredOn: "2026-07-15"},
			wantCategory: "other", wantAmount: 10,
		},
		{
			name:         "金额归一到分",
			params:       repository.OperatingCostParams{Category: "server", Amount: 10.005, OccurredOn: "2026-07-15"},
			wantCategory: "server", wantAmount: 10.01,
		},
		{
			// 归一后为 0 必须拒绝：否则会写入一堆不影响金额却污染明细的空记录
			name:    "金额过小归零后拒绝",
			params:  repository.OperatingCostParams{Category: "other", Amount: 0.004, OccurredOn: "2026-07-15"},
			wantErr: ErrInvalidInput,
		},
		{
			name:    "金额为 0 拒绝",
			params:  repository.OperatingCostParams{Category: "other", Amount: 0, OccurredOn: "2026-07-15"},
			wantErr: ErrInvalidInput,
		},
		{
			name:    "金额为负拒绝",
			params:  repository.OperatingCostParams{Category: "other", Amount: -5, OccurredOn: "2026-07-15"},
			wantErr: ErrInvalidInput,
		},
		{
			name:    "非法类别拒绝",
			params:  repository.OperatingCostParams{Category: "rent", Amount: 10, OccurredOn: "2026-07-15"},
			wantErr: ErrInvalidInput,
		},
		{
			// occurred_on 参与字符串比较的区间查询，非法格式会让该行永远落在区间外而失踪
			name:    "非法日期格式拒绝",
			params:  repository.OperatingCostParams{Category: "other", Amount: 10, OccurredOn: "2026/07/15"},
			wantErr: ErrInvalidInput,
		},
		{
			name:    "日期含时刻拒绝",
			params:  repository.OperatingCostParams{Category: "other", Amount: 10, OccurredOn: "2026-07-15T10:00:00Z"},
			wantErr: ErrInvalidInput,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, selfID, _ := newTestOpCostService(t)
			tt.params.ProviderID = selfID

			got, err := svc.Create(context.Background(), tt.params)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("未预期的错误: %v", err)
			}
			if got.Category != tt.wantCategory {
				t.Errorf("category = %q, want %q", got.Category, tt.wantCategory)
			}
			if got.Amount != tt.wantAmount {
				t.Errorf("amount = %v, want %v", got.Amount, tt.wantAmount)
			}
			// 币种恒为 USD：必须与上游实扣同币种才能直接相加
			if got.Currency != "USD" {
				t.Errorf("currency = %q, want USD", got.Currency)
			}
		})
	}
}

// 日期留空取配置时区的今天，而不是 UTC 或零值。
func TestCreateDefaultsOccurredOnToToday(t *testing.T) {
	svc, selfID, _ := newTestOpCostService(t)

	got, err := svc.Create(context.Background(), repository.OperatingCostParams{
		ProviderID: selfID, Category: "account", Amount: 50,
	})
	if err != nil {
		t.Fatalf("未预期的错误: %v", err)
	}
	want := time.Now().In(time.UTC).Format("2006-01-02")
	if got.OccurredOn != want {
		t.Errorf("occurred_on = %q, want %q", got.OccurredOn, want)
	}
}

// 非自营站必须被拒：其成本已由上游实扣完整表达，再记一笔必然重复计算。
func TestCreateRejectsNonSelfOperated(t *testing.T) {
	svc, _, normalID := newTestOpCostService(t)

	_, err := svc.Create(context.Background(), repository.OperatingCostParams{
		ProviderID: normalID, Category: "account", Amount: 200, OccurredOn: "2026-07-15",
	})
	if !errors.Is(err, ErrNotSelfOperated) {
		t.Fatalf("err = %v, want ErrNotSelfOperated", err)
	}
}

// 站点不存在应回 ErrNotFound，让 handler 能转 404 而非 400/500。
func TestCreateRejectsUnknownProvider(t *testing.T) {
	svc, _, _ := newTestOpCostService(t)

	_, err := svc.Create(context.Background(), repository.OperatingCostParams{
		ProviderID: 9999, Category: "account", Amount: 200, OccurredOn: "2026-07-15",
	})
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// List 按闭区间过滤并算出合计；边界日期须包含在内。
func TestListFiltersByDateRangeInclusive(t *testing.T) {
	svc, selfID, _ := newTestOpCostService(t)
	ctx := context.Background()

	for _, d := range []struct {
		date   string
		amount float64
	}{
		{"2026-06-30", 1}, // 区间前一天
		{"2026-07-01", 10},
		{"2026-07-15", 20},
		{"2026-07-31", 30},
		{"2026-08-01", 2}, // 区间后一天
	} {
		if _, err := svc.Create(ctx, repository.OperatingCostParams{
			ProviderID: selfID, Category: "other", Amount: d.amount, OccurredOn: d.date,
		}); err != nil {
			t.Fatalf("写入 %s 失败: %v", d.date, err)
		}
	}

	items, total, err := svc.List(ctx, selfID, "2026-07-01", "2026-07-31")
	if err != nil {
		t.Fatalf("未预期的错误: %v", err)
	}
	if len(items) != 3 {
		t.Errorf("len(items) = %d, want 3", len(items))
	}
	if total != 60 {
		t.Errorf("total = %v, want 60", total)
	}
	// 发生日降序：最近的排最前
	if items[0].OccurredOn != "2026-07-31" {
		t.Errorf("items[0].occurred_on = %q, want 2026-07-31（应按发生日降序）", items[0].OccurredOn)
	}
}

// 取消自营标记后仍能读到历史记录，否则数据会成为无法访问的孤儿。
func TestListWorksAfterSelfOperatedRevoked(t *testing.T) {
	svc, selfID, _ := newTestOpCostService(t)
	ctx := context.Background()

	if _, err := svc.Create(ctx, repository.OperatingCostParams{
		ProviderID: selfID, Category: "account", Amount: 100, OccurredOn: "2026-07-15",
	}); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	// 直接改标记（模拟用户在站点设置里关掉自营）
	if _, err := svc.providerRepo.Update(ctx, selfID, repository.UpdateParams{
		Name: "自营站", BalanceType: "none", SelfOperated: false,
	}); err != nil {
		t.Fatalf("取消自营标记失败: %v", err)
	}

	items, total, err := svc.List(ctx, selfID, "2026-07-01", "2026-07-31")
	if err != nil {
		t.Fatalf("取消自营后 List 应仍可用: %v", err)
	}
	if len(items) != 1 || total != 100 {
		t.Errorf("items=%d total=%v, want 1 条 / 100", len(items), total)
	}
}

// 删除不存在的记录应回 ErrNotFound，让 handler 能转 404 而非静默成功。
func TestDeleteMissingReturnsNotFound(t *testing.T) {
	svc, _, _ := newTestOpCostService(t)

	if err := svc.Delete(context.Background(), 9999); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// 删除后合计随之下降（合计由明细算出，不是缓存字段）。
func TestDeleteRemovesFromTotal(t *testing.T) {
	svc, selfID, _ := newTestOpCostService(t)
	ctx := context.Background()

	first, err := svc.Create(ctx, repository.OperatingCostParams{
		ProviderID: selfID, Category: "account", Amount: 200, OccurredOn: "2026-07-15",
	})
	if err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if _, err := svc.Create(ctx, repository.OperatingCostParams{
		ProviderID: selfID, Category: "server", Amount: 50, OccurredOn: "2026-07-16",
	}); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	if err := svc.Delete(ctx, first.ID); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	_, total, err := svc.List(ctx, selfID, "2026-07-01", "2026-07-31")
	if err != nil {
		t.Fatalf("未预期的错误: %v", err)
	}
	if total != 50 {
		t.Errorf("total = %v, want 50", total)
	}
}
