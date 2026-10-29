package handler

import (
	"encoding/json"
	"strings"
	"sub2api-account-monitor/internal/repository"
	"testing"
)

func TestPelicanPublicDTOHasNoInternalFields(t *testing.T) {
	v := &repository.PelicanResult{ID: 1, BatchID: 9, GroupID: 3, Model: "test", Status: "completed", PelicanMetadata: repository.PelicanMetadata{KeyID: 777, Error: "private-error", GroupName: "public", Prompt: "prompt", Source: "account", AccountID: 7, AccountName: "pelican-account-secret", BaseURL: "https://internal.example.com", KeyFP: "fingerprint"}, Output: &repository.PelicanOutput{Document: "<svg></svg>", Kind: "svg", Text: "internal-original", Usage: json.RawMessage(`{"internal":true}`)}}
	raw, err := json.Marshal(publicPelican(v))
	if err != nil {
		t.Fatal(err)
	}
	// 所属账号只给管理端：客户接口连账号 id / 名称 / 地址都不能带出去。
	for _, secret := range []string{"key_id", "777", "batch_id", "private-error", "internal-original", "usage", "status", "account_name", "pelican-account-secret", "account_id", "base_url", "internal.example.com", "fingerprint"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("public DTO leaked %s: %s", secret, raw)
		}
	}
	if !strings.Contains(string(raw), "document") {
		t.Fatal("missing public artwork")
	}
}

// 糖果题结果客户也能看，但只带判定与答案：错误原因、耗时与回复全文都不下发。
func TestPelicanPublicDTOCandySummaryOnly(t *testing.T) {
	v := &repository.PelicanResult{ID: 1, Status: "completed", PelicanMetadata: repository.PelicanMetadata{
		GroupName: "public",
		Candy:     &repository.PelicanCandy{Status: "wrong", Answer: "29", Expected: "21", DurationMs: 4321, Error: "candy-private-error"},
	}, Output: &repository.PelicanOutput{Document: "<svg></svg>", Kind: "svg", CandyText: "candy-private-reply"}}
	raw, err := json.Marshal(publicPelican(v))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"candy":{"status":"wrong","answer":"29","expected":"21"}`) {
		t.Fatalf("public DTO must carry the candy verdict: %s", raw)
	}
	for _, secret := range []string{"candy-private-error", "candy-private-reply", "candy_text", "4321"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("public DTO leaked %s: %s", secret, raw)
		}
	}
	if raw, _ := json.Marshal(publicPelican(&repository.PelicanResult{ID: 2})); strings.Contains(string(raw), "candy") {
		t.Fatalf("records without the candy question must not show one: %s", raw)
	}
}
