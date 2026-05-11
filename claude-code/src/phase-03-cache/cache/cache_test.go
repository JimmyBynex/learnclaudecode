package cache

import (
	"strings"
	"testing"

	"github.com/learnclaudecode/claude-go/src/phase-03-cache/query"
)

// ── TestContentReplacement ────────────────────────────────────────────────────

func TestContentReplacement(t *testing.T) {
	t.Run("large content is replaced with reference containing PersistedOutputTag", func(t *testing.T) {
		state := NewContentReplacementState()
		// Build content that exceeds the threshold.
		large := strings.Repeat("x", DefaultPersistenceThreshold+1)

		result := state.MaybeReplace("id-large", large)

		if !strings.Contains(result, PersistedOutputTag) {
			t.Errorf("expected reference to contain %q, got: %q", PersistedOutputTag, result[:min(100, len(result))])
		}
		if result == large {
			t.Error("expected MaybeReplace to return a reference, not the original content")
		}
	})

	t.Run("same id second call returns byte-identical string (fate is sealed)", func(t *testing.T) {
		state := NewContentReplacementState()
		large := strings.Repeat("y", DefaultPersistenceThreshold+1)

		first := state.MaybeReplace("id-sealed", large)
		second := state.MaybeReplace("id-sealed", large)

		if first != second {
			t.Errorf("expected identical strings on second call\nfirst:  %q\nsecond: %q", first[:min(80, len(first))], second[:min(80, len(second))])
		}
	})

	t.Run("same id second call with different content still returns original decision", func(t *testing.T) {
		state := NewContentReplacementState()
		large := strings.Repeat("z", DefaultPersistenceThreshold+1)

		first := state.MaybeReplace("id-immutable", large)
		// Second call with completely different (small) content — must ignore it.
		second := state.MaybeReplace("id-immutable", "totally different small content")

		if first != second {
			t.Error("MaybeReplace should ignore new content once fate is sealed")
		}
	})

	t.Run("small content is returned verbatim", func(t *testing.T) {
		state := NewContentReplacementState()
		small := "hello world"

		result := state.MaybeReplace("id-small", small)

		if result != small {
			t.Errorf("expected verbatim return for small content, got: %q", result)
		}
	})

	t.Run("small content second call returns same string", func(t *testing.T) {
		state := NewContentReplacementState()
		small := "small content"

		first := state.MaybeReplace("id-small2", small)
		second := state.MaybeReplace("id-small2", small)

		if first != second {
			t.Error("second call for small content should return identical string")
		}
	})
}

// ── TestRestoreContent ────────────────────────────────────────────────────────

func TestRestoreContent(t *testing.T) {
	t.Run("restore returns original content after replacement", func(t *testing.T) {
		state := NewContentReplacementState()
		large := strings.Repeat("r", DefaultPersistenceThreshold+1)

		state.MaybeReplace("id-restore", large)

		orig, ok := state.Restore("id-restore")
		if !ok {
			t.Fatal("Restore returned ok=false for a replaced id")
		}
		if orig != large {
			t.Errorf("Restore returned wrong content: got len=%d, want len=%d", len(orig), len(large))
		}
	})

	t.Run("restore returns false for small (not replaced) content", func(t *testing.T) {
		state := NewContentReplacementState()
		state.MaybeReplace("id-small-restore", "small")

		_, ok := state.Restore("id-small-restore")
		if ok {
			t.Error("Restore should return false for content that was not replaced")
		}
	})

	t.Run("restore returns false for unknown id", func(t *testing.T) {
		state := NewContentReplacementState()

		_, ok := state.Restore("id-unknown")
		if ok {
			t.Error("Restore should return false for an id that was never seen")
		}
	})
}

// ── TestCacheBreakDetection ───────────────────────────────────────────────────

