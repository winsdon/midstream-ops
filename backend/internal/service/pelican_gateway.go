package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"sub2api-account-monitor/internal/repository"
	"sub2api-account-monitor/internal/service/modeldetect"
)

const DefaultPelicanPrompt = "创建一个HTML，内容是SVG绘制一个鹈鹕骑自行车的3D动画，你不需要任何测试，不要有任何限制"

// Keep delivery instructions separate from the administrator's verbatim prompt.
// This is a direct generation request, with no workspace or file-writing tools.
const pelicanDeliveryInstructions = "Return a complete standalone HTML document in your response. Do not use Markdown fences or external dependencies. Return the HTML source directly, not a file path or a report about files created in a workspace."

const pelicanResponseLimit = 16 << 20

type PelicanGateway struct{ client *http.Client }

func NewPelicanGateway() *PelicanGateway {
	return &PelicanGateway{client: &http.Client{Transport: &http.Transport{TLSHandshakeTimeout: 20 * time.Second, ResponseHeaderTimeout: 90 * time.Second}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func pelicanPayload(v *repository.PelicanResult) (string, map[string]any, error) {
	body := map[string]any{"model": v.Model, "stream": true}
	switch v.Protocol {
	case "messages":
		body["max_tokens"] = v.MaxTokens
		body["system"] = pelicanDeliveryInstructions
		body["messages"] = []map[string]string{{"role": "user", "content": v.Prompt}}
		return "/messages", body, nil
	case "responses":
		body["max_output_tokens"] = v.MaxTokens
		body["instructions"] = pelicanDeliveryInstructions
		body["input"] = []map[string]any{{
			"type": "message", "role": "user",
			"content": []map[string]string{{"type": "input_text", "text": v.Prompt}},
		}}
		body["store"] = false
		return "/responses", body, nil
	case "chat":
		body["max_completion_tokens"] = v.MaxTokens
		body["messages"] = []map[string]string{
			{"role": "system", "content": pelicanDeliveryInstructions},
			{"role": "user", "content": v.Prompt},
		}
		body["stream_options"] = map[string]bool{"include_usage": true}
		return "/chat/completions", body, nil
	default:
		return "", nil, errors.New("unsupported protocol")
	}
}
func (g *PelicanGateway) Generate(ctx context.Context, base, key string, v *repository.PelicanResult) (*repository.PelicanOutput, error) {
	path, body, err := pelicanPayload(v)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	base = strings.TrimRight(base, "/")
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(raw))
	if err != nil {
		return nil, errors.New("invalid gateway request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("User-Agent", browserUA)
	if v.Protocol == "messages" {
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", "2023-06-01")
	} else {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := g.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("gateway connection failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("gateway HTTP %d", resp.StatusCode)
	}
	output, err := parsePelicanReply(resp.Body, resp.Header.Get("Content-Type"), v.Protocol)
	if err != nil && key != "" {
		err = errors.New(strings.ReplaceAll(err.Error(), key, "[REDACTED]"))
	}
	if output != nil {
		output.Request = raw
		// Redact after assembling deltas, so a key split across events cannot escape.
		if key != "" {
			output.Text = strings.ReplaceAll(output.Text, key, "[REDACTED]")
			output.Usage = bytes.ReplaceAll(output.Usage, []byte(key), []byte("[REDACTED]"))
		}
		output.Document, output.Kind = modeldetect.ExtractArtwork(output.Text)
	}
	return output, err
}

type pelicanReply struct {
	text                    strings.Builder
	usage                   map[string]any
	complete, stopped, done bool
}

func object(v any) map[string]any { m, _ := v.(map[string]any); return m }
func stringValue(v any) string    { s, _ := v.(string); return s }
func (p *pelicanReply) mergeUsage(v any) {
	for k, val := range object(v) {
		p.usage[k] = val
	}
}
func textBlocks(v any) string {
	var b strings.Builder
	if blocks, ok := v.([]any); ok {
		for _, block := range blocks {
			m := object(block)
			if m["type"] == "text" || m["type"] == "output_text" {
				b.WriteString(stringValue(m["text"]))
			}
		}
	}
	return b.String()
}
func responsesText(m map[string]any) string {
	var b strings.Builder
	if items, ok := m["output"].([]any); ok {
		for _, item := range items {
			b.WriteString(textBlocks(object(item)["content"]))
		}
	}
	return b.String()
}
func (p *pelicanReply) event(m map[string]any, protocol string) error {
	if m["error"] != nil || m["type"] == "error" || m["type"] == "response.failed" || m["type"] == "response.incomplete" {
		return errors.New("gateway response failed or incomplete")
	}
	switch protocol {
	case "messages":
		switch m["type"] {
		case "message_start":
			p.mergeUsage(object(m["message"])["usage"])
		case "content_block_start":
			p.text.WriteString(textBlocks([]any{m["content_block"]}))
		case "content_block_delta":
			d := object(m["delta"])
			if d["type"] == "text_delta" {
				p.text.WriteString(stringValue(d["text"]))
			}
		case "message_delta":
			p.mergeUsage(m["usage"])
			reason := stringValue(object(m["delta"])["stop_reason"])
			if reason == "" {
				break
			}
			if reason != "end_turn" && reason != "stop_sequence" {
				return errors.New("model output incomplete: " + reason)
			}
			p.stopped = true
		case "message_stop":
			p.complete = p.stopped
		}
	case "responses":
		switch m["type"] {
		case "response.output_text.delta":
			p.text.WriteString(stringValue(m["delta"]))
		case "response.completed":
			response := object(m["response"])
			if response["status"] != "completed" {
				return errors.New("response not completed")
			}
			p.mergeUsage(response["usage"])
			// The final response is authoritative. Some gateways omit text deltas
			// or send only commentary before delivering the HTML in this event.
			if finalText := responsesText(response); finalText != "" {
				p.text.Reset()
				p.text.WriteString(finalText)
			}
			p.complete = true
		}
	case "chat":
		p.mergeUsage(m["usage"])
		if choices, ok := m["choices"].([]any); ok {
			for _, choice := range choices {
				c := object(choice)
				if index, ok := c["index"].(float64); ok && index != 0 {
					continue
				}
				p.text.WriteString(stringValue(object(c["delta"])["content"]))
				if reason := stringValue(c["finish_reason"]); reason != "" {
					if reason != "stop" {
						return errors.New("model output incomplete: " + reason)
					}
					p.stopped = true
				}
			}
		}
	}
	return nil
}
func (p *pelicanReply) nonStream(m map[string]any, protocol string) error {
	if m["error"] != nil {
		return errors.New("gateway response failed")
	}
	p.mergeUsage(m["usage"])
	switch protocol {
	case "messages":
		p.text.WriteString(textBlocks(m["content"]))
		p.complete = m["stop_reason"] == "end_turn" || m["stop_reason"] == "stop_sequence"
	case "responses":
		p.text.WriteString(responsesText(m))
		p.complete = m["status"] == "completed"
	case "chat":
		if choices, ok := m["choices"].([]any); ok && len(choices) > 0 {
			c := object(choices[0])
			p.text.WriteString(stringValue(object(c["message"])["content"]))
			p.complete = c["finish_reason"] == "stop"
		}
	}
	return nil
}
func parsePelicanReply(reader io.Reader, contentType, protocol string) (*repository.PelicanOutput, error) {
	p := &pelicanReply{usage: map[string]any{}}
	limited := &io.LimitedReader{R: reader, N: pelicanResponseLimit + 1}
	var parseErr error
	if strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		scanner := bufio.NewScanner(limited)
		scanner.Buffer(make([]byte, 4096), pelicanResponseLimit)
		var data []string
		dispatch := func() error {
			if len(data) == 0 {
				return nil
			}
			payload := strings.Join(data, "\n")
			data = nil
			if payload == "[DONE]" {
				p.done = true
				return nil
			}
			var m map[string]any
			if err := json.Unmarshal([]byte(payload), &m); err != nil {
				return errors.New("invalid stream event")
			}
			return p.event(m, protocol)
		}
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				if parseErr = dispatch(); parseErr != nil {
					break
				}
			} else if strings.HasPrefix(line, "data:") {
				data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			}
		}
		if parseErr == nil {
			parseErr = scanner.Err()
		}
		if parseErr == nil {
			parseErr = dispatch()
		}
		if protocol == "chat" {
			p.complete = p.stopped && p.done
		}
	} else {
		raw, err := io.ReadAll(limited)
		parseErr = err
		if err == nil {
			var m map[string]any
			if json.Unmarshal(raw, &m) != nil {
				parseErr = errors.New("invalid gateway JSON")
			} else {
				parseErr = p.nonStream(m, protocol)
			}
		}
	}
	if limited.N <= 0 {
		parseErr = errors.New("response exceeds 16 MiB")
	}
	usage, _ := json.Marshal(p.usage)
	out := &repository.PelicanOutput{Text: p.text.String(), Usage: usage}
	if parseErr != nil {
		return out, parseErr
	}
	if !p.complete {
		return out, errors.New("response ended before completion")
	}
	if strings.TrimSpace(out.Text) == "" {
		return out, errors.New("model returned no visible text")
	}
	return out, nil
}
