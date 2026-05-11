package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/learnclaudecode/claude-go/src/phase-02-tools/query"
)

const (
	bashMaxOutputBytes   = 100 * 1024 // 100 KB
	bashDefaultTimeoutMs = 120_000    // 120 s
)

// BashTool executes shell commands via bash -c.
type BashTool struct{}

// NewBashTool returns a new BashTool instance.
func NewBashTool() *BashTool { return &BashTool{} }

func (t *BashTool) Name() string { return "Bash" }

func (t *BashTool) Description(_ map[string]any) string {
	return "Executes a given bash command and returns its output."
}

func (t *BashTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "The bash command to execute.",
			},
			"timeout": map[string]any{
				"type":        "integer",
				"description": "Timeout in milliseconds (default 120000).",
			},
		},
		"required": []string{"command"},
	}
}

func (t *BashTool) ValidateInput(input map[string]any, _ *query.ToolUseContext) query.ValidationResult {
	cmd, ok := input["command"].(string)
	if !ok || cmd == "" {
		return query.ValidationResult{OK: false, Message: "command must be a non-empty string"}
	}
	return query.ValidationResult{OK: true}
}

func (t *BashTool) CheckPermissions(_ map[string]any, _ *query.ToolUseContext) query.PermissionDecision {
	// Phase 2: always allow; permission UI is Phase 6+.
	return query.PermissionDecision{Behavior: query.PermAllow}
}

func (t *BashTool) IsReadOnly(_ map[string]any) bool { return false }

func (t *BashTool) Call(ctx context.Context, input map[string]any, _ *query.ToolUseContext, onProgress query.ToolCallProgress) (query.ToolResult, error) {
	command, _ := input["command"].(string)

	timeoutMs := bashDefaultTimeoutMs
	if v, ok := input["timeout"]; ok {
		switch tv := v.(type) {
		case int:
			timeoutMs = tv
		case float64:
			timeoutMs = int(tv)
		}
	}

	deadline := time.Duration(timeoutMs) * time.Millisecond
	runCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "bash", "-c", command)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	err := cmd.Run()

	output := buf.String()
	isError := false

	if err != nil {
		isError = true
		// Append the error message if no output captured.
		if output == "" {
			output = fmt.Sprintf("bash error: %v", err)
		}
	}

	// Truncate if too large.
	if len(output) > bashMaxOutputBytes {
		output = output[:bashMaxOutputBytes] + "\n[output truncated: exceeded 100KB limit]"
	}

	return query.ToolResult{Content: output, IsError: isError}, nil
}
