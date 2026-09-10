package handler

import (
	"testing"
	"time"

	"sub2api-account-monitor/internal/repository"
)

func TestTaskDurationMSFromCreatedAndUpdated(t *testing.T) {
	created := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	updated := created.Add(8*time.Second + 200*time.Millisecond)

	got := taskDurationMS(repository.MediaTask{
		Status:    repository.MediaStatusSucceeded,
		CreatedAt: created.Format(time.RFC3339Nano),
		UpdatedAt: updated.Format(time.RFC3339Nano),
	})
	if got != 8200 {
		t.Fatalf("成功任务耗时应为 8200ms，实得 %d", got)
	}
}

func TestTaskDurationMSPendingUsesNow(t *testing.T) {
	created := time.Now().UTC().Add(-3 * time.Second)
	got := taskDurationMS(repository.MediaTask{
		Status:    repository.MediaStatusPending,
		CreatedAt: created.Format(time.RFC3339Nano),
		UpdatedAt: created.Format(time.RFC3339Nano),
	})
	if got < 2500 || got > 5000 {
		t.Fatalf("进行中任务耗时应约 3s，实得 %d", got)
	}
}

func TestTaskDurationMSParsesPostgresTextTimestamp(t *testing.T) {
	got := taskDurationMS(repository.MediaTask{
		Status:    repository.MediaStatusSucceeded,
		CreatedAt: "2026-09-05 12:00:00.000000+00",
		UpdatedAt: "2026-09-05 12:00:08.200000+00",
	})
	if got != 8200 {
		t.Fatalf("应能解析 PG 文本时间戳，实得 %d", got)
	}
}

func TestToMediaTaskDTOExposesDurationAndRFC3339CreatedAt(t *testing.T) {
	created := time.Date(2026, 9, 5, 4, 5, 6, 0, time.UTC)
	updated := created.Add(12 * time.Second)
	dto := toMediaTaskDTO(repository.MediaTask{
		ID:        9,
		Status:    repository.MediaStatusSucceeded,
		CreatedAt: created.Format(time.RFC3339Nano),
		UpdatedAt: updated.Format(time.RFC3339Nano),
	}, nil)
	if dto.DurationMS != 12000 {
		t.Fatalf("DTO 应下发 duration_ms=12000，实得 %d", dto.DurationMS)
	}
	if dto.CreatedAt != "2026-09-05T04:05:06Z" {
		t.Fatalf("created_at 应归一成 RFC3339，实得 %q", dto.CreatedAt)
	}
}
