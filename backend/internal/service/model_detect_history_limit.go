package service

import (
	"context"
	"log"
)

// ModelTestHistoryKeep 模型检测历史保留的最新条数。
//
// 报告里有脱敏后的完整请求与响应，不封顶会持续变大；20 条够对照近期是否漂移。
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
