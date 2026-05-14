# Phase 1：轨迹骨架学习指南

## Section 1：心智模型

### 1.1 本 Phase 添加了什么

Phase 1 建立了整个 claude-code Go 重写项目的核心骨架：一个能够与 Anthropic API 进行流式对话、自动调用 Bash 工具、并在任何中断（Ctrl-C、超时、错误）发生时都能保持消息序列合法性的 **轨迹闭合循环**。所谓"轨迹闭合"，是指 Anthropic API 要求每一个 `tool_use` 块都必须有对应的 `tool_result` 块，违反此约束 API 会返回 400 错误；Phase 1 通过 `RepairTrajectory` 函数在中断时自动合成缺失的 `tool_result`，从根本上消除了这个问题。

实现中有两个关键的设计边界，它们决定了流式数据如何被观测和持久化：

- **yield 边界（per-block）**：每当收到 `content_block_stop` 事件时，立即把完成的 block 推送到 `msgCh`，让调用方能实时看到每个 block（包括 tool_use 的执行结果），而不必等到整条消息完全结束。
- **commit 边界（per-turn）**：只有在收到 `message_stop` 事件时，才把本轮所有的 `assistantBlocks` 和 `pendingToolResults` 整体写入 `messages`。这确保了 `messages` 中不会出现"assistant 消息已追加、tool_result 还没追加"的窗口期，消除了孤立 tool_use 的根因。

---

### 1.2 在架构中的位置

```
CLI (main.go, flag -p)
   ↓  query.SetCallModel(api.CallModel)   ← Phase 1：连接 API 客户端
   ↓
QueryEngine.Query(ctx, QueryParams)        ← Phase 1：入口
   ↓
queryLoop()                                ← Phase 1：核心循环（本 Phase 重点）
   ├── callModel() → Anthropic API (SSE)  ← Phase 1：流式客户端
   ├── eventLoop（内联）                   ← Phase 1：SSE 事件处理（per-block）
   │     content_block_stop → buildBlockAt() → executeOneTool()
   ├── message_stop → commit to messages  ← Phase 1：commit 边界
   └── type=stop（无 tool_use）→ Terminal{completed}
   
中断路径（本 Phase 核心约束）：
   ctx.Done() → RepairTrajectory()        ← Phase 1：轨迹修复
                 → Terminal{aborted}
```

---

### 1.3 控制流

```
caller
  │
  ▼
Query(ctx, QueryParams)
  │  启动 goroutine
  ▼
queryLoop(ctx, p, msgCh, termCh)
  │
  ├─[每轮]─ ctx.Done()? → RepairTrajectory(messages) → Terminal{aborted}
  │
  ├── callModelFn(ctx, CallModelParams) ──────────────────────────────────────┐
  │     ↓ 返回 <-chan StreamEvent                                              │
  │                                                                           │ SSE 流
  ├── eventLoop（内联 for 循环）                                               │
  │     │                                                                     │
  │     ├── content_block_start → ca.startBlock(index, type, id, name)        │
  │     │                                                                     │
  │     ├── content_block_delta → ca.applyDelta(index, deltaType, ...)        │
  │     │                                                                     │
  │     ├── content_block_stop                                                │
  │     │     │  → buildBlockAt(index) → block                                │
  │     │     │  → push block 到 msgCh             ← yield 边界（per-block）  │
  │     │     │  → 若是 tool_use：executeOneTool()                             │
  │     │     │        → push result 到 msgCh                                 │
  │     │     │  → 加入 assistantBlocks / pendingToolResults（局部变量）        │
  │     │                                                                     │
  │     ├── message_stop                                                      │
  │     │     │  → assistantBlocks + pendingToolResults → messages            │
  │     │     │                              ← commit 边界（per-turn）         │
  │     │     │  无 tool_use → Terminal{completed}  ←── 正常终止路径           │
  │     │     │  有 tool_use → break eventLoop（继续下一轮）                   │
  │     │                                                                     │
  │     └── ctx.Done() → aborted=true → break eventLoop                      │
  │                                                                           │
  ├── streamErr != nil → RepairTrajectory(messages) → Terminal{error}         │
  │                                                                           │
  ├── aborted && 零 block  → RepairTrajectory(messages) → Terminal{aborted}   │
  ├── aborted && 有 block  → partial commit → RepairTrajectory → Terminal{aborted}
  │                                                                           │
  └─[下一轮]─────────────────────────────────────────────────────────────────┘

超过 maxTurns → RepairTrajectory(messages) → Terminal{max_turns}
API 错误      → RepairTrajectory(messages) → Terminal{error}
```

