package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/learnclaudecode/claude-go/src/phase-03-cache/query"
)

const (
	webSearchAPIURL     = "https://api.anthropic.com/v1/messages"
	webSearchAPIVersion = "2023-06-01"
	webSearchModel      = "claude-haiku-4-5"
)

// WebSearchTool uses the Anthropic web_search_20250305 built-in tool to
// perform web searches via the Claude API.
type WebSearchTool struct{}

// NewWebSearchTool returns a new WebSearchTool instance.
func NewWebSearchTool() *WebSearchTool { return &WebSearchTool{} }

func (t *WebSearchTool) Name() string { return "WebSearch" }

func (t *WebSearchTool) Description(_ map[string]any) string {
	return "Searches the web using the Anthropic web_search tool and returns results."
}

func (t *WebSearchTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "The search query.",
			},
		},
		"required": []string{"query"},
	}
}

func (t *WebSearchTool) ValidateInput(input map[string]any, _ *query.ToolUseContext) query.ValidationResult {
	q, ok := input["query"].(string)
	if !ok || q == "" {
		return query.ValidationResult{OK: false, Message: "query must be a non-empty string"}
	}
	return query.ValidationResult{OK: true}
}

func (t *WebSearchTool) CheckPermissions(_ map[string]any, _ *query.ToolUseContext) query.PermissionDecision {
	return query.PermissionDecision{Behavior: query.PermAllow}
}

func (t *WebSearchTool) IsReadOnly(_ map[string]any) bool { return true }

func (t *WebSearchTool) Call(ctx context.Context, input map[string]any, _ *query.ToolUseContext, _ query.ToolCallProgress) (query.ToolResult, error) {
	q, _ := input["query"].(string)

	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return query.ToolResult{
			Content: "ANTHROPIC_API_KEY not set; cannot perform web search",
			IsError: true,
		}, nil
	}

	// Build the request body using the Anthropic web_search_20250305 built-in tool.
	reqBody := map[string]any{
		"model":      webSearchModel,
		"max_tokens": 1024,
		"tools": []map[string]any{
			{
				"type": "web_search_20250305",
				"name": "web_search",
			},
		},
		"messages": []map[string]any{
			{
				"role": "user",
				"content": fmt.Sprintf("Search the web for: %s. Summarize the top results.", q),
			},
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return query.ToolResult{
			Content: fmt.Sprintf("error marshaling request: %v", err),
			IsError: true,
		}, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webSearchAPIURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return query.ToolResult{
			Content: fmt.Sprintf("error creating request: %v", err),
			IsError: true,
		}, nil
	}
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", webSearchAPIVersion)
	req.Header.Set("anthropic-beta", "web-search-2025-03-05")
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return query.ToolResult{
			Content: fmt.Sprintf("error performing search request: %v", err),
			IsError: true,
		}, nil
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return query.ToolResult{
			Content: fmt.Sprintf("error reading search response: %v", err),
			IsError: true,
		}, nil
	}

	if resp.StatusCode != http.StatusOK {
		return query.ToolResult{
			Content: fmt.Sprintf("search API error (HTTP %d): %s", resp.StatusCode, string(respBytes)),
			IsError: true,
		}, nil
	}

	// Parse the API response to extract text content.
	var apiResp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respBytes, &apiResp); err != nil {
		return query.ToolResult{
			Content: fmt.Sprintf("error parsing search response: %v\nraw: %s", err, string(respBytes)),
			IsError: true,
		}, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Search results for: %s\n\n", q))
	for _, block := range apiResp.Content {
		if block.Type == "text" && block.Text != "" {
			sb.WriteString(block.Text)
			sb.WriteByte('\n')
		}
	}

	result := sb.String()
	if strings.TrimSpace(result) == fmt.Sprintf("Search results for: %s", q) {
		result += "(no text results returned)"
	}

	return query.ToolResult{Content: result}, nil
}
