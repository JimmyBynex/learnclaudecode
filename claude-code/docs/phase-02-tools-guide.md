# Phase 2：工具调度面学习指南

---

## Section 1：心智模型

### 1.1 本 Phase 添加了什么

Phase 2 在 Phase 1 的 query loop 骨架之上，建立了一套完整的工具调度管道。当模型在回复中声明"我要调用 X 工具"（即产出 `tool_use` block），系统不再像 Phase 1 那样用硬编码的 stub 处理，而是通过 `ToolRegistry` 找到对应工具，依次经过输入校验、权限检查、实际执行三道关卡，最终把结果包装成 `ToolResultBlock` 追加到对话历史，让模型能看到执行结果并继续推理。与此同时，Phase 2 实现了 8 个具体工具：BashTool、FileReadTool、FileWriteTool、FileEditTool、GlobTool、GrepTool、WebFetchTool、WebSearchTool，覆盖文件操作、命令执行、文件搜索和网络访问四大能力域。

---

### 1.2 在架构中的位置

```
┌─────────────────────────────────────────────────────────────┐
│                            CLI                              │
└────────────────────────────┬────────────────────────────────┘
                             │
┌────────────────────────────▼────────────────────────────────┐
│                         Query()                             │
│                        queryLoop()                          │
│  ┌──────────────────────────────────────────────────────┐   │
│  │ Phase 1 基础 (已继承):                               │   │
│  │   RepairTrajectory()                                 │   │
│  │   Message / ToolUseBlock / ToolResultBlock 类型      │   │
│  └──────────────────────────────────────────────────────┘   │
│  ┌──────────────────────────────────────────────────────┐   │
│  │ Phase 2 (重写 queryLoop + 新增工具层):               │   │
│  │   inline event loop (两决策边界):                    │   │
│  │     content_block_stop → executeOneTool()            │   │
│  │       ├── ToolRegistry.FindByName()                  │   │
│  │       └── ExecuteTool()                              │   │
│  │             ├── tool.ValidateInput()                 │   │
│  │             ├── tool.CheckPermissions()              │   │
│  │             └── tool.Call()                          │   │
│  │     message_stop  → commit to messages               │   │
│  │                                                      │   │
│  │   8 个具体工具实现（tools/ 包）                      │   │
│  │   DefaultBuiltins() / NewDefaultRegistry()           │   │
│  └──────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
```

---

### 1.3 控制流

```
queryLoop() 内联事件循环
    │
    ├── content_block_start → 记录 blockType / id / name 到 accumulator
    │
    ├── content_block_delta → 追加 text / input_json / thinking
    │
    ├── content_block_stop → buildBlockAt(index) 构造 block
    │       │
    │       ├── 推送 partialMsg 到 msgCh（yield 边界）
    │       │
    │       └── 若 block 是 ToolUseBlock：
    │               │
    │               ▼
    │           executeOneTool(ctx, tu, tctx)
    │               │
    │               ├── ctx.Done? → 返回 Interrupted error result
    │               │
    │               ├── tctx.Tools.FindByName(tu.Name) → Tool, found bool
    │               │       found=false → output="unknown tool: X", isError=true
    │               │       found=true  ↓
    │               └── ExecuteTool(ctx, tool, tu.Input, tctx)
    │                       ├─ tool.ValidateInput(input, tctx)
    │                       ├─ tool.CheckPermissions(input, tctx)
    │                       └─ tool.Call(ctx, input, tctx, nil)
    │               │
    │               ▼
    │           推送 resultMsg 到 msgCh（立即推送工具结果）
    │
    └── message_stop → 原子提交（commit 边界）
            ├── messages = append(messages, assistantMsg{assistantBlocks})
            └── messages = append(messages, toolResultMsg{pendingToolResults})
                    │
                    ├── 有 tool_use → 继续下一轮
                    └── 无 tool_use → Terminal{completed}
```

---

### 1.4 数据流