---

### 1.4 数据流

```
入口：
  QueryParams{
    Messages    []Message          // 初始对话历史
    SystemParts []SystemPart       // 系统提示数组
    ToolCtx     *ToolUseContext    // 可用工具列表（Phase 1 stub）
    MaxTurns    int                // 最大轮数，默认 10
    Model       string             // 模型 ID，默认 "claude-sonnet-4-6"
    QuerySource string             // 调用来源（如 "cli"）
  }

callModel 参数：
  CallModelParams{
    Messages  []Message
    System    []SystemPart
    Tools     []ToolDefinition    // 包含 BashTool 定义
    Model     string
    MaxTokens int                 // 默认 8192
  }

callModel 返回：
  <-chan StreamEvent{
    Type      string              // "content_block_start" | "content_block_delta" |
                                  // "content_block_stop" | "message_stop" | "error"
    Index     int                 // 块在 content 数组中的下标
    BlockMeta *struct{            // content_block_start 时填充（block 元数据）
      Type string                 // "text" | "thinking" | "tool_use"
      ID   string                 // tool_use only：工具调用 ID
      Name string                 // tool_use only：工具名
    }
    Delta *struct{                // content_block_delta 时填充
      Type        string          // "text_delta" | "input_json_delta" | "thinking_delta"
      Text        string          // text_delta → 文本片段
      PartialJSON string          // input_json_delta → JSON 片段
      Thinking    string          // thinking_delta → 思考内容片段
    }
    Usage *Usage                  // token 计数（可为 nil）
  }

content_block_stop 时（per-block yield）：
  buildBlockAt(index) → ContentBlock（TextBlock | ThinkingBlock | ToolUseBlock）
  → push 到 msgCh（partialMsg，单 block 的 assistant Message）

tool_use 到达时（executeOneTool 立即执行）：
  ToolUseBlock{
    Type  string                  // "tool_use"
    ID    string                  // 唯一 ID，如 "toolu_01..."
    Name  string                  // 工具名，如 "Bash"
    Input map[string]any          // 如 {"command": "echo hello"}
  }
  → push result 到 msgCh（resultMsg，单 block 的 user Message）

message_stop 时（per-turn commit）：
  assistantBlocks → messages（assistant Message，包含本轮所有 blocks）
  pendingToolResults → messages（user Message，包含本轮所有 tool results）

出口：
  Terminal{
    Reason   TerminalReason      // "completed" | "aborted" | "max_turns" | "error"
    Messages []Message            // 修复后的完整轨迹
    Error    error                // 仅 Reason="error" 时非 nil
  }
```

---

### 1.5 与前序 Phase 的关系

Phase 1 是整个项目的第一个 Phase，没有前序依赖。它为后续 Phase 建立了以下基础：

- **消息类型系统**（`types.go`）：`ContentBlock` 接口 + 四种实现类型，以及 `Message`、`QueryParams`、`Terminal` 等所有核心类型，后续 Phase 在此基础上扩展，无需改变类型结构。
- **依赖注入点**（`CallModel` var + `SetCallModel`）：query 包不直接依赖 api 包，避免循环导入；后续 Phase 可替换 `callModelFn` 进行测试或扩展。
- **轨迹约束保证**（`RepairTrajectory`）：所有后续 Phase 无论如何修改循环逻辑，只要在中断路径上调用此函数，就能保证向 API 提交的消息序列始终合法。
- **可取消的上下文传播**：`ctx` 贯穿 `queryLoop` 和 `executeOneTool` 两层，后续 Phase 的新工具实现只需接收相同的 `ctx` 即可无缝集成。

---

## Section 2：实现演练

### 2.1 数据结构

**概念讲解**

Anthropic API 的消息 `content` 字段是一个多态数组，每个元素可以是 `text`、`thinking`、`tool_use`、`tool_result` 四种类型之一。在 Go 中表达多态有三种常见方案：

1. 用 `interface{}` + 运行时类型断言（过于宽松，失去编译期类型检查）
2. 用单个大 struct 加大量可选字段（臃肿，容易出错）
3. **用 sealed interface + 具体类型（本实现采用）**：定义 `ContentBlock` 接口，让所有具体类型实现 `BlockType() string`，在需要区分类型时通过 type switch 精确处理

`interface{ BlockType() string }` 的设计让编译器能检查所有 `ContentBlock` 的使用点，同时保留了在事件循环中用 `b.(ToolUseBlock)` 进行精确类型断言的能力。

