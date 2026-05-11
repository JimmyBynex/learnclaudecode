package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/learnclaudecode/claude-go/src/phase-03-cache/cache"
	"github.com/learnclaudecode/claude-go/src/phase-03-cache/query"
	"github.com/learnclaudecode/claude-go/src/phase-03-cache/tools"
)

// buildLargeToolUseEvents returns SSE events simulating the API requesting a
// Bash tool call whose output will be large enough to exceed the replacement threshold.
func buildLargeToolUseEvents(toolID, cmd string) []query.StreamEvent {
	return []query.StreamEvent{
		{
			Type:  "content_block_start",
			Index: 0,
			BlockMeta: &struct{ Type, ID, Name string }{
				Type: "tool_use",
				ID:   toolID,
				Name: "Bash",
			},
		},
		{
			Type:  "content_block_delta",
			Index: 0,
			Delta: &struct{ Type, Text, PartialJSON, Thinking string }{
				Type:        "input_json_delta",
				PartialJSON: `{"command":"` + cmd + `"}`,
			},
		},
		{Type: "content_block_stop", Index: 0},
		{Type: "message_stop"},
	}
}

// largeBashTool is a Bash tool stub that returns a tool result exceeding the
// cache replacement threshold, simulating a real large command output.
type largeBashTool struct {
	output string // pre-configured output returned by Call
}

func (t *largeBashTool) Name() string                        { return "Bash" }
func (t *largeBashTool) Description(_ map[string]any) string { return "Run a bash command." }
func (t *largeBashTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{"type": "string"},
		},
		"required": []string{"command"},
	}
}
func (t *largeBashTool) ValidateInput(input map[string]any, _ *query.ToolUseContext) query.ValidationResult {
	if _, ok := input["command"].(string); !ok {
		return query.ValidationResult{OK: false, Message: "command must be a string"}
	}
	return query.ValidationResult{OK: true}
}
func (t *largeBashTool) CheckPermissions(_ map[string]any, _ *query.ToolUseContext) query.PermissionDecision {
	return query.PermissionDecision{Behavior: query.PermAllow}
}
func (t *largeBashTool) IsReadOnly(_ map[string]any) bool { return false }
func (t *largeBashTool) Call(_ context.Context, _ map[string]any, _ *query.ToolUseContext, _ query.ToolCallProgress) (query.ToolResult, error) {
	return query.ToolResult{Content: t.output}, nil
}

// captureCallModel wraps a mock and records the CallModelParams for each call.
type captureCallModel struct {
	inner  func(ctx context.Context, p query.CallModelParams) (<-chan query.StreamEvent, error)
	params []query.CallModelParams
}

func (c *captureCallModel) call(ctx context.Context, p query.CallModelParams) (<-chan query.StreamEvent, error) {
	c.params = append(c.params, p)
	return c.inner(ctx, p)
}

