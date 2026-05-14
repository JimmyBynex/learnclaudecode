# Phase 3：缓存前缀稳定性（不变量 II）

## Section 1：心智模型

### 1.1 本 Phase 添加了什么

Phase 3 在工具执行管道中插入了一层"命运封印"机制：每当 `executeTools` 拿到工具返回的原始内容后，立刻通过 `MaybeReplace` 决定这条内容是以原样、还是以一个固定的引用字符串写入消息历史。一旦决定（无论哪种），同一个 `tool_use_id` 在后续任何轮对话中都会得到**完全相同的字节序列**，从而保证发给 Anthropic API 的消息前缀字节永不漂移，让 prompt cache 能持续命中，大幅降低 token 费用。

### 1.2 在架构中的位置

```
queryLoop() 内联事件循环
    ↓ content_block_stop 触发
executeOneTool()
    ↓ ExecuteTool() 执行工具后
MaybeReplace(toolUseID, content)   ← ContentReplacementState（Phase 3 新增）
    ↓
构造 ToolResultBlock（内容已替换或原样）→ commit 到 messages（message_stop 时）
    ↓
发给 API 的消息前缀字节一致 → cache HIT
```

### 1.3 控制流

```
content_block_stop → executeOneTool(ctx, tu, tctx)
    ↓ ExecuteTool() 返回后
MaybeReplace(id, content)
    ├── id 在 seenIDs? → 是 → 在 replacements? → 是 → 返回 replacements[id]（命运已定，替换路径）
    │                              → 否 → 在 originals? → 是 → 返回 originals[id]（命运已定，直通路径）
    │                                                   → 否 → 返回 content 原样（兜底）
    └── 否（新 id）
         ├── len(content) > DefaultPersistenceThreshold(50000)? → 是 → seenIDs[id]=true
         │                                                              originals[id]=content
         │                                                              replacements[id]=buildRef(...)
         │                                                              返回引用字符串
         └── 否 → seenIDs[id]=true → originals[id]=content → 返回 content 原样
```

### 1.4 数据流

```
入口：toolUseID string, content string（可能 50KB+）

ContentReplacementState.seenIDs:      map[string]bool   → 记录"是否已处理过"
ContentReplacementState.replacements: map[string]string → id → 引用字符串（仅大内容）
ContentReplacementState.originals:    map[string]string → id → 原始内容（用于 Restore）

出口（替换路径）：string = "<persisted-output>\nTool output for {id} was persisted ({N} chars)..."（几百字节）
出口（直通路径）：string = content 原样
```

### 1.5 与前序 Phase 的关系

- **Phase 1** 建立了消息结构（`Message`、`ToolResultBlock`），定义了对话历史的内存表示。
- **Phase 2** 建立了工具执行管道（`ExecuteTool`），实现了 `executeTools` 把工具调用结果收集进用户消息。
- **Phase 3** 在 Phase 2 的 `executeTools` 之后插入一层：对每个 tool_result 的 `content` 调用 `MaybeReplace`，确保同一 `id` 的内容永远字节一致。此外，`ToolUseContext` 新增了 `ReplacementState ContentReplacer` 字段，`query` 包通过接口而非直接导入 `cache` 包来避免循环依赖。

---

## Section 2：实现演练

### 2.1 数据结构：ContentReplacementState

**为什么需要三个 map？**

- `seenIDs`：记录"这个 id 是否已经被处理过"。这是"命运封印"的关键——只要进入过 `MaybeReplace`，无论当时内容大小，后续调用一律冻结。
- `replacements`：仅存储被替换的 id → 引用字符串。如果一个 id 内容小于阈值，`replacements` 里不会有它的条目。
- `originals`：存储原始内容，供 `Restore` 方法使用（本地显示用，不影响 API 前缀）。

**为什么不能直接用 `replacements` 做"是否见过"的判断？**

因为有些 id 见过但内容小于阈值，不需要替换，`replacements` 中没有该条目。如果用 `replacements` 判断是否见过，这类 id 会被误判为"从未见过"，导致每轮都重新评估内容——如果内容在某轮变化了（理论上不应该，但防御性设计），就会产生不同的输出字节，破坏缓存前缀。`seenIDs` 负责兜底这部分。

**常量说明：**

- `DefaultPersistenceThreshold = 50_000`：超过 5 万字符的内容才替换，与 TypeScript 原版 `DEFAULT_MAX_RESULT_SIZE_CHARS` 一致。
- `PersistedOutputTag = "<persisted-output>"`：引用字符串的 XML 风格包装标签。