`Message` 结构体额外携带 `UUID` 和 `ParentUUID` 字段，用于构建消息的因果链（parent→child 关系），这是 `RepairTrajectory` 能正确定位孤立 `tool_use` 的基础。

`StreamEvent` 的 `BlockMeta` 字段专门承载 `content_block_start` 事件中的 block 元数据（type/id/name），与 `Delta` 字段（承载 `content_block_delta` 的增量数据）严格分离，不再复用同一个字段进行编码。

**【现在手敲】**

```go
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
```

**【验证】**

```
运行：go build ./src/phase-01-trajectory/query/...
期望输出：（无输出表示成功）
```

---

### 2.2 核心逻辑

**概念讲解**

**两个边界：yield 和 commit**

旧设计的问题在于两个边界对齐不当：`collectAssistantMessage` 在 `message_stop` 才一次性返回整个 assistant message（yield 太晚），而 `queryLoop` 在工具执行前就把 assistant message 写入 `messages`，工具执行后才追加 tool result（commit 太早）。这中间有一个窗口期：`messages` 里存在 assistant message 但没有对应的 tool_result，如果此时发生中断，轨迹就是非法的。

新设计对齐了 TypeScript 原始版本的两个边界：

- **yield 边界（`content_block_stop`）**：每个 block 完成时立即推送给调用方，工具也立即执行并推送结果。这让调用方能流式看到每个 block，包括工具执行的实时反馈。
- **commit 边界（`message_stop`）**：整条消息结束时才把 `assistantBlocks` 和 `pendingToolResults` 一起写入 `messages`。两者原子性地追加，消除了孤立 tool_use 的窗口期。

**为什么删除 `collectAssistantMessage`？**

`collectAssistantMessage` 的设计是"等到 `message_stop` 才 build 并返回整个 assistant message"，这与新的 per-block yield 边界根本冲突。新设计把事件处理内联到 `queryLoop` 的 `eventLoop` 中，在 `content_block_stop` 时立即处理每个 block，无法再用一个独立函数封装"等到最后"的行为。

**为什么删除 `executeTools`（批量执行）、改用 `executeOneTool`（单个执行）？**

旧的 `executeTools` 接受一个 `[]ToolUseBlock`，在所有 blocks 都收集完之后批量执行。新设计在每个 `content_block_stop` 触发时就立即执行当前 block 对应的工具，不等其他 block。`executeOneTool` 只处理单个 `ToolUseBlock`，与 per-block 的事件驱动模型完全匹配。

**`buildBlockAt(index)` 的作用**

旧的 `build()` 方法遍历所有 blocks 并一次性返回全部。新的 `buildBlockAt(index)` 按 index 取出单个 block，在 `content_block_stop` 携带的 `Index` 处调用，精确构建当前刚完成的那个 block。

**【现在手敲】**

