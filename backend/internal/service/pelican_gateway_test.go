package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"sub2api-account-monitor/internal/repository"
)

func TestPelicanProtocols(t *testing.T) {
	cases := []struct {
		name, protocol, body, contentType string
		wantErr                           bool
	}{
		{"messages JSON", "messages", `{"content":[{"type":"text","text":"<svg></svg>"},{"type":"thinking","thinking":"private"}],"stop_reason":"end_turn","usage":{"input_tokens":12,"output_tokens":7}}`, "application/json", false},
		{"responses JSON", "responses", `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"<html></html>"}]}],"usage":{"output_tokens":8}}`, "application/json", false},
		{"chat JSON", "chat", `{"choices":[{"message":{"content":"<svg></svg>"},"finish_reason":"stop"}],"usage":{"completion_tokens":9}}`, "application/json", false},
		{"messages SSE", "messages", "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":12}}}\n\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"<svg></svg>\"}}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":7}}\n\ndata: {\"type\":\"message_stop\"}\n\n", "text/event-stream", false},
		{"responses SSE", "responses", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"<svg></svg>\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"output_tokens\":8}}}\n\n", "text/event-stream", false},
		{"chat SSE", "chat", "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"<svg></svg>\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"completion_tokens\":9}}\n\ndata: [DONE]\n\n", "text/event-stream", false},
		{"truncated messages", "messages", `{"content":[{"type":"text","text":"<svg></svg>"}],"stop_reason":"max_tokens"}`, "application/json", true},
		{"truncated responses", "responses", `{"status":"incomplete","output":[]}`, "application/json", true},
		{"truncated chat", "chat", `{"choices":[{"message":{"content":"<svg></svg>"},"finish_reason":"length"}]}`, "application/json", true},
		{"disconnected stream", "messages", "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"<svg></svg>\"}}\n\n", "text/event-stream", true},
		{"chat no terminator", "chat", "data: {\"choices\":[{\"delta\":{\"content\":\"<svg></svg>\"},\"finish_reason\":\"stop\"}]}\n\n", "text/event-stream", true},
		{"stream failure", "responses", "data: {\"type\":\"response.failed\"}\n\n", "text/event-stream", true},
		{"invalid event", "messages", "data: broken\n\n", "text/event-stream", true},
		{"error JSON", "chat", `{"error":{"message":"private upstream error"}}`, "application/json", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := parsePelicanReply(strings.NewReader(tc.body), tc.contentType, tc.protocol)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v", err)
			}
			if !tc.wantErr && (!strings.Contains(out.Text, "</") || strings.Contains(out.Text, "private") || len(out.Usage) == 0) {
				t.Fatalf("bad visible output: %+v", out)
			}
		})
	}
}
func TestPelicanGatewayRequestAndRedaction(t *testing.T) {
	for _, protocol := range []string{"messages", "responses", "chat"} {
		t.Run(protocol, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var body map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Error("invalid JSON")
				}
				if body["model"] != "test-model" || body["stream"] != true || body["thinking"] != nil {
					t.Errorf("unexpected payload %v", body)
				}
				prompt := ""
				instructions := ""
				switch protocol {
				case "responses":
					input := object(body["input"].([]any)[0])
					if input["role"] != "user" || input["type"] != "message" {
						t.Errorf("unexpected input %v", input)
					}
					prompt = stringValue(object(input["content"].([]any)[0])["text"])
					instructions = stringValue(body["instructions"])
				case "messages":
					prompt = stringValue(object(body["messages"].([]any)[0])["content"])
					instructions = stringValue(body["system"])
				case "chat":
					messages := body["messages"].([]any)
					if len(messages) != 2 || object(messages[0])["role"] != "system" || object(messages[1])["role"] != "user" {
						t.Fatalf("unexpected messages %v", messages)
					}
					instructions = stringValue(object(messages[0])["content"])
					prompt = stringValue(object(messages[1])["content"])
				}
				if instructions != pelicanDeliveryInstructions || !strings.Contains(instructions, "HTML source directly") {
					t.Errorf("missing HTML delivery instructions: %q", instructions)
				}
				if body["tools"] != nil || body["reasoning"] != nil {
					t.Errorf("unexpected tools or forced reasoning: %v", body)
				}
				if prompt != "  untouched prompt\n" {
					t.Errorf("modified prompt %q", prompt)
				}
				if protocol == "messages" {
					if r.Header.Get("x-api-key") != "secret-key" {
						t.Error("missing key")
					}
				} else if r.Header.Get("Authorization") != "Bearer secret-key" {
					t.Error("missing bearer")
				}
				expected := map[string]string{"messages": "/v1/messages", "responses": "/v1/responses", "chat": "/v1/chat/completions"}[protocol]
				if r.URL.Path != expected {
					t.Errorf("path %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				switch protocol {
				case "messages":
					fmt.Fprint(w, `{"content":[{"type":"text","text":"<svg>secret-key</svg>"}],"stop_reason":"end_turn"}`)
				case "responses":
					fmt.Fprint(w, `{"status":"completed","output":[{"content":[{"type":"output_text","text":"<svg>secret-key</svg>"}]}]}`)
				case "chat":
					fmt.Fprint(w, `{"choices":[{"message":{"content":"<svg>secret-key</svg>"},"finish_reason":"stop"}]}`)
				}
			}))
			defer server.Close()
			result := &repository.PelicanResult{Model: "test-model", PelicanMetadata: repository.PelicanMetadata{Protocol: protocol, Prompt: "  untouched prompt\n", MaxTokens: 32000}}
			out, err := NewPelicanGateway().Generate(context.Background(), server.URL+"/v1", "secret-key", result)
			if err != nil || out.Document != "<svg>[REDACTED]</svg>" || calls != 1 {
				t.Fatalf("result=%+v err=%v calls=%d", out, err, calls)
			}
			if !strings.Contains(string(out.Request), pelicanDeliveryInstructions) {
				t.Fatal("actual delivery instructions missing from request snapshot")
			}
		})
	}
}