```
入口：
  ToolUseBlock{
      Type:  "tool_use",
      ID:    string,        // 模型分配的唯一 ID
      Name:  string,        // 工具名称，如 "Bash"
      Input: map[string]any // JSON 解析后的参数
  }

ValidateInput 返回：
  ValidationResult{
      OK:      bool,
      Message: string  // OK=false 时携带原因
  }

CheckPermissions 返回：
  PermissionDecision{
      Behavior: PermissionBehavior,  // "allow" | "deny" | "ask"
      Message:  string
  }

Call 返回：
  ToolResult{
      Content:     string,    // 工具执行结果文本
      IsError:     bool,      // true 表示工具层面的错误
      NewMessages: []Message  // 极少用，工具产出的额外消息
  }

出口：
  Message{
      Role: "user",
      Content: []ContentBlock{
          ToolResultBlock{
              Type:      "tool_result",
              ToolUseID: string,  // 对应入口 ToolUseBlock.ID
              Content:   any,     // string | []ContentBlock
              IsError:   bool,
          },
          // ... 每个 tool_use 对应一个 ToolResultBlock
      },
      UUID:       string,
      ParentUUID: string,
  }
```

---

### 1.5 与 Phase 1 的关系

Phase 1 建立了 query loop 的完整骨架，包括：
- `Message` / `Role` / `ContentBlock` / `TextBlock` / `ThinkingBlock` / `ToolUseBlock` / `ToolResultBlock` 等核心类型
- `Query()` / `queryLoop()` 的两通道设计（`msgCh` + `termCh`）
- `collectAssistantMessage()` 中的 SSE 流处理和 `contentAccumulator`
- `RepairTrajectory()` 轨迹修复（确保每个 tool_use 都有对应的 tool_result）
- `CallModel` / `SetCallModel` 的注入点

Phase 2 做了两层改动：

1. **重写 queryLoop**：采用与 Phase 1 相同的两决策边界设计——`content_block_stop` 作为推送边界（per-block 立即推送到 `msgCh` 并执行工具），`message_stop` 作为提交边界（原子写入 `messages`）。旧版中 `collectAssistantMessage`、`collectToolUseBlocks`、`executeTools` 三个函数已移除，取而代之的是内联事件循环 + `buildBlockAt` + `executeOneTool`。

2. **实现工具层**：通过 `ToolRegistry` 和 `ExecuteTool` 管道支持 8 个具体工具，替代了 Phase 1 硬编码的 bash stub。

`Query()`、`RepairTrajectory()`、核心类型（`Message`、`ContentBlock` 系列）的语义不变，但 `queryLoop` 本身已重写。`StreamEvent` 新增了 `BlockMeta` 字段供 `content_block_start` 使用。

---

## Section 2：实现演练

### 2.1 数据结构

**概念讲解**

`Tool` 是一个 Go interface 而不是 struct，因为我们需要 8 种行为各异的工具都能被同等对待——注册进同一个 `ToolRegistry`，被 `ExecuteTool` 统一调度。如果用 struct 加函数字段（闭包），类型断言会变得复杂，测试替身（mock）也更难写；interface 让每个工具的实现逻辑内聚在自己的文件里。

`ToolRegistry` 把工具分成 `builtins` 和 `extras` 两组，`FindByName` 始终先搜 builtins。这种 builtin-first 的稳定排序保证了注册顺序不随输入变化——这对 Phase 3 的提示词缓存至关重要：API 每次收到的 `tools` 数组顺序一致，缓存前缀才能命中（若顺序随机变化，每次都是缓存 miss）。

**【现在手敲】**

```go
// query/types.go（节选：Phase 2 新增部分）

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

// ToolUseContext carries context into the query and tool execution layers.
// Phase 2 expands the Phase 1 stub with a full ToolRegistry and state accessors.
type ToolUseContext struct {
    Tools       *ToolRegistry
    AbortCh     <-chan struct{}
    GetAppState func() *AppState
    SetAppState func(func(*AppState))
    Depth       int
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

// ToolDefinitions returns the ToolDefinition slice used to call the API.
// It is a convenience wrapper over Tools.ToDefinitions() that handles nil.
func (tctx *ToolUseContext) ToolDefinitions() []ToolDefinition {
    if tctx == nil || tctx.Tools == nil {
        return nil
    }
    return tctx.Tools.ToDefinitions()
}
```

