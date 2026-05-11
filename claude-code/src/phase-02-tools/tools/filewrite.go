package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/learnclaudecode/claude-go/src/phase-02-tools/query"
)

// FileWriteTool writes content to a file, creating parent directories as needed.
type FileWriteTool struct{}

// NewFileWriteTool returns a new FileWriteTool instance.
func NewFileWriteTool() *FileWriteTool { return &FileWriteTool{} }

func (t *FileWriteTool) Name() string { return "Write" }

func (t *FileWriteTool) Description(_ map[string]any) string {
	return "Writes a file to the local filesystem, creating parent directories as needed."
}

func (t *FileWriteTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "Absolute path of the file to write.",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "Content to write to the file.",
			},
		},
		"required": []string{"file_path", "content"},
	}
}

func (t *FileWriteTool) ValidateInput(input map[string]any, _ *query.ToolUseContext) query.ValidationResult {
	fp, ok := input["file_path"].(string)
	if !ok || fp == "" {
		return query.ValidationResult{OK: false, Message: "file_path must be a non-empty string"}
	}
	if _, ok := input["content"].(string); !ok {
		return query.ValidationResult{OK: false, Message: "content must be a string"}
	}
	return query.ValidationResult{OK: true}
}

func (t *FileWriteTool) CheckPermissions(_ map[string]any, _ *query.ToolUseContext) query.PermissionDecision {
	// Write operations default to "ask" — Phase 2 auto-allows for simplicity.
	return query.PermissionDecision{Behavior: query.PermAsk, Message: "file write operation"}
}

func (t *FileWriteTool) IsReadOnly(_ map[string]any) bool { return false }

func (t *FileWriteTool) Call(_ context.Context, input map[string]any, _ *query.ToolUseContext, _ query.ToolCallProgress) (query.ToolResult, error) {
	filePath, _ := input["file_path"].(string)
	content, _ := input["content"].(string)

	// Create parent directories.
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return query.ToolResult{
			Content: fmt.Sprintf("error creating directories for %q: %v", filePath, err),
			IsError: true,
		}, nil
	}

	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		return query.ToolResult{
			Content: fmt.Sprintf("error writing file %q: %v", filePath, err),
			IsError: true,
		}, nil
	}

	return query.ToolResult{
		Content: fmt.Sprintf("Successfully wrote %d bytes to %s", len(content), filePath),
	}, nil
}