// TestCachePrefixStability verifies that across two turns using the same
// tool_use_id, the content sent to the API is byte-for-byte identical.
//
// This is the central invariant of Phase 3: the cache prefix must not drift
// between requests, so the API can serve a cache hit on the second turn.
func TestCachePrefixStability(t *testing.T) {
	const toolID = "cache-stability-tool-1"

	// The tool will return content larger than DefaultPersistenceThreshold.
	largeOutput := strings.Repeat("C", cache.DefaultPersistenceThreshold+1000)

	// Round 0: API asks for a Bash tool call.
	// Round 1: API asks for the same Bash tool call again (re-uses same toolID).
	// Round 2: API returns plain text → terminal.
	round0 := buildLargeToolUseEvents(toolID, "generate large output")
	round1 := buildLargeToolUseEvents(toolID, "generate large output again")
	round2 := buildTextEvents("Cache stability verified.")

	callCount := 0
	rawMock := func(ctx context.Context, p query.CallModelParams) (<-chan query.StreamEvent, error) {
		idx := callCount
		callCount++
		var events []query.StreamEvent
		switch idx {
		case 0:
			events = round0
		case 1:
			events = round1
		default:
			events = round2
		}
		ch := make(chan query.StreamEvent, len(events)+1)
		go func() {
			defer close(ch)
			for _, ev := range events {
				select {
				case ch <- ev:
				case <-ctx.Done():
					return
				}
			}
		}()
		return ch, nil
	}

	capture := &captureCallModel{inner: rawMock}

	origCallModel := query.CallModel
	query.SetCallModel(capture.call)
	defer func() { query.SetCallModel(origCallModel) }()

	// Build a registry with the large-output Bash tool stub.
	bashTool := &largeBashTool{output: largeOutput}
	registry := tools.NewDefaultRegistry([]query.Tool{})
	// Replace the default registry with one using our large output tool.
	registry = query.NewToolRegistry([]query.Tool{bashTool}, nil)

	// Phase 3: attach a ContentReplacementState.
	replacementState := cache.NewContentReplacementState()

	ctx := context.Background()
	params := query.QueryParams{
		Messages: []query.Message{
			{
				Role:    query.RoleUser,
				Content: []query.ContentBlock{query.TextBlock{Type: "text", Text: "run the large command"}},
				UUID:    "user-1",
			},
		},
		ToolCtx: &query.ToolUseContext{
			Tools:            registry,
			ReplacementState: replacementState,
		},
		MaxTurns: 10,
		Model:    "claude-sonnet-4-6",
	}

	msgCh, termCh := query.Query(ctx, params)

	// Drain messages.
	for range msgCh {
	}
	term := <-termCh

	if term.Reason != query.TerminalCompleted {
		t.Fatalf("expected Terminal.Reason=completed, got %q (err=%v)", term.Reason, term.Error)
	}

	// We expect at least 3 API calls (round 0, round 1, round 2).
	if len(capture.params) < 3 {
		t.Fatalf("expected at least 3 API calls, got %d", len(capture.params))
	}

	// Extract the tool_result content for toolID from API calls 1 and 2
	// (after round 0 returned the tool_use, the tool result appears in rounds 1+).
	findToolResultContent := func(params query.CallModelParams, id string) (string, bool) {
		for _, msg := range params.Messages {
			for _, block := range msg.Content {
				tr, ok := block.(query.ToolResultBlock)
				if ok && tr.ToolUseID == id {
					if s, ok := tr.Content.(string); ok {
						return s, true
					}
				}
			}
		}
		return "", false
	}

	// The tool_result for toolID should appear starting from the second API call.
	contentAtCall1, found1 := findToolResultContent(capture.params[1], toolID)
	if !found1 {
		t.Fatal("tool_result for toolID not found in second API call")
	}

	// If there is a third API call (round 2), the same tool_result must appear
	// with byte-identical content.
	if len(capture.params) >= 3 {
		contentAtCall2, found2 := findToolResultContent(capture.params[2], toolID)
		if !found2 {
			t.Fatal("tool_result for toolID not found in third API call")
		}
		if contentAtCall1 != contentAtCall2 {
			t.Errorf(
				"cache prefix drift: tool_result content differs between API calls\n"+
					"call 1 content (len=%d): %q...\n"+
					"call 2 content (len=%d): %q...",
				len(contentAtCall1), contentAtCall1[:min3(80, len(contentAtCall1))],
				len(contentAtCall2), contentAtCall2[:min3(80, len(contentAtCall2))],
			)
		}
	}

	// Verify the content is the replacement reference, not the raw large output.
	if !strings.Contains(contentAtCall1, cache.PersistedOutputTag) {
		t.Errorf("expected tool_result to contain replacement reference, got: %q...",
			contentAtCall1[:min3(80, len(contentAtCall1))])
	}

	t.Logf("cache stability verified: tool_result content is stable across turns (len=%d)", len(contentAtCall1))
}

func min3(a, b int) int {
	if a < b {
		return a
	}
	return b
}
