package query

import (
	"context"
	"testing"
)

// ── TestRepairTrajectory ──────────────────────────────────────────────────────

// TestRepairTrajectory verifies that RepairTrajectory injects synthetic
// tool_result messages for every tool_use that has no corresponding result.
func TestRepairTrajectory(t *testing.T) {
	t.Run("missing tool_result gets synthetic result", func(t *testing.T) {
		assistantUUID := "asst-1"
		msgs := []Message{
			{
				Role: RoleUser,
				Content: []ContentBlock{
					TextBlock{Type: "text", Text: "run echo hello"},
				},
				UUID: "user-1",
			},
			{
				Role: RoleAssistant,
				Content: []ContentBlock{
					ToolUseBlock{
						Type:  "tool_use",
						ID:    "tu-1",
						Name:  "Bash",
						Input: map[string]any{"command": "echo hello"},
					},
				},
				UUID:       assistantUUID,
				ParentUUID: "user-1",
			},
			// No tool_result message — this is the incomplete trajectory.
		}

		repaired := RepairTrajectory(msgs)

		// We should now have 3 messages (original 2 + 1 synthetic result).
		if len(repaired) != 3 {
			t.Fatalf("expected 3 messages after repair, got %d", len(repaired))
		}

		last := repaired[2]
		if last.Role != RoleUser {
			t.Fatalf("expected synthetic message role=user, got %q", last.Role)
		}
		if len(last.Content) != 1 {
			t.Fatalf("expected 1 content block in synthetic message, got %d", len(last.Content))
		}
		tr, ok := last.Content[0].(ToolResultBlock)
		if !ok {
			t.Fatalf("expected ToolResultBlock, got %T", last.Content[0])
		}
		if tr.ToolUseID != "tu-1" {
			t.Errorf("expected ToolUseID=tu-1, got %q", tr.ToolUseID)
		}
		if !tr.IsError {
			t.Error("expected synthetic tool_result to be an error")
		}
	})

	t.Run("already resolved tool_use is not duplicated", func(t *testing.T) {
		msgs := []Message{
			{
				Role: RoleUser,
				Content: []ContentBlock{
					TextBlock{Type: "text", Text: "run echo hello"},
				},
				UUID: "user-1",
			},
			{
				Role: RoleAssistant,
				Content: []ContentBlock{
					ToolUseBlock{
						Type:  "tool_use",
						ID:    "tu-2",
						Name:  "Bash",
						Input: map[string]any{"command": "echo hello"},
					},
				},
				UUID:       "asst-2",
				ParentUUID: "user-1",
			},
			{
				Role: RoleUser,
				Content: []ContentBlock{
					ToolResultBlock{
						Type:      "tool_result",
						ToolUseID: "tu-2",
						Content:   "hello\n",
						IsError:   false,
					},
				},
				UUID:       "user-2",
				ParentUUID: "asst-2",
			},
		}

		repaired := RepairTrajectory(msgs)
		if len(repaired) != 3 {
			t.Fatalf("expected 3 messages (no synthetic needed), got %d", len(repaired))
		}
	})

	t.Run("empty message list returns empty", func(t *testing.T) {
		repaired := RepairTrajectory(nil)
		if len(repaired) != 0 {
			t.Fatalf("expected 0 messages, got %d", len(repaired))
		}
	})
}

// ── TestQueryLoopBasic ────────────────────────────────────────────────────────

// mockCallModel creates a call-model function that serves a predetermined
// sequence of event channels.
func mockCallModel(rounds [][]StreamEvent) func(ctx context.Context, p CallModelParams) (<-chan StreamEvent, error) {
	call := 0
	return func(ctx context.Context, p CallModelParams) (<-chan StreamEvent, error) {
		idx := call
		call++
		ch := make(chan StreamEvent, len(rounds[idx])+1)
		go func() {
			defer close(ch)
			for _, ev := range rounds[idx] {
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

// toolUseRound returns a sequence of StreamEvents that simulates the API
// returning one tool_use block (Bash) and then stopping.
func toolUseRound(toolID, cmd string) []StreamEvent {
	return []StreamEvent{
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

// textRound returns a sequence of StreamEvents that simulates the API
// returning a single text block and then stopping (terminal turn).
func textRound(text string) []StreamEvent {
	return []StreamEvent{
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

// minimalBashTool is a lightweight Bash tool used only for the query loop test.
type minimalBashTool struct{}

func (m *minimalBashTool) Name() string                    { return "Bash" }
func (m *minimalBashTool) Description(_ map[string]any) string { return "Run a bash command." }
func (m *minimalBashTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{"type": "string"},
		},
		"required": []string{"command"},
	}
}
func (m *minimalBashTool) ValidateInput(input map[string]any, _ *ToolUseContext) ValidationResult {
	if _, ok := input["command"].(string); !ok {
		return ValidationResult{OK: false, Message: "command must be a string"}
	}
	return ValidationResult{OK: true}
}
func (m *minimalBashTool) CheckPermissions(_ map[string]any, _ *ToolUseContext) PermissionDecision {
	return PermissionDecision{Behavior: PermAllow}
}
func (m *minimalBashTool) IsReadOnly(_ map[string]any) bool { return false }
func (m *minimalBashTool) Call(ctx context.Context, input map[string]any, _ *ToolUseContext, _ ToolCallProgress) (ToolResult, error) {
	cmd, _ := input["command"].(string)
	return ToolResult{Content: "output of: " + cmd}, nil
}

// TestQueryLoopBasic verifies that Query:
//  1. Handles a tool_use response by executing Bash and feeding the result back.
//  2. Returns Terminal.Reason == "completed" when the model's second response
//     contains no tool calls.
func TestQueryLoopBasic(t *testing.T) {
	// Round 0: model returns a Bash tool_use.
	// Round 1: model returns plain text (no tool calls) → completed.
	mock := mockCallModel([][]StreamEvent{
		toolUseRound("tool-abc", "echo hello"),
		textRound("The command output was: hello"),
	})

	// Temporarily replace the callModelFn so we don't need a real API key.
	orig := callModelFn
	callModelFn = mock
	defer func() { callModelFn = orig }()

	registry := NewToolRegistry([]Tool{&minimalBashTool{}}, nil)

	ctx := context.Background()
	params := QueryParams{
		Messages: []Message{
			{
				Role:    RoleUser,
				Content: []ContentBlock{TextBlock{Type: "text", Text: "run echo hello"}},
				UUID:    "user-1",
			},
		},
		ToolCtx:  &ToolUseContext{Tools: registry},
		MaxTurns: 5,
		Model:    "claude-sonnet-4-6",
	}

	msgCh, termCh := Query(ctx, params)

	// Drain all messages.
	var msgs []Message
	for m := range msgCh {
		msgs = append(msgs, m)
	}

	term := <-termCh

	if term.Reason != TerminalCompleted {
		t.Errorf("expected Terminal.Reason=%q, got %q (err=%v)", TerminalCompleted, term.Reason, term.Error)
	}

	// We should have received at least the assistant message and the tool-result user message.
	if len(msgs) < 3 {
		t.Errorf("expected at least 3 messages, got %d", len(msgs))
	}
}
