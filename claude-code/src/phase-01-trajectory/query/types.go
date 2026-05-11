// Package query defines all message types, interfaces, and function signatures
// for the Phase 1 agent query loop.
package query

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

// ── ToolUseContext (Phase 1 stub) ─────────────────────────────────────────────

// ToolUseContext carries the list of available tools into the query loop.
// Phase 2 will expand this with permissions, MCP state, etc.
type ToolUseContext struct {
	Tools []ToolDefinition
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