```go
// Package query implements the agent query loop and trajectory-repair utilities.
package query

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
)

func executeBash(command string) (string, bool) {
	cmd := exec.Command("bash", "-c", command)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), true
	}
	return string(out), false
}

func RepairTrajectory(messages []Message) []Message {
	resolved := make(map[string]bool)
	for _, m := range messages {
		if m.Role != RoleUser {
			continue
		}
		for _, b := range m.Content {
			if tr, ok := b.(ToolResultBlock); ok {
				resolved[tr.ToolUseID] = true
			}
		}
	}
	result := make([]Message, len(messages))
	copy(result, messages)
	for _, m := range messages {
		if m.Role != RoleAssistant {
			continue
		}
		for _, b := range m.Content {
			tu, ok := b.(ToolUseBlock)
			if !ok {
				continue
			}
			if resolved[tu.ID] {
				continue
			}
			synthetic := Message{
				Role: RoleUser,
				Content: []ContentBlock{
					ToolResultBlock{
						Type:      "tool_result",
						ToolUseID: tu.ID,
						Content:   "Interrupted by user",
						IsError:   true,
					},
				},
				UUID:       newUUID(),
				ParentUUID: m.UUID,
			}
			result = append(result, synthetic)
			resolved[tu.ID] = true
		}
	}
	return result
}

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return hex.EncodeToString(b[:4]) + "-" + hex.EncodeToString(b[4:6]) + "-" + hex.EncodeToString(b[6:8]) + "-" + hex.EncodeToString(b[8:10]) + "-" + hex.EncodeToString(b[10:])
}

var CallModel func(ctx context.Context, p CallModelParams) (<-chan StreamEvent, error)

func SetCallModel(fn func(ctx context.Context, p CallModelParams) (<-chan StreamEvent, error)) {
	CallModel = fn
}

var callModelFn func(ctx context.Context, p CallModelParams) (<-chan StreamEvent, error)

func Query(ctx context.Context, p QueryParams) (<-chan Message, <-chan Terminal) {
	msgCh := make(chan Message, 64)
	termCh := make(chan Terminal, 1)
	go func() {
		defer close(msgCh)
		defer close(termCh)
		queryLoop(ctx, p, msgCh, termCh)
	}()
	return msgCh, termCh
}

func queryLoop(
	ctx context.Context,
	p QueryParams,
	msgCh chan<- Message,
	termCh chan<- Terminal,
) {
	messages := make([]Message, len(p.Messages))
	copy(messages, p.Messages)

	model := p.Model
	if model == "" {
		model = "claude-sonnet-4-6"
	}
	maxTurns := p.MaxTurns
	if maxTurns == 0 {
		maxTurns = 10
	}

	tools := []ToolDefinition{}
	if p.ToolCtx != nil {
		tools = p.ToolCtx.Tools
	}
	hasBash := false
	for _, t := range tools {
		if t.Name == "Bash" {
			hasBash = true
			break
		}
	}
	if !hasBash {
		tools = append(tools, ToolDefinition{
			Name:        "Bash",
			Description: "Run a bash command and return its output.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type":        "string",
						"description": "The bash command to run.",
					},
				},
				"required": []string{"command"},
			},
		})
	}

	for turn := 0; turn < maxTurns; turn++ {
		select {
		case <-ctx.Done():
			repaired := RepairTrajectory(messages)
			termCh <- Terminal{Reason: TerminalAborted, Messages: repaired}
			return
		default:
		}

		callFn := callModelFn
		if callFn == nil {
			callFn = CallModel
		}

		evCh, err := callFn(ctx, CallModelParams{
			Messages:  messages,
			System:    p.SystemParts,
			Tools:     tools,
			Model:     model,
			MaxTokens: 8192,
		})
		if err != nil {
			repaired := RepairTrajectory(messages)
			termCh <- Terminal{Reason: TerminalError, Messages: repaired, Error: fmt.Errorf("callModel: %w", err)}
			return
		}

		ca := &contentAccumulator{}
		var assistantBlocks []ContentBlock
		var pendingToolResults []ContentBlock
		anyBlockReceived := false
		aborted := false
		var streamErr error

		assistantUUID := newUUID()
		var parentUUID string
		if len(messages) > 0 {
			parentUUID = messages[len(messages)-1].UUID
		}

	eventLoop:
		for {
			select {
			case <-ctx.Done():
				aborted = true
				break eventLoop
			case ev, ok := <-evCh:
				if !ok {
					break eventLoop
				}
				switch ev.Type {
				case "content_block_start":
					if ev.BlockMeta != nil {
						ca.startBlock(ev.Index, ev.BlockMeta.Type, ev.BlockMeta.ID, ev.BlockMeta.Name)
					}
				case "content_block_delta":
					if ev.Delta != nil {
						ca.applyDelta(ev.Index, ev.Delta.Type, ev.Delta.Text, ev.Delta.PartialJSON, ev.Delta.Thinking)
					}
				case "content_block_stop":
					block, buildErr := ca.buildBlockAt(ev.Index)
					if buildErr != nil {
						streamErr = buildErr
						break eventLoop
					}
					if block == nil {
						break
					}
					anyBlockReceived = true
					assistantBlocks = append(assistantBlocks, block)
					partialMsg := Message{
						Role:       RoleAssistant,
						Content:    []ContentBlock{block},
						UUID:       assistantUUID,
						ParentUUID: parentUUID,
					}
					select {
					case msgCh <- partialMsg:
					case <-ctx.Done():
						aborted = true
						break eventLoop
					}
					if tu, ok := block.(ToolUseBlock); ok {
						result := executeOneTool(ctx, tu)
						pendingToolResults = append(pendingToolResults, result)
						resultMsg := Message{
							Role:       RoleUser,
							Content:    []ContentBlock{result},
							UUID:       newUUID(),
							ParentUUID: assistantUUID,
						}
						select {
						case msgCh <- resultMsg:
						case <-ctx.Done():
							aborted = true
							break eventLoop
						}
					}
				case "message_stop":
					if len(assistantBlocks) > 0 {
						assistantMsg := Message{
							Role:       RoleAssistant,
							Content:    assistantBlocks,
							UUID:       assistantUUID,
							ParentUUID: parentUUID,
						}
						messages = append(messages, assistantMsg)
						if len(pendingToolResults) > 0 {
							toolResultMsg := Message{
								Role:       RoleUser,
								Content:    pendingToolResults,
								UUID:       newUUID(),
								ParentUUID: assistantUUID,
							}
							messages = append(messages, toolResultMsg)
						}
					}
					hasToolUse := false
					for _, b := range assistantBlocks {
						if _, ok := b.(ToolUseBlock); ok {
							hasToolUse = true
							break
						}
					}
					if !hasToolUse {
						termCh <- Terminal{Reason: TerminalCompleted, Messages: messages}
						return
					}
					break eventLoop
				case "error":
					streamErr = fmt.Errorf("stream error event received from API")
					break eventLoop
				}
			}
		}

		if streamErr != nil {
			repaired := RepairTrajectory(messages)
			termCh <- Terminal{Reason: TerminalError, Messages: repaired, Error: streamErr}
			return
		}
		if aborted {
			if !anyBlockReceived {
				repaired := RepairTrajectory(messages)
				termCh <- Terminal{Reason: TerminalAborted, Messages: repaired}
				return
			}
			if len(assistantBlocks) > 0 {
				assistantMsg := Message{
					Role:       RoleAssistant,
					Content:    assistantBlocks,
					UUID:       assistantUUID,
					ParentUUID: parentUUID,
				}
				messages = append(messages, assistantMsg)
			}
			repaired := RepairTrajectory(messages)
			termCh <- Terminal{Reason: TerminalAborted, Messages: repaired}
			return
		}
	}

	repaired := RepairTrajectory(messages)
	termCh <- Terminal{Reason: TerminalMaxTurns, Messages: repaired}
}

type accumBlock struct {
	blockType string
	id        string
	name      string
	text      []byte
	inputJSON []byte
	thinking  []byte
}

func (b *accumBlock) appendText(s string)     { b.text = append(b.text, s...) }
func (b *accumBlock) appendInput(s string)    { b.inputJSON = append(b.inputJSON, s...) }
func (b *accumBlock) appendThinking(s string) { b.thinking = append(b.thinking, s...) }

type contentAccumulator struct {
	blocks []*accumBlock
}

func (ca *contentAccumulator) ensureIndex(index int) {
	for len(ca.blocks) <= index {
		ca.blocks = append(ca.blocks, nil)
	}
}

func (ca *contentAccumulator) startBlock(index int, blockType, id, name string) {
	ca.ensureIndex(index)
	ca.blocks[index] = &accumBlock{blockType: blockType, id: id, name: name}
}

func (ca *contentAccumulator) applyDelta(index int, deltaType, text, partialJSON, thinking string) {
	ca.ensureIndex(index)
	b := ca.blocks[index]
	if b == nil {
		return
	}
	switch deltaType {
	case "text_delta":
		b.appendText(text)
	case "input_json_delta":
		b.appendInput(partialJSON)
	case "thinking_delta":
		b.appendThinking(thinking)
	}
}

func (ca *contentAccumulator) buildBlockAt(index int) (ContentBlock, error) {
	if index >= len(ca.blocks) || ca.blocks[index] == nil {
		return nil, nil
	}
	b := ca.blocks[index]
	switch b.blockType {
	case "text":
		return TextBlock{Type: "text", Text: string(b.text)}, nil
	case "thinking":
		return ThinkingBlock{Type: "thinking", Thinking: string(b.thinking)}, nil
	case "tool_use":
		var input map[string]any
		if len(b.inputJSON) > 0 {
			if err := json.Unmarshal(b.inputJSON, &input); err != nil {
				return nil, fmt.Errorf("parse tool_use input JSON for %q: %w", b.name, err)
			}
		}
		return ToolUseBlock{Type: "tool_use", ID: b.id, Name: b.name, Input: input}, nil
	}
	return nil, nil
}

func executeOneTool(ctx context.Context, tu ToolUseBlock) ToolResultBlock {
	select {
	case <-ctx.Done():
		return ToolResultBlock{
			Type:      "tool_result",
			ToolUseID: tu.ID,
			Content:   "Interrupted by user",
			IsError:   true,
		}
	default:
	}
	var output string
	var isError bool
	switch tu.Name {
	case "Bash":
		cmd, _ := tu.Input["command"].(string)
		output, isError = executeBash(cmd)
	default:
		output = fmt.Sprintf("unknown tool: %s", tu.Name)
		isError = true
	}
	return ToolResultBlock{
		Type:      "tool_result",
		ToolUseID: tu.ID,
		Content:   output,
		IsError:   isError,
	}
}
```