```go
// query/registry.go

package query

import "context"

// NewToolRegistry creates a ToolRegistry with builtins listed before extras.
// Within each group the original order is preserved (stable sort).
func NewToolRegistry(builtins, extras []Tool) *ToolRegistry {
    r := &ToolRegistry{}
    r.builtins = make([]Tool, len(builtins))
    copy(r.builtins, builtins)
    r.extras = make([]Tool, len(extras))
    copy(r.extras, extras)
    return r
}

// FindByName returns the first tool with the given name, searching builtins
// before extras.
func (r *ToolRegistry) FindByName(name string) (Tool, bool) {
    for _, t := range r.builtins {
        if t.Name() == name {
            return t, true
        }
    }
    for _, t := range r.extras {
        if t.Name() == name {
            return t, true
        }
    }
    return nil, false
}

// All returns all registered tools (builtins first, then extras).
func (r *ToolRegistry) All() []Tool {
    out := make([]Tool, 0, len(r.builtins)+len(r.extras))
    out = append(out, r.builtins...)
    out = append(out, r.extras...)
    return out
}

// ToDefinitions returns a ToolDefinition slice suitable for passing to the API.
func (r *ToolRegistry) ToDefinitions() []ToolDefinition {
    all := r.All()
    defs := make([]ToolDefinition, 0, len(all))
    for _, t := range all {
        defs = append(defs, ToolDefinition{
            Name:        t.Name(),
            Description: t.Description(nil),
            InputSchema: t.InputSchema(),
        })
    }
    return defs
}
```

**【验证】**

```
运行：go build ./src/phase-02-tools/query/...
期望输出：（无输出表示成功）
```

---

### 2.2 核心逻辑

**概念讲解**

`ExecuteTool` 是工具执行的四步管道：

1. **Validate**：调用 `tool.ValidateInput()`，检查必填字段类型是否正确。校验失败直接返回 `ValidationError`，阻止后续步骤。为什么不能跳过？因为后面的 `Call()` 会直接类型断言输入字段，如果字段缺失或类型错误会 panic。Validate 是安全屏障。
2. **Permission**：调用 `tool.CheckPermissions()`，返回 allow / deny / ask 三种决策。Phase 2 对 `PermAsk` 自动放行（Phase 6+ 才会弹出交互式确认界面）；`PermDeny` 直接返回错误 ToolResult，不执行 Call。
3. **Execute**：调用 `tool.Call()`，工具层面的错误（如文件不存在）通过 `ToolResult{IsError:true}` 表达，而非 Go error——这样模型能看到错误消息并自我修正。
4. **Result wrap**：`Call()` 已经返回 `ToolResult`，`ExecuteTool` 直接透传。调用方（`executeTools()`）再把它包装成 `ToolResultBlock` 加入消息列表。

**【现在手敲】**

```go
// query/registry.go（ExecuteTool 及 ValidationError）

// ExecuteTool runs the validate → permission → execute → result-wrap pipeline.
// It returns an error only when the pipeline itself fails (not when the tool
// returns IsError=true content).
func ExecuteTool(ctx context.Context, t Tool, input map[string]any, tctx *ToolUseContext) (ToolResult, error) {
    // 1. Validate
    vr := t.ValidateInput(input, tctx)
    if !vr.OK {
        return ToolResult{}, &ValidationError{Message: vr.Message}
    }

    // 2. Permission check
    pd := t.CheckPermissions(input, tctx)
    switch pd.Behavior {
    case PermDeny:
        return ToolResult{
            Content: "Permission denied: " + pd.Message,
            IsError: true,
        }, nil
    case PermAsk:
        // Phase 2: auto-allow for "ask" (interactive permission UI is Phase 6+)
    }

    // 3. Execute
    result, err := t.Call(ctx, input, tctx, nil)
    if err != nil {
        return ToolResult{
            Content: err.Error(),
            IsError: true,
        }, nil
    }

    // 4. Result wrap (already wrapped by Call)
    return result, nil
}

// ValidationError is returned by ExecuteTool when validation fails.
type ValidationError struct {
    Message string
}

func (e *ValidationError) Error() string {
    return "validation failed: " + e.Message
}
```