```go
【现在手敲】
```

从 `src/phase-03-cache/cache/replacement.go` 复制以下内容：

```go
// DefaultPersistenceThreshold is the character count above which a tool result
// is replaced with a stable reference string.  Corresponds to the TypeScript
// DEFAULT_MAX_RESULT_SIZE_CHARS constant.
const DefaultPersistenceThreshold = 50_000

// PersistedOutputTag is the XML-like wrapper used in the reference string.
// Kept identical to the original TypeScript implementation.
const PersistedOutputTag = "<persisted-output>"

// ContentReplacementState is the core of Invariant II.
//
// "Fate is sealed" means: the moment a tool_use_id is processed by MaybeReplace
// (regardless of whether it was actually replaced), it is recorded in seenIDs
// and its fate—original content or replacement reference—is frozen forever.
// Subsequent calls for the same id always return the exact same bytes, ensuring
// the cache prefix never drifts.
type ContentReplacementState struct {
	seenIDs      map[string]bool   // tool_use_ids that have already been processed
	replacements map[string]string // id → replacement reference string
	originals    map[string]string // id → original content (for Restore)
}

// NewContentReplacementState creates an empty, ready-to-use replacement state.
func NewContentReplacementState() *ContentReplacementState {
	return &ContentReplacementState{
		seenIDs:      make(map[string]bool),
		replacements: make(map[string]string),
		originals:    make(map[string]string),
	}
}
```

**【验证】**

```
运行：go build ./src/phase-03-cache/cache/...
期望输出：（无输出表示成功）
```

---

### 2.2 核心逻辑：MaybeReplace + Restore

**MaybeReplace 的三条路径：**

1. **seenIDs + 替换路径**：id 已见过，且 `replacements` 有记录 → 直接返回之前存好的引用字符串。内容大小、是否变化都不影响结果。
2. **seenIDs + 直通路径**：id 已见过，但 `replacements` 没有记录（说明当时内容小于阈值）→ 从 `originals` 返回当时存的原始内容。
3. **新 id 路径**：
   - 内容超阈值 → 存 `originals`、生成并存 `replacements`、标记 `seenIDs` → 返回引用字符串。
   - 内容未超阈值 → 存 `originals`、标记 `seenIDs` → 返回原始内容。

**Restore 的用途：**

`Restore` 供本地 UI 显示原始内容时使用，不影响发给 API 的消息前缀。它只对曾经被替换过的 id 有效（即在 `replacements` 中有记录），返回 `originals` 中保存的原始字符串。

```go
【现在手敲】
```

从 `src/phase-03-cache/cache/replacement.go` 复制以下内容：

```go
// MaybeReplace inspects content for the given toolUseID.
//
//   - If the id has been seen before, the previously decided representation is
//     returned unchanged (the "fate is sealed" guarantee).
//   - If the id is new and content exceeds DefaultPersistenceThreshold characters,
//     the content is stored in originals and a stable reference string is stored
//     in replacements; the reference string is returned.
//   - If the id is new and content is within the threshold, content is returned
//     as-is; the id is still recorded in seenIDs so future calls are consistent.
func (s *ContentReplacementState) MaybeReplace(toolUseID, content string) string {
	// Fate already sealed: return the previously decided representation.
	if s.seenIDs[toolUseID] {
		if ref, ok := s.replacements[toolUseID]; ok {
			return ref
		}
		// Was seen but not replaced: return original content.
		if orig, ok := s.originals[toolUseID]; ok {
			return orig
		}
		return content
	}

	// First encounter: seal the fate.
	s.seenIDs[toolUseID] = true

	if len(content) <= DefaultPersistenceThreshold {
		// Small enough to keep verbatim.
		s.originals[toolUseID] = content
		return content
	}

	// Large content: persist original, generate stable reference.
	s.originals[toolUseID] = content
	ref := buildRef(toolUseID, content)
	s.replacements[toolUseID] = ref
	return ref
}

// Restore returns the original content for a previously replaced tool result.
// Returns ("", false) if the id was never replaced (either not seen or small
// enough to be kept verbatim).
func (s *ContentReplacementState) Restore(toolUseID string) (string, bool) {
	if _, replaced := s.replacements[toolUseID]; !replaced {
		return "", false
	}
	orig, ok := s.originals[toolUseID]
	return orig, ok
}

// buildRef constructs the stable reference string that replaces large content
// in API requests.  The format mirrors the TypeScript PERSISTED_OUTPUT_TAG usage.
func buildRef(toolUseID, content string) string {
	return fmt.Sprintf(
		"%s\nTool output for %s was persisted (%d chars). Use the tool result directly.\n</persisted-output>",
		PersistedOutputTag,
		toolUseID,
		len(content),
	)
}
```

