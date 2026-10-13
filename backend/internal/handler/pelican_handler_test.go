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