**【验证】**

```
运行：go test ./src/phase-01-trajectory/query/... -run TestRepairTrajectory -v
期望输出：
=== RUN   TestRepairTrajectory
=== RUN   TestRepairTrajectory/missing_tool_result_gets_synthetic_result
=== RUN   TestRepairTrajectory/already_resolved_tool_use_is_not_duplicated
=== RUN   TestRepairTrajectory/empty_message_list_returns_empty
--- PASS: TestRepairTrajectory (0.00s)
    --- PASS: TestRepairTrajectory/missing_tool_result_gets_synthetic_result (0.00s)
    --- PASS: TestRepairTrajectory/already_resolved_tool_use_is_not_duplicated (0.00s)
    --- PASS: TestRepairTrajectory/empty_message_list_returns_empty (0.00s)
PASS
```

---

### 2.3 Wiring（SSE 客户端 + 与 query 的连接）

**概念讲解**

**SSE（Server-Sent Events）格式**

Anthropic API 使用标准 SSE 协议推送流式响应。每个 SSE 事件由若干行组成，以空行分隔：

```
event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_01...","name":"Bash"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"command\":\"echo hello\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_stop
data: {"type":"message_stop"}
```

**为什么用标准 `net/http` 而不是第三方 SSE 库？**

标准库的 `bufio.Scanner` + 逐行解析足以覆盖 SSE 协议的所有需求，无需引入外部依赖。更重要的是，`http.NewRequestWithContext(ctx, ...)` 让 `ctx` 的取消能直接中止底层 TCP 连接，不需要额外的 goroutine 管理。