```go
// query/query.go（节选：内联事件循环核心部分）

// buildBlockAt builds the ContentBlock at the given index.
// Returns nil (no error) if the slot is empty.
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

// executeOneTool executes a single ToolUseBlock using the tool registry.
func executeOneTool(ctx context.Context, tu ToolUseBlock, tctx *ToolUseContext) ToolResultBlock {
    select {
    case <-ctx.Done():
        return ToolResultBlock{
            Type: "tool_result", ToolUseID: tu.ID,
            Content: "Interrupted by user", IsError: true,
        }
    default:
    }

    var output string
    var isError bool

    if tctx != nil && tctx.Tools != nil {
        tool, found := tctx.Tools.FindByName(tu.Name)
        if !found {
            output = fmt.Sprintf("unknown tool: %s", tu.Name)
            isError = true
        } else {
            result, err := ExecuteTool(ctx, tool, tu.Input, tctx)
            if err != nil {
                output = err.Error()
                isError = true
            } else {
                output = result.Content
                isError = result.IsError
            }
        }
    } else {
        output = fmt.Sprintf("unknown tool: %s (no tool registry)", tu.Name)
        isError = true
    }

    return ToolResultBlock{
        Type: "tool_result", ToolUseID: tu.ID,
        Content: output, IsError: isError,
    }
}

// 在 queryLoop 的事件循环中（content_block_stop 分支）：
case "content_block_stop":
    block, buildErr := ca.buildBlockAt(ev.Index)
    // ...
    assistantBlocks = append(assistantBlocks, block)
    // yield 边界：立即推送到 msgCh
    msgCh <- partialMsg

    if tu, ok := block.(ToolUseBlock); ok {
        result := executeOneTool(ctx, tu, p.ToolCtx)
        pendingToolResults = append(pendingToolResults, result)
        msgCh <- resultMsg
    }

case "message_stop":
    // commit 边界：原子写入 messages
    messages = append(messages, Message{Role: RoleAssistant, Content: assistantBlocks, ...})
    if len(pendingToolResults) > 0 {
        messages = append(messages, Message{Role: RoleUser, Content: pendingToolResults, ...})
    }
```

**【验证】**

```
运行：go test ./src/phase-02-tools/query/... -run TestQueryLoopBasic -v
期望输出：
=== RUN   TestQueryLoopBasic
--- PASS: TestQueryLoopBasic (0.00s)
PASS
```

---

### 2.3 工具实现

#### 2.3a 文件操作工具（FileRead / FileWrite / FileEdit）

**概念讲解**

三个文件工具的权限策略刻意不同：

- `FileReadTool.CheckPermissions()` 返回 `PermAllow`——读文件是只读操作，不需要任何确认。
- `FileWriteTool.CheckPermissions()` 返回 `PermAsk`——写操作会改变磁盘状态，设计上需要用户确认（Phase 2 自动放行，Phase 6+ 会弹出 UI）。
- `FileEditTool.CheckPermissions()` 同样返回 `PermAsk`——原地替换文件内容，同属写操作。

`FileEditTool` 有一个重要约束：`old_string` 在文件中必须恰好出现一次。出现零次说明定位错误，出现多次说明替换有歧义——两种情况都通过 `ToolResult{IsError:true}` 告知模型，让模型重新提供更精确的 `old_string`。

**【现在手敲】**

```go
// tools/fileread.go

package tools

import (
    "bufio"
    "context"
    "fmt"
    "os"
    "strings"

    "github.com/learnclaudecode/claude-go/src/phase-02-tools/query"
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
```

```go
// tools/filewrite.go

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
```

```go
// tools/fileedit.go

package tools

import (
    "context"
    "fmt"
    "os"
    "strings"

    "github.com/learnclaudecode/claude-go/src/phase-02-tools/query"
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
```

**【验证】**

```
运行：go test ./src/phase-02-tools/tools/... -run "TestFileReadTool|TestFileWriteTool|TestFileEditTool" -v
期望输出：
=== RUN   TestFileReadTool
--- PASS: TestFileReadTool (0.00s)
=== RUN   TestFileWriteTool
--- PASS: TestFileWriteTool (0.00s)
=== RUN   TestFileEditTool
--- PASS: TestFileEditTool (0.00s)
PASS
```

---

#### 2.3b Bash + 搜索工具（Bash / Glob / Grep）

**概念讲解**

`BashTool` 有两个安全阀：

