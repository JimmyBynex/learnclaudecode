// Package api implements the Anthropic streaming messages API client.
package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/learnclaudecode/claude-go/src/phase-03-cache/query"
)

const (
	anthropicAPIURL     = "https://api.anthropic.com/v1/messages"
	anthropicAPIVersion = "2023-06-01"
)

// toAPIContent converts a slice of ContentBlock to the JSON-serialisable
// representation that the Anthropic API expects.
func toAPIContent(blocks []query.ContentBlock) []map[string]any {
	out := make([]map[string]any, 0, len(blocks))
	for _, b := range blocks {
		switch v := b.(type) {
		case query.TextBlock:
			out = append(out, map[string]any{
				"type": "text",
				"text": v.Text,
			})
		case query.ThinkingBlock:
			out = append(out, map[string]any{
				"type":      "thinking",
				"thinking":  v.Thinking,
				"signature": v.Signature,
			})
		case query.ToolUseBlock:
			out = append(out, map[string]any{
				"type":  "tool_use",
				"id":    v.ID,
				"name":  v.Name,
				"input": v.Input,
			})
		case query.ToolResultBlock:
			m := map[string]any{
				"type":        "tool_result",
				"tool_use_id": v.ToolUseID,
				"is_error":    v.IsError,
			}
			switch c := v.Content.(type) {
			case string:
				m["content"] = c
			default:
				m["content"] = c
			}
			out = append(out, m)
		}
	}
	return out
}

// toAPIMessages converts internal Messages to the JSON structure the API expects.
func toAPIMessages(msgs []query.Message) []map[string]any {
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, map[string]any{
			"role":    string(m.Role),
			"content": toAPIContent(m.Content),
		})
	}
	return out
}

// toAPISystem converts SystemPart slice to API JSON.
func toAPISystem(parts []query.SystemPart) []map[string]any {
	out := make([]map[string]any, 0, len(parts))
	for _, p := range parts {
		m := map[string]any{
			"type": p.Type,
			"text": p.Text,
		}
		if p.CacheControl != nil {
			m["cache_control"] = map[string]any{"type": p.CacheControl.Type}
		}
		out = append(out, m)
	}
	return out
}

// toAPITools converts ToolDefinition slice to API JSON.
func toAPITools(tools []query.ToolDefinition) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]any{
			"name":         t.Name,
			"description":  t.Description,
			"input_schema": t.InputSchema,
		})
	}
	return out
}

// sseEvent holds a parsed "event: …" / "data: …" pair from an SSE stream.
type sseEvent struct {
	name string
	data string
}

// readSSE reads one SSE event from the scanner.  Returns nil, nil at EOF.
func readSSE(scanner *bufio.Scanner) (*sseEvent, error) {
	var ev sseEvent
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if ev.name != "" || ev.data != "" {
				return &ev, nil
			}
			continue
		}
		if strings.HasPrefix(line, "event: ") {
			ev.name = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			ev.data = strings.TrimPrefix(line, "data: ")
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return nil, nil // EOF
}

// rawEvent is the top-level JSON structure of an SSE data payload.
type rawEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	// content_block_start carries tool metadata here
	ContentBlock *struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"content_block"`
	// content_block_delta and content_block_start (text) use delta
	Delta *struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
		Thinking    string `json:"thinking"`
	} `json:"delta"`
	// usage appears directly or nested under message
	Usage   *rawUsage `json:"usage"`
	Message *struct {
		Usage *rawUsage `json:"usage"`
	} `json:"message"`
}

type rawUsage struct {
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	CacheCreationTokens int `json:"cache_creation_input_tokens"`
	CacheReadTokens     int `json:"cache_read_input_tokens"`
}

// parseStreamEvent converts a raw SSE data payload to a query.StreamEvent.
//
// For content_block_start events the block's type / id / name are stored in
// BlockMeta.
func parseStreamEvent(data string) (*query.StreamEvent, error) {
	var raw rawEvent
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		return nil, fmt.Errorf("json unmarshal SSE: %w", err)
	}

	se := &query.StreamEvent{
		Type:  raw.Type,
		Index: raw.Index,
	}

	// Populate BlockMeta or Delta.
	switch raw.Type {
	case "content_block_start":
		if raw.ContentBlock != nil {
			se.BlockMeta = &struct{ Type, ID, Name string }{
				Type: raw.ContentBlock.Type,
				ID:   raw.ContentBlock.ID,
				Name: raw.ContentBlock.Name,
			}
		}
	case "content_block_delta":
		if raw.Delta != nil {
			se.Delta = &struct{ Type, Text, PartialJSON, Thinking string }{
				Type:        raw.Delta.Type,
				Text:        raw.Delta.Text,
				PartialJSON: raw.Delta.PartialJSON,
				Thinking:    raw.Delta.Thinking,
			}
		}
	}

	// Populate Usage.
	if raw.Usage != nil {
		se.Usage = &query.Usage{
			InputTokens:         raw.Usage.InputTokens,
			OutputTokens:        raw.Usage.OutputTokens,
			CacheCreationTokens: raw.Usage.CacheCreationTokens,
			CacheReadTokens:     raw.Usage.CacheReadTokens,
		}
	} else if raw.Message != nil && raw.Message.Usage != nil {
		u := raw.Message.Usage
		se.Usage = &query.Usage{
			InputTokens:         u.InputTokens,
			OutputTokens:        u.OutputTokens,
			CacheCreationTokens: u.CacheCreationTokens,
			CacheReadTokens:     u.CacheReadTokens,
		}
	}

	return se, nil
}

// CallModel opens a streaming POST to the Anthropic messages API and sends
// parsed SSE events on the returned channel.  The channel is closed when the
// stream ends or ctx is cancelled.
func CallModel(ctx context.Context, p query.CallModelParams) (<-chan query.StreamEvent, error) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY environment variable not set")
	}

	model := p.Model
	if model == "" {
		model = "claude-sonnet-4-6"
	}
	maxTokens := p.MaxTokens
	if maxTokens == 0 {
		maxTokens = 8192
	}

	body := map[string]any{
		"model":      model,
		"max_tokens": maxTokens,
		"stream":     true,
	}
	if len(p.System) > 0 {
		body["system"] = toAPISystem(p.System)
	}
	body["messages"] = toAPIMessages(p.Messages)
	if len(p.Tools) > 0 {
		body["tools"] = toAPITools(p.Tools)
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicAPIURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", anthropicAPIVersion)
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http do: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("API error: HTTP %d", resp.StatusCode)
	}

	ch := make(chan query.StreamEvent, 64)
	go func() {
		defer resp.Body.Close()
		defer close(ch)

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 1<<20), 1<<20)

		for {
			ev, err := readSSE(scanner)
			if err != nil {
				ch <- query.StreamEvent{Type: "error"}
				return
			}
			if ev == nil {
				return // EOF
			}
			if ev.data == "" || ev.data == "[DONE]" {
				continue
			}
			se, err := parseStreamEvent(ev.data)
			if err != nil {
				ch <- query.StreamEvent{Type: "error"}
				return
			}
			select {
			case ch <- *se:
			case <-ctx.Done():
				return
			}
		}
	}()

	return ch, nil
}