**`BlockMeta` 与 `Delta` 的分离**

API 的 `content_block_start` 事件中，block 的元数据（type、id、name）在 `content_block` 字段里，而 `content_block_delta` 的增量数据在 `delta` 字段里。`StreamEvent` 使用 `BlockMeta` 和 `Delta` 两个独立字段分别承载这两类信息，`parseStreamEvent` 把 `content_block.type/id/name` 映射到 `BlockMeta`，把 `delta.type/text/partial_json/thinking` 映射到 `Delta`，两者不再共用字段，语义清晰。

**【现在手敲】**

```go
// Package api implements the Anthropic streaming messages API client.
package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/learnclaudecode/claude-go/src/phase-01-trajectory/query"
)

const (
	anthropicAPIURL     = "https://api.anthropic.com/v1/messages"
	anthropicAPIVersion = "2023-06-01"
)

// toAPIContent converts a slice of ContentBlock to the JSON-serialisable
// representation that the Anthropic API expects.
func toAPIContent(blocks []query.ContentBlock) []map[string]any {
	out := make([]map[string]any, 0, len(blocks))
	for _, b := range blocks {
		switch v := b.(type) {
		case query.TextBlock:
			out = append(out, map[string]any{
				"type": "text",
				"text": v.Text,
			})
		case query.ThinkingBlock:
			out = append(out, map[string]any{
				"type":      "thinking",
				"thinking":  v.Thinking,
				"signature": v.Signature,
			})
		case query.ToolUseBlock:
			out = append(out, map[string]any{
				"type":  "tool_use",
				"id":    v.ID,
				"name":  v.Name,
				"input": v.Input,
			})
		case query.ToolResultBlock:
			m := map[string]any{
				"type":        "tool_result",
				"tool_use_id": v.ToolUseID,
				"is_error":    v.IsError,
			}
			switch c := v.Content.(type) {
			case string:
				m["content"] = c
			default:
				m["content"] = c
			}
			out = append(out, m)
		}
	}
	return out
}

// toAPIMessages converts internal Messages to the JSON structure the API expects.
func toAPIMessages(msgs []query.Message) []map[string]any {
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, map[string]any{
			"role":    string(m.Role),
			"content": toAPIContent(m.Content),
		})
	}
	return out
}

// toAPISystem converts SystemPart slice to API JSON.
func toAPISystem(parts []query.SystemPart) []map[string]any {
	out := make([]map[string]any, 0, len(parts))
	for _, p := range parts {
		m := map[string]any{
			"type": p.Type,
			"text": p.Text,
		}
		if p.CacheControl != nil {
			m["cache_control"] = map[string]any{"type": p.CacheControl.Type}
		}
		out = append(out, m)
	}
	return out
}

// toAPITools converts ToolDefinition slice to API JSON.
func toAPITools(tools []query.ToolDefinition) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]any{
			"name":         t.Name,
			"description":  t.Description,
			"input_schema": t.InputSchema,
		})
	}
	return out
}

// sseEvent holds a parsed "event: …" / "data: …" pair from an SSE stream.
type sseEvent struct {
	name string
	data string
}

// readSSE reads one SSE event from the scanner.  Returns nil, nil at EOF.
func readSSE(scanner *bufio.Scanner) (*sseEvent, error) {
	var ev sseEvent
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if ev.name != "" || ev.data != "" {
				return &ev, nil
			}
			continue
		}
		if strings.HasPrefix(line, "event: ") {
			ev.name = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			ev.data = strings.TrimPrefix(line, "data: ")
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return nil, nil // EOF
}

// rawEvent is the top-level JSON structure of an SSE data payload.
type rawEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	// content_block_start carries tool metadata here
	ContentBlock *struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"content_block"`
	// content_block_delta and content_block_start (text) use delta
	Delta *struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
		Thinking    string `json:"thinking"`
	} `json:"delta"`
	// usage appears directly or nested under message
	Usage   *rawUsage `json:"usage"`
	Message *struct {
		Usage *rawUsage `json:"usage"`
	} `json:"message"`
}

