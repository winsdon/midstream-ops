package handler

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"sub2api-account-monitor/internal/repository"
)

func TestBaselineDTOIncludesExchange(t *testing.T) {
	report, err := json.Marshal(map[string]any{
		"exchange": map[string]any{"method": "POST", "url": "https://x/v1/messages", "raw": "hello"},
	})
	if err != nil {
		t.Fatal(err)
	}
	dto := baselineDTO(&repository.ModelDetectionBaseline{
		ID: 1, TargetName: "ccmax", Model: "claude-opus-5", Report: report, CreatedAt: time.Now(),
	})
	raw, ok := dto["exchange"]
	if !ok {
		t.Fatal("dto missing exchange")
	}
	blob, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(blob), "v1/messages") {
		t.Fatalf("exchange=%s", blob)
	}
}
