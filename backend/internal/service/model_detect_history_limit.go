package service

import (
	"context"
	"log"

	"sub2api-account-monitor/internal/repository"
)

// ModelTestHistoryKeep 模型测试历史各自保留的最新条数。
//
// 真伪判定与智商测试分表存储，上限各算各的。报告里有脱敏后的完整请求与响应，
// 不封顶会持续变大；20 条够对照近期是否漂移。
const ModelTestHistoryKeep = 20

// TrimDetectionHistory 把真伪检测历史裁到最新 ModelTestHistoryKeep 条。
func (s *ModelDetectService) TrimDetectionHistory(ctx context.Context) {
	if s == nil || s.repo == nil {
		return
	}
	trimHistory(ctx, "真伪检测", func() (int64, error) {
		return s.repo.DeleteBeyondLatest(ctx, ModelTestHistoryKeep)
	})
}

// DeleteIQHistory 删除一条智商测试历史。未注入存储时与不存在同等对待。
func (s *ModelDetectService) DeleteIQHistory(ctx context.Context, id int64) error {
	if s == nil || s.iqRepo == nil {
		return repository.ErrIQRunNotFound
	}
	return s.iqRepo.Delete(ctx, id)
}

// TrimIQHistory 把智商测试历史裁到最新 ModelTestHistoryKeep 条。
func (s *ModelDetectService) TrimIQHistory(ctx context.Context) {
	if s == nil || s.iqRepo == nil {
		return
	}
	trimHistory(ctx, "智商测试", func() (int64, error) {
		return s.iqRepo.DeleteBeyondLatest(ctx, ModelTestHistoryKeep)
	})
}

func trimHistory(ctx context.Context, name string, deleteFn func() (int64, error)) {
	if err := ctx.Err(); err != nil {
		log.Printf("[detect] 裁剪%s历史跳过: %v", name, err)
		return
	}
	n, err := deleteFn()
	if err != nil {
		log.Printf("[detect] 裁剪%s历史失败: %v", name, err)
		return
	}
	if n > 0 {
		log.Printf("[detect] %s历史仅保留最新 %d 条，删除 %d 行", name, ModelTestHistoryKeep, n)
	}
}
