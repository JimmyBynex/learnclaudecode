package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/learnclaudecode/claude-go/src/phase-03-cache/query"
)

// FileEditTool performs an exact-string replacement in a file.
// old_string must appear exactly once in the file; otherwise an error is returned.
type FileEditTool struct{}

// NewFileEditTool returns a new FileEditTool instance.
func NewFileEditTool() *FileEditTool { return &FileEditTool{} }

func (t *FileEditTool) Name() string { return "Edit" }

func (t *FileEditTool) Description(_ map[string]any) string {
	return "Performs exact string replacements in files. old_string must appear exactly once."
}

func (t *FileEditTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "Absolute path to the file to modify.",
			},
			"old_string": map[string]any{
				"type":        "string",
				"description": "The text to replace (must appear exactly once).",
			},
			"new_string": map[string]any{
				"type":        "string",
				"description": "The replacement text.",
			},
		},
		"required": []string{"file_path", "old_string", "new_string"},
	}
}

func (t *FileEditTool) ValidateInput(input map[string]any, _ *query.ToolUseContext) query.ValidationResult {
	if fp, ok := input["file_path"].(string); !ok || fp == "" {
		return query.ValidationResult{OK: false, Message: "file_path must be a non-empty string"}
	}
	if _, ok := input["old_string"].(string); !ok {
		return query.ValidationResult{OK: false, Message: "old_string must be a string"}
	}
	if _, ok := input["new_string"].(string); !ok {
		return query.ValidationResult{OK: false, Message: "new_string must be a string"}
	}
	return query.ValidationResult{OK: true}
}

func (t *FileEditTool) CheckPermissions(_ map[string]any, _ *query.ToolUseContext) query.PermissionDecision {
	return query.PermissionDecision{Behavior: query.PermAsk, Message: "file edit operation"}
}

func (t *FileEditTool) IsReadOnly(_ map[string]any) bool { return false }

func (t *FileEditTool) Call(_ context.Context, input map[string]any, _ *query.ToolUseContext, _ query.ToolCallProgress) (query.ToolResult, error) {
	filePath, _ := input["file_path"].(string)
	oldStr, _ := input["old_string"].(string)
	newStr, _ := input["new_string"].(string)

	data, err := os.ReadFile(filePath)
	if err != nil {
		return query.ToolResult{
			Content: fmt.Sprintf("error reading file %q: %v", filePath, err),
			IsError: true,
		}, nil
	}

	content := string(data)
	count := strings.Count(content, oldStr)

	switch {
	case count == 0:
		return query.ToolResult{
			Content: fmt.Sprintf("old_string not found in %q", filePath),
			IsError: true,
		}, nil
	case count > 1:
		return query.ToolResult{
			Content: fmt.Sprintf("old_string appears %d times in %q; must be unique", count, filePath),
			IsError: true,
		}, nil
	}

	updated := strings.Replace(content, oldStr, newStr, 1)
	if err := os.WriteFile(filePath, []byte(updated), 0o644); err != nil {
		return query.ToolResult{
			Content: fmt.Sprintf("error writing file %q: %v", filePath, err),
			IsError: true,
		}, nil
	}

	return query.ToolResult{
		Content: fmt.Sprintf("Successfully edited %s", filePath),
	}, nil
}
