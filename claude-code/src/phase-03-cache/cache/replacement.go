// Package cache implements Phase 3 cache-prefix stability mechanisms.
// The key invariant (Invariant II) is: once a tool result's content has been
// seen by the API (seenIDs), its representation in future API requests must
// remain byte-for-byte identical so the cache prefix does not drift.
package cache

import (
	"fmt"
)

// DefaultPersistenceThreshold is the character count above which a tool result
// is replaced with a stable reference string.  Corresponds to the TypeScript
// DEFAULT_MAX_RESULT_SIZE_CHARS constant.
const DefaultPersistenceThreshold = 50_000

// PersistedOutputTag is the XML-like wrapper used in the reference string.
// Kept identical to the original TypeScript implementation.
const PersistedOutputTag = "<persisted-output>"

// ContentReplacementState is the core of Invariant II.
//
// "Fate is sealed" means: the moment a tool_use_id is processed by MaybeReplace
// (regardless of whether it was actually replaced), it is recorded in seenIDs
// and its fate—original content or replacement reference—is frozen forever.
// Subsequent calls for the same id always return the exact same bytes, ensuring
// the cache prefix never drifts.
type ContentReplacementState struct {
	seenIDs      map[string]bool   // tool_use_ids that have already been processed
	replacements map[string]string // id → replacement reference string
	originals    map[string]string // id → original content (for Restore)
}

// NewContentReplacementState creates an empty, ready-to-use replacement state.
func NewContentReplacementState() *ContentReplacementState {
	return &ContentReplacementState{
		seenIDs:      make(map[string]bool),
		replacements: make(map[string]string),
		originals:    make(map[string]string),
	}
}

// MaybeReplace inspects content for the given toolUseID.
//
//   - If the id has been seen before, the previously decided representation is
//     returned unchanged (the "fate is sealed" guarantee).
//   - If the id is new and content exceeds DefaultPersistenceThreshold characters,
//     the content is stored in originals and a stable reference string is stored
//     in replacements; the reference string is returned.
//   - If the id is new and content is within the threshold, content is returned
//     as-is; the id is still recorded in seenIDs so future calls are consistent.
func (s *ContentReplacementState) MaybeReplace(toolUseID, content string) string {
	// Fate already sealed: return the previously decided representation.
	if s.seenIDs[toolUseID] {
		if ref, ok := s.replacements[toolUseID]; ok {
			return ref
		}
		// Was seen but not replaced: return original content.
		if orig, ok := s.originals[toolUseID]; ok {
			return orig
		}
		return content
	}

	// First encounter: seal the fate.
	s.seenIDs[toolUseID] = true

	if len(content) <= DefaultPersistenceThreshold {
		// Small enough to keep verbatim.
		s.originals[toolUseID] = content
		return content
	}

	// Large content: persist original, generate stable reference.
	s.originals[toolUseID] = content
	ref := buildRef(toolUseID, content)
	s.replacements[toolUseID] = ref
	return ref
}

// Restore returns the original content for a previously replaced tool result.
// Returns ("", false) if the id was never replaced (either not seen or small
// enough to be kept verbatim).
func (s *ContentReplacementState) Restore(toolUseID string) (string, bool) {
	if _, replaced := s.replacements[toolUseID]; !replaced {
		return "", false
	}
	orig, ok := s.originals[toolUseID]
	return orig, ok
}

// buildRef constructs the stable reference string that replaces large content
// in API requests.  The format mirrors the TypeScript PERSISTED_OUTPUT_TAG usage.
func buildRef(toolUseID, content string) string {
	return fmt.Sprintf(
		"%s\nTool output for %s was persisted (%d chars). Use the tool result directly.\n</persisted-output>",
		PersistedOutputTag,
		toolUseID,
		len(content),
	)
}
