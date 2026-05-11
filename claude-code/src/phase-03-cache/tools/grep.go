package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/learnclaudecode/claude-go/src/phase-03-cache/query"
)

const grepMaxResults = 100

// GrepTool searches file contents for a regexp pattern.
type GrepTool struct{}

// NewGrepTool returns a new GrepTool instance.
func NewGrepTool() *GrepTool { return &GrepTool{} }

func (t *GrepTool) Name() string { return "Grep" }

func (t *GrepTool) Description(_ map[string]any) string {
	return "A powerful search tool that searches file contents for a regular expression pattern."
}

func (t *GrepTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "Regular expression pattern to search for.",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "File or directory to search in (default: current directory).",
			},
			"glob": map[string]any{
				"type":        "string",
				"description": "Glob pattern to filter files (e.g. \"*.go\").",
			},
		},
		"required": []string{"pattern"},
	}
}

func (t *GrepTool) ValidateInput(input map[string]any, _ *query.ToolUseContext) query.ValidationResult {
	p, ok := input["pattern"].(string)
	if !ok || p == "" {
		return query.ValidationResult{OK: false, Message: "pattern must be a non-empty string"}
	}
	if _, err := regexp.Compile(p); err != nil {
		return query.ValidationResult{OK: false, Message: fmt.Sprintf("invalid regexp: %v", err)}
	}
	return query.ValidationResult{OK: true}
}

func (t *GrepTool) CheckPermissions(_ map[string]any, _ *query.ToolUseContext) query.PermissionDecision {
	return query.PermissionDecision{Behavior: query.PermAllow}
}

func (t *GrepTool) IsReadOnly(_ map[string]any) bool { return true }

func (t *GrepTool) Call(_ context.Context, input map[string]any, _ *query.ToolUseContext, _ query.ToolCallProgress) (query.ToolResult, error) {
	pattern, _ := input["pattern"].(string)
	searchPath, _ := input["path"].(string)
	globPattern, _ := input["glob"].(string)

	re, _ := regexp.Compile(pattern)

	if searchPath == "" {
		var err error
		searchPath, err = os.Getwd()
		if err != nil {
			searchPath = "."
		}
	}

	var results []string
	truncated := false

	err := filepath.Walk(searchPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		// Apply glob filter.
		if globPattern != "" {
			matched, merr := filepath.Match(globPattern, filepath.Base(path))
			if merr != nil || !matched {
				return nil
			}
		}
		if truncated {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			if re.MatchString(scanner.Text()) {
				results = append(results, fmt.Sprintf("%s:%d:%s", path, lineNum, scanner.Text()))
				if len(results) >= grepMaxResults {
					truncated = true
					return nil
				}
			}
		}
		return nil
	})

	if err != nil {
		return query.ToolResult{
			Content: fmt.Sprintf("grep error: %v", err),
			IsError: true,
		}, nil
	}

	if len(results) == 0 {
		return query.ToolResult{Content: "(no matches found)"}, nil
	}

	var sb strings.Builder
	for _, r := range results {
		sb.WriteString(r)
		sb.WriteByte('\n')
	}
	if truncated {
		sb.WriteString(fmt.Sprintf("[results truncated at %d matches]\n", grepMaxResults))
	}

	return query.ToolResult{Content: sb.String()}, nil
}