- **超时**：默认 120 秒（`bashDefaultTimeoutMs = 120_000`），通过 `context.WithTimeout` 实现。调用方可通过 `input["timeout"]` 覆盖。超时后 `cmd.Run()` 返回错误，`isError = true`。
- **输出截断**：输出超过 100KB（`bashMaxOutputBytes = 100 * 1024`）时截断并追加说明文字，防止单条 tool_result 消耗过多 token。

`GrepTool` 有结果数量上限（`grepMaxResults = 100`）。超过 100 条匹配时停止扫描，在输出末尾加提示，让模型知道结果已被截断。`ValidateInput` 还会预编译正则，确保无效正则在执行前就被拒绝。

`GlobTool` 使用标准库 `filepath.Glob`，不支持 `**` 递归通配（Go 标准库限制）；结果按修改时间降序排列，最新修改的文件排在前面。

**【现在手敲】**

```go
// tools/bash.go

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
```

```go
// tools/glob.go

package tools

import (
    "context"
    "fmt"
    "os"
    "path/filepath"
    "sort"
    "strings"
    "time"

    "github.com/learnclaudecode/claude-go/src/phase-02-tools/query"
)

// GlobTool finds files matching a glob pattern, returning paths sorted by
// modification time (most recent first).
type GlobTool struct{}

// NewGlobTool returns a new GlobTool instance.
func NewGlobTool() *GlobTool { return &GlobTool{} }

func (t *GlobTool) Name() string { return "Glob" }

func (t *GlobTool) Description(_ map[string]any) string {
    return "Fast file pattern matching tool. Returns matching file paths sorted by modification time."
}

func (t *GlobTool) InputSchema() any {
    return map[string]any{
        "type": "object",
        "properties": map[string]any{
            "pattern": map[string]any{
                "type":        "string",
                "description": "Glob pattern to match (e.g. \"**/*.go\").",
            },
            "path": map[string]any{
                "type":        "string",
                "description": "Directory to search in (default: current directory).",
            },
        },
        "required": []string{"pattern"},
    }
}

func (t *GlobTool) ValidateInput(input map[string]any, _ *query.ToolUseContext) query.ValidationResult {
    if p, ok := input["pattern"].(string); !ok || p == "" {
        return query.ValidationResult{OK: false, Message: "pattern must be a non-empty string"}
    }
    return query.ValidationResult{OK: true}
}

func (t *GlobTool) CheckPermissions(_ map[string]any, _ *query.ToolUseContext) query.PermissionDecision {
    return query.PermissionDecision{Behavior: query.PermAllow}
}

func (t *GlobTool) IsReadOnly(_ map[string]any) bool { return true }

func (t *GlobTool) Call(_ context.Context, input map[string]any, _ *query.ToolUseContext, _ query.ToolCallProgress) (query.ToolResult, error) {
    pattern, _ := input["pattern"].(string)
    basePath, _ := input["path"].(string)

    if basePath == "" {
        var err error
        basePath, err = os.Getwd()
        if err != nil {
            basePath = "."
        }
    }

    // Build the full pattern relative to basePath.
    fullPattern := filepath.Join(basePath, pattern)

    matches, err := filepath.Glob(fullPattern)
    if err != nil {
        return query.ToolResult{
            Content: fmt.Sprintf("glob error: %v", err),
            IsError: true,
        }, nil
    }

    // Sort by modification time descending.
    type fileEntry struct {
        path    string
        modTime time.Time
    }
    entries := make([]fileEntry, 0, len(matches))
    for _, m := range matches {
        info, err := os.Stat(m)
        if err != nil {
            entries = append(entries, fileEntry{path: m})
            continue
        }
        entries = append(entries, fileEntry{path: m, modTime: info.ModTime()})
    }
    sort.Slice(entries, func(i, j int) bool {
        return entries[i].modTime.After(entries[j].modTime)
    })

    if len(entries) == 0 {
        return query.ToolResult{Content: "(no files matched)"}, nil
    }

    var sb strings.Builder
    for _, e := range entries {
        sb.WriteString(e.path)
        sb.WriteByte('\n')
    }

    return query.ToolResult{Content: sb.String()}, nil
}
```

```go
// tools/grep.go

package tools

import (
    "bufio"
    "context"
    "fmt"
    "os"
    "path/filepath"
    "regexp"
    "strings"

    "github.com/learnclaudecode/claude-go/src/phase-02-tools/query"
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
```