**【验证】**

```
运行：go test ./src/phase-03-cache/cache/... -run TestContentReplacement -v
期望输出：
=== RUN   TestContentReplacement
...
--- PASS: TestContentReplacement (0.00s)
PASS
```

---

### 2.3 Wiring：queryLoop 中的集成

**在哪里调用 MaybeReplace？**

在 `executeOneTool` 中，`ExecuteTool` 返回 `result.Content` 之后、构造 `ToolResultBlock` 之前。`executeOneTool` 在事件循环的 `content_block_stop` 分支中被调用，每个 tool_use block 单独触发一次。此时内容是最新的工具输出，经过替换后写入 `ToolResultBlock`，这条消息在 `message_stop` 时原子提交进 `messages`，从此永久稳定。

**为什么通过接口而不是直接导入 cache 包？**

`cache` 包导入了 `query` 包（用到了 `query.Message`、`query.ToolResultBlock` 等类型）；如果 `query` 包再直接导入 `cache` 包，就会形成循环依赖。解决方案是在 `query` 包中定义 `ContentReplacer` 接口，`cache.ContentReplacementState` 满足该接口，`ToolUseContext.ReplacementState` 持有接口值，实现零循环依赖。

```go
【现在手敲】
```

从 `src/phase-03-cache/query/types.go` 复制以下内容（新增的接口和字段）：

```go
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
```

从 `src/phase-03-cache/query/query.go` 复制 `executeOneTool` 中调用 `MaybeReplace` 的部分：

```go
		// Phase 3: apply cache-stability replacement.
		// MaybeReplace is idempotent for a given id: repeated turns keep the
		// same bytes in the API request so the cache prefix never drifts.
		if !isError && tctx != nil && tctx.ReplacementState != nil {
			output = tctx.ReplacementState.MaybeReplace(tu.ID, output)
		}
```

**【验证】**

```
运行：go test ./src/phase-03-cache/integration/... -run TestCachePrefixStability -v
期望输出：
=== RUN   TestCachePrefixStability
--- PASS: TestCachePrefixStability (0.00s)
PASS
```

---

### 2.4 错误路径：DetectCacheBreak + MicroCompact

**CacheBreakEvent 是观测手段：**

`DetectCacheBreak` 对比前后两次 API 调用的 `Usage` 统计：如果上一轮创建了缓存（`CacheCreationTokens > 0`）但本轮没有读到任何缓存（`CacheReadTokens == 0`），说明消息前缀已经发生漂移——cache 命中失败。`CacheBreakEvent` 记录了预期命中量与实际命中量的差值，以及人类可读的原因说明，用于日志监控和告警，**不主动修复漂移**。

**MicroCompact 是预防手段：**

`MicroCompact` 遍历已有的消息列表，对每个 `ToolResultBlock` 的字符串内容调用 `state.MaybeReplace`，确保整个历史消息中所有超阈值的 tool_result 都已经被替换为稳定引用字符串。它返回 `MicroCompactResult`，将本地显示视图（`LocalMessages`）和 API 发送视图（`APIMessages`）分离，为未来阶段进一步区分两个视图预留空间（Phase 3 中两者内容相同）。

```go
【现在手敲】
```

从 `src/phase-03-cache/cache/cachebreak.go` 复制以下内容：

```go
// CacheBreakEvent records a detected cache-prefix drift between two consecutive
// API requests.  A drift means the cache created in one request was not read
// in the next, indicating the prompt bytes changed.
type CacheBreakEvent struct {
	// ExpectedCacheReadTokens is a rough estimate of what should have been read.
	ExpectedCacheReadTokens int
	// ActualCacheReadTokens is what the API actually reported.
	ActualCacheReadTokens int
	// SuspectedCause is a human-readable description of the likely root cause.
	SuspectedCause string
}

// DetectCacheBreak compares two consecutive Usage values and returns a
// CacheBreakEvent when a cache miss is detected.
//
// Drift condition: the previous request created cache tokens
// (prev.CacheCreationTokens > 0) but the current request did not read any
// cached tokens (curr.CacheReadTokens == 0), meaning the cache prefix changed
// between requests.
//
// Returns nil when the cache appears healthy (either no cache was created, or
// it was successfully read).
func DetectCacheBreak(prev, curr query.Usage) *CacheBreakEvent {
	if prev.CacheCreationTokens > 0 && curr.CacheReadTokens == 0 {
		return &CacheBreakEvent{
			// Rough estimate: creation tokens ÷ 5 (cache read is typically cheaper)
			ExpectedCacheReadTokens: prev.CacheCreationTokens / 5,
			ActualCacheReadTokens:   0,
			SuspectedCause:          "cache prefix drift detected",
		}
	}
	return nil
}
```

