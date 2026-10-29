package service

import (
	"context"
	"testing"
)

func TestTrimHistoryWithoutStore(t *testing.T) {
	s := NewModelDetectService(nil, nil, nil, nil)
	s.TrimDetectionHistory(context.Background())

	var none *ModelDetectService
	none.TrimDetectionHistory(context.Background())
}
