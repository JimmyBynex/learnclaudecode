package cache

import (
	"github.com/learnclaudecode/claude-go/src/phase-03-cache/query"
)

// CacheBreakEvent records a detected cache-prefix drift between two consecutive
// API requests.  A drift means the cache created in one request was not read
// in the next, indicating the prompt bytes changed.
type CacheBreakEvent struct {
	// ExpectedCacheReadTokens is a rough estimate of what should have been read.
	ExpectedCacheReadTokens int
	// ActualCacheReadTokens is what the API actually reported.
	ActualCacheReadTokens int
	// SuspectedCause is a human-readable description of the likely root cause.
	SuspectedCause string
}

// DetectCacheBreak compares two consecutive Usage values and returns a
// CacheBreakEvent when a cache miss is detected.
//
// Drift condition: the previous request created cache tokens
// (prev.CacheCreationTokens > 0) but the current request did not read any
// cached tokens (curr.CacheReadTokens == 0), meaning the cache prefix changed
// between requests.
//
// Returns nil when the cache appears healthy (either no cache was created, or
// it was successfully read).
func DetectCacheBreak(prev, curr query.Usage) *CacheBreakEvent {
	if prev.CacheCreationTokens > 0 && curr.CacheReadTokens == 0 {
		return &CacheBreakEvent{
			// Rough estimate: creation tokens ÷ 5 (cache read is typically cheaper)
			ExpectedCacheReadTokens: prev.CacheCreationTokens / 5,
			ActualCacheReadTokens:   0,
			SuspectedCause:          "cache prefix drift detected",
		}
	}
	return nil
}