**【验证】**

```
运行：go test ./src/phase-02-tools/tools/... -run "TestBashTool|TestGlobTool|TestGrepTool" -v
期望输出：
=== RUN   TestBashTool
--- PASS: TestBashTool (0.00s)
=== RUN   TestGlobTool
--- PASS: TestGlobTool (0.00s)
=== RUN   TestGrepTool
--- PASS: TestGrepTool (0.00s)
PASS
```

---

#### 2.3c Web 工具（WebFetch / WebSearch）

**概念讲解**

`WebFetchTool` 直接用 Go 标准库 `net/http` 发起 GET 请求，取回原始 HTML 后做三步文本清洗：

1. 用正则 `<[^>]+>` 剥离所有 HTML 标签（替换为空格）
2. 用 `[ \t]+` 折叠连续空白为单个空格
3. 用 `\n{3,}` 折叠多行空行为两行

最终限制为 50KB（`webFetchMaxBytes`），超出截断。这不是完整的 HTML 解析，但对给模型看的文本内容已经够用。

`WebSearchTool` 没有自己实现搜索引擎调用；它借助 Anthropic 平台内置的 `web_search_20250305` 工具，通过直接调用 Claude API（`claude-haiku-4-5` 模型）并挂载该内置工具来完成搜索。这意味着搜索能力由 Anthropic 平台提供，而非本地网络爬取。需要 `ANTHROPIC_API_KEY` 环境变量，且要设置 `anthropic-beta: web-search-2025-03-05` 请求头。

**【现在手敲】**

```go
// tools/webfetch.go

package tools

import (
    "context"
    "fmt"
    "io"
    "net/http"
    "regexp"

    "github.com/learnclaudecode/claude-go/src/phase-02-tools/query"
)

const webFetchMaxBytes = 50 * 1024 // 50 KB

var htmlTagRe = regexp.MustCompile(`<[^>]+>`)
var whitespaceRe = regexp.MustCompile(`[ \t]+`)
var blankLineRe = regexp.MustCompile(`\n{3,}`)

// WebFetchTool fetches a URL and returns its content as plain text.
type WebFetchTool struct{}

// NewWebFetchTool returns a new WebFetchTool instance.
func NewWebFetchTool() *WebFetchTool { return &WebFetchTool{} }

func (t *WebFetchTool) Name() string { return "WebFetch" }

func (t *WebFetchTool) Description(_ map[string]any) string {
    return "Fetches content from a URL and returns it as plain text (HTML tags stripped)."
}

func (t *WebFetchTool) InputSchema() any {
    return map[string]any{
        "type": "object",
        "properties": map[string]any{
            "url": map[string]any{
                "type":        "string",
                "description": "The URL to fetch.",
            },
            "prompt": map[string]any{
                "type":        "string",
                "description": "A description of what you want from the page.",
            },
        },
        "required": []string{"url", "prompt"},
    }
}

func (t *WebFetchTool) ValidateInput(input map[string]any, _ *query.ToolUseContext) query.ValidationResult {
    url, ok := input["url"].(string)
    if !ok || url == "" {
        return query.ValidationResult{OK: false, Message: "url must be a non-empty string"}
    }
    if _, ok := input["prompt"].(string); !ok {
        return query.ValidationResult{OK: false, Message: "prompt must be a string"}
    }
    return query.ValidationResult{OK: true}
}

func (t *WebFetchTool) CheckPermissions(_ map[string]any, _ *query.ToolUseContext) query.PermissionDecision {
    return query.PermissionDecision{Behavior: query.PermAllow}
}

func (t *WebFetchTool) IsReadOnly(_ map[string]any) bool { return true }

func (t *WebFetchTool) Call(ctx context.Context, input map[string]any, _ *query.ToolUseContext, _ query.ToolCallProgress) (query.ToolResult, error) {
    url, _ := input["url"].(string)

    req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
    if err != nil {
        return query.ToolResult{
            Content: fmt.Sprintf("error creating request: %v", err),
            IsError: true,
        }, nil
    }
    req.Header.Set("User-Agent", "claude-code/1.0")

    resp, err := http.DefaultClient.Do(req)
    if err != nil {
        return query.ToolResult{
            Content: fmt.Sprintf("error fetching URL: %v", err),
            IsError: true,
        }, nil
    }
    defer resp.Body.Close()

    body, err := io.ReadAll(io.LimitReader(resp.Body, webFetchMaxBytes+1))
    if err != nil {
        return query.ToolResult{
            Content: fmt.Sprintf("error reading response: %v", err),
            IsError: true,
        }, nil
    }

    truncated := false
    if len(body) > webFetchMaxBytes {
        body = body[:webFetchMaxBytes]
        truncated = true
    }

    // Strip HTML tags to plain text.
    text := htmlTagRe.ReplaceAllString(string(body), " ")
    text = whitespaceRe.ReplaceAllString(text, " ")
    text = blankLineRe.ReplaceAllString(text, "\n\n")

    if truncated {
        text += "\n[content truncated: exceeded 50KB limit]"
    }

    return query.ToolResult{Content: text}, nil
}
```

