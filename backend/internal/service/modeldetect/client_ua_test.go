package modeldetect

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Reproduce gateways that serve HTML 404 to browser UAs on Messages.
func TestCaptureBaselineWithCLIGatedUpstream(t *testing.T) {
	for _, tc := range []struct {
		name   string
		extra  map[string]string
		wantUA string
	}{
		{name: "default", wantUA: "claude-cli/2.1.0 (external, cli)"},
		{name: "explicit override", extra: map[string]string{"User-Agent": "custom-client/1.0"}, wantUA: "custom-client/1.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				if strings.HasPrefix(r.UserAgent(), "Mozilla/") {
					http.Error(w, "<html>404 Not Found</html>", http.StatusNotFound)
					return
				}
				if r.UserAgent() != tc.wantUA {
					t.Errorf("User-Agent=%q, want %q", r.UserAgent(), tc.wantUA)
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"content":[{"type":"text","text":"2"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":20}}`)
			}))
			defer srv.Close()
			c := NewClient(Target{BaseURL: srv.URL, APIKey: "test-secret", AuthMode: AuthAPIKey, Model: "claude-fable-5", Timeout: time.Second, ExtraHeaders: tc.extra})
			stats, ex := CaptureBaseline(context.Background(), c)
			if !ex.OK() || stats == nil || !stats.QualityOK {
				t.Fatalf("baseline failed: HTTP %d, stats=%+v, response=%s", ex.Status, stats, ex.Raw)
			}
			if ex.RequestHeaders["user-agent"] != tc.wantUA {
				t.Errorf("recorded UA=%q", ex.RequestHeaders["user-agent"])
			}
		})
	}
}
