package integration

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/learnclaudecode/claude-go/src/phase-02-tools/query"
	"github.com/learnclaudecode/claude-go/src/phase-02-tools/tools"
)

// buildFileReadToolUseEvents returns SSE events simulating the API asking for
// a FileRead (Read) tool call.
func buildFileReadToolUseEvents(toolID, filePath string) []query.StreamEvent {
	inputJSON, _ := json.Marshal(map[string]any{"file_path": filePath})
	return []query.StreamEvent{
		{
			Type:  "content_block_start",
			Index: 0,
			BlockMeta: &struct{ Type, ID, Name string }{
				Type: "tool_use",
				ID:   toolID,
				Name: "Read",
			},
		},
		{
			Type:  "content_block_delta",
			Index: 0,
			Delta: &struct{ Type, Text, PartialJSON, Thinking string }{
				Type:        "input_json_delta",
				PartialJSON: string(inputJSON),
			},
		},
		{Type: "content_block_stop", Index: 0},
		{Type: "message_stop"},
	}
}

// buildTextEvents returns SSE events for a simple text response (terminal turn).
func buildTextEvents(text string) []query.StreamEvent {
	return []query.StreamEvent{
		{
			Type:  "content_block_start",
			Index: 0,
			BlockMeta: &struct{ Type, ID, Name string }{
				Type: "text",
			},
		},
		{
			Type:  "content_block_delta",
			Index: 0,
			Delta: &struct{ Type, Text, PartialJSON, Thinking string }{
				Type: "text_delta",
				Text: text,
			},
		},
		{Type: "content_block_stop", Index: 0},
		{Type: "message_stop"},
	}
}

// mockTwoRounds creates a mock that serves exactly two rounds of events.
func mockTwoRounds(round0, round1 []query.StreamEvent) func(ctx context.Context, p query.CallModelParams) (<-chan query.StreamEvent, error) {
	call := 0
	return func(ctx context.Context, p query.CallModelParams) (<-chan query.StreamEvent, error) {
		idx := call
		call++
		var events []query.StreamEvent
		switch idx {
		case 0:
			events = round0
		case 1:
			events = round1
		default:
			ch := make(chan query.StreamEvent)
			close(ch)
			return ch, nil
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
}

// TestToolExecutionPipeline verifies the full tool_use → tool_result pipeline:
// 1. Mock API returns a Read tool_use.
// 2. The query loop calls FileReadTool.
// 3. The tool_result message contains the file content.
// 4. The mock API's second response is a plain text → Terminal.Completed.
func TestToolExecutionPipeline(t *testing.T) {
	// Create a temp file with known content.
	f, err := os.CreateTemp(t.TempDir(), "phase2-integ-*.txt")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	const fileContent = "integration test file content\nline 2\n"
	if _, err := f.WriteString(fileContent); err != nil {
		t.Fatalf("WriteString: %v", err)
	}
	f.Close()
	filePath := f.Name()

	toolID := "read-tool-1"
	round0 := buildFileReadToolUseEvents(toolID, filePath)
	round1 := buildTextEvents("Done reading the file.")

	mock := mockTwoRounds(round0, round1)

	origCallModel := query.CallModel
	query.SetCallModel(mock)
	defer func() { query.SetCallModel(origCallModel) }()

	registry := tools.NewDefaultRegistry(nil)

	ctx := context.Background()
	params := query.QueryParams{
		Messages: []query.Message{
			{
				Role:    query.RoleUser,
				Content: []query.ContentBlock{query.TextBlock{Type: "text", Text: "read the file"}},
				UUID:    "user-1",
			},
		},
		ToolCtx:  &query.ToolUseContext{Tools: registry},
		MaxTurns: 5,
		Model:    "claude-sonnet-4-6",
	}

	msgCh, termCh := query.Query(ctx, params)

	var allMessages []query.Message
	for m := range msgCh {
		allMessages = append(allMessages, m)
	}
	term := <-termCh

	if term.Reason != query.TerminalCompleted {
		t.Errorf("expected Terminal.Reason=completed, got %q (err=%v)", term.Reason, term.Error)
	}

	// Find the tool_result message and verify it contains the file content.
	foundToolResult := false
	for _, msg := range allMessages {
		if msg.Role != query.RoleUser {
			continue
		}
		for _, block := range msg.Content {
			tr, ok := block.(query.ToolResultBlock)
			if !ok {
				continue
			}
			if tr.ToolUseID != toolID {
				continue
			}
			foundToolResult = true
			content, ok := tr.Content.(string)
			if !ok {
				t.Errorf("tool_result Content is not a string: %T", tr.Content)
				continue
			}
			if !strings.Contains(content, "integration test file content") {
				t.Errorf("expected file content in tool_result, got: %q", content)
			}
			if tr.IsError {
				t.Errorf("tool_result should not be an error, content: %q", content)
			}
		}
	}

	if !foundToolResult {
		t.Error("no tool_result block found for the Read tool call")
	}
}
