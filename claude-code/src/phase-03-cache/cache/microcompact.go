package cache

import (
	"github.com/learnclaudecode/claude-go/src/phase-03-cache/query"
)

// MicroCompactResult separates the local display view from the API prefix view.
//
// The invariant: LocalMessages and APIMessages carry the same logical
// conversation but may differ in how large tool results are rendered.
// Currently both views use the same stable replacement reference once content
// has been processed through ContentReplacementState; the split exists to allow
// future phases to diverge them further (e.g., clearing old content locally
// while keeping the API prefix unchanged).
type MicroCompactResult struct {
	// LocalMessages is the view used for local display.
	// For replaced tool results the reference string is used (same as API).
	LocalMessages []query.Message
	// APIMessages is the view sent to the Anthropic API.
	// Byte-for-byte identical to LocalMessages in Phase 3; kept separate for
	// forward compatibility.
	APIMessages []query.Message
}

// MicroCompact iterates over messages and ensures every ToolResultBlock whose
// content exceeds the threshold is replaced with a stable reference string via
// state.MaybeReplace.
//
// Core guarantee: the same tool_use_id always produces the same byte sequence
// in both LocalMessages and APIMessages, preventing cache-prefix drift.
func MicroCompact(messages []query.Message, state *ContentReplacementState) MicroCompactResult {
	localMsgs := make([]query.Message, 0, len(messages))
	apiMsgs := make([]query.Message, 0, len(messages))

	for _, msg := range messages {
		localBlocks := make([]query.ContentBlock, 0, len(msg.Content))
		apiBlocks := make([]query.ContentBlock, 0, len(msg.Content))

		for _, block := range msg.Content {
			tr, ok := block.(query.ToolResultBlock)
			if !ok {
				// Non-tool-result blocks are passed through unchanged.
				localBlocks = append(localBlocks, block)
				apiBlocks = append(apiBlocks, block)
				continue
			}

			// Extract string content, applying MaybeReplace.
			var rawContent string
			switch c := tr.Content.(type) {
			case string:
				rawContent = c
			default:
				// Non-string content (e.g. []ContentBlock) is kept as-is.
				localBlocks = append(localBlocks, block)
				apiBlocks = append(apiBlocks, block)
				continue
			}

			// MaybeReplace enforces the "fate is sealed" invariant.
			processed := state.MaybeReplace(tr.ToolUseID, rawContent)

			replacedBlock := query.ToolResultBlock{
				Type:      tr.Type,
				ToolUseID: tr.ToolUseID,
				Content:   processed,
				IsError:   tr.IsError,
			}

			// Both views receive the same processed content in Phase 3.
			localBlocks = append(localBlocks, replacedBlock)
			apiBlocks = append(apiBlocks, replacedBlock)
		}

		localMsg := msg
		localMsg.Content = localBlocks
		apiMsg := msg
		apiMsg.Content = apiBlocks

		localMsgs = append(localMsgs, localMsg)
		apiMsgs = append(apiMsgs, apiMsg)
	}

	return MicroCompactResult{
		LocalMessages: localMsgs,
		APIMessages:   apiMsgs,
	}
}
