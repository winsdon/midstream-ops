package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestNewAPIProvisionAuthAndDefaults(t *testing.T) {
	for _, auth := range []NewAPIAuth{{Cookie: "session=test", UserID: "7"}, {JWT: "jwt"}, {AccessToken: "pat", UserID: "7"}} {
		t.Run(fmt.Sprintf("%+v", auth), func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("Cookie") != auth.Cookie || r.Header.Get("Authorization") != map[bool]string{true: "Bearer " + auth.bearer(), false: ""}[auth.bearer() != ""] || r.Header.Get("New-Api-User") != auth.UserID {
					t.Error("authentication mismatch")
				}
				if r.Method == "POST" {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if body["name"] != "readable-unique" || body["group"] != "vip" || body["expired_time"] != float64(-1) || body["unlimited_quota"] != true || body["model_limits_enabled"] != false || body["allow_ips"] != "" {
						t.Errorf("defaults: %v", body)
					}
					if r.Header.Get("Content-Type") != "application/json" {
						t.Error("missing content type")
					}
				} else if r.Method != "DELETE" || r.URL.Path != "/api/token/42" {
					t.Error("incorrect delete route")
				}
				fmt.Fprint(w, `{"success":true}`)
			}))
			defer srv.Close()
			client := newAPITestClient()
			if err := client.CreateToken(context.Background(), srv.URL, auth, "readable-unique", "vip"); err != nil {
				t.Fatal(err)
			}
			if err := client.DeleteToken(context.Background(), srv.URL, auth, 42); err != nil {
				t.Fatal(err)
			}
			if calls != 2 {
				t.Fatalf("calls = %d", calls)
			}
		})
	}
}

func TestNewAPIProvisionTokenName(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		name, err := NewAPITokenName(strings.Repeat("中文分组", 30))
		if err != nil || len(name) > 50 || !utf8.ValidString(name) || !strings.HasPrefix(name, "中文分组") || seen[name] {
			t.Fatalf("invalid/nonunique name %q: %v", name, err)
		}
		seen[name] = true
	}
}

func TestNewAPIProvisionFindExactToken(t *testing.T) {
	for _, count := range []int{0, 1, 2} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				tokens := []NewAPIToken{{ID: 100, Name: "same", Group: "other"}, {ID: 101, Name: "same-old", Group: "vip"}}
				for i := 0; i < count; i++ {
					tokens = append(tokens, NewAPIToken{ID: int64(i + 1), Name: "same", Group: "vip"})
				}
				json.NewEncoder(w).Encode(map[string]any{"success": true, "data": tokens})
			}))
			defer srv.Close()
			token, err := newAPITestClient().FindToken(context.Background(), srv.URL, NewAPIAuth{}, "same", "vip")
			if (err == nil) != (count == 1) {
				t.Fatalf("count=%d err=%v", count, err)
			}
			if err == nil && token.ID != 1 {
				t.Fatalf("adopted wrong token %d", token.ID)
			}
		})
	}
}

func TestNewAPIProvisionKeyCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name, key, list string
		status          int
		want            bool
	}{
		{"raw", "abcdefghijklmnopqrstuvwxyz", "", 200, true},
		{"prefixed", "sk-abcdefghijklmnopqrstuvwxyz", "", 200, true},
		{"double-prefix", "sk-sk-abcdefghijklmnopqrstuvwxyz", "", 200, true},
		{"masked", "sk-abc********xyz", "abcdefghijklmnopqrstuvwxyz", 200, false},
		{"ellipsis", "sk-abcdef…xyzabcdef", "", 200, false},
		{"legacy", "", "abcdefghijklmnopqrstuvwxyz", 404, true},
		{"legacy-masked", "", "abcdef...xyzabcdef", 404, false},
		{"forbidden-no-fallback", "", "abcdefghijklmnopqrstuvwxyz", 403, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]string{"key": tc.key}})
			}))
			defer srv.Close()
			key, err := newAPITestClient().ProvisionTokenKey(context.Background(), srv.URL, NewAPIAuth{}, &NewAPIToken{ID: 42, Key: tc.list})
			if (err == nil) != tc.want {
				t.Fatalf("err=%v", err)
			}
			if tc.want && key != "sk-abcdefghijklmnopqrstuvwxyz" {
				t.Error("prefix normalization failed")
			}
		})
	}
}

func TestNewAPIProvisionDoesNotExposeUpstreamSecrets(t *testing.T) {
	for _, status := range []int{200, 400, 429, 500} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			fmt.Fprint(w, `{"success":false,"message":"secret-sensitive-key","data":{"key":"secret-sensitive-key"}}`)
		}))
		err := newAPITestClient().CreateToken(context.Background(), srv.URL, NewAPIAuth{}, "n", "g")
		srv.Close()
		if err == nil || strings.Contains(err.Error(), "secret-sensitive-key") {
			t.Fatalf("unsafe error: %v", err)
		}
	}
}
