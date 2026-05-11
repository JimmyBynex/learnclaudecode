package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"

	"github.com/learnclaudecode/claude-go/src/phase-03-cache/query"
)

const webFetchMaxBytes = 50 * 1024 // 50 KB

var htmlTagRe = regexp.MustCompile(`<[^>]+>`)
var whitespaceRe = regexp.MustCompile(`[ \t]+`)
var blankLineRe = regexp.MustCompile(`\n{3,}`)

// WebFetchTool fetches a URL and returns its content as plain text.
type WebFetchTool struct{}

// NewWebFetchTool returns a new WebFetchTool instance.
func NewWebFetchTool() *WebFetchTool { return &WebFetchTool{} }

func (t *WebFetchTool) Name() string { return "WebFetch" }

func (t *WebFetchTool) Description(_ map[string]any) string {
	return "Fetches content from a URL and returns it as plain text (HTML tags stripped)."
}

func (t *WebFetchTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "The URL to fetch.",
			},
			"prompt": map[string]any{
				"type":        "string",
				"description": "A description of what you want from the page.",
			},
		},
		"required": []string{"url", "prompt"},
	}
}

func (t *WebFetchTool) ValidateInput(input map[string]any, _ *query.ToolUseContext) query.ValidationResult {
	url, ok := input["url"].(string)
	if !ok || url == "" {
		return query.ValidationResult{OK: false, Message: "url must be a non-empty string"}
	}
	if _, ok := input["prompt"].(string); !ok {
		return query.ValidationResult{OK: false, Message: "prompt must be a string"}
	}
	return query.ValidationResult{OK: true}
}

func (t *WebFetchTool) CheckPermissions(_ map[string]any, _ *query.ToolUseContext) query.PermissionDecision {
	return query.PermissionDecision{Behavior: query.PermAllow}
}

func (t *WebFetchTool) IsReadOnly(_ map[string]any) bool { return true }

func (t *WebFetchTool) Call(ctx context.Context, input map[string]any, _ *query.ToolUseContext, _ query.ToolCallProgress) (query.ToolResult, error) {
	url, _ := input["url"].(string)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return query.ToolResult{
			Content: fmt.Sprintf("error creating request: %v", err),
			IsError: true,
		}, nil
	}
	req.Header.Set("User-Agent", "claude-code/1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return query.ToolResult{
			Content: fmt.Sprintf("error fetching URL: %v", err),
			IsError: true,
		}, nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, webFetchMaxBytes+1))
	if err != nil {
		return query.ToolResult{
			Content: fmt.Sprintf("error reading response: %v", err),
			IsError: true,
		}, nil
	}

	truncated := false
	if len(body) > webFetchMaxBytes {
		body = body[:webFetchMaxBytes]
		truncated = true
	}

	// Strip HTML tags to plain text.
	text := htmlTagRe.ReplaceAllString(string(body), " ")
	text = whitespaceRe.ReplaceAllString(text, " ")
	text = blankLineRe.ReplaceAllString(text, "\n\n")

	if truncated {
		text += "\n[content truncated: exceeded 50KB limit]"
	}

	return query.ToolResult{Content: text}, nil
}
