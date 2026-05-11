// Package integration contains integration tests for Phase 1 of the
// claude-code Go rewrite.  All tests use a mock API and do not make real
// network calls.
package integration

import (
	"context"
	"testing"

	"github.com/learnclaudecode/claude-go/src/phase-02-tools/query"
	"github.com/learnclaudecode/claude-go/src/phase-02-tools/tools"
)

// ── mock helpers ──────────────────────────────────────────────────────────────

// buildToolUseEvents returns the SSE events for one tool_use round (Bash).
func buildToolUseEvents(toolID, cmd string) []query.StreamEvent {
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

// mockOneRound creates a CallModel mock that sends one round of events then
// blocks until ctx is cancelled (simulating an in-flight request that is aborted).
func mockOneRound(events []query.StreamEvent) func(ctx context.Context, p query.CallModelParams) (<-chan query.StreamEvent, error) {
	called := false
	return func(ctx context.Context, p query.CallModelParams) (<-chan query.StreamEvent, error) {
		ch := make(chan query.StreamEvent, len(events)+1)
		if !called {
			called = true
			// Send all events synchronously then close so the loop can process them.
			go func() {
				defer close(ch)
				for _, ev := range events {
					select {
					case ch <- ev:
					case <-ctx.Done():
						return
					}
				}
				// After sending events, block until ctx is cancelled so the caller
				// has a chance to cancel before the loop proceeds to a second turn.
				<-ctx.Done()
			}()
		} else {
			// Subsequent calls: just close immediately (should not happen in this test).
			close(ch)
		}
		return ch, nil
	}
}

// ── TestTrajectoryClose ───────────────────────────────────────────────────────

// TestTrajectoryClose verifies that when the context is cancelled after the
// model returns a tool_use block (but before tool results are fed back),
// RepairTrajectory produces a message list where every tool_use has a
// corresponding tool_result.
func TestTrajectoryClose(t *testing.T) {
	toolID := "integ-tool-1"
	events := buildToolUseEvents(toolID, "echo integration")

	// Use a cancellable context.  We cancel it after the assistant message
	// arrives so the loop aborts before the second API call.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // ensure cancel is always called (vet: no context leak)

	mock := mockOneRound(events)

	// Inject the mock.
	origCallModel := query.CallModel
	query.SetCallModel(mock)
	defer func() { query.SetCallModel(origCallModel) }()

	registry := tools.NewDefaultRegistry(nil)

	params := query.QueryParams{
		Messages: []query.Message{
			{
				Role: query.RoleUser,
				Content: []query.ContentBlock{
					query.TextBlock{Type: "text", Text: "run echo integration"},
				},
				UUID: "integ-user-1",
			},
		},
		ToolCtx:  &query.ToolUseContext{Tools: registry},
		MaxTurns: 5,
		Model:    "claude-sonnet-4-6",
	}

	msgCh, termCh := query.Query(ctx, params)

	// Cancel as soon as we receive the first assistant message (which contains
	// the tool_use block).
	var receivedMessages []query.Message
	for m := range msgCh {
		receivedMessages = append(receivedMessages, m)
		if m.Role == query.RoleAssistant {
			cancel()
		}
	}

	term := <-termCh

	// The terminal reason should be aborted.
	if term.Reason != query.TerminalAborted {
		t.Errorf("expected Terminal.Reason=%q, got %q", query.TerminalAborted, term.Reason)
	}

	// Now apply RepairTrajectory to the final message list.
	repaired := query.RepairTrajectory(term.Messages)

	// Verify that every tool_use has a matching tool_result.
	toolUseIDs := make(map[string]bool)
	toolResultIDs := make(map[string]bool)

	for _, msg := range repaired {
		for _, block := range msg.Content {
			switch b := block.(type) {
			case query.ToolUseBlock:
				toolUseIDs[b.ID] = true
			case query.ToolResultBlock:
				toolResultIDs[b.ToolUseID] = true
			}
		}
	}

	for id := range toolUseIDs {
		if !toolResultIDs[id] {
			t.Errorf("tool_use %q has no matching tool_result after RepairTrajectory", id)
		}
	}

	if len(toolUseIDs) == 0 {
		t.Error("no tool_use blocks found in messages — test may not be exercising the right path")
	}

	t.Logf("repaired trajectory: %d messages, %d tool_use(s) all resolved",
		len(repaired), len(toolUseIDs))
}