func TestPelicanResponsesFinalOutput(t *testing.T) {
	const html = "<!DOCTYPE html><html><body><svg></svg></body></html>"
	for _, tc := range []struct {
		name, delta, final, want string
	}{
		{"final HTML after commentary", "我先查看工作区", html, html},
		{"final HTML without deltas", "", html, html},
		{"final text does not duplicate deltas", html, html, html},
		{"delta fallback", html, "", html},
		{"file report is not artwork", "已创建 [pelican-bicycle.html](/pelican-bicycle.html)", "", "已创建 [pelican-bicycle.html](/pelican-bicycle.html)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				delta, _ := json.Marshal(map[string]any{"type": "response.output_text.delta", "delta": tc.delta})
				final, _ := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{
					"status": "completed", "usage": map[string]int{"output_tokens": 42},
					"output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": tc.final}}}},
				}})
				fmt.Fprintf(w, "data: %s\n\ndata: %s\n\n", delta, final)
			}))
			defer server.Close()
			out, err := NewPelicanGateway().Generate(context.Background(), server.URL, "test-key", &repository.PelicanResult{
				Model: "test-model", PelicanMetadata: repository.PelicanMetadata{Protocol: "responses", Prompt: DefaultPelicanPrompt, MaxTokens: 32000},
			})
			if err != nil || out.Text != tc.want {
				t.Fatalf("output=%+v error=%v", out, err)
			}
			if tc.want == html && out.Document != html {
				t.Fatalf("final HTML lost: %q", out.Document)
			}
			if tc.want != html && out.Document != "" {
				t.Fatal("file report was incorrectly accepted as artwork")
			}
		})
	}
}
func TestPelicanGatewayCancellationAndHTTPFailure(t *testing.T) {
	t.Run("cancellation", func(t *testing.T) {
		release := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-release:
			}
		}))
		defer server.Close()
		defer close(release)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()
		_, err := NewPelicanGateway().Generate(ctx, server.URL, "secret", &repository.PelicanResult{PelicanMetadata: repository.PelicanMetadata{Protocol: "responses"}})
		if err == nil {
			t.Fatal("expected cancellation")
		}
	})
	t.Run("no automatic retries", func(t *testing.T) {
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; http.Error(w, "secret error", 429) }))
		defer server.Close()
		_, err := NewPelicanGateway().Generate(context.Background(), server.URL, "secret", &repository.PelicanResult{PelicanMetadata: repository.PelicanMetadata{Protocol: "chat"}})
		if err == nil || calls != 1 || strings.Contains(err.Error(), "secret") {
			t.Fatalf("calls %d err %v", calls, err)
		}
	})
}
func TestPelicanKeyCannotCrossGroupsOrProtocols(t *testing.T) {
	keys := []repository.PGUserKey{{ID: 1, GroupID: 2, Platform: "anthropic"}}
	for _, target := range []PelicanTarget{{KeyID: 1, GroupID: 3, Protocol: "messages"}, {KeyID: 1, GroupID: 2, Protocol: "chat"}, {KeyID: 7, GroupID: 2, Protocol: "messages"}} {
		if _, err := pelicanKey(keys, target); err == nil {
			t.Fatalf("accepted invalid target %+v", target)
		}
	}
	if _, err := pelicanKey(keys, PelicanTarget{KeyID: 1, GroupID: 2, Protocol: "messages"}); err != nil {
		t.Fatal(err)
	}
}
