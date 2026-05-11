// Package tools provides the built-in tool implementations for Phase 2.
// The Tool interface and ToolRegistry are defined in the query package to
// avoid import cycles (tools imports query, not vice-versa).
package tools

import (
	"github.com/learnclaudecode/claude-go/src/phase-03-cache/query"
)

// Re-export the key types so callers can use tools.Tool, tools.ToolRegistry, etc.
// without having to import both packages.

// Tool is an alias for query.Tool.
type Tool = query.Tool

// ToolResult is an alias for query.ToolResult.
type ToolResult = query.ToolResult

// ToolUseContext is an alias for query.ToolUseContext.
type ToolUseContext = query.ToolUseContext

// ValidationResult is an alias for query.ValidationResult.
type ValidationResult = query.ValidationResult

// PermissionDecision is an alias for query.PermissionDecision.
type PermissionDecision = query.PermissionDecision

// ToolCallProgress is an alias for query.ToolCallProgress.
type ToolCallProgress = query.ToolCallProgress

// DefaultBuiltins returns the canonical set of built-in tools.
func DefaultBuiltins() []query.Tool {
	return []query.Tool{
		NewBashTool(),
		NewFileReadTool(),
		NewFileWriteTool(),
		NewFileEditTool(),
		NewGlobTool(),
		NewGrepTool(),
		NewWebFetchTool(),
		NewWebSearchTool(),
	}
}

// NewDefaultRegistry builds a ToolRegistry populated with all built-in tools
// and any extras supplied by the caller.
func NewDefaultRegistry(extras []query.Tool) *query.ToolRegistry {
	return query.NewToolRegistry(DefaultBuiltins(), extras)
}
