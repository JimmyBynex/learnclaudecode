package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/learnclaudecode/claude-go/src/phase-03-cache/query"
)

// GlobTool finds files matching a glob pattern, returning paths sorted by
// modification time (most recent first).
type GlobTool struct{}

// NewGlobTool returns a new GlobTool instance.
func NewGlobTool() *GlobTool { return &GlobTool{} }

func (t *GlobTool) Name() string { return "Glob" }

func (t *GlobTool) Description(_ map[string]any) string {
	return "Fast file pattern matching tool. Returns matching file paths sorted by modification time."
}

func (t *GlobTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "Glob pattern to match (e.g. \"**/*.go\").",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Directory to search in (default: current directory).",
			},
		},
		"required": []string{"pattern"},
	}
}

func (t *GlobTool) ValidateInput(input map[string]any, _ *query.ToolUseContext) query.ValidationResult {
	if p, ok := input["pattern"].(string); !ok || p == "" {
		return query.ValidationResult{OK: false, Message: "pattern must be a non-empty string"}
	}
	return query.ValidationResult{OK: true}
}

func (t *GlobTool) CheckPermissions(_ map[string]any, _ *query.ToolUseContext) query.PermissionDecision {
	return query.PermissionDecision{Behavior: query.PermAllow}
}

func (t *GlobTool) IsReadOnly(_ map[string]any) bool { return true }

func (t *GlobTool) Call(_ context.Context, input map[string]any, _ *query.ToolUseContext, _ query.ToolCallProgress) (query.ToolResult, error) {
	pattern, _ := input["pattern"].(string)
	basePath, _ := input["path"].(string)

	if basePath == "" {
		var err error
		basePath, err = os.Getwd()
		if err != nil {
			basePath = "."
		}
	}

	// Build the full pattern relative to basePath.
	fullPattern := filepath.Join(basePath, pattern)

	matches, err := filepath.Glob(fullPattern)
	if err != nil {
		return query.ToolResult{
			Content: fmt.Sprintf("glob error: %v", err),
			IsError: true,
		}, nil
	}

	// Sort by modification time descending.
	type fileEntry struct {
		path    string
		modTime time.Time
	}
	entries := make([]fileEntry, 0, len(matches))
	for _, m := range matches {
		info, err := os.Stat(m)
		if err != nil {
			entries = append(entries, fileEntry{path: m})
			continue
		}
		entries = append(entries, fileEntry{path: m, modTime: info.ModTime()})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].modTime.After(entries[j].modTime)
	})

	if len(entries) == 0 {
		return query.ToolResult{Content: "(no files matched)"}, nil
	}

	var sb strings.Builder
	for _, e := range entries {
		sb.WriteString(e.path)
		sb.WriteByte('\n')
	}

	return query.ToolResult{Content: sb.String()}, nil
}