type rawUsage struct {
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	CacheCreationTokens int `json:"cache_creation_input_tokens"`
	CacheReadTokens     int `json:"cache_read_input_tokens"`
}

// parseStreamEvent converts a raw SSE data payload to a query.StreamEvent.
//
// For content_block_start events, block metadata (type/id/name) is placed into
// BlockMeta.  For content_block_delta events, incremental data goes into Delta.
func parseStreamEvent(data string) (*query.StreamEvent, error) {
	var raw rawEvent
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		return nil, fmt.Errorf("json unmarshal SSE: %w", err)
	}

	se := &query.StreamEvent{
		Type:  raw.Type,
		Index: raw.Index,
	}

	switch raw.Type {
	case "content_block_start":
		if raw.ContentBlock != nil {
			se.BlockMeta = &struct{ Type, ID, Name string }{
				Type: raw.ContentBlock.Type,
				ID:   raw.ContentBlock.ID,
				Name: raw.ContentBlock.Name,
			}
		}
	case "content_block_delta":
		if raw.Delta != nil {
			se.Delta = &struct{ Type, Text, PartialJSON, Thinking string }{
				Type:        raw.Delta.Type,
				Text:        raw.Delta.Text,
				PartialJSON: raw.Delta.PartialJSON,
				Thinking:    raw.Delta.Thinking,
			}
		}
	}

	// Populate Usage.
	if raw.Usage != nil {
		se.Usage = &query.Usage{
			InputTokens:         raw.Usage.InputTokens,
			OutputTokens:        raw.Usage.OutputTokens,
			CacheCreationTokens: raw.Usage.CacheCreationTokens,
			CacheReadTokens:     raw.Usage.CacheReadTokens,
		}
	} else if raw.Message != nil && raw.Message.Usage != nil {
		u := raw.Message.Usage
		se.Usage = &query.Usage{
			InputTokens:         u.InputTokens,
			OutputTokens:        u.OutputTokens,
			CacheCreationTokens: u.CacheCreationTokens,
			CacheReadTokens:     u.CacheReadTokens,
		}
	}

	return se, nil
}

