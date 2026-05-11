package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/learnclaudecode/claude-go/src/phase-03-cache/query"
)

// FileReadTool reads a file from the filesystem, optionally with line-range
// control, and returns the content with 1-based line-number prefixes.
type FileReadTool struct{}

// NewFileReadTool returns a new FileReadTool instance.
func NewFileReadTool() *FileReadTool { return &FileReadTool{} }

func (t *FileReadTool) Name() string { return "Read" }

func (t *FileReadTool) Description(_ map[string]any) string {
	return "Reads a file from the local filesystem. Returns content with line numbers."
}

func (t *FileReadTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "Absolute path to the file to read.",
			},
			"offset": map[string]any{
				"type":        "integer",
				"description": "1-based line number to start reading from.",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Maximum number of lines to return.",
			},
		},
		"required": []string{"file_path"},
	}
}

func (t *FileReadTool) ValidateInput(input map[string]any, _ *query.ToolUseContext) query.ValidationResult {
	fp, ok := input["file_path"].(string)
	if !ok || fp == "" {
		return query.ValidationResult{OK: false, Message: "file_path must be a non-empty string"}
	}
	return query.ValidationResult{OK: true}
}

func (t *FileReadTool) CheckPermissions(_ map[string]any, _ *query.ToolUseContext) query.PermissionDecision {
	return query.PermissionDecision{Behavior: query.PermAllow}
}

func (t *FileReadTool) IsReadOnly(_ map[string]any) bool { return true }

func (t *FileReadTool) Call(_ context.Context, input map[string]any, _ *query.ToolUseContext, _ query.ToolCallProgress) (query.ToolResult, error) {
	filePath, _ := input["file_path"].(string)

	offset := 0
	if v, ok := input["offset"]; ok {
		switch ov := v.(type) {
		case int:
			offset = ov
		case float64:
			offset = int(ov)
		}
	}

	limit := 0
	if v, ok := input["limit"]; ok {
		switch lv := v.(type) {
		case int:
			limit = lv
		case float64:
			limit = int(lv)
		}
	}

	f, err := os.Open(filePath)
	if err != nil {
		return query.ToolResult{
			Content: fmt.Sprintf("error reading file %q: %v", filePath, err),
			IsError: true,
		}, nil
	}
	defer f.Close()

	var sb strings.Builder
	scanner := bufio.NewScanner(f)
	lineNum := 0
	written := 0

	for scanner.Scan() {
		lineNum++

		// offset is 1-based; skip lines before it.
		if offset > 0 && lineNum < offset {
			continue
		}
		// Stop after limit lines (0 = unlimited).
		if limit > 0 && written >= limit {
			break
		}

		fmt.Fprintf(&sb, "%4d\t%s\n", lineNum, scanner.Text())
		written++
	}

	if err := scanner.Err(); err != nil {
		return query.ToolResult{
			Content: fmt.Sprintf("error reading file %q: %v", filePath, err),
			IsError: true,
		}, nil
	}

	return query.ToolResult{Content: sb.String()}, nil
}