```go
// tools/websearch.go

package tools

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "os"
    "strings"

    "github.com/learnclaudecode/claude-go/src/phase-02-tools/query"
)

const (
    webSearchAPIURL     = "https://api.anthropic.com/v1/messages"
    webSearchAPIVersion = "2023-06-01"
    webSearchModel      = "claude-haiku-4-5"
)

// WebSearchTool uses the Anthropic web_search_20250305 built-in tool to
// perform web searches via the Claude API.
type WebSearchTool struct{}

// NewWebSearchTool returns a new WebSearchTool instance.
func NewWebSearchTool() *WebSearchTool { return &WebSearchTool{} }

func (t *WebSearchTool) Name() string { return "WebSearch" }

func (t *WebSearchTool) Description(_ map[string]any) string {
    return "Searches the web using the Anthropic web_search tool and returns results."
}

func (t *WebSearchTool) InputSchema() any {
    return map[string]any{
        "type": "object",
        "properties": map[string]any{
            "query": map[string]any{
                "type":        "string",
                "description": "The search query.",
            },
        },
        "required": []string{"query"},
    }
}

func (t *WebSearchTool) ValidateInput(input map[string]any, _ *query.ToolUseContext) query.ValidationResult {
    q, ok := input["query"].(string)
    if !ok || q == "" {
        return query.ValidationResult{OK: false, Message: "query must be a non-empty string"}
    }
    return query.ValidationResult{OK: true}
}

func (t *WebSearchTool) CheckPermissions(_ map[string]any, _ *query.ToolUseContext) query.PermissionDecision {
    return query.PermissionDecision{Behavior: query.PermAllow}
}

func (t *WebSearchTool) IsReadOnly(_ map[string]any) bool { return true }

func (t *WebSearchTool) Call(ctx context.Context, input map[string]any, _ *query.ToolUseContext, _ query.ToolCallProgress) (query.ToolResult, error) {
    q, _ := input["query"].(string)

    apiKey := os.Getenv("ANTHROPIC_API_KEY")
    if apiKey == "" {
        return query.ToolResult{
            Content: "ANTHROPIC_API_KEY not set; cannot perform web search",
            IsError: true,
        }, nil
    }

    // Build the request body using the Anthropic web_search_20250305 built-in tool.
    reqBody := map[string]any{
        "model":      webSearchModel,
        "max_tokens": 1024,
        "tools": []map[string]any{
            {
                "type": "web_search_20250305",
                "name": "web_search",
            },
        },
        "messages": []map[string]any{
            {
                "role": "user",
                "content": fmt.Sprintf("Search the web for: %s. Summarize the top results.", q),
            },
        },
    }

    bodyBytes, err := json.Marshal(reqBody)
    if err != nil {
        return query.ToolResult{
            Content: fmt.Sprintf("error marshaling request: %v", err),
            IsError: true,
        }, nil
    }

    req, err := http.NewRequestWithContext(ctx, http.MethodPost, webSearchAPIURL, bytes.NewReader(bodyBytes))
    if err != nil {
        return query.ToolResult{
            Content: fmt.Sprintf("error creating request: %v", err),
            IsError: true,
        }, nil
    }
    req.Header.Set("x-api-key", apiKey)
    req.Header.Set("anthropic-version", webSearchAPIVersion)
    req.Header.Set("anthropic-beta", "web-search-2025-03-05")
    req.Header.Set("content-type", "application/json")

    resp, err := http.DefaultClient.Do(req)
    if err != nil {
        return query.ToolResult{
            Content: fmt.Sprintf("error performing search request: %v", err),
            IsError: true,
        }, nil
    }
    defer resp.Body.Close()

    respBytes, err := io.ReadAll(resp.Body)
    if err != nil {
        return query.ToolResult{
            Content: fmt.Sprintf("error reading search response: %v", err),
            IsError: true,
        }, nil
    }

    if resp.StatusCode != http.StatusOK {
        return query.ToolResult{
            Content: fmt.Sprintf("search API error (HTTP %d): %s", resp.StatusCode, string(respBytes)),
            IsError: true,
        }, nil
    }

    // Parse the API response to extract text content.
    var apiResp struct {
        Content []struct {
            Type string `json:"type"`
            Text string `json:"text"`
        } `json:"content"`
    }
    if err := json.Unmarshal(respBytes, &apiResp); err != nil {
        return query.ToolResult{
            Content: fmt.Sprintf("error parsing search response: %v\nraw: %s", err, string(respBytes)),
            IsError: true,
        }, nil
    }

    var sb strings.Builder
    sb.WriteString(fmt.Sprintf("Search results for: %s\n\n", q))
    for _, block := range apiResp.Content {
        if block.Type == "text" && block.Text != "" {
            sb.WriteString(block.Text)
            sb.WriteByte('\n')
        }
    }

    result := sb.String()
    if strings.TrimSpace(result) == fmt.Sprintf("Search results for: %s", q) {
        result += "(no text results returned)"
    }

    return query.ToolResult{Content: result}, nil
}
```