// CallModel opens a streaming POST to the Anthropic messages API and sends
// parsed SSE events on the returned channel.  The channel is closed when the
// stream ends or ctx is cancelled.
func CallModel(ctx context.Context, p query.CallModelParams) (<-chan query.StreamEvent, error) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY environment variable not set")
	}

	model := p.Model
	if model == "" {
		model = "claude-sonnet-4-6"
	}
	maxTokens := p.MaxTokens
	if maxTokens == 0 {
		maxTokens = 8192
	}

	body := map[string]any{
		"model":      model,
		"max_tokens": maxTokens,
		"stream":     true,
	}
	if len(p.System) > 0 {
		body["system"] = toAPISystem(p.System)
	}
	body["messages"] = toAPIMessages(p.Messages)
	if len(p.Tools) > 0 {
		body["tools"] = toAPITools(p.Tools)
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicAPIURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", anthropicAPIVersion)
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http do: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("API error: HTTP %d", resp.StatusCode)
	}

	ch := make(chan query.StreamEvent, 64)
	go func() {
		defer resp.Body.Close()
		defer close(ch)

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 1<<20), 1<<20)

		for {
			ev, err := readSSE(scanner)
			if err != nil {
				ch <- query.StreamEvent{Type: "error"}
				return
			}
			if ev == nil {
				return // EOF
			}
			if ev.data == "" || ev.data == "[DONE]" {
				continue
			}
			se, err := parseStreamEvent(ev.data)
			if err != nil {
				ch <- query.StreamEvent{Type: "error"}
				return
			}
			select {
			case ch <- *se:
			case <-ctx.Done():
				return
			}
		}
	}()

	return ch, nil
}
```

**【验证】**

```
运行：go build ./src/phase-01-trajectory/api/...
期望输出：（无输出表示成功）
```

---

### 2.4 错误路径

**概念讲解**

**为什么中断后必须合成 tool_result？**

Anthropic API 执行以下约束（在服务端校验）：

> 如果 `messages` 数组中某个 `role: assistant` 的消息包含 `tool_use` 类型的 content block，则后续（或当前轮次的下一条）`role: user` 消息必须包含对应 `tool_use_id` 的 `tool_result` block。

违反这个约束，API 直接返回 HTTP 400。

**commit-on-complete 如何消除旧设计的"两次写入窗口期"？**

旧设计（`executeTools` 批量执行）在工具执行前就把 assistant message 写入 `messages`，执行后才追加 tool result message。如果中断发生在两次写入之间，`messages` 里就存在一个 assistant message 包含 tool_use 但没有对应 tool_result，轨迹非法。

新设计（commit-on-complete）把两次写入合并到 `message_stop` 时的单个代码块：先 `append(messages, assistantMsg)`，紧接着 `append(messages, toolResultMsg)`，中间没有任何 `ctx.Done()` 检查点。`message_stop` 之后 `break eventLoop`，进入 abort 检查前轨迹已经完整。

**中断可能发生的两类情况：**

1. **零 block 收到时 ctx 取消**（`anyBlockReceived == false`）：事件循环在收到任何 `content_block_stop` 之前就被 `ctx.Done()` 中断。此时 `assistantBlocks` 为空，本轮没有对 `messages` 造成任何修改，直接 `RepairTrajectory(messages)` 返回。`RepairTrajectory` 会处理上一轮可能残留的孤立 tool_use（若有）。

2. **有 block 已收集但未 commit 时 ctx 取消**（`anyBlockReceived == true`，`aborted == true`）：已收到至少一个 `content_block_stop`，`assistantBlocks` 非空，但 `message_stop` 还没到，所以 `messages` 尚未被本轮修改。此时做 partial commit：把当前收集到的 `assistantBlocks` 作为 assistant message 追加到 `messages`，然后调用 `RepairTrajectory`。`RepairTrajectory` 会为这些 tool_use blocks（若有）合成对应的 error tool_result，使轨迹重新合法。

注意：partial commit 不追加 `pendingToolResults`，因为工具已经执行（`executeOneTool` 在 `content_block_stop` 时立即执行），但我们只把 assistant blocks commit 进 messages，tool results 由 `RepairTrajectory` 用合成的 error 结果来补全。这样做是正确的——`RepairTrajectory` 扫描的是 `messages` 里的 tool_use，而 `pendingToolResults` 里的真实结果在轨迹修复语义上可以直接由合成的 error 结果替代（中断场景下，这条对话本身就是要终止的）。

**【现在手敲】**

以下是 `RepairTrajectory` 和 abort 路径的关键代码片段（已在 2.2 的完整代码中包含，此处单独展示 abort 处理逻辑以便对照理解）：

```go
		if aborted {
			if !anyBlockReceived {
				// 零 block：本轮未污染 messages，直接修复
				repaired := RepairTrajectory(messages)
				termCh <- Terminal{Reason: TerminalAborted, Messages: repaired}
				return
			}
			// 有 block：partial commit assistantBlocks，再修复
			if len(assistantBlocks) > 0 {
				assistantMsg := Message{
					Role:       RoleAssistant,
					Content:    assistantBlocks,
					UUID:       assistantUUID,
					ParentUUID: parentUUID,
				}
				messages = append(messages, assistantMsg)
			}
			repaired := RepairTrajectory(messages)
			termCh <- Terminal{Reason: TerminalAborted, Messages: repaired}
			return
		}
```

**【验证】**

```
运行：go test ./src/phase-01-trajectory/integration/... -run TestTrajectoryClose -v
期望输出：
=== RUN   TestTrajectoryClose
--- PASS: TestTrajectoryClose (0.00s)
PASS
```

---

## 验证你的实现

SubAgent A 已预写好测试，直接运行：

**【验证】**

```
运行：go test ./src/phase-01-trajectory/query/... ./src/phase-01-trajectory/integration/...
期望输出：
ok  	github.com/learnclaudecode/claude-go/src/phase-01-trajectory/query	0.XXs
ok  	github.com/learnclaudecode/claude-go/src/phase-01-trajectory/integration	0.XXs

如果全部 PASS，Phase 1 完成。
```