从 `src/phase-03-cache/cache/microcompact.go` 复制以下内容：

```go
// MicroCompactResult separates the local display view from the API prefix view.
//
// The invariant: LocalMessages and APIMessages carry the same logical
// conversation but may differ in how large tool results are rendered.
// Currently both views use the same stable replacement reference once content
// has been processed through ContentReplacementState; the split exists to allow
// future phases to diverge them further (e.g., clearing old content locally
// while keeping the API prefix unchanged).
type MicroCompactResult struct {
	// LocalMessages is the view used for local display.
	// For replaced tool results the reference string is used (same as API).
	LocalMessages []query.Message
	// APIMessages is the view sent to the Anthropic API.
	// Byte-for-byte identical to LocalMessages in Phase 3; kept separate for
	// forward compatibility.
	APIMessages []query.Message
}

// MicroCompact iterates over messages and ensures every ToolResultBlock whose
// content exceeds the threshold is replaced with a stable reference string via
// state.MaybeReplace.
//
// Core guarantee: the same tool_use_id always produces the same byte sequence
// in both LocalMessages and APIMessages, preventing cache-prefix drift.
func MicroCompact(messages []query.Message, state *ContentReplacementState) MicroCompactResult {
	localMsgs := make([]query.Message, 0, len(messages))
	apiMsgs := make([]query.Message, 0, len(messages))

	for _, msg := range messages {
		localBlocks := make([]query.ContentBlock, 0, len(msg.Content))
		apiBlocks := make([]query.ContentBlock, 0, len(msg.Content))

		for _, block := range msg.Content {
			tr, ok := block.(query.ToolResultBlock)
			if !ok {
				// Non-tool-result blocks are passed through unchanged.
				localBlocks = append(localBlocks, block)
				apiBlocks = append(apiBlocks, block)
				continue
			}

			// Extract string content, applying MaybeReplace.
			var rawContent string
			switch c := tr.Content.(type) {
			case string:
				rawContent = c
			default:
				// Non-string content (e.g. []ContentBlock) is kept as-is.
				localBlocks = append(localBlocks, block)
				apiBlocks = append(apiBlocks, block)
				continue
			}

			// MaybeReplace enforces the "fate is sealed" invariant.
			processed := state.MaybeReplace(tr.ToolUseID, rawContent)

			replacedBlock := query.ToolResultBlock{
				Type:      tr.Type,
				ToolUseID: tr.ToolUseID,
				Content:   processed,
				IsError:   tr.IsError,
			}

			// Both views receive the same processed content in Phase 3.
			localBlocks = append(localBlocks, replacedBlock)
			apiBlocks = append(apiBlocks, replacedBlock)
		}

		localMsg := msg
		localMsg.Content = localBlocks
		apiMsg := msg
		apiMsg.Content = apiBlocks

		localMsgs = append(localMsgs, localMsg)
		apiMsgs = append(apiMsgs, apiMsg)
	}

	return MicroCompactResult{
		LocalMessages: localMsgs,
		APIMessages:   apiMsgs,
	}
}
```

**【验证】**

```
运行：go test ./src/phase-03-cache/cache/... -run "TestCacheBreakDetection|TestMicroCompact" -v
期望输出：
=== RUN   TestCacheBreakDetection
--- PASS: TestCacheBreakDetection (0.00s)
=== RUN   TestMicroCompact
--- PASS: TestMicroCompact (0.00s)
PASS
```

---

## 验证你的实现

**【验证】**

```
运行：go test ./src/phase-03-cache/...
期望输出：
?     github.com/learnclaudecode/claude-go/src/phase-03-cache/api  [no test files]
ok    github.com/learnclaudecode/claude-go/src/phase-03-cache/cache
ok    github.com/learnclaudecode/claude-go/src/phase-03-cache/integration
ok    github.com/learnclaudecode/claude-go/src/phase-03-cache/query
ok    github.com/learnclaudecode/claude-go/src/phase-03-cache/tools

如果全部 PASS，Phase 3 完成。
```
