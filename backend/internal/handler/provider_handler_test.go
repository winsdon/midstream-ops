package handler

import (
	"testing"
	"time"

	"sub2api-account-monitor/internal/repository"
)

func TestFormatProviderTimeUsesConfiguredLocation(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}

	got := formatProviderTime(time.Date(2026, 7, 28, 16, 30, 0, 0, time.UTC), loc)
	if got != "2026-07-29 00:30:00" {
		t.Fatalf("formatProviderTime() = %q, want %q", got, "2026-07-29 00:30:00")
	}
}

func TestProviderDTOUsesConfiguredLocationForBalanceUpdate(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	balanceAt := time.Date(2026, 7, 28, 16, 30, 0, 0, time.UTC)

	dto := toDTO(&repository.Provider{LastBalanceAt: &balanceAt}, 0, loc)
	if dto.LastBalanceAt == nil || *dto.LastBalanceAt != "2026-07-29 00:30:00" {
		t.Fatalf("LastBalanceAt = %v, want %q", dto.LastBalanceAt, "2026-07-29 00:30:00")
	}
}

func TestSyncStateDTOUsesConfiguredLocation(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	lastSuccessAt := time.Date(2026, 7, 28, 16, 30, 0, 0, time.UTC)

	dto := toSyncStateDTO(repository.CollectorState{LastSuccessAt: &lastSuccessAt}, loc)
	if dto.LastSuccessAt == nil || *dto.LastSuccessAt != "2026-07-29 00:30:00" {
		t.Fatalf("LastSuccessAt = %v, want %q", dto.LastSuccessAt, "2026-07-29 00:30:00")
	}
}
