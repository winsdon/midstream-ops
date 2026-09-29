package service

import (
	"context"
	"errors"
	"testing"

	"sub2api-account-monitor/internal/repository"
)

func TestTrimHistoryWithoutStore(t *testing.T) {
	s := NewModelDetectService(nil, nil, nil, nil)
	s.TrimDetectionHistory(context.Background())
	s.TrimIQHistory(context.Background())
	if err := s.DeleteIQHistory(context.Background(), 1); !errors.Is(err, repository.ErrIQRunNotFound) {
		t.Fatalf("未注入智商测试存储时应视为记录不存在，实际 %v", err)
	}

	var none *ModelDetectService
	none.TrimDetectionHistory(context.Background())
	none.TrimIQHistory(context.Background())
	if err := none.DeleteIQHistory(context.Background(), 1); !errors.Is(err, repository.ErrIQRunNotFound) {
		t.Fatalf("未注入存储时应视为记录不存在，实际 %v", err)
	}
}