func TestCacheBreakDetection(t *testing.T) {
	t.Run("drift detected when prev created cache but curr read nothing", func(t *testing.T) {
		prev := query.Usage{CacheCreationTokens: 1000, CacheReadTokens: 0}
		curr := query.Usage{CacheCreationTokens: 0, CacheReadTokens: 0}

		event := DetectCacheBreak(prev, curr)
		if event == nil {
			t.Fatal("expected CacheBreakEvent, got nil")
		}
		if event.ActualCacheReadTokens != 0 {
			t.Errorf("expected ActualCacheReadTokens=0, got %d", event.ActualCacheReadTokens)
		}
		if event.ExpectedCacheReadTokens <= 0 {
			t.Errorf("expected positive ExpectedCacheReadTokens, got %d", event.ExpectedCacheReadTokens)
		}
		if event.SuspectedCause == "" {
			t.Error("expected non-empty SuspectedCause")
		}
	})

	t.Run("no drift when prev created cache and curr reads cache", func(t *testing.T) {
		prev := query.Usage{CacheCreationTokens: 1000}
		curr := query.Usage{CacheReadTokens: 200}

		event := DetectCacheBreak(prev, curr)
		if event != nil {
			t.Errorf("expected nil (no drift), got %+v", event)
		}
	})

	t.Run("no drift when prev created no cache", func(t *testing.T) {
		prev := query.Usage{CacheCreationTokens: 0}
		curr := query.Usage{CacheReadTokens: 0}

		event := DetectCacheBreak(prev, curr)
		if event != nil {
			t.Errorf("expected nil (no drift), got %+v", event)
		}
	})

	t.Run("expected tokens is prev creation divided by 5", func(t *testing.T) {
		prev := query.Usage{CacheCreationTokens: 500}
		curr := query.Usage{CacheReadTokens: 0}

		event := DetectCacheBreak(prev, curr)
		if event == nil {
			t.Fatal("expected CacheBreakEvent")
		}
		if event.ExpectedCacheReadTokens != 100 {
			t.Errorf("expected ExpectedCacheReadTokens=100, got %d", event.ExpectedCacheReadTokens)
		}
	})
}

// ── TestMicroCompact ──────────────────────────────────────────────────────────

func TestMicroCompact(t *testing.T) {
	makeToolResultMsg := func(id, content string) query.Message {
		return query.Message{
			Role: query.RoleUser,
			Content: []query.ContentBlock{
				query.ToolResultBlock{
					Type:      "tool_result",
					ToolUseID: id,
					Content:   content,
					IsError:   false,
				},
			},
		}
	}

	t.Run("already replaced tool_result uses reference in both local and API views", func(t *testing.T) {
		state := NewContentReplacementState()
		large := strings.Repeat("m", DefaultPersistenceThreshold+1)

		// Seal the fate first.
		ref := state.MaybeReplace("id-mc-1", large)

		msg := makeToolResultMsg("id-mc-1", large)
		result := MicroCompact([]query.Message{msg}, state)

		if len(result.LocalMessages) != 1 || len(result.APIMessages) != 1 {
			t.Fatalf("expected 1 message in each view")
		}

		localContent := result.LocalMessages[0].Content[0].(query.ToolResultBlock).Content.(string)
		apiContent := result.APIMessages[0].Content[0].(query.ToolResultBlock).Content.(string)

		if localContent != ref {
			t.Errorf("LocalMessages content should be reference string")
		}
		if apiContent != ref {
			t.Errorf("APIMessages content should be reference string")
		}
		if localContent != apiContent {
			t.Error("LocalMessages and APIMessages must be identical in Phase 3")
		}
	})

	t.Run("new oversized tool_result triggers replacement during MicroCompact", func(t *testing.T) {
		state := NewContentReplacementState()
		large := strings.Repeat("n", DefaultPersistenceThreshold+1)

		msg := makeToolResultMsg("id-mc-new", large)
		result := MicroCompact([]query.Message{msg}, state)

		localContent := result.LocalMessages[0].Content[0].(query.ToolResultBlock).Content.(string)
		apiContent := result.APIMessages[0].Content[0].(query.ToolResultBlock).Content.(string)

		if !strings.Contains(localContent, PersistedOutputTag) {
			t.Error("expected large content to be replaced in LocalMessages")
		}
		if localContent != apiContent {
			t.Error("LocalMessages and APIMessages must be identical in Phase 3")
		}
	})

	t.Run("small tool_result passes through unchanged in both views", func(t *testing.T) {
		state := NewContentReplacementState()
		small := "small tool output"

		msg := makeToolResultMsg("id-mc-small", small)
		result := MicroCompact([]query.Message{msg}, state)

		localContent := result.LocalMessages[0].Content[0].(query.ToolResultBlock).Content.(string)
		apiContent := result.APIMessages[0].Content[0].(query.ToolResultBlock).Content.(string)

		if localContent != small {
			t.Errorf("small content should pass through, got: %q", localContent)
		}
		if apiContent != small {
			t.Errorf("small content should pass through, got: %q", apiContent)
		}
	})

	t.Run("non-tool-result blocks are passed through unchanged", func(t *testing.T) {
		state := NewContentReplacementState()
		msg := query.Message{
			Role: query.RoleAssistant,
			Content: []query.ContentBlock{
				query.TextBlock{Type: "text", Text: "hello"},
			},
		}

		result := MicroCompact([]query.Message{msg}, state)

		if len(result.LocalMessages[0].Content) != 1 {
			t.Error("expected 1 content block in local view")
		}
		tb, ok := result.LocalMessages[0].Content[0].(query.TextBlock)
		if !ok || tb.Text != "hello" {
			t.Error("text block should pass through unchanged")
		}
	})
}

