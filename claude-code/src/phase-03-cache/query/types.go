// Package query defines all message types, interfaces, and function signatures
// for the Phase 2 agent query loop.
package query

import "context"

// ── Role ──────────────────────────────────────────────────────────────────────

// Role is the speaker role in a conversation turn.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// ── Content blocks ─────────────────────────────────────────────────────────────

// ContentBlock is the sealed interface for all content block variants.
type ContentBlock interface{ BlockType() string }

// TextBlock holds a plain-text content block.
type TextBlock struct {
	Type string
	Text string
}

func (b TextBlock) BlockType() string { return b.Type }

// ThinkingBlock holds an extended-thinking content block.
type ThinkingBlock struct {
	Type      string
	Thinking  string
	Signature string
}

func (b ThinkingBlock) BlockType() string { return b.Type }

// ToolUseBlock holds a tool-call request from the assistant.
type ToolUseBlock struct {
	Type  string
	ID    string
	Name  string
	Input map[string]any
}

func (b ToolUseBlock) BlockType() string { return b.Type }

// ToolResultBlock holds the result of a tool call returned by the user turn.
type ToolResultBlock struct {
	Type      string // "tool_result"
	ToolUseID string
	Content   any  // string | []ContentBlock
	IsError   bool
}

func (b ToolResultBlock) BlockType() string { return b.Type }

// ── Internal message ──────────────────────────────────────────────────────────

// Message is the internal representation of a conversation turn with causal
// metadata (UUID / parent UUID) for trajectory repair.
type Message struct {
	Role       Role
	Content    []ContentBlock
	UUID       string
	ParentUUID string
	IsMeta     bool
}

// ── Streaming API types ───────────────────────────────────────────────────────

// Usage records token counts reported by the API.
type Usage struct {
	InputTokens         int
	OutputTokens        int
	CacheCreationTokens int
	CacheReadTokens     int
}

// StreamEvent is a parsed SSE event from the Anthropic streaming API.
// For content_block_start the BlockMeta carries block metadata:
//
//	BlockMeta.Type = block type ("text" | "thinking" | "tool_use")
//	BlockMeta.ID   = tool id        (tool_use only)
//	BlockMeta.Name = tool name      (tool_use only)
type StreamEvent struct {
	Type      string
	Index     int
	BlockMeta *struct{ Type, ID, Name string }
	Delta     *struct{ Type, Text, PartialJSON, Thinking string }
	Usage     *Usage
}

// SystemPart is one element of the system prompt array sent to the API.
type SystemPart struct {
	Type         string
	Text         string
	CacheControl *struct{ Type string }
}

// ToolDefinition describes a tool the model may call.
type ToolDefinition struct {
	Name        string
	Description string
	InputSchema any
}

// CallModelParams bundles all parameters for a single API call.
type CallModelParams struct {
	Messages  []Message
	System    []SystemPart
	Tools     []ToolDefinition
	Model     string
	MaxTokens int
}

// ── Phase 2: Tool interface and execution types ───────────────────────────────

// PermissionBehavior describes how a tool permission decision should be handled.
type PermissionBehavior string

const (
	PermAllow PermissionBehavior = "allow"
	PermDeny  PermissionBehavior = "deny"
	PermAsk   PermissionBehavior = "ask"
)

// PermissionDecision is the result of CheckPermissions.
type PermissionDecision struct {
	Behavior PermissionBehavior
	Message  string
}

// ValidationResult is the result of ValidateInput.
type ValidationResult struct {
	OK      bool
	Message string
}

// ToolCallProgress is a callback for streaming tool progress updates.
type ToolCallProgress func(update struct{ Type, Content string })

// ToolResult is the result returned by a tool call.
type ToolResult struct {
	Content     string
	IsError     bool
	NewMessages []Message
}

// AppState is the application state passed through ToolUseContext.
type AppState struct{}

// ContentReplacer is the interface through which the query loop applies Phase 3
// cache-stability replacements without importing the cache package (which
// imports query, so a direct import would be cyclic).
//
// The concrete implementation is *cache.ContentReplacementState.
type ContentReplacer interface {
	// MaybeReplace returns a stable, byte-identical string for the given
	// toolUseID and content.  Once an id has been processed, all future calls
	// return the same bytes regardless of the content argument.
	MaybeReplace(toolUseID, content string) string
}

// ToolUseContext carries context into the query and tool execution layers.
// Phase 3 adds ReplacementState for cache-prefix stability (Invariant II).
type ToolUseContext struct {
	Tools            *ToolRegistry
	AbortCh          <-chan struct{}
	GetAppState      func() *AppState
	SetAppState      func(func(*AppState))
	Depth            int
	ReplacementState ContentReplacer // Phase 3: nil means no replacement
}

// ToolRegistry is the builtin-first stable-sorted registry of available tools.
// It is defined in the tools package but referenced here via an alias to avoid
// an import cycle.  The concrete type is *tools.ToolRegistry, but since query
// imports tools and tools imports query we keep ToolRegistry declared here and
// the tools package embeds/uses it directly.
//
// NOTE: ToolRegistry is defined in query/registry.go (part of this package) so
// that tools/ can import query without a cycle.
type ToolRegistry struct {
	builtins []Tool
	extras   []Tool
}

// Tool is the interface every tool must implement.
type Tool interface {
	Name() string
	Description(input map[string]any) string
	InputSchema() any
	Call(ctx context.Context, input map[string]any, tctx *ToolUseContext, onProgress ToolCallProgress) (ToolResult, error)
	ValidateInput(input map[string]any, tctx *ToolUseContext) ValidationResult
	CheckPermissions(input map[string]any, tctx *ToolUseContext) PermissionDecision
	IsReadOnly(input map[string]any) bool
}

// ── ToolUseContext – legacy accessor (Phase 1 compat) ─────────────────────────

// ToolDefinitions returns the ToolDefinition slice used to call the API.
// It is a convenience wrapper over Tools.ToDefinitions() that handles nil.
func (tctx *ToolUseContext) ToolDefinitions() []ToolDefinition {
	if tctx == nil || tctx.Tools == nil {
		return nil
	}
	return tctx.Tools.ToDefinitions()
}

// ── Query loop types ──────────────────────────────────────────────────────────

// TerminalReason describes why a query loop ended.
type TerminalReason string

const (
	TerminalCompleted TerminalReason = "completed"
	TerminalAborted   TerminalReason = "aborted"
	TerminalMaxTurns  TerminalReason = "max_turns"
	TerminalError     TerminalReason = "error"
)

// Terminal is the final value emitted by Query once the loop ends.
type Terminal struct {
	Reason   TerminalReason
	Messages []Message
	Error    error
}

// QueryParams bundles all inputs to Query.
type QueryParams struct {
	Messages    []Message
	SystemParts []SystemPart
	ToolCtx     *ToolUseContext
	MaxTurns    int
	Model       string
	QuerySource string
}