**【验证】**

```
运行：go build ./src/phase-02-tools/tools/...
期望输出：（无输出表示成功）
（注：WebFetch/WebSearch 需要真实网络，单元测试不覆盖真实调用）
```

---

### 2.4 Wiring

**概念讲解**

`Tool` interface 定义在 `query` 包而不是 `tools` 包，这是为了避免循环依赖。依赖关系是：

```
tools → query（tools 包中每个文件都 import query）
query → （不 import tools）
```

如果把 `Tool` interface 放在 `tools` 包，那么 `query` 包就必须 import `tools` 才能使用 interface，形成 `query ↔ tools` 循环，Go 编译器会拒绝。解决方案是把 interface 和注册表都放在 `query` 包，`tools` 包单向依赖 `query` 即可。

`tools/registry.go` 通过类型别名（`type Tool = query.Tool`）把 `query` 包的类型重新导出，让调用方可以只 import `tools` 包，不用同时 import `query`。

**【现在手敲】**

```go
// tools/registry.go

package tools

import (
    "github.com/learnclaudecode/claude-go/src/phase-02-tools/query"
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
```

```go
// query/query.go（节选：在 content_block_stop 时调用 executeOneTool）

// executeOneTool 内的核心调度逻辑：
if tctx != nil && tctx.Tools != nil {
    tool, found := tctx.Tools.FindByName(tu.Name)
    if !found {
        output = fmt.Sprintf("unknown tool: %s", tu.Name)
        isError = true
    } else {
        result, err := ExecuteTool(ctx, tool, tu.Input, tctx)
        if err != nil {
            output = err.Error()
            isError = true
        } else {
            output = result.Content
            isError = result.IsError
        }
    }
} else {
    output = fmt.Sprintf("unknown tool: %s (no tool registry)", tu.Name)
    isError = true
}
```

**【验证】**

```
运行：go test ./src/phase-02-tools/integration/... -run TestToolExecutionPipeline -v
期望输出：
=== RUN   TestToolExecutionPipeline
--- PASS: TestToolExecutionPipeline (0.00s)
PASS
```

---

## 验证你的实现

**【验证】**

```
运行：go test ./src/phase-02-tools/...
期望输出：
?     github.com/learnclaudecode/claude-go/src/phase-02-tools/api  [no test files]
ok    github.com/learnclaudecode/claude-go/src/phase-02-tools/integration
ok    github.com/learnclaudecode/claude-go/src/phase-02-tools/query
ok    github.com/learnclaudecode/claude-go/src/phase-02-tools/tools

如果全部 PASS，Phase 2 完成。
```
