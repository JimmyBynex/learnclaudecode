package query

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// TerminalReason describes why a query loop ended.
type TerminalReason string

type QueryParams struct {
	Messages    []Message
	SystemParts []SystemPart
	ToolCtx     *ToolUseContext
	Model       string
	MaxTurns    int
	QuerySource string
}

const (
	TerminalCompleted TerminalReason = "completed"
	TerminalAborted   TerminalReason = "aborted"
	TerminalMaxTurns  TerminalReason = "max_turns"
	TerminalError     TerminalReason = "error"
)

type Terminal struct {
	Reason   TerminalReason
	Messages []Message
	Error    error
}

type ToolUseContext struct {
	Tools []ToolDefinition
}

type CallModelParams struct {
	Model     string
	Messages  []Message
	MaxTokens int
	Tools     []ToolDefinition
	//为什么anthropic要这样设计，不应该一直保持全部systemPrompt不变吗
	//原因是模型对systemPrompt的遵循更强，对于rag和一些skill的注入，效果更好
	//虽然会有损失，但是fix一部分永远不变的systemPrompt有一点点收益
	System []SystemPart
}

type Message struct {
	Role       Role
	Content    []ContentBlock
	UUID       string
	ParentUUID string
	IsMeta     bool
}

// ContentBlock is a sealed interface for all the contentBlock variants
type ContentBlock interface {
	BlockType() string
}

type TextBlock struct {
	//为什么不直接把type写死，为了方便json和反json
	Type string
	Text string
}

func (b TextBlock) BlockType() string {
	return b.Type
}

type ToolUseBlock struct {
	Type string
	ID   string
	Name string
	//非常灵活的处理
	Input map[string]any
}

func (b ToolUseBlock) BlockType() string {
	return b.Type
}

type ToolResultBlock struct {
	Type      string
	ToolUseID string
	Content   any
	IsError   bool
}

func (b ToolResultBlock) BlockType() string {
	return b.Type
}

type ThinkingBlock struct {
	Type      string
	Thinking  string
	Signature string
}

func (b ThinkingBlock) BlockType() string {
	return b.Type
}

type SystemPart struct {
	Type string
	Text string
	// 方便空值，代表不使用缓存，目前是只有 "ephemeral"
	CacheControl *struct{ Type string }
}

type ToolDefinition struct {
	Name        string
	Description string
	//输入规范
	InputSchema any
}

type StreamEvent struct {
	Type      string //error||content_block_start||message_start||content_block_stop||content_block_delta||message_delta||message_stop
	Index     int
	BlockMeta *BlockMeta // content_block_start 专用
	Delta     *Delta     //content_block_delta
	Usage     *Usage
}

type BlockMeta struct {
	Type string
	ID   string
	Name string
}
type Delta struct {
	Type        string
	Text        string
	Thinking    string
	PartialJSON string
}

type Usage struct {
	InputTokens         int
	OutputToken         int
	CacheCreationTokens int
	CacheReadTokens     int
}
