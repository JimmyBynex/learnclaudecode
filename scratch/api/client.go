package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"scratch/query"
	"strings"
)

type Role string

const (
	anthropicAPIURL     = "https://cc.codesome.ai/v1/messages"
	anthropicAPIVersion = "2023-06-01"
)

// toAPIContent converts a slices of contentBlock to the JSON-serialisable representation
func toAPIContent(blocks []query.ContentBlock) []map[string]any {
	out := make([]map[string]any, 0, len(blocks))
	for _, b := range blocks {
		switch v := b.(type) {
		case query.TextBlock:
			out = append(out, map[string]any{
				"type": "text",
				"text": v.Text,
			})
		case query.ToolUseBlock:
			out = append(out, map[string]any{
				"type":  "tool_use",
				"id":    v.ID,
				"name":  v.Name,
				"input": v.Input,
			})
		case query.ToolResultBlock:
			out = append(out, map[string]any{
				"type":        "tool_result",
				"tool_use_id": v.ToolUseID,
				"content":     v.Content,
			})
		case query.ThinkingBlock:
			out = append(out, map[string]any{
				"type":      "thinking",
				"thinking":  v.Thinking,
				"signature": v.Signature,
			})
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

func toAPISystem(system []query.SystemPart) []map[string]any {
	out := make([]map[string]any, 0, len(system))
	for _, sys := range system {
		m := map[string]any{
			"type": sys.Type,
			"text": sys.Text,
		}
		if sys.CacheControl != nil {
			m["cache_control"] = map[string]any{"type": sys.CacheControl.Type}
		}
		out = append(out, m)
	}
	return out
}

func toAPITools(tools []query.ToolDefinition) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		out = append(out, map[string]any{
			"name":         tool.Name,
			"description":  tool.Description,
			"input_schema": tool.InputSchema,
		})
	}
	return out
}

func CallModel(ctx context.Context, p query.CallModelParams) (<-chan query.StreamEvent, error) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("ANTHROPIC environment variable is not set")
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
	if len(p.Tools) > 0 {
		body["tools"] = toAPITools(p.Tools)
	}
	if len(p.System) > 0 {
		body["system"] = toAPISystem(p.System)
	}
	body["messages"] = toAPIMessages(p.Messages)
	bodyBytes, err := json.Marshal(body)

	if err != nil {
		return nil, fmt.Errorf("marshal request body :%w", err)
	}
	//为什么byte还要放进缓冲流，可以详细了解http/tcp运作逻辑
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicAPIURL, bytes.NewReader(bodyBytes))
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", anthropicAPIVersion)
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http do :%w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error: %d", resp.StatusCode)
	}

	ch := make(chan query.StreamEvent, 64)
	go func() {
		defer resp.Body.Close()
		defer close(ch)
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 1<<20), 1<<20)
		for {
			//先是读取原始字符串
			ev, err := readSSE(scanner)
			if err != nil {
				ch <- query.StreamEvent{Type: "error"}
				return
			}
			//如果返回为nil，正常结束
			if ev == nil {
				return
			}

			//再转成streamEvent
			se, err := parseStreamEvent(ev.data)
			if err != nil {
				ch <- query.StreamEvent{Type: "error"}
				return
			}
			//select+case实现非阻塞传输和接受ctx
			select {
			case ch <- *se:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

type sseEvent struct {
	name string
	data string
}

func readSSE(scanner *bufio.Scanner) (*sseEvent, error) {
	var se sseEvent
	for scanner.Scan() {
		//这就是scanner的好处，能够自己实现/n分割
		line := scanner.Text()
		//sse协议规定，每个event传输之间一定会有空行
		//至于为什么要continue，因为有可能流开头或者是心跳行，因此当检测到空行的时候，要判断当前的状态
		if line == "" {
			//保证有数据
			if se.name != "" && se.data != "" {
				return &se, nil
			}
			continue
		}
		if strings.HasPrefix(line, "event: ") {
			se.name = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "data: ") {
			se.data = strings.TrimPrefix(line, "data: ")
		}
		if err := scanner.Err(); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

// rawEvent is the top-level JSON structure of an SSE data payload.
// 可以看看anthropic返回的block示例，使用指针是因为delta和contentBlock，message不是同时存在
type rawEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	// content_block_start carries tool metadata here
	ContentBlock *struct {
		Type string `json:"type"` //text||tool_use||thinking
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

func parseStreamEvent(data string) (*query.StreamEvent, error) {
	var rawEv rawEvent
	err := json.Unmarshal([]byte(data), &rawEv)
	if err != nil {
		return nil, fmt.Errorf("data unmarshal: %w", err)
	}
	ev := query.StreamEvent{
		Type:  rawEv.Type,
		Index: rawEv.Index,
	}
	//只对contentBlock，进行深处理，其他类型的主看type就够了
	switch rawEv.Type {
	case "content_block_delta":
		ev.Delta = &query.Delta{
			Type:        rawEv.Delta.Type,
			Text:        rawEv.Delta.Text,
			Thinking:    rawEv.Delta.Thinking,
			PartialJSON: rawEv.Delta.PartialJSON,
		}
	case "content_block_start":
		ev.BlockMeta = &query.BlockMeta{
			Type: rawEv.ContentBlock.Type,
			ID:   rawEv.ContentBlock.ID,
			Name: rawEv.ContentBlock.Name,
		}
	}
	return &ev, nil
}
